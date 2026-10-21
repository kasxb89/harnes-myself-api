package common

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type smsTestTransport func(*http.Request) (*http.Response, error)

func (f smsTestTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestSendSmsCodeDistinguishesDefiniteRejectionFromUnknownDelivery(t *testing.T) {
	oldID, oldSecret, oldSign, oldTemplate := SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret, SmsAliyunSignName, SmsAliyunTemplateCode
	oldTransport := http.DefaultTransport
	SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret, SmsAliyunSignName, SmsAliyunTemplateCode = "fixture-id", "fixture-secret", "fixture-sign", "SMS_FIXTURE"
	t.Cleanup(func() {
		SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret, SmsAliyunSignName, SmsAliyunTemplateCode = oldID, oldSecret, oldSign, oldTemplate
		http.DefaultTransport = oldTransport
	})
	for _, test := range []struct {
		name, body             string
		status                 int
		transportErr, expected error
	}{
		{"accepted", `{"Code":"OK"}`, 200, nil, nil},
		{"rejected", `{"Code":"isv.SMS_TEMPLATE_ILLEGAL"}`, 200, nil, ErrSmsRejected},
		{"credential rejection", `{"Code":"SignatureDoesNotMatch"}`, 403, nil, ErrSmsRejected},
		{"insufficient provider funds", `{"Code":"isv.AMOUNT_NOT_ENOUGH"}`, 200, nil, ErrSmsRejected},
		{"throttled", `{"Code":"isv.BUSINESS_LIMIT_CONTROL"}`, 200, nil, ErrSmsThrottled},
		{"unknown provider code", `{"Code":"FutureProviderFailure"}`, 200, nil, ErrSmsDeliveryUnknown},
		{"unknown template code", `{"Code":"isv.SMS_TEMPLATE_FUTURE_FAILURE"}`, 200, nil, ErrSmsDeliveryUnknown},
		{"redirect is not delivery", `{"Code":"OK"}`, 307, nil, ErrSmsDeliveryUnknown},
		{"empty body", "", 200, nil, ErrSmsDeliveryUnknown},
		{"missing code", `{}`, 200, nil, ErrSmsDeliveryUnknown},
		{"malformed body", `{`, 200, nil, ErrSmsDeliveryUnknown},
		{"success code HTTP mismatch", `{"Code":"OK"}`, 503, nil, ErrSmsDeliveryUnknown},
		{"transport timeout", "", 0, context.DeadlineExceeded, ErrSmsDeliveryUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			http.DefaultTransport = smsTestTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "dysmsapi.aliyuncs.com", request.URL.Host)
				if test.transportErr != nil {
					return nil, test.transportErr
				}
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: request}, nil
			})
			err := SendSmsCode("+8613800138000", "123456")
			if test.expected == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.expected)
			}
			assert.Equal(t, 1, calls, "the sender must not retry an uncertain dispatch")
		})
	}
}

// The parameter set and the expected signature are Aliyun's own published
// example for the RPC signing procedure, so this pins the canonicalization the
// sort order, the percent-encoding and the HMAC input all at once.
func TestAliyunRpcSignature_MatchesThePublishedExample(t *testing.T) {
	parameters := map[string]string{
		"AccessKeyId":      "testid",
		"Action":           "DescribeRegions",
		"Format":           "XML",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   "3ee8c1b8-83d3-44af-a94f-4e0ad82fd6cf",
		"SignatureVersion": "1.0",
		"Timestamp":        "2016-02-23T12:46:24Z",
		"Version":          "2014-05-26",
	}

	assert.Equal(t, "OLeaidS1JvxuMvnyHOwuJ+uX5qY=",
		aliyunRpcSignature("GET", parameters, "testsecret"))
}

func TestSendSmsCode_ReportsAMissingConfiguration(t *testing.T) {
	oldId, oldSecret := SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret
	oldSign, oldTemplate := SmsAliyunSignName, SmsAliyunTemplateCode
	SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret = "", ""
	SmsAliyunSignName, SmsAliyunTemplateCode = "", ""
	t.Cleanup(func() {
		SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret = oldId, oldSecret
		SmsAliyunSignName, SmsAliyunTemplateCode = oldSign, oldTemplate
	})

	assert.False(t, SmsAliyunConfigured())
	assert.ErrorIs(t, SendSmsCode("+8613800138000", "123456"), ErrSmsNotConfigured)
}

func TestSendSmsCode_RejectsANumberOrCodeTheTemplateCannotCarry(t *testing.T) {
	oldId, oldSecret := SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret
	oldSign, oldTemplate := SmsAliyunSignName, SmsAliyunTemplateCode
	SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret = "test-id", "test-secret"
	SmsAliyunSignName, SmsAliyunTemplateCode = "test-sign", "SMS_0000001"
	t.Cleanup(func() {
		SmsAliyunAccessKeyId, SmsAliyunAccessKeySecret = oldId, oldSecret
		SmsAliyunSignName, SmsAliyunTemplateCode = oldSign, oldTemplate
	})

	assert.True(t, SmsAliyunConfigured())
	// Each of these is refused before any request is made: the approved
	// mainland template carries neither a foreign number, nor a malformed code,
	// nor the E.164 form with the prefix stripped twice.
	assert.ErrorIs(t, SendSmsCode("+14155550123", "123456"), ErrSmsRequest)
	assert.ErrorIs(t, SendSmsCode("+8613800138000", "12345"), ErrSmsRequest)
	assert.ErrorIs(t, SendSmsCode("8613800138000", "123456"), ErrSmsRequest)
	assert.ErrorIs(t, SendSmsCode("+8613800138000", "abcdef"), ErrSmsRequest)
}
