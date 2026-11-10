package controller

import (
	"context"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	taskadaptor "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type modelRetailCacheEntry struct {
	version string
	at      time.Time
	rows    []map[string]any
}

var modelRetailCache = struct {
	sync.Mutex
	entries map[string]modelRetailCacheEntry
}{entries: map[string]modelRetailCacheEntry{}}

// The caller holds the configuration read guard. These are the same ungrouped
// CNY tariffs as the existing public retail rows, for text-only input. This path
// can decode and meter a request; it has no credentials, submission or funding.
func unifiedModelRetailRows(ctx context.Context, name string, record model.PublishedModelConfiguration, parameters map[string]any, groups []string, version string) []map[string]any {
	if record.Definition.Kind != "image" && record.Definition.Kind != "video" {
		return nil
	}
	encoded, err := common.Marshal([]any{version, parameters, groups})
	if err != nil || version == "" {
		return nil
	}
	key := string(encoded)
	modelRetailCache.Lock()
	cached, exists := modelRetailCache.entries[key]
	modelRetailCache.Unlock()
	if exists && cached.version == key && time.Since(cached.at) < 15*time.Second {
		return cached.rows
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows := []map[string]any{}
	for _, spec := range unifiedRetailSpecifications(parameters) {
		var agreed string
		valid, candidates := true, 0
		allFree := true
		for _, route := range record.Definition.Routes {
			if !route.Enabled || len(groups) > 0 && !slices.Contains(groups, route.Group) {
				continue
			}
			var channel model.Channel
			if model.DB.Select("id", "type", "status", "base_url", "setting", "settings").First(&channel, route.ChannelID).Error != nil || channel.Status != common.ChannelStatusEnabled || !model.IsChannelEnabledForGroupModel(route.Group, name, route.ChannelID) {
				continue
			}
			generation := jsplugin.DefaultRegistry.Generation()
			path, protocol := "/v1/images/generations", "openai_image"
			if record.Definition.Kind == "video" {
				path, protocol = "/v1/videos", "openai_video"
			}
			matched := false
			for _, binding := range generation.LookupEndpointCandidates("POST", path, route.UpstreamModel) {
				plugin := binding.Plugin
				if plugin == nil {
					continue
				}
				bound := channel.GetSetting().BindsTaskPlugin(plugin.Meta.Key) && (channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI)
				if factory, ok := generation.GetByChannelType(channel.Type); ok && factory == plugin {
					bound = true
				}
				if !bound {
					continue
				}
				matched = true
				price, free, ok := unifiedRetailSpecificationPrice(ctx, name, route.UpstreamModel, channel, plugin, protocol, path, binding.Operation.Name, spec, record.Definition.Kind)
				if !ok || agreed != "" && agreed != price {
					valid = false
					break
				}
				agreed = price
				allFree = allFree && free
				candidates++
			}
			if !matched {
				valid = false
			}
			if !valid {
				break
			}
		}
		if !valid || candidates == 0 {
			continue
		}
		// A positive tariff rounded below one quota unit is not proof of a
		// configured free model, and must not become a selectable zero price.
		if agreed == "0" && !allFree {
			continue
		}
		params := maps.Clone(spec)
		if _, present := params["mode"]; !present {
			params["mode"] = "text_to_" + record.Definition.Kind
		}
		row := map[string]any{"params": params, "unit": "CNY / " + map[string]string{"image": "image", "video": "second"}[record.Definition.Kind], "currency": "CNY", "retail_cny": agreed}
		if allFree {
			row["is_free"] = true
		}
		rows = append(rows, row)
	}
	if ctx.Err() != nil {
		return nil
	}
	modelRetailCache.Lock()
	if len(modelRetailCache.entries) >= 512 {
		clear(modelRetailCache.entries)
	}
	modelRetailCache.entries[key] = modelRetailCacheEntry{version: key, at: time.Now(), rows: rows}
	modelRetailCache.Unlock()
	return rows
}

func unifiedRetailSpecificationPrice(ctx context.Context, name, native string, channel model.Channel, plugin *jsplugin.LoadedPlugin, protocol, path, operation string, spec map[string]any, kind string) (string, bool, bool) {
	request := maps.Clone(spec)
	request["model"] = name
	request["prompt"] = "Generation tariff estimate"
	pc := jsplugin.ProtocolRequestContext{RouteRequestContext: jsplugin.RouteRequestContext{Path: path, Method: "POST", Body: map[string]any{"kind": "json", "value": request}}, Model: name, UpstreamModel: native, Protocol: protocol, Operation: operation}
	pc, err := pc.WithNativeBody()
	if err != nil {
		return "", false, false
	}
	decoded, err := plugin.Engine.CallPath(ctx, "protocols", []string{protocol, "decodeRequest"}, pc.DecodeValueFor(plugin.Meta))
	if err != nil {
		return "", false, false
	}
	intent, ok := decoded.(map[string]any)
	if !ok || intent["kind"] != "submit" || intent["model"] != native {
		return "", false, false
	}
	body, ok := intent["requestBody"].(map[string]any)
	if !ok {
		return "", false, false
	}
	meter := &gin.Context{Request: (&http.Request{Method: "POST", URL: &url.URL{Path: path}, Header: make(http.Header)}).WithContext(ctx)}
	meter.Set("task_request", body)
	info := &relaycommon.RelayInfo{OriginModelName: name, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: channel.Type, ChannelBaseUrl: channel.GetBaseURL(), UpstreamModelName: native}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	info.Action, _ = intent["action"].(string)
	adapter := taskadaptor.New(plugin)
	adapter.Init(info)
	facts, err := adapter.ExtractUsageFactsValidated(meter, info)
	if err != nil {
		return "", false, false
	}
	quantity := float64(1)
	if kind == "video" {
		quantity, _ = facts["seconds"].(float64)
	}
	if kind == "image" {
		if count, ok := facts["image_count"].(float64); ok {
			quantity = count
		}
	}
	if quantity <= 0 || math.IsNaN(quantity) || math.IsInf(quantity, 0) {
		return "", false, false
	}
	expression, configured := billing_setting.ResolveTaskBillingExpr(plugin.Meta.Key, name, native)
	var usd float64
	if configured {
		if billingexpr.UsesFixedPricing(expression) {
			return "", false, false
		}
		var evalErr error
		usd, _, evalErr = billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts, EvaluationTime: time.Now()})
		if evalErr != nil {
			return "", false, false
		}
	} else {
		if billing_setting.GetBillingMode(name) == billing_setting.BillingModeTieredExpr {
			return "", false, false
		}
		price, priced := ratio_setting.GetModelPrice(name, false)
		if !priced {
			return "", false, false
		}
		ratios, err := adapter.EstimateBillingValidated(meter, info)
		if err != nil {
			return "", false, false
		}
		// Nontrivial legacy quantity pricing needs the full host estimator.
		// Do not advertise a fixed unit tariff from a partial calculation.
		for _, ratio := range ratios {
			if ratio != 1 {
				return "", false, false
			}
		}
		usd = price
	}
	if usd < 0 || math.IsNaN(usd) || math.IsInf(usd, 0) || common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) || operation_setting.USDExchangeRate <= 0 || math.IsNaN(operation_setting.USDExchangeRate) || math.IsInf(operation_setting.USDExchangeRate, 0) {
		return "", false, false
	}
	quota, clamp := common.QuotaRoundChecked(usd * common.QuotaPerUnit)
	if clamp != nil {
		return "", false, false
	}
	return decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromFloat(operation_setting.USDExchangeRate)).Div(decimal.NewFromFloat(quantity)).String(), usd == 0, true
}

