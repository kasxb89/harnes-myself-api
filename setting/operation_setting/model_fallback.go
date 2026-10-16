package operation_setting

import (
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// ModelFallbackSetting is the cross-model fallback table an operator keeps in the
// admin settings: which models may serve a request whose own model has no usable
// channel. Same-model failover between channels happens first and needs no
// configuration here.
type ModelFallbackSetting struct {
	// ModelFallbackMap maps one requested model to the models that may serve it
	// instead, in the order to try them, as a JSON object such as
	// {"glm-5.2":["glm-5.1","kimi-k2.6"]}. Empty disables fallback entirely.
	ModelFallbackMap string `json:"model_fallback_map"`
	// ModelFallbackMaxCandidates caps how many fallback models one request may
	// try. Values below 1 disable fallback.
	ModelFallbackMaxCandidates int `json:"model_fallback_max_candidates"`
}

var modelFallbackSetting = ModelFallbackSetting{
	ModelFallbackMap:           "",
	ModelFallbackMaxCandidates: 3,
}

func init() {
	config.GlobalConfig.Register("model_fallback_setting", &modelFallbackSetting)
}

// GetModelFallbackSetting returns the live fallback settings.
func GetModelFallbackSetting() *ModelFallbackSetting {
	return &modelFallbackSetting
}

// parsed fallback table memo, keyed by the raw JSON it was parsed from: the
// relay asks once per request, and a malformed value must not log on every one.
var (
	modelFallbackCacheMu    sync.RWMutex
	modelFallbackCacheRaw   string
	modelFallbackCacheTable map[string][]string
)

// GetModelFallbackCandidates returns the models that may serve `model` when its
// own channels are unusable: the configured order, deduplicated, never containing
// `model` itself, truncated to the configured cap. A missing entry, a malformed
// table, or a disabled cap returns nothing, so the relay simply keeps its
// same-model behavior.
func GetModelFallbackCandidates(model string) []string {
	limit := modelFallbackSetting.ModelFallbackMaxCandidates
	raw := strings.TrimSpace(modelFallbackSetting.ModelFallbackMap)
	if model == "" || raw == "" || limit < 1 {
		return nil
	}

	modelFallbackCacheMu.RLock()
	table := modelFallbackCacheTable
	cached := modelFallbackCacheRaw == raw
	modelFallbackCacheMu.RUnlock()
	if !cached {
		parsed := map[string][]string{}
		if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
			common.SysError(fmt.Sprintf("model fallback map is not valid JSON, fallback stays off: %s", err.Error()))
			parsed = map[string][]string{}
		}
		modelFallbackCacheMu.Lock()
		modelFallbackCacheRaw, modelFallbackCacheTable = raw, parsed
		modelFallbackCacheMu.Unlock()
		table = parsed
	}

	seen := map[string]struct{}{model: {}}
	candidates := make([]string, 0, limit)
	for _, candidate := range table[model] {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
		if len(candidates) == limit {
			break
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return candidates
}
