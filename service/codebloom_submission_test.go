package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeBloomUnverifiedRequestFence(t *testing.T) {
	models, err := codebloom.Models()
	require.NoError(t, err)
	for _, m := range models {
		t.Run(m.ID, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			apiErr := ClampModelCapacityRequest(c, &relaycommon.RelayInfo{OriginModelName: m.ID})
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			if m.ActivationReady {
				assert.Contains(t, apiErr.Error(), "declared generation endpoint")
			} else {
				assert.Equal(t, "model_not_available", string(apiErr.GetErrorCode()))
			}
		})
	}
}
func TestCodeBloomUncertainSubmissionIsNonRetryableAndSafe(t *testing.T) {
	assert.Nil(t, ProtectCodeBloomImageSubmissionFailure(nil))
	original := &dto.TaskError{Code: "do_request_failed", StatusCode: 500, Message: "Bearer synthetic-provider-secret https://private.example", Data: map[string]any{"key": "synthetic-provider-secret"}, Error: errors.New("credential=synthetic-provider-secret")}
	safe := ProtectCodeBloomImageSubmissionFailure(original)
	require.NotNil(t, safe)
	assert.True(t, safe.NoRetry)
	assert.True(t, safe.LocalError)
	assert.Equal(t, http.StatusBadGateway, safe.StatusCode)
	assert.Equal(t, "image_submission_unconfirmed", safe.Code)
	assert.Nil(t, safe.Data)
	assert.NotContains(t, safe.Message, "synthetic-provider-secret")
	assert.NotContains(t, safe.Error.Error(), "synthetic-provider-secret")
	assert.NotContains(t, safe.Message, "private.example")
	assert.NotSame(t, original, safe)
	assert.False(t, original.NoRetry, "old provider error behavior is not mutated by a shared pointer")
}
