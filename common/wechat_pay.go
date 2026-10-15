package common

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// WeChat Pay v3 Native (merchant-presented QR). Only the pieces New API needs
// are implemented: create a native transaction, and verify + decrypt the
// asynchronous payment notification. No third-party SDK is required.
var (
	WechatPayEnabled = GetEnvOrDefaultBool("WECHAT_PAY_ENABLED", false)

	WechatAppId           = strings.TrimSpace(os.Getenv("WECHAT_APP_ID"))
	WechatMchId           = strings.TrimSpace(os.Getenv("WECHAT_MCH_ID"))
	WechatApiV3Key        = strings.TrimSpace(os.Getenv("WECHAT_API_V3_KEY"))
	WechatMerchantSerial  = strings.TrimSpace(os.Getenv("WECHAT_MERCHANT_SERIAL"))
	WechatMerchantKeyPath = strings.TrimSpace(os.Getenv("WECHAT_MERCHANT_PRIVATE_KEY_PATH"))
	WechatPlatformKeyId   = strings.TrimSpace(os.Getenv("WECHAT_PLATFORM_PUBLIC_KEY_ID"))
	WechatPlatformKeyPath = strings.TrimSpace(os.Getenv("WECHAT_PLATFORM_PUBLIC_KEY_PATH"))
	WechatNotifyUrl       = strings.TrimSpace(os.Getenv("WECHAT_NOTIFY_URL"))
	WechatApiBase         = GetEnvOrDefaultString("WECHAT_API_BASE", "https://api.mch.weixin.qq.com")
	// WechatReconcileIntervalSeconds controls how often pending orders are
	// checked against WeChat when no notification arrived.
	WechatReconcileIntervalSeconds = GetEnvOrDefault("WECHAT_RECONCILE_INTERVAL_SECONDS", 120)
	// WechatReconcileMinAgeSeconds is how old a pending order must be before
	// reconciliation trusts WeChat's view of it over an in-flight notification.
	// Defaults to the order TTL; local test setups shorten it.
	WechatReconcileMinAgeSeconds = GetEnvOrDefault("WECHAT_RECONCILE_MIN_AGE_SECONDS", 1800)
)

// WechatPayConfigured reports whether every credential the flow needs is set.
func WechatPayConfigured() bool {
	return WechatAppId != "" && WechatMchId != "" && len(WechatApiV3Key) == 32 &&
		WechatMerchantSerial != "" && WechatMerchantKeyPath != "" &&
		WechatPlatformKeyId != "" && WechatPlatformKeyPath != "" && WechatNotifyUrl != ""
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("wechat_merchant_key_not_pem")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("wechat_merchant_key_not_rsa")
		}
		return rsaKey, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func loadPublicKey(path string) (*rsa.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("wechat_platform_key_not_pem")
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		rsaKey, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("wechat_platform_cert_not_rsa")
		}
		return rsaKey, nil
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("wechat_platform_key_not_rsa")
	}
	return rsaKey, nil
}

func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// WechatPayAuthorization builds the Authorization header for a v3 request.
func WechatPayAuthorization(method string, urlPath string, body string) (string, error) {
	key, err := loadPrivateKey(WechatMerchantKeyPath)
	if err != nil {
		return "", err
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}
	message := strings.Join([]string{method, urlPath, timestamp, nonce, body, ""}, "\n")
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",signature="%s",timestamp="%s",serial_no="%s"`,
		WechatMchId, nonce, base64.StdEncoding.EncodeToString(signature), timestamp, WechatMerchantSerial,
	), nil
}

// VerifyWechatNotification checks the platform signature over the raw body.
func VerifyWechatNotification(timestamp string, nonce string, body string, signature string) error {
	key, err := loadPublicKey(WechatPlatformKeyPath)
	if err != nil {
		return err
	}
	message := strings.Join([]string{timestamp, nonce, body, ""}, "\n")
	digest := sha256.Sum256([]byte(message))
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return err
	}
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], decoded)
}

// WechatNotificationTimestampFresh rejects stale notifications. A signed
// callback stays valid forever otherwise, and although settlement is
// idempotent, accepting arbitrarily old callbacks is not standard practice.
const WechatNotificationMaxSkew = 5 * time.Minute

func WechatNotificationTimestampFresh(timestamp string) bool {
	seconds, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return false
	}
	skew := time.Since(time.Unix(seconds, 0))
	if skew < 0 {
		skew = -skew
	}
	return skew <= WechatNotificationMaxSkew
}

// WechatNotificationSerialMatches verifies the callback was signed with the
// platform certificate this deployment is configured with. Skipped only when
// no platform key id is configured.
func WechatNotificationSerialMatches(serial string) bool {
	if WechatPlatformKeyId == "" {
		return true
	}
	return strings.TrimSpace(serial) == WechatPlatformKeyId
}

// DecryptWechatResource decrypts an AEAD_AES_256_GCM notification resource.
func DecryptWechatResource(ciphertext string, nonce string, associatedData string) ([]byte, error) {
	key := []byte(WechatApiV3Key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, []byte(nonce), raw, []byte(associatedData))
}

type wechatNativeOrderReply struct {
	CodeUrl string `json:"code_url"`
}

type wechatOrderQueryReply struct {
	OutTradeNo    string `json:"out_trade_no"`
	TransactionId string `json:"transaction_id"`
	TradeState    string `json:"trade_state"`
	Amount        struct {
		Total int64 `json:"total"`
	} `json:"amount"`
}

// QueryWechatOrder asks WeChat for the authoritative state of one order. Used
// by reconciliation when a notification never arrived.
func QueryWechatOrder(outTradeNo string) (*wechatOrderQueryReply, error) {
	urlPath := "/v3/pay/transactions/out-trade-no/" + outTradeNo + "?mchid=" + WechatMchId
	authorization, err := WechatPayAuthorization(http.MethodGet, urlPath, "")
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodGet, WechatApiBase+urlPath, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", authorization)
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("wechat_order_query_failed:%d:%s", response.StatusCode, string(raw))
	}
	var reply wechatOrderQueryReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// CreateWechatNativeOrder asks WeChat for a native payment QR payload.
func CreateWechatNativeOrder(outTradeNo string, description string, totalFen int64, expireAt time.Time) (string, error) {
	payload := map[string]any{
		"appid":        WechatAppId,
		"mchid":        WechatMchId,
		"description":  description,
		"out_trade_no": outTradeNo,
		"notify_url":   WechatNotifyUrl,
		"amount":       map[string]any{"total": totalFen, "currency": "CNY"},
	}
	if !expireAt.IsZero() {
		payload["time_expire"] = expireAt.Format(time.RFC3339)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	urlPath := "/v3/pay/transactions/native"
	authorization, err := WechatPayAuthorization(http.MethodPost, urlPath, string(body))
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest(http.MethodPost, WechatApiBase+urlPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", authorization)
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("wechat_native_order_failed:%d:%s", response.StatusCode, string(raw))
	}
	var reply wechatNativeOrderReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", err
	}
	if reply.CodeUrl == "" {
		return "", errors.New("wechat_native_order_missing_code_url")
	}
	return reply.CodeUrl, nil
}