// Expand only finite declared specification fields. The native decoder remains
// the authority for legal combinations. References are deliberately absent.
func unifiedRetailSpecifications(schema map[string]any) []map[string]any {
	encoded, err := common.Marshal(schema)
	if err != nil {
		return nil
	}
	var normalized map[string]any
	if common.Unmarshal(encoded, &normalized) != nil {
		return nil
	}
	base, _ := normalized["properties"].(map[string]any)
	branches, _ := normalized["oneOf"].([]any)
	if len(branches) == 0 {
		branches = []any{map[string]any{}}
	}
	result := []map[string]any{}
	seen := map[string]bool{}
	for _, raw := range branches {
		branch, _ := raw.(map[string]any)
		fields := maps.Clone(base)
		overrides, _ := branch["properties"].(map[string]any)
		for name, value := range overrides {
			field, _ := fields[name].(map[string]any)
			copy := maps.Clone(field)
			if copy == nil {
				copy = map[string]any{}
			}
			if extra, ok := value.(map[string]any); ok {
				maps.Copy(copy, extra)
			}
			fields[name] = copy
		}
		combinations := []map[string]any{{}}
		for _, name := range []string{"size", "resolution", "quality", "ratio", "aspect_ratio", "seconds", "duration", "mode", "generate_audio", "audio"} {
			if name == "aspect_ratio" && (fields["size"] != nil || fields["ratio"] != nil) || name == "duration" && fields["seconds"] != nil {
				continue
			}
			field, ok := fields[name].(map[string]any)
			if !ok {
				continue
			}
			values, _ := field["enum"].([]any)
			if value, ok := field["const"]; ok {
				values = []any{value}
			}
			if len(values) == 0 && (name == "seconds" || name == "duration") {
				minimum, minok := field["minimum"].(float64)
				maximum, maxok := field["maximum"].(float64)
				if minok && maxok && minimum >= 1 && maximum <= common.MaxTaskDurationSeconds && maximum-minimum < 256 {
					for value := minimum; value <= maximum; value++ {
						values = append(values, value)
					}
				}
			}
			if len(values) == 0 {
				if value, ok := field["default"]; ok {
					values = []any{value}
				}
			}
			if len(values) == 0 || len(combinations)*len(values) > 256 {
				return nil
			}
			next := []map[string]any{}
			for _, prior := range combinations {
				for _, value := range values {
					item := maps.Clone(prior)
					item[name] = value
					next = append(next, item)
				}
			}
			combinations = next
		}
		for _, spec := range combinations {
			key, err := common.Marshal(spec)
			if err != nil {
				continue
			}
			if !seen[string(key)] {
				seen[string(key)] = true
				result = append(result, spec)
			}
		}
		if len(result) > 256 {
			return nil
		}
	}
	return result
}
