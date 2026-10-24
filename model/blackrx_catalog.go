package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

const BlackrxCatalogTTL = 5 * time.Minute

// BlackrxCatalogChannelSnapshots reads owned routing/account values for catalog
// projections, including alternate providers. Cached Channel pointers are not
// safe here: multi-key status maps are updated in place under the polling lock.
// These rows never share their maps or polling cursor with the channel cache.
func BlackrxCatalogChannelSnapshots(ctx context.Context, ids []int) (map[int]*Channel, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	channels := make(map[int]*Channel, len(ids))
	for start := 0; start < len(ids); start += 500 {
		var batch []Channel
		if err := DB.WithContext(ctx).Select("id", "type", "status", "models", "group", "key", "base_url", "setting", "settings", "model_mapping", "param_override", "header_override", "channel_info").
			Where("id IN ?", ids[start:min(start+500, len(ids))]).Find(&batch).Error; err != nil {
			return nil, err
		}
		for index := range batch {
			channels[batch[index].Id] = &batch[index]
		}
	}
	return channels, nil
}

// Discovery is advisory state, never a replacement for configured models or a
// historical task's pinned upstream identity. Keys themselves are not retained.
type BlackrxCatalogSnapshot struct {
	Models           []string
	DurationPolicies map[string]BlackrxDurationPolicy
	VerifiedAt       time.Time
	AttemptedAt      time.Time
}

// Values are the complete, validated upstream policy, not an expansion of the
// installed plugin's capabilities. A missing policy remains unknown.
type BlackrxDurationPolicy struct {
	Values  []int
	Default int
}

var blackrxCatalog = struct {
	sync.RWMutex
	entries map[string]BlackrxCatalogSnapshot
}{entries: make(map[string]BlackrxCatalogSnapshot)}

func IsBlackrxCatalogChannel(channel *Channel) bool {
	if channel == nil || channel.Type != constant.ChannelTypeTaskPlugin || channel.Setting == nil {
		return false
	}
	var setting dto.ChannelSettings
	return common.UnmarshalJsonStr(*channel.Setting, &setting) == nil && setting.TaskPluginKey == "blackrxai-video"
}

func BlackrxCatalogIdentity(channel *Channel, key string) string {
	setting := ""
	if channel.Setting != nil {
		setting = *channel.Setting
	}
	// The transport settings and account identity are both part of freshness.
	headerOverride := ""
	if channel.HeaderOverride != nil {
		headerOverride = *channel.HeaderOverride
	}
	return fmt.Sprintf("%d:%x", channel.Id, sha256.Sum256([]byte(channel.GetBaseURL()+"\x00"+setting+"\x00"+key+"\x00"+headerOverride)))
}

func ReadBlackrxCatalog(channel *Channel, key string) BlackrxCatalogSnapshot {
	blackrxCatalog.RLock()
	snapshot := blackrxCatalog.entries[BlackrxCatalogIdentity(channel, key)]
	blackrxCatalog.RUnlock()
	snapshot.Models = slices.Clone(snapshot.Models)
	if snapshot.DurationPolicies != nil {
		policies := make(map[string]BlackrxDurationPolicy, len(snapshot.DurationPolicies))
		for name, policy := range snapshot.DurationPolicies {
			policy.Values = slices.Clone(policy.Values)
			policies[name] = policy
		}
		snapshot.DurationPolicies = policies
	}
	return snapshot
}

// RecordBlackrxCatalog keeps the last verified list when discovery fails. An
// empty or malformed response must never turn the configured catalog into zero.
func RecordBlackrxCatalog(channel *Channel, key string, models []string, now time.Time) {
	RecordBlackrxCatalogPolicies(channel, key, models, nil, now)
}

func RecordBlackrxCatalogPolicies(channel *Channel, key string, models []string, policies map[string]BlackrxDurationPolicy, now time.Time) {
	identity := BlackrxCatalogIdentity(channel, key)
	blackrxCatalog.Lock()
	defer blackrxCatalog.Unlock()
	_, exists := blackrxCatalog.entries[identity]
	if !exists && len(blackrxCatalog.entries) >= 1024 {
		oldestKey := ""
		var oldest time.Time
		for candidate, snapshot := range blackrxCatalog.entries {
			if oldestKey == "" || snapshot.AttemptedAt.Before(oldest) {
				oldestKey, oldest = candidate, snapshot.AttemptedAt
			}
		}
		delete(blackrxCatalog.entries, oldestKey)
	}
	snapshot := blackrxCatalog.entries[identity]
	snapshot.AttemptedAt = now
	if len(models) > 0 {
		snapshot.Models = slices.Clone(models)
		snapshot.DurationPolicies = make(map[string]BlackrxDurationPolicy, len(policies))
		for name, policy := range policies {
			policy.Values = slices.Clone(policy.Values)
			snapshot.DurationPolicies[name] = policy
		}
		snapshot.VerifiedAt = now
	}
	blackrxCatalog.entries[identity] = snapshot
}

