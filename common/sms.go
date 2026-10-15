package common

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
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

// An upstream code matching either pattern is a caller-side problem worth
// distinguishing from an outage: throttling to retry, rejection to fix in the
// console. Anything else is treated as unavailable.
var (
	smsThrottledCode = regexp.MustCompile(`THROTTL|BUSINESS_LIMIT_CONTROL|FLOW_CONTROL`)
	smsRejectedCode  = regexp.MustCompile(`MOBILE_NUMBER|SIGNATURE|TEMPLATE|RAM_PERMISSION|PERMISSION|ACCESS_KEY|ACCESSKEY|AUTH|PARAM`)
)

// Errors returned by SendSmsCode, for callers that need to tell a retryable
// failure from a configuration one.
var (
	ErrSmsNotConfigured = errors.New("sms_not_configured")
	ErrSmsRequest       = errors.New("sms_request_invalid")
	ErrSmsThrottled     = errors.New("sms_provider_throttled")
	ErrSmsRejected      = errors.New("sms_provider_rejected")
	ErrSmsUnavailable   = errors.New("sms_provider_unavailable")
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

	response, err := (&http.Client{Timeout: smsAliyunTimeout}).Do(request)
	if err != nil {
		return ErrSmsUnavailable
	}
	defer response.Body.Close()

	var reply aliyunSmsResponse
	if err := DecodeJson(response.Body, &reply); err != nil {
		return ErrSmsUnavailable
	}
	if reply.Code == "OK" {
		return nil
	}
	switch {
	case smsThrottledCode.MatchString(reply.Code):
		return ErrSmsThrottled
	case smsRejectedCode.MatchString(reply.Code):
		SysError(LogText("aliyun rejected the sms request: %s", reply.Code))
		return ErrSmsRejected
	default:
		SysError(LogText("aliyun sms send failed: %s", reply.Code))
		return ErrSmsUnavailable
	}
}
