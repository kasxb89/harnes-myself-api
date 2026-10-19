package kitutil

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorHooksRemainDistinct(t *testing.T) {
	previousError := logError.Load()
	previousSystemError := logSystemError.Load()
	t.Cleanup(func() {
		logError.Store(previousError)
		logSystemError.Store(previousSystemError)
	})

	var ordinaryMessages []string
	var systemMessages []string
	SetLogging(nil, func(message string) {
		ordinaryMessages = append(ordinaryMessages, message)
	})
	SetSystemErrorLogging(func(message string) {
		systemMessages = append(systemMessages, message)
	})

	LogError("invalid dto")
	LogSystemError("converter failure")

	assert.Equal(t, []string{"invalid dto"}, ordinaryMessages)
	assert.Equal(t, []string{"converter failure"}, systemMessages)
}

func TestErrorDiagnosticsMaskCredentialsWithoutLosingFailureReason(t *testing.T) {
	for _, input := range []string{
		`400 reasoning_content missing; key sk-user-test-secret-123456`,
		`request failed Authorization: Bearer opaque-provider-secret`,
		`request failed Authorization: Basic dXNlcjpwYXNzd29yZA==`,
		`{"error":"reasoning_content missing","api_key":"opaque-provider-secret"}`,
		`{"error":"reasoning_content missing","headers":{"X-API-Key":"opaque-provider-secret"}}`,
		`request failed access_token=opaque-provider-secret`,
		"request failed -----BEGIN PRIVATE KEY-----\nopaque-provider-secret\n-----END PRIVATE KEY-----",
	} {
		t.Run(input, func(t *testing.T) {
			masked := MaskSensitiveInfo(input)
			assert.NotContains(t, masked, "sk-user-test-secret-123456")
			assert.NotContains(t, masked, "opaque-provider-secret")
			assert.NotContains(t, masked, "dXNlcjpwYXNzd29yZA==")
			if strings.Contains(input, "reasoning_content") {
				assert.Contains(t, masked, "reasoning_content missing")
			}
		})
	}
}

func TestCredentialJSONKeepsDiagnosticFieldsAndExactNumbers(t *testing.T) {
	const original = `{"id":9007199254740993,"generation":18446744073709551615,"status_code":400,"message":"reasoning_content missing","details":[{"Authorization":"Bearer secret-value"},{"nested":"{\"refresh_token\":\"another-secret\"}"}],"secret":{"value":"hidden-secret"}}`
	masked := MaskCredentials(original)
	assert.True(t, Valid([]byte(masked)))
	for _, secret := range []string{"secret-value", "another-secret", "hidden-secret"} {
		assert.NotContains(t, masked, secret)
	}
	for _, detail := range []string{`"id":9007199254740993`, `"generation":18446744073709551615`, `"status_code":400`, "reasoning_content missing"} {
		assert.Contains(t, masked, detail)
	}
	assert.Equal(t, masked, MaskCredentials(masked))
}

func TestCredentialMaskLeavesModelAndRequestDiagnosticsIntact(t *testing.T) {
	const diagnostic = `model=deepseek-v4-flash request_id=263e1220-7892-4bf1-84bd-2e965de9259b max_tokens=2048 token_id=7 invalid_request_error reasoning_content missing`
	assert.Equal(t, diagnostic, MaskCredentials(diagnostic))
}

func TestCredentialMaskHandlesStatusPrefixedNestedJSON(t *testing.T) {
	const diagnostic = `status_code=400, {"nested":"{\"api_key\":\"opaque-provider-secret\"}","message":"reasoning_content missing"}`
	masked := MaskCredentials(diagnostic)
	assert.NotContains(t, masked, "opaque-provider-secret")
	assert.Contains(t, masked, "status_code=400, ")
	assert.Contains(t, masked, "reasoning_content missing")
	assert.Equal(t, masked, MaskCredentials(masked))
}

func TestCredentialMaskRemovesURLCredentialsAndSignedQueryValues(t *testing.T) {
	const diagnostic = `{"message":"upstream denied https://private-user:private-pass@upstream.test/v1?key=query-secret&X-Amz-Signature=signature-secret#fragment-secret","request_path":"/harness/v1/chat/completions"}`
	masked := MaskCredentials(diagnostic)
	assert.True(t, Valid([]byte(masked)))
	for _, secret := range []string{"private-user", "private-pass", "query-secret", "signature-secret", "fragment-secret"} {
		assert.NotContains(t, masked, secret)
	}
	assert.Contains(t, masked, "upstream.test/v1")
	assert.Contains(t, masked, "/harness/v1/chat/completions")
	assert.Equal(t, masked, MaskCredentials(masked))
}