// BlackrxEnabledKeys accepts an owned channel snapshot, never a mutable
// CacheGetChannel pointer. Discovery must not advance the shared polling cursor.
func BlackrxEnabledKeys(channel *Channel) []string {
	if channel == nil {
		return nil
	}
	if !channel.ChannelInfo.IsMultiKey {
		if key := strings.TrimSpace(channel.Key); key != "" {
			return []string{key}
		}
		return nil
	}
	keys := make([]string, 0)
	for index, key := range channel.GetKeys() {
		status, hasStatus := channel.ChannelInfo.MultiKeyStatusList[index]
		if hasStatus && status != common.ChannelStatusEnabled {
			continue
		}
		if key = strings.TrimSpace(key); key != "" && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// BlackrxCatalogAllowsModel filters newly offered models only. Unknown accounts
// retain their configured display entries, but submit preflight must verify a
// fresh snapshot before permitting any paid request.
func BlackrxCatalogAllowsModel(channel *Channel, requestedModel string) bool {
	if !IsBlackrxCatalogChannel(channel) {
		return true
	}
	upstreamModel, valid := BlackrxCatalogUpstreamModel(channel, requestedModel)
	if !valid {
		return false
	}
	plugin, declared := jsplugin.DefaultRegistry.Get("blackrxai-video")
	if !declared || !slices.Contains(plugin.Meta.Models, upstreamModel) {
		return false
	}
	_, priced := ratio_setting.GetModelPrice(requestedModel, false)
	_, ratioPriced, _ := ratio_setting.GetModelRatio(requestedModel)
	expression, expressionPriced := billing_setting.ResolveTaskBillingExpr("blackrxai-video", requestedModel, upstreamModel)
	schema, _ := plugin.Meta.UsageForModels(upstreamModel, requestedModel)
	if !priced && !ratioPriced && (!expressionPriced || !billing_setting.TaskExprCompatible(expression, schema)) {
		return false
	}
	keys := BlackrxEnabledKeys(channel)
	for _, key := range keys {
		snapshot := ReadBlackrxCatalog(channel, key)
		if snapshot.VerifiedAt.IsZero() || slices.Contains(snapshot.Models, upstreamModel) {
			if values, known := BlackrxAccountDurations(channel, key, upstreamModel); !known || len(values) > 0 {
				return true
			}
		}
	}
	return false
}

func BlackrxCatalogUpstreamModel(channel *Channel, requestedModel string) (string, bool) {
	if raw := strings.TrimSpace(channel.GetModelMapping()); raw != "" && raw != "null" {
		var mapping map[string]string
		if common.UnmarshalJsonStr(raw, &mapping) != nil {
			return "", false
		}
		upstream, cycle := followChannelModelMapping(mapping, requestedModel)
		return upstream, !cycle
	}
	return requestedModel, true
}

// BlackrxSupportedDurations reuses the installed host's existing input schema.
// Live discovery may narrow it, but cannot enable another duration or model.
func BlackrxSupportedDurations(upstreamModel string) []int {
	parameters := blackrxVideoClientParameters(upstreamModel)
	if parameters == nil {
		return nil
	}
	return slices.Clone(parameters["properties"].(map[string]any)["seconds"].(map[string]any)["enum"].([]int))
}

func BlackrxAccountDurations(channel *Channel, key, upstreamModel string) ([]int, bool) {
	values := BlackrxSupportedDurations(upstreamModel)
	snapshot := ReadBlackrxCatalog(channel, key)
	policy, known := snapshot.DurationPolicies[upstreamModel]
	// Display retains the last verified restrictions on refresh failures.
	// Paid-submit callers must separately require a fresh account snapshot.
	if !known || snapshot.VerifiedAt.IsZero() {
		return values, false
	}
	intersection := make([]int, 0, len(values))
	for _, seconds := range values {
		if slices.Contains(policy.Values, seconds) {
			intersection = append(intersection, seconds)
		}
	}
	return intersection, true
}

// BlackrxChannelDurations is a display/quote union across enabled accounts.
// Submission still checks the exact key, so a key with a narrower policy cannot
// borrow another account's permission to send.
func BlackrxChannelDurations(channel *Channel, upstreamModel string) []int {
	var values []int
	for _, key := range BlackrxEnabledKeys(channel) {
		snapshot := ReadBlackrxCatalog(channel, key)
		if !snapshot.VerifiedAt.IsZero() && !slices.Contains(snapshot.Models, upstreamModel) {
			continue
		}
		allowed, _ := BlackrxAccountDurations(channel, key, upstreamModel)
		values = append(values, allowed...)
	}
	slices.Sort(values)
	return slices.Compact(values)
}
