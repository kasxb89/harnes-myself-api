package relay

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeBloomUnavailableBillingDoesNotStartOrReprice(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, name := range []string{"codebloom-gpt-6.1-sol", "codebloom-unknown"} {
		t.Run(name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}],"max_tokens":16}`))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set(string(constant.ContextKeyUsingGroup), "default")
			info := &relaycommon.RelayInfo{OriginModelName: name, UsingGroup: "default", UserGroup: "default", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 1, UpstreamModelName: name}, Request: &dto.GeneralOpenAIRequest{Model: name, Messages: []dto.Message{{Role: "user", Content: "hello"}}, MaxTokens: common.GetPointer(uint(16))}}
			err := PrepareRequestBilling(ctx, info)
			require.NotNil(t, err)
			assert.Equal(t, "model_not_available", string(err.GetErrorCode()))
			assert.True(t, types.IsSkipRetryError(err))
			assert.Nil(t, info.Billing)

			info.OriginModelName = "preserved-model"
			info.BillingModelName = "preserved-billing-identity"
			err = ReprepareFallbackBilling(ctx, info, name)
			require.NotNil(t, err)
			assert.Equal(t, "model_not_available", string(err.GetErrorCode()))
			assert.True(t, types.IsSkipRetryError(err))
			assert.Equal(t, "preserved-model", info.OriginModelName)
			assert.Equal(t, "preserved-billing-identity", info.BillingModelName)
			assert.Nil(t, info.Billing)
		})
	}
}
