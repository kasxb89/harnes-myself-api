package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type videoQuoteCandidate struct {
	ChannelID int                    `json:"channel_id"`
	Group     string                 `json:"group"`
	Model     string                 `json:"model"`
	PluginKey string                 `json:"plugin_key"`
	Version   string                 `json:"version"`
	Expr      string                 `json:"expression"`
	Ratio     float64                `json:"group_ratio"`
	Durations []int                  `json:"durations,omitempty"`
	Plugin    *jsplugin.LoadedPlugin `json:"-"`
}

type videoQuotePricing struct {
	Generation uint64                `json:"generation"`
	Candidates []videoQuoteCandidate `json:"candidates"`
	Currency   string                `json:"currency"`
	Rate       float64               `json:"rate"`
	QuotaUnit  float64               `json:"quota_unit"`
}

// QuoteVideo evaluates the same request-derived task expression used before
// submission. No task adaptor, funding session, channel key, or transport is
// available on this path. This is an estimate, never an admission or a payment.
func QuoteVideo(c *gin.Context, request *types.VideoQuoteRequest) (*types.VideoQuoteResponse, *types.VideoQuoteError) {
	invalid := &types.VideoQuoteError{Code: "invalid_quote_parameters", Message: "Invalid video quote parameters", StatusCode: http.StatusBadRequest}
	unavailable := &types.VideoQuoteError{Code: "quote_unavailable", Message: "A reliable video quote is not available for these parameters", StatusCode: http.StatusUnprocessableEntity}
	if request == nil || request.Model == "" || len(request.Model) > 255 || strings.TrimSpace(request.Model) != request.Model ||
		request.WorksCount < 1 || request.WorksCount > kitdto.MaxImageN || len(request.Segments) < 1 || len(request.Segments) > kitdto.MaxImageN ||
		request.WorksCount > kitdto.MaxImageN/len(request.Segments) {
		return nil, invalid
	}
	seen := make(map[string]bool, len(request.Segments))
	for _, segment := range request.Segments {
		if segment.ID == "" || len(segment.ID) > 128 || strings.TrimSpace(segment.ID) != segment.ID || seen[segment.ID] || segment.Parameters == nil {
			return nil, invalid
		}
		seen[segment.ID] = true
		counts := segment.ReferenceCounts
		if counts.Images < 0 || counts.Images > kitdto.MaxImageN || counts.Videos < 0 || counts.Videos > kitdto.MaxImageN || counts.Audios < 0 || counts.Audios > kitdto.MaxImageN || counts.Images+counts.Videos+counts.Audios > kitdto.MaxImageN {
			return nil, invalid
		}
		if err := validateVideoQuoteParameters(segment.Parameters, 0); err != nil {
			return nil, invalid
		}
		if err := validateVideoQuoteWireParameters(segment.Parameters); err != nil {
			return nil, invalid
		}
	}
	generation := jsplugin.DefaultRegistry.Generation()
	pricing, err := snapshotVideoQuotePricing(c, request.Model, generation)
	if err != nil || len(pricing.Candidates) == 0 {
		return nil, unavailable
	}
	pricingDigest, err := videoQuoteDigest(pricing)
	if err != nil {
		return nil, unavailable
	}
	created := time.Now()
	response := &types.VideoQuoteResponse{
		Object: "video.quote", SchemaVersion: 1, Model: request.Model, WorksCount: request.WorksCount,
		Segments: make([]types.VideoQuoteSegment, 0, len(request.Segments)), Currency: pricing.Currency,
		QuotaPerUSD: pricing.QuotaUnit, USDExchangeRate: pricing.Rate,
		CreatedAt: created.Unix(), ExpiresAt: created.Add(time.Minute).Unix(), EstimateOnly: true,
	}
	// Explicitly clear a possible inherited fetcher. Even a plugin trying to
	// fetch reference metadata receives an unavailable-host-function error.
	boundedContext, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	hookContext := jsplugin.WithFetcher(boundedContext, nil)
	normalized := make([]any, 0, len(request.Segments))
	for _, segment := range request.Segments {
		var agreement string
		var selectedFacts map[string]any
		var selectedQuota int
		durationUnavailable := false
		evaluated := make(map[string]bool)
		for _, candidate := range pricing.Candidates {
			evaluationKey := candidate.PluginKey + ":" + candidate.Model + ":" + candidate.Expr + ":" + strconv.FormatFloat(candidate.Ratio, 'g', -1, 64) + ":" + fmt.Sprint(candidate.Durations)
			if evaluated[evaluationKey] {
				continue
			}
			evaluated[evaluationKey] = true
			if candidate.PluginKey == "hailuo" {
				if videoQuoteHasAudio(segment.Parameters) {
					return nil, unavailable
				}
				for key := range segment.Parameters {
					if !slices.Contains([]string{"model", "prompt", "seconds", "duration", "size", "resolution", "ratio", "metadata", "content", "metaso_content", "metaso_resolution", "metaso_ratio", "images", "image", "reference_image", "reference_video", "video_reference", "n"}, key) {
						return nil, invalid
					}
				}
			}
			if candidate.PluginKey == "hailuo" && (segment.ReferenceCounts.Videos != 0 || segment.ReferenceCounts.Audios != 0) {
				return nil, unavailable
			}
			parameters := make(map[string]any, len(segment.Parameters)+2)
			for key, value := range segment.Parameters {
				parameters[key] = value
			}
			if value, present := parameters["model"]; present && value != request.Model {
				return nil, invalid
			}
			parameters["model"] = request.Model
			// Both supported drivers price duration/material quantities, not
			// prompt text. A placeholder lets the caller quote before writing it.
			if _, present := parameters["prompt"]; !present {
				parameters["prompt"] = "Video price estimate"
			}
			parsedBody, files, bodyErr := videoQuoteRequestBody(parameters, segment.ReferenceCounts, candidate.PluginKey)
			if bodyErr != nil {
				return nil, invalid
			}
			protocol := jsplugin.ProtocolRequestContext{
				RouteRequestContext: jsplugin.RouteRequestContext{Path: "/v1/videos", Method: http.MethodPost,
					Body: parsedBody},
				Protocol: "openai_video", Operation: "create", Model: request.Model, UpstreamModel: candidate.Model,
			}
			decoded, decodeErr := candidate.Plugin.Engine.CallPath(hookContext, "protocols", []string{"openai_video", "decodeRequest"}, protocol.JSValueFor(candidate.Plugin.Meta))
			if decodeErr != nil {
				return nil, unavailable
			}
			intent, ok := decoded.(map[string]any)
			if !ok || intent["kind"] != "submit" || intent["model"] != request.Model {
				return nil, unavailable
			}
			if origins, present := intent["originTaskIds"]; present && origins != nil {
				list, listOK := origins.([]any)
				if !listOK || len(list) != 0 {
					return nil, unavailable
				}
			}
			body, bodyOK := intent["requestBody"].(map[string]any)
			if !bodyOK || validateVideoQuoteParameters(body, 0) != nil {
				return nil, unavailable
			}
			rawFacts, factsErr := candidate.Plugin.Engine.Call(hookContext, "extractUsage", map[string]any{
				"model": request.Model, "upstreamModel": candidate.Model, "requestBody": body,
				"action": intent["action"], "usagePurpose": "facts", "files": files,
			})
			returnedFacts, factsOK := rawFacts.(map[string]any)
			facts := make(map[string]any, len(returnedFacts))
			for key, value := range returnedFacts {
				facts[key] = value
			}
			schema, _ := candidate.Plugin.Meta.UsageForModels(candidate.Model, request.Model)
			if factsErr != nil || !factsOK || validateVideoQuoteFacts(facts, schema) != nil {
				return nil, unavailable
			}
			if candidate.PluginKey == "blackrxai-video" {
				seconds, valid := videoQuoteNumber(facts["seconds"])
				if !valid || seconds <= 0 || seconds > relaycommon.MaxTaskDurationSeconds || math.Trunc(seconds) != seconds {
					return nil, invalid
				}
				if !slices.Contains(candidate.Durations, int(seconds)) {
					durationUnavailable = true
					continue
				}
			}
			// H3 reference-video settlement uses measured input_seconds. We
			// deliberately make no network request to measure an input here.
			if number, _ := videoQuoteNumber(facts["input_video_seconds"]); number > 0 {
				return nil, unavailable
			}
			snapshot := &billingexpr.BillingSnapshot{ExprString: candidate.Expr, ExprHash: billingexpr.ExprHashString(candidate.Expr),
				GroupRatio: candidate.Ratio, QuotaPerUnit: pricing.QuotaUnit, TaskUsageBilling: true, BillingTime: created.Unix()}
			result, calcErr := billingexpr.ComputeTieredQuotaWithRequest(snapshot, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
			if calcErr != nil || result.Clamp != nil || result.ActualQuotaAfterGroup < 0 || math.IsNaN(result.ActualQuotaBeforeGroup) || math.IsInf(result.ActualQuotaBeforeGroup, 0) || result.ActualQuotaBeforeGroup < 0 {
				return nil, unavailable
			}
			canonical, digestErr := videoQuoteDigest(map[string]any{"request": body, "usage": facts, "quota": result.ActualQuotaAfterGroup})
			if digestErr != nil || agreement != "" && agreement != canonical {
				return nil, unavailable
			}
			agreement, selectedFacts, selectedQuota = canonical, facts, result.ActualQuotaAfterGroup
		}
		if agreement == "" {
			if durationUnavailable {
				return nil, &types.VideoQuoteError{Code: "video_duration_unavailable", Message: "该视频型号当前不支持本次时长，请重新选择可用时长", StatusCode: http.StatusBadRequest}
			}
			return nil, unavailable
		}
		if response.OneWorkQuota > common.MaxWalletQuota-selectedQuota {
			return nil, invalid
		}
		response.OneWorkQuota += selectedQuota
		response.Segments = append(response.Segments, types.VideoQuoteSegment{ID: segment.ID, Parameters: segment.Parameters,
			Usage: selectedFacts, Quota: selectedQuota, EstimatedAmount: videoQuoteAmount(selectedQuota, pricing), ReferenceCounts: segment.ReferenceCounts})
		normalized = append(normalized, map[string]any{"id": segment.ID, "agreement": agreement, "parameters": segment.Parameters, "reference_counts": segment.ReferenceCounts})
	}
	if response.OneWorkQuota > common.MaxWalletQuota/request.WorksCount {
		return nil, invalid
	}
	response.TotalQuota = response.OneWorkQuota * request.WorksCount
	response.EstimatedOneWork = videoQuoteAmount(response.OneWorkQuota, pricing)
	response.EstimatedTotal = videoQuoteAmount(response.TotalQuota, pricing)
	// Detect changes during evaluation rather than mixing two observed runtime
	// configurations. This is not a claimed global transactional price version.
	after, afterErr := snapshotVideoQuotePricing(c, request.Model, generation)
	afterDigest, digestErr := videoQuoteDigest(after)
	if afterErr != nil || digestErr != nil || pricingDigest != afterDigest || jsplugin.DefaultRegistry.Generation() != generation {
		return nil, &types.VideoQuoteError{Code: "quote_pricing_changed", Message: "Video pricing changed; request a new quote", StatusCode: http.StatusConflict}
	}
	modelScope, _ := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
	autoGroups, _ := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups)
	binding, bindingErr := videoQuoteDigest(map[string]any{"user": c.GetInt("id"), "token": c.GetInt("token_id"),
		"user_group": common.GetContextKeyString(c, constant.ContextKeyUserGroup), "using_group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		"model_limit_enabled": common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled), "models": modelScope, "auto_groups": autoGroups})
	if bindingErr != nil || c.GetInt("id") <= 0 || c.GetInt("token_id") <= 0 {
		return nil, unavailable
	}
	response.AccountBinding = common.GenerateHMAC("video-quote-account-v1:" + binding)
	response.PricingRevision, err = videoQuoteDigest(map[string]any{"pricing": pricingDigest, "account": response.AccountBinding,
		"model": request.Model, "works_count": request.WorksCount, "segments": normalized, "total_quota": response.TotalQuota})
	if err != nil {
		return nil, unavailable
	}
	response.QuoteID = common.GenerateHMAC("video-quote-estimate-v1:" + response.PricingRevision + ":" + strconv.FormatInt(response.CreatedAt, 10))
	return response, nil
}

func snapshotVideoQuotePricing(c *gin.Context, requestedModel string, generation *jsplugin.RoutingGeneration) (videoQuotePricing, error) {
	pricing := videoQuotePricing{}
	if generation == nil {
		return pricing, fmt.Errorf("plugin routing unavailable")
	}
	pricing.Generation = generation.Number
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	groups := []string{usingGroup}
	if usingGroup == "auto" {
		groups = GetRequestAutoGroups(c, userGroup)
	}
	if usingGroup == "" || len(groups) == 0 {
		return pricing, fmt.Errorf("no usable groups")
	}
	normalizedModel := ratio_setting.RoutingMatchModelName(requestedModel)
	models := []string{requestedModel}
	if normalizedModel != "" && normalizedModel != requestedModel {
		models = append(models, normalizedModel)
	}
	abilities, err := model.GetVideoQuoteAbilities(c.Request.Context(), groups, models)
	if err != nil {
		return pricing, err
	}
	var ids []int
	eligible := make(map[string]bool)
	for _, ability := range abilities {
		if slices.Contains(groups, ability.Group) && (ability.Model == requestedModel || normalizedModel != "" && ability.Model == normalizedModel) {
			ids = append(ids, ability.ChannelId)
			eligible[ability.Group+":"+strconv.Itoa(ability.ChannelId)] = true
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 || len(ids) > 64 {
		return pricing, fmt.Errorf("no bounded eligible route")
	}
	catalogChannels, err := model.BlackrxCatalogChannelSnapshots(c.Request.Context(), ids)
	if err != nil {
		return pricing, err
	}
	// Quote routing and private catalog discovery use the same owned DB
	// snapshot. Do not read a cached Channel while its key status is changing.
	// The expression/pricing projection below deliberately excludes secrets.
	channels := make([]model.Channel, 0, len(catalogChannels))
	for _, id := range ids {
		if channel := catalogChannels[id]; channel != nil {
			channels = append(channels, model.Channel{Id: channel.Id, Type: channel.Type, Status: channel.Status,
				Models: channel.Models, Group: channel.Group, OtherSettings: channel.OtherSettings,
				Setting: channel.Setting, ModelMapping: channel.ModelMapping, ParamOverride: channel.ParamOverride, HeaderOverride: channel.HeaderOverride})
		}
	}
	catalogAvailable := make(map[int]bool)
	catalogDurations := make(map[int][]int)
	for index := range channels {
		channel := &channels[index]
		if !model.IsBlackrxCatalogChannel(channel) {
			continue
		}
		// Keep private account values out of the pricing snapshot and complete
		// discovery before acquiring the option-map read lock.
		catalogChannel := catalogChannels[channel.Id]
		mapped, mapErr := videoQuoteMappedModel(requestedModel, channel.GetModelMapping())
		if catalogChannel != nil && mapErr == nil && catalogChannel.Status == common.ChannelStatusEnabled && model.IsBlackrxCatalogChannel(catalogChannel) {
			catalogAvailable[channel.Id] = CheckBlackrxChannelModelAvailability(c.Request.Context(), catalogChannel, requestedModel, mapped) == nil
			catalogDurations[channel.Id] = model.BlackrxChannelDurations(catalogChannel, mapped)
		}
	}
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	pricing.QuotaUnit = common.QuotaPerUnit
	pricing.Currency = operation_setting.GetQuotaDisplayType()
	switch pricing.Currency {
	case operation_setting.QuotaDisplayTypeUSD:
		pricing.Rate = 1
	case operation_setting.QuotaDisplayTypeCNY:
		pricing.Rate = operation_setting.USDExchangeRate
	default:
		return pricing, fmt.Errorf("unsupported display currency")
	}
	if pricing.QuotaUnit <= 0 || math.IsInf(pricing.QuotaUnit, 0) || math.IsNaN(pricing.QuotaUnit) || pricing.Rate <= 0 || math.IsInf(pricing.Rate, 0) || math.IsNaN(pricing.Rate) {
		return pricing, fmt.Errorf("invalid money conversion")
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue
		}
		var settings kitdto.ChannelSettings
		if channel.Setting != nil && *channel.Setting != "" && common.UnmarshalJsonStr(*channel.Setting, &settings) != nil {
			return pricing, fmt.Errorf("invalid channel settings")
		}
		// Overrides can change quantities after native decoding. Until the
		// exact override pipeline can be reproduced without transport, refuse.
		for _, override := range []*string{channel.ParamOverride, channel.HeaderOverride} {
			if override != nil && strings.TrimSpace(*override) != "" && strings.TrimSpace(*override) != "{}" && strings.TrimSpace(*override) != "null" {
				return pricing, fmt.Errorf("channel overrides cannot be quoted")
			}
		}
		mapped, mapErr := videoQuoteMappedModel(requestedModel, channel.GetModelMapping())
		if mapErr != nil {
			return pricing, mapErr
		}
		if model.IsBlackrxCatalogChannel(&channel) && (!catalogAvailable[channel.Id] || len(catalogDurations[channel.Id]) == 0) {
			continue
		}
		endpointModel, declared := generation.CanonicalModel(requestedModel)
		// The normal create path decodes once before channel mapping and
		// again afterwards. A cross-model alias may have different accepted
		// durations on those two passes, so this estimate fails closed.
		if !declared || endpointModel != requestedModel || mapped != requestedModel {
			return pricing, fmt.Errorf("video endpoint model is not declared")
		}
		bindings := generation.LookupEndpointCandidates(http.MethodPost, "/v1/videos", endpointModel)
		var plugin *jsplugin.LoadedPlugin
		if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
			for _, candidate := range bindings {
				if settings.BindsTaskPlugin(candidate.Plugin.Meta.Key) {
					plugin = candidate.Plugin
					break
				}
			}
		} else {
			owner, _ := generation.GetByChannelType(channel.Type)
			for _, candidate := range bindings {
				if candidate.Plugin == owner {
					plugin = owner
					break
				}
			}
		}
		if plugin == nil || plugin.Meta.Key != "hailuo" && plugin.Meta.Key != "blackrxai-video" {
			return pricing, fmt.Errorf("unsupported native video pricing")
		}
		if !slices.Contains(plugin.Meta.Models, mapped) || !plugin.Engine.HasExport("extractUsage") {
			return pricing, fmt.Errorf("unsupported mapped video model")
		}
		expression, configured := billing_setting.ResolveTaskBillingExpr(plugin.Meta.Key, requestedModel, mapped)
		schema, _ := plugin.Meta.UsageForModels(mapped, requestedModel)
		if !configured || !billing_setting.TaskExprCompatible(expression, schema) {
			return pricing, fmt.Errorf("video expression is not configured")
		}
		for _, group := range groups {
			if !eligible[group+":"+strconv.Itoa(channel.Id)] || !model.IsChannelEnabledForGroupModel(group, requestedModel, channel.Id) {
				continue
			}
			ratio := GetUserGroupRatio(userGroup, group)
			if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
				return pricing, fmt.Errorf("invalid group ratio")
			}
			pricing.Candidates = append(pricing.Candidates, videoQuoteCandidate{ChannelID: channel.Id, Group: group,
				Model: mapped, PluginKey: plugin.Meta.Key, Version: plugin.Meta.Version, Expr: expression, Ratio: ratio, Plugin: plugin, Durations: catalogDurations[channel.Id]})
			if len(pricing.Candidates) > 64 {
				return pricing, fmt.Errorf("too many quote routes")
			}
		}
	}
	slices.SortFunc(pricing.Candidates, func(a, b videoQuoteCandidate) int {
		if a.ChannelID != b.ChannelID {
			return a.ChannelID - b.ChannelID
		}
		return strings.Compare(a.Group, b.Group)
	})
	return pricing, nil
}

func videoQuoteRequestBody(parameters map[string]any, counts types.VideoQuoteReferenceCounts, pluginKey string) (map[string]any, []any, error) {
	files := make([]any, 0, counts.Images+counts.Videos+counts.Audios)
	for _, media := range []struct {
		count                  int
		field, extension, mime string
	}{
		{counts.Images, "images", "jpg", "image/jpeg"}, {counts.Videos, "videos", "mp4", "video/mp4"}, {counts.Audios, "audios", "mp3", "audio/mpeg"},
	} {
		field := media.field
		if pluginKey == "hailuo" {
			field = map[string]string{"images": "reference_image", "videos": "reference_video", "audios": "reference_audio"}[field]
		}
		for index := range media.count {
			files = append(files, map[string]any{"field": field, "ref": jsplugin.FileReference(field, index),
				"filename": "quote." + media.extension, "mimeType": media.mime, "size": 0})
		}
	}
	if len(files) == 0 {
		return map[string]any{"kind": "json", "value": parameters}, files, nil
	}
	fields := make(map[string][]string, len(parameters))
	for key, value := range parameters {
		if key == "image_urls" || key == "video_urls" || key == "audio_urls" {
			values, ok := value.([]any)
			if !ok {
				return nil, nil, fmt.Errorf("invalid reference URL array")
			}
			for _, item := range values {
				text, ok := item.(string)
				if !ok {
					return nil, nil, fmt.Errorf("invalid reference URL")
				}
				fields[key] = append(fields[key], text)
			}
			continue
		}
		if text, ok := value.(string); ok {
			fields[key] = []string{text}
			continue
		}
		encoded, err := common.Marshal(value)
		if err != nil {
			return nil, nil, err
		}
		fields[key] = []string{string(encoded)}
	}
	return map[string]any{"kind": "multipart", "fields": fields, "files": files}, files, nil
}

func videoQuoteMappedModel(original, mapping string) (string, error) {
	if strings.TrimSpace(mapping) == "" || strings.TrimSpace(mapping) == "{}" {
		return original, nil
	}
	var values map[string]string
	if err := common.UnmarshalJsonStr(mapping, &values); err != nil {
		return "", err
	}
	current := original
	seen := map[string]bool{original: true}
	for {
		next := values[current]
		if next == "" {
			next = values[hostreasoning.BaseModelName(current)]
		}
		if next == "" || next == current {
			return current, nil
		}
		if seen[next] {
			return "", fmt.Errorf("cyclic model mapping")
		}
		seen[next], current = true, next
	}
}

// Quotes accept native JSON scalars and public URLs only. Uploaded files and
// local/base64 material need a separate owned-material contract; treating them
// as an empty reference set would silently underquote the eventual request.
func validateVideoQuoteWireParameters(parameters map[string]any) error {
	allowed := []string{"model", "prompt", "seconds", "duration", "size", "resolution", "ratio", "aspect_ratio", "metadata", "content", "metaso_content", "metaso_resolution", "metaso_ratio", "images", "image", "reference_image", "reference_video", "video_reference", "reference_audio", "audio_reference", "image_url", "image_urls", "videos", "video_urls", "audios", "audio_urls", "gen_type", "n"}
	for key := range parameters {
		if !slices.Contains(allowed, key) {
			return fmt.Errorf("unsupported video quote field")
		}
	}
	if n, present := parameters["n"]; present {
		number, ok := videoQuoteNumber(n)
		if !ok || number != 1 {
			return fmt.Errorf("one video per segment is required")
		}
	}
	if metadata, present := parameters["metadata"]; present {
		values, ok := metadata.(map[string]any)
		if !ok {
			return fmt.Errorf("video metadata must be an object")
		}
		allowedMetadata := []string{"resolution", "metaso_resolution", "ratio", "metaso_ratio", "aspect_ratio", "content", "metaso_content", "first_frame_image", "last_frame_image", "reference_image", "reference_video", "video_reference", "reference_audio", "audio_reference", "metaso"}
		for key := range values {
			if !slices.Contains(allowedMetadata, key) {
				return fmt.Errorf("unsupported video quote metadata")
			}
		}
		if metaso, present := values["metaso"]; present {
			metasoValues, ok := metaso.(map[string]any)
			if !ok {
				return fmt.Errorf("video metadata must be an object")
			}
			for key := range metasoValues {
				if key == "metaso" || !slices.Contains(allowedMetadata, key) {
					return fmt.Errorf("unsupported video quote metadata")
				}
			}
		}
	}
	return validateVideoQuoteMaterial(parameters, false)
}

func validateVideoQuoteMaterial(value any, material bool) error {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if key == "__fileRef" || key == "file_id" || key == "filename" || key == "file_path" || key == "path" || key == "base64" || key == "b64_json" || key == "referenceFiles" {
				return fmt.Errorf("uploaded material cannot be quoted")
			}
			isMaterial := material || slices.Contains([]string{"url", "image_url", "image_urls", "images", "image", "reference_image", "reference_video", "video_reference", "reference_audio", "audio_reference", "first_frame_image", "last_frame_image", "video_url", "video_urls", "videos", "audio_url", "audio_urls", "audios"}, key)
			if err := validateVideoQuoteMaterial(item, isMaterial); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range value {
			if err := validateVideoQuoteMaterial(item, material); err != nil {
				return err
			}
		}
	case string:
		if material {
			parsed, err := url.Parse(value)
			if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || strings.ContainsAny(value, "\\\r\n\t ") {
				return fmt.Errorf("quote references must be HTTP URLs")
			}
		}
	}
	return nil
}

