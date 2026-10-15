package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
