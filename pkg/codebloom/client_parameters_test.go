package codebloom

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeBloomClientCandidateParameters(t *testing.T) {
	models, err := Models()
	require.NoError(t, err)
	require.Len(t, models, 37)
	previews := make([]map[string]any, 0, len(models))
	for _, m := range models {
		t.Run(m.ID, func(t *testing.T) {
			assert.False(t, PubliclyAvailable(m.ID), "approved user prices do not grant unverified capability/credential readiness")
			params := ClientParameters(m.ID)
			require.NotNil(t, params)
			props := params["properties"].(map[string]any)
			if m.Kind == "image" {
				assert.Equal(t, "image", params["x-model-kind"])
				assert.Equal(t, false, props["quality"])
				for _, unsupported := range []string{"output_format", "moderation", "format"} {
					assert.Equal(t, false, props[unsupported])
				}
				assert.Equal(t, false, props["resolution"])
				assert.Equal(t, false, props["image"])
				assert.Equal(t, false, props["mask"])
				assert.Equal(t, []int{1}, props["n"].(map[string]any)["enum"])
				if m.Transport == "images_generations" {
					assert.Equal(t, []string{"1024x1024"}, props["size"].(map[string]any)["enum"])
				} else {
					assert.Equal(t, false, props["size"])
				}
				assert.Equal(t, 0, params["x-reference-limits"].(map[string]any)["images"])
			} else {
				caps := params["x-text-capabilities"].(map[string]any)
				assert.Equal(t, false, caps["vision"])
				assert.Equal(t, false, caps["tools"])
				assert.Equal(t, false, caps["reasoning"])
				assert.Equal(t, "pending_verification", caps["operational_status"])
				assert.NotContains(t, caps, "max_input_tokens", "no capacity was verified")
			}
			prices := ClientPrices(m.ID)
			require.NotEmpty(t, prices)
			raw, err := common.Marshal(prices)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "account_credit")
			assert.NotContains(t, string(raw), "credential")
			assert.NotContains(t, string(raw), "account_multiplier")
			params["x-model-kind"] = "tampered"
			assert.Equal(t, m.Kind, ClientParameters(m.ID)["x-model-kind"])
		})
		previews = append(previews, map[string]any{"id": m.ID, "kind": m.Kind, "available": false, "availability": "unavailable", "offline_checkpoint": true, "transport": m.Transport, "retail_price_rows": ClientPrices(m.ID), "parameters": ClientParameters(m.ID)})
	}
	for _, name := range []string{"ycapi-gpt-6.1-sol", "MiniMax-H3", "yunshu-internal-gpt-image-2", "preserved-chat"} {
		assert.True(t, PubliclyAvailable(name))
	}
	assert.False(t, PubliclyAvailable("codebloom-unknown"))
	assert.Nil(t, ClientParameters("codebloom-unknown"))
	assert.Nil(t, ClientPrices("codebloom-unknown"))
	if path := os.Getenv("TEST_CODEBLOOM_METADATA_OUTPUT"); path != "" {
		raw, err := common.Marshal(previews)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0o600))
	}
}