func videoQuoteHasAudio(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if key == "reference_audio" || key == "audio_reference" || key == "audio_url" || key == "audio_urls" || key == "audios" || key == "type" && item == "audio_url" {
				return true
			}
			if videoQuoteHasAudio(item) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if videoQuoteHasAudio(item) {
				return true
			}
		}
	}
	return false
}

func validateVideoQuoteParameters(value any, depth int) error {
	if depth > 24 {
		return fmt.Errorf("video parameters are too deeply nested")
	}
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
			limit := 0
			switch normalized {
			case "duration", "durationseconds", "second", "seconds":
				limit = relaycommon.MaxTaskDurationSeconds
			case "n", "count", "imagecount", "samplecount", "batchcount", "numimages":
				limit = kitdto.MaxImageN
			}
			if limit != 0 {
				number, ok := videoQuoteNumber(item)
				if !ok || number < 0 || number > float64(limit) {
					return fmt.Errorf("invalid video quantity")
				}
			}
			if err := validateVideoQuoteParameters(item, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(value) > kitdto.MaxImageN {
			return fmt.Errorf("video collection exceeds host limit")
		}
		for _, item := range value {
			if err := validateVideoQuoteParameters(item, depth+1); err != nil {
				return err
			}
		}
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("non-finite video quantity")
		}
	}
	return nil
}

