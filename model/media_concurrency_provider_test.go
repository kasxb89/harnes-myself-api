package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaConcurrencyLegacyPolicyAndProviderDefaults(t *testing.T) {
	policy, err := decodeMediaPolicy(`{"schema_version":1,"defaults":{"video":4,"image":10},"pool_overrides":[]}`)
	require.NoError(t, err)
	value, err := mediaPolicyValue(policy)
	require.NoError(t, err)
	assert.Contains(t, value, `"audio":1`)
	assert.Contains(t, value, `"provider_defaults":{"ycapi":{"video":20,"image":20,"audio":20}}`)
	_, err = decodeMediaPolicy(`{"schema_version":1,"defaults":{"video":64,"image":64,"audio":64},"provider_defaults":{"ycapi":{"video":64,"image":64,"audio":64}},"pool_overrides":[]}`)
	require.NoError(t, err)
	for _, invalid := range []string{
		`{}`,
		`{"schema_version":1,"defaults":{"video":null,"image":10},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":null},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10,"audio":0},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10,"audio":null},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":65,"image":10},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10},"provider_defaults":{"ycapi":{"video":0,"image":20,"audio":20}},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10},"provider_defaults":null,"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10},"provider_defaults":{},"pool_overrides":[]}`,
	} {
		_, err = decodeMediaPolicy(invalid)
		assert.Error(t, err, invalid)
	}
	assert.Equal(t, 1, mediaEffectiveLimit(policy, "generic-audio", MediaAudio, 0), "other suppliers retain their existing audio default")
}
