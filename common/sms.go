package common

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Aliyun SMS delivers the phone sign-in verification code. The deployment
// reuses the Aliyun account the rest of the stack already sends through, so the
// sign name and template are the ones approved there and need no new filing.
//
// The credentials are admin-editable options rather than environment variables,
// matching the SMTP settings: a rejected template or a rotated key is a console
// change, not a redeploy.
var (
	// SmsAliyunAccessKeyId and SmsAliyunAccessKeySecret are the RAM credential.
	SmsAliyunAccessKeyId     = ""
	SmsAliyunAccessKeySecret = ""
	// SmsAliyunSignName is the approved SMS sign name.
	SmsAliyunSignName = ""
	// SmsAliyunTemplateCode is the approved template, which must carry a
	// numeric `code` parameter.
	SmsAliyunTemplateCode = ""
)

const (
	// Aliyun's RPC endpoint and API version for the SMS product.
	smsAliyunEndpoint = "https://dysmsapi.aliyuncs.com"
	smsAliyunVersion  = "2017-05-25"
	smsAliyunTimeout  = 8 * time.Second
)

// SmsAliyunConfigured reports whether every value a send needs is set, so a
// caller can report a misconfiguration instead of a delivery failure.
func SmsAliyunConfigured() bool {
	return SmsAliyunAccessKeyId != "" && SmsAliyunAccessKeySecret != "" &&
		SmsAliyunSignName != "" && SmsAliyunTemplateCode != ""
}

// Sms accepts only a mainland mobile number, which is what the approved
// template is filed for; the E.164 +86 prefix is dropped for the API call.
var (
	smsMainlandNumberPattern = regexp.MustCompile(`^\+86(1[3-9][0-9]{9})$`)
	smsCodePattern           = regexp.MustCompile(`^[0-9]{6}$`)
)

// Errors returned by SendSmsCode, for callers that need to tell a retryable
// failure from a configuration one.
var (
	ErrSmsNotConfigured = errors.New("sms_not_configured")
	ErrSmsRequest       = errors.New("sms_request_invalid")
	ErrSmsThrottled     = errors.New("sms_provider_throttled")
	ErrSmsRejected      = errors.New("sms_provider_rejected")
	ErrSmsUnavailable   = errors.New("sms_provider_unavailable")
	// The request may have reached the provider. Callers must retain its code
	// and dispatch reservation rather than sending another SMS automatically.
	ErrSmsDeliveryUnknown = errors.New("sms_delivery_unknown")
)

type aliyunSmsResponse struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

// aliyunPercentEncode applies the encoding Aliyun's RPC signature requires:
// RFC 3986, with `+`, `*` and `%7E` normalized to their documented forms.
func aliyunPercentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	return strings.ReplaceAll(encoded, "%7E", "~")
}

// aliyunRpcSignature signs one RPC request. method is the HTTP method the call
// uses; parameters holds every request parameter except Signature; and
// accessKeySecret is the RAM secret, not the id.
func aliyunRpcSignature(method string, parameters map[string]string, accessKeySecret string) string {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, aliyunPercentEncode(key)+"="+aliyunPercentEncode(parameters[key]))
	}

	stringToSign := method + "&" + aliyunPercentEncode("/") + "&" + aliyunPercentEncode(strings.Join(pairs, "&"))
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// SendSmsCode delivers one verification code to a mainland mobile number.
// It never logs the code or the credential.
func SendSmsCode(phoneE164 string, code string) error {
	if !SmsAliyunConfigured() {
		return ErrSmsNotConfigured
	}
	number := smsMainlandNumberPattern.FindStringSubmatch(phoneE164)
	if number == nil || !smsCodePattern.MatchString(code) {
		return ErrSmsRequest
	}

	parameters := map[string]string{
		"AccessKeyId":      SmsAliyunAccessKeyId,
		"Action":           "SendSms",
		"Format":           "JSON",
		"PhoneNumbers":     number[1],
		"RegionId":         "cn-hangzhou",
		"SignName":         SmsAliyunSignName,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   GetRandomString(32),
		"SignatureVersion": "1.0",
		"TemplateCode":     SmsAliyunTemplateCode,
		"TemplateParam":    `{"code":"` + code + `"}`,
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          smsAliyunVersion,
	}
	parameters["Signature"] = aliyunRpcSignature(http.MethodPost, parameters, SmsAliyunAccessKeySecret)

	form := url.Values{}
	for key, value := range parameters {
		form.Set(key, value)
	}
	request, err := http.NewRequest(http.MethodPost, smsAliyunEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrSmsUnavailable
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: smsAliyunTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return ErrSmsDeliveryUnknown
	}
	defer response.Body.Close()

	var reply aliyunSmsResponse
	if err := DecodeJson(io.LimitReader(response.Body, 64<<10), &reply); err != nil {
		return ErrSmsDeliveryUnknown
	}
	if response.StatusCode == http.StatusOK && reply.Code == "OK" {
		return nil
	}
	if reply.Code == "" || reply.Code == "OK" {
		return ErrSmsDeliveryUnknown
	}
	// Only documented rejection codes release the dispatch reservation. A
	// future provider code or intermediary failure is conservatively unknown.
	switch reply.Code {
	case "isv.BUSINESS_LIMIT_CONTROL", "isv.DAY_LIMIT_CONTROL", "isv.LIMIT_CONTROL", "isv.ALIYUN_CLOUD_SMS_OVER_LIMIT", "Throttling", "Throttling.User", "Throttling.Api":
		return ErrSmsThrottled
	case "isv.SMS_TEMPLATE_ILLEGAL", "isv.SMS_SIGNATURE_ILLEGAL", "isv.SMS_SIGN_NAME_ILLEGAL", "isv.SMS_SIGNATURE_SCENE_ILLEGAL",
		"isv.MOBILE_NUMBER_ILLEGAL", "isv.MOBILE_COUNT_OVER_LIMIT", "isv.TEMPLATE_MISSING_PARAMETERS", "isv.TEMPLATE_PARAMS_ILLEGAL", "isv.TEMPLATE_PARAM_ILLEGAL",
		"isv.INVALID_PARAMETERS", "isv.PARAM_LENGTH_LIMIT", "isv.PARAM_NOT_SUPPORT_URL", "isv.AMOUNT_NOT_ENOUGH", "isv.OUT_OF_SERVICE", "isv.PRODUCT_UN_SUBSCRIPT", "isv.PRODUCT_UNSUBSCRIBE", "isv.RAM_PERMISSION_DENY", "isv.SMS_CONTENT_ILLEGAL",
		"InvalidAccessKeyId.NotFound", "InvalidAccessKeyId.Inactive", "SignatureDoesNotMatch", "SignatureNonceUsed", "SignatureNotFound", "TemplateNotFound", "InvalidSecurityToken.Expired", "InvalidSecurityToken.NotFound",
		"MissingParameter", "InvalidParameter", "Forbidden.RAM", "Forbidden.NoPermission", "InvalidVersion", "InvalidAction.NotFound", "IncompleteSignature", "MissingSignature", "InvalidTimeStamp.Expired":
		return ErrSmsRejected
	default:
		return ErrSmsDeliveryUnknown
	}
}