func validateVideoQuoteFacts(facts map[string]any, schema map[string]jsplugin.UsageFieldSchema) error {
	for key, field := range schema {
		value, present := facts[key]
		if !present {
			return fmt.Errorf("missing usage fact")
		}
		if len(field.Enum) != 0 {
			text, ok := value.(string)
			if !ok || !slices.Contains(field.Enum, text) {
				return fmt.Errorf("invalid usage enum")
			}
			continue
		}
		if field.Type == "boolean" {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("invalid usage flag")
			}
			continue
		}
		// The supported native video drivers have deterministic seconds/count
		// facts. Token/credit estimation requires measured provider data.
		limit := relaycommon.MaxTaskDurationSeconds
		if field.Unit == "count" {
			limit = kitdto.MaxImageN
		} else if field.Unit != "second" {
			return fmt.Errorf("usage quantity is not independently quotable")
		}
		number, ok := videoQuoteNumber(value)
		if !ok || number < 0 || number > float64(limit) {
			return fmt.Errorf("invalid usage quantity")
		}
		facts[key] = number
	}
	return nil
}

func videoQuoteNumber(value any) (float64, bool) {
	var number float64
	switch value := value.(type) {
	case float64:
		number = value
	case int64:
		number = float64(value)
	case int:
		number = float64(value)
	case string:
		var err error
		number, err = strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func videoQuoteDigest(value any) (string, error) {
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func videoQuoteAmount(quota int, pricing videoQuotePricing) string {
	return decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(pricing.QuotaUnit)).Mul(decimal.NewFromFloat(pricing.Rate)).String()
}
