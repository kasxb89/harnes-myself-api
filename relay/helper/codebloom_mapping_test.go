package helper

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeBloomModelMappingRejectsPendingAtEveryNode(t *testing.T) {
	for _, tc := range []struct {
		name, origin string
		mapping      map[string]string
	}{
		{"pending origin", "codebloom-gpt-6.1-sol", nil},
		{"unknown origin", "codebloom-unknown", nil},
		{"mapped destination", "alias", map[string]string{"alias": "codebloom-gpt-6.1-sol"}},
		{"hidden intermediate", "alias", map[string]string{"alias": "codebloom-gpt-6.1-sol", "codebloom-gpt-6.1-sol": "gpt-6.1-sol"}},
		{"hidden unknown intermediate", "alias", map[string]string{"alias": "middle", "middle": "codebloom-unknown", "codebloom-unknown": "gpt-6.1-sol"}},
		{"same reserved node", "codebloom-gpt-6.1-sol", map[string]string{"codebloom-gpt-6.1-sol": "codebloom-gpt-6.1-sol"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.mapping != nil {
				raw, err := common.Marshal(tc.mapping)
				require.NoError(t, err)
				c.Set("model_mapping", string(raw))
			}
			info := &relaycommon.RelayInfo{OriginModelName: tc.origin, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tc.origin}}
			request := &dto.GeneralOpenAIRequest{Model: tc.origin}
			err := ModelMappedHelper(c, info, request)
			require.Error(t, err)
			var apiErr *types.NewAPIError
			require.True(t, errors.As(err, &apiErr))
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Equal(t, "model_not_available", string(apiErr.GetErrorCode()))
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.Equal(t, "The selected model is not available", apiErr.Error())
			assert.Equal(t, tc.origin, request.Model, "no partial outbound rewrite is released")
			assert.Equal(t, tc.origin, info.UpstreamModelName)
		})
	}
}

func TestCodeBloomMappingFencePreservesExistingMappings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mapping map[string]string
		want    string
	}{
		{"ordinary chained", map[string]string{"alias": "middle", "middle": "gpt-6.1-sol"}, "gpt-6.1-sol"},
		{"ordinary direct", map[string]string{"alias": "ycapi-gpt-6.1-sol"}, "ycapi-gpt-6.1-sol"},
		{"no mapping", nil, "alias"},
		{"ordinary self", map[string]string{"alias": "alias"}, "alias"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.mapping != nil {
				raw, err := common.Marshal(tc.mapping)
				require.NoError(t, err)
				c.Set("model_mapping", string(raw))
			}
			info := &relaycommon.RelayInfo{OriginModelName: "alias", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "alias"}}
			request := &dto.GeneralOpenAIRequest{Model: "alias"}
			require.NoError(t, ModelMappedHelper(c, info, request))
			assert.Equal(t, tc.want, info.UpstreamModelName)
			assert.Equal(t, tc.want, request.Model)
		})
	}
}
