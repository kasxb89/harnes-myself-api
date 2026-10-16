package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetModelFallbackCandidates(t *testing.T) {
	previous := modelFallbackSetting
	t.Cleanup(func() { modelFallbackSetting = previous })

	for _, tc := range []struct {
		name  string
		table string
		limit int
		model string
		want  []string
	}{
		{name: "the configured order is kept", table: `{"glm-5.2":["glm-5.1","kimi-k2.6"]}`, limit: 3, model: "glm-5.2", want: []string{"glm-5.1", "kimi-k2.6"}},
		{name: "a self reference is dropped", table: `{"glm-5.2":["glm-5.2","glm-5.1"]}`, limit: 3, model: "glm-5.2", want: []string{"glm-5.1"}},
		{name: "a duplicate is dropped", table: `{"glm-5.2":["glm-5.1","glm-5.1"]}`, limit: 3, model: "glm-5.2", want: []string{"glm-5.1"}},
		{name: "the cap truncates the list", table: `{"glm-5.2":["glm-5.1","kimi-k2.6","qwen3.7-max"]}`, limit: 2, model: "glm-5.2", want: []string{"glm-5.1", "kimi-k2.6"}},
		{name: "an unlisted model has no candidates", table: `{"glm-5.2":["glm-5.1"]}`, limit: 3, model: "kimi-k2.6", want: nil},
		{name: "a malformed table falls back to nothing", table: `{"glm-5.2":`, limit: 3, model: "glm-5.2", want: nil},
		{name: "a disabled cap falls back to nothing", table: `{"glm-5.2":["glm-5.1"]}`, limit: 0, model: "glm-5.2", want: nil},
		{name: "an empty table falls back to nothing", table: ``, limit: 3, model: "glm-5.2", want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modelFallbackSetting = ModelFallbackSetting{ModelFallbackMap: tc.table, ModelFallbackMaxCandidates: tc.limit}
			assert.Equal(t, tc.want, GetModelFallbackCandidates(tc.model))
		})
	}
}
