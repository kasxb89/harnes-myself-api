package service

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"golang.org/x/sync/singleflight"
)

const (
	blackrxCatalogTimeout = 8 * time.Second
	blackrxCatalogRetry   = 15 * time.Second
	blackrxCatalogMaxBody = 1024 * 1024
)

var blackrxCatalogFlights singleflight.Group
var blackrxCatalogSlots = make(chan struct{}, 4)

type BlackrxModelAvailabilityError struct {
	Code       string
	HTTPStatus int
}

func (err *BlackrxModelAvailabilityError) Error() string {
	if err.Code == "model_not_available" {
		return "当前上游账号已不再提供该视频型号，本次未提交生成、未扣费"
	}
	if err.Code == "video_duration_unavailable" {
		return "该视频型号当前不支持本次时长，本次未提交生成、未扣费，请重新选择可用时长"
	}
	return "暂时无法核实上游视频型号目录，本次未提交生成、未扣费，请稍后重试"
}

// CheckBlackrxModelAvailability is a submit-only fence. Call it before quota
// reservation, admission, or provider POST, never from historical task queries.
func CheckBlackrxModelAvailability(ctx context.Context, channel *model.Channel, selectedKey, requestedModel, upstreamModel string) error {
	if !model.IsBlackrxCatalogChannel(channel) {
		return nil
	}
	if upstreamModel == "" {
		upstreamModel = requestedModel
	}
	snapshot := refreshBlackrxAccountCatalog(ctx, channel, selectedKey)
	if snapshot.VerifiedAt.IsZero() || time.Since(snapshot.VerifiedAt) >= model.BlackrxCatalogTTL {
		return &BlackrxModelAvailabilityError{Code: "upstream_catalog_unverified", HTTPStatus: http.StatusServiceUnavailable}
	}
	if !slices.Contains(snapshot.Models, upstreamModel) {
		return &BlackrxModelAvailabilityError{Code: "model_not_available", HTTPStatus: http.StatusBadRequest}
	}
	return nil
}

// CheckBlackrxVideoDuration rejects a known live-policy conflict without
// changing an explicit duration. Missing legacy policy fields preserve the
// installed plugin's existing request validation.
func CheckBlackrxVideoDuration(ctx context.Context, channel *model.Channel, selectedKey, requestedModel, upstreamModel string, seconds int) error {
	if !model.IsBlackrxCatalogChannel(channel) {
		return nil
	}
	if err := CheckBlackrxModelAvailability(ctx, channel, selectedKey, requestedModel, upstreamModel); err != nil {
		return err
	}
	if upstreamModel == "" {
		upstreamModel = requestedModel
	}
	if seconds <= 0 || seconds > relaycommon.MaxTaskDurationSeconds {
		return &BlackrxModelAvailabilityError{Code: "video_duration_unavailable", HTTPStatus: http.StatusBadRequest}
	}
	allowed, known := model.BlackrxAccountDurations(channel, strings.TrimSpace(selectedKey), upstreamModel)
	if known && !slices.Contains(allowed, seconds) {
		return &BlackrxModelAvailabilityError{Code: "video_duration_unavailable", HTTPStatus: http.StatusBadRequest}
	}
	return nil
}

// A quotation can use any eligible account; a chosen submit key is checked
// separately by CheckBlackrxModelAvailability before sending.
func CheckBlackrxChannelModelAvailability(ctx context.Context, channel *model.Channel, requestedModel, upstreamModel string) error {
	if !model.IsBlackrxCatalogChannel(channel) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, blackrxCatalogTimeout)
	defer cancel()
	keys := model.BlackrxEnabledKeys(channel)
	unverified := len(keys) == 0
	for _, key := range keys {
		err := CheckBlackrxModelAvailability(ctx, channel, key, requestedModel, upstreamModel)
		if err == nil {
			return nil
		}
		unverified = unverified || err.(*BlackrxModelAvailabilityError).Code == "upstream_catalog_unverified"
	}
	if unverified {
		return &BlackrxModelAvailabilityError{Code: "upstream_catalog_unverified", HTTPStatus: http.StatusServiceUnavailable}
	}
	return &BlackrxModelAvailabilityError{Code: "model_not_available", HTTPStatus: http.StatusBadRequest}
}

func refreshBlackrxAccountCatalog(ctx context.Context, channel *model.Channel, key string) model.BlackrxCatalogSnapshot {
	key = strings.TrimSpace(key)
	snapshot := model.ReadBlackrxCatalog(channel, key)
	if !snapshot.VerifiedAt.IsZero() && time.Since(snapshot.VerifiedAt) < model.BlackrxCatalogTTL || !snapshot.AttemptedAt.IsZero() && time.Since(snapshot.AttemptedAt) < blackrxCatalogRetry {
		return snapshot
	}
	identity := model.BlackrxCatalogIdentity(channel, key)
	result := blackrxCatalogFlights.DoChan(identity, func() (any, error) {
		// A second reader may have refreshed between the first read and DoChan.
		current := model.ReadBlackrxCatalog(channel, key)
		if !current.AttemptedAt.IsZero() && time.Since(current.AttemptedAt) < blackrxCatalogRetry {
			return nil, nil
		}
		select {
		case blackrxCatalogSlots <- struct{}{}:
			defer func() { <-blackrxCatalogSlots }()
		case <-ctx.Done():
			return nil, nil
		}
		models, policies, _ := fetchBlackrxAccountModelIDs(ctx, channel, key)
		model.RecordBlackrxCatalogPolicies(channel, key, models, policies, time.Now())
		return nil, nil
	})
	select {
	case <-result:
	case <-ctx.Done():
	}
	return model.ReadBlackrxCatalog(channel, key)
}

func fetchBlackrxAccountModelIDs(ctx context.Context, channel *model.Channel, key string) ([]string, map[string]model.BlackrxDurationPolicy, error) {
	invalid := errors.New("upstream video model catalog could not be verified")
	var setting dto.ChannelSettings
	if key == "" || channel.Setting == nil || common.UnmarshalJsonStr(*channel.Setting, &setting) != nil {
		return nil, nil, invalid
	}
	// Arbitrary overrides are not interpreted by this discovery path. Never
	// accidentally verify a different account than the selected submit key.
	if channel.HeaderOverride != nil {
		raw := strings.TrimSpace(*channel.HeaderOverride)
		if raw != "" && raw != "{}" && raw != "null" {
			return nil, nil, invalid
		}
	}
	base, err := url.Parse(strings.TrimRight(channel.GetBaseURL(), "/"))
	if err != nil || base.Scheme != "https" && base.Scheme != "http" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, nil, invalid
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/v1/models"
	base.RawPath = ""
	ctx, cancel := context.WithTimeout(ctx, blackrxCatalogTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, nil, invalid
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept", "application/json")
	baseClient, err := GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		return nil, nil, invalid
	}
	client := *baseClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, invalid
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, invalid
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, blackrxCatalogMaxBody+1))
	if err != nil || len(body) > blackrxCatalogMaxBody || !utf8.Valid(body) {
		return nil, nil, invalid
	}
	var envelope struct {
		Data *[]struct {
			ID             string            `json:"id"`
			Durations      common.RawMessage `json:"durations"`
			DurationPolicy common.RawMessage `json:"duration_policy"`
		} `json:"data"`
	}
	if common.Unmarshal(body, &envelope) != nil || envelope.Data == nil || len(*envelope.Data) == 0 || len(*envelope.Data) > 2048 {
		return nil, nil, invalid
	}
	models := make([]string, 0, len(*envelope.Data))
	policies := make(map[string]model.BlackrxDurationPolicy)
	seen := make(map[string]bool)
	for _, row := range *envelope.Data {
		if row.ID == "" || len(row.ID) > 255 || strings.TrimSpace(row.ID) != row.ID || strings.ContainsFunc(row.ID, unicode.IsControl) || seen[row.ID] {
			return nil, nil, invalid
		}
		seen[row.ID] = true
		if len(row.Durations) != 0 || len(row.DurationPolicy) != 0 {
			policy, err := parseBlackrxDurationPolicy(row.Durations, row.DurationPolicy)
			if err != nil {
				return nil, nil, invalid
			}
			policies[row.ID] = policy
		}
		models = append(models, row.ID)
	}
	slices.Sort(models)
	return slices.Compact(models), policies, nil
}

func parseBlackrxDurationPolicy(rawDurations, rawPolicy common.RawMessage) (model.BlackrxDurationPolicy, error) {
	invalid := errors.New("invalid upstream video duration policy")
	var durations []int
	var wire struct {
		Mode    string `json:"mode"`
		Values  []int  `json:"values"`
		Min     int    `json:"min"`
		Max     int    `json:"max"`
		Step    int    `json:"step"`
		Default int    `json:"default"`
	}
	if common.Unmarshal(rawDurations, &durations) != nil || common.Unmarshal(rawPolicy, &wire) != nil || len(durations) == 0 || len(durations) > relaycommon.MaxTaskDurationSeconds {
		return model.BlackrxDurationPolicy{}, invalid
	}
	var values []int
	switch wire.Mode {
	case "fixed":
		values = wire.Values
	case "range":
		if wire.Min < 1 || wire.Max > relaycommon.MaxTaskDurationSeconds || wire.Max < wire.Min || wire.Step < 1 || wire.Step > relaycommon.MaxTaskDurationSeconds {
			return model.BlackrxDurationPolicy{}, invalid
		}
		for seconds := wire.Min; seconds <= wire.Max; seconds += wire.Step {
			values = append(values, seconds)
		}
	default:
		return model.BlackrxDurationPolicy{}, invalid
	}
	if len(values) == 0 || len(values) > relaycommon.MaxTaskDurationSeconds {
		return model.BlackrxDurationPolicy{}, invalid
	}
	slices.Sort(values)
	slices.Sort(durations)
	for index, seconds := range values {
		if seconds < 1 || seconds > relaycommon.MaxTaskDurationSeconds || index > 0 && seconds == values[index-1] {
			return model.BlackrxDurationPolicy{}, invalid
		}
	}
	if !slices.Equal(values, durations) || !slices.Contains(values, wire.Default) {
		return model.BlackrxDurationPolicy{}, invalid
	}
	return model.BlackrxDurationPolicy{Values: values, Default: wire.Default}, nil
}

// RefreshBlackrxCatalog performs bounded, read-only discovery for public
// catalogs. A failed discovery leaves display configuration intact; submission
// still requires a recent verified account-specific result.
func RefreshBlackrxCatalog(ctx context.Context) {
	if model.DB == nil {
		return
	}
	var channels []model.Channel
	if model.DB.Where("type = ? AND status = ?", constant.ChannelTypeTaskPlugin, common.ChannelStatusEnabled).Find(&channels).Error != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, blackrxCatalogTimeout)
	defer cancel()
	var workers sync.WaitGroup
	accounts := 0
	for index := range channels {
		channel := &channels[index]
		if !model.IsBlackrxCatalogChannel(channel) {
			continue
		}
		for _, key := range model.BlackrxEnabledKeys(channel) {
			if accounts >= 256 || ctx.Err() != nil {
				break
			}
			accounts++
			workers.Go(func() { refreshBlackrxAccountCatalog(ctx, channel, key) })
		}
	}
	workers.Wait()
}

// FilterUnavailableCatalogModels preserves a same-name model if any enabled
// route in the requested groups is usable, including another provider. It does
// not filter historical task retrieval or change persisted channel abilities.
func FilterUnavailableCatalogModels(names []string, groups []string) []string {
	if len(names) == 0 || model.DB == nil {
		return names
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	var abilities []model.Ability
	if model.DB.Where("enabled = ?", true).Find(&abilities).Error != nil {
		return names
	}
	var channelIDs []int
	for _, ability := range abilities {
		if wanted[ability.Model] && (len(groups) == 0 || slices.Contains(groups, ability.Group)) {
			channelIDs = append(channelIDs, ability.ChannelId)
		}
	}
	channels, err := model.BlackrxCatalogChannelSnapshots(context.Background(), channelIDs)
	if err != nil {
		return names
	}
	allowed := make(map[string]bool)
	seen := make(map[string]bool)
	for _, ability := range abilities {
		if !wanted[ability.Model] || len(groups) > 0 && !slices.Contains(groups, ability.Group) {
			continue
		}
		channel := channels[ability.ChannelId]
		if channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		seen[ability.Model] = true
		allowed[ability.Model] = allowed[ability.Model] || model.BlackrxCatalogAllowsModel(channel, ability.Model)
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if !seen[name] || allowed[name] {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

// NarrowBlackrxPricingParameters copies only display metadata. The shared
// pricing cache, billing expressions, and existing request values are untouched.
func NarrowBlackrxPricingParameters(pricing []model.Pricing, groups []string) []model.Pricing {
	if len(pricing) == 0 || model.DB == nil {
		return pricing
	}
	wanted := make(map[string]bool, len(pricing))
	for _, row := range pricing {
		wanted[row.ModelName] = true
	}
	var abilities []model.Ability
	if model.DB.Where("enabled = ?", true).Find(&abilities).Error != nil {
		return pricing
	}
	var channelIDs []int
	for _, ability := range abilities {
		if wanted[ability.Model] && (len(groups) == 0 || slices.Contains(groups, ability.Group)) {
			channelIDs = append(channelIDs, ability.ChannelId)
		}
	}
	channels, err := model.BlackrxCatalogChannelSnapshots(context.Background(), channelIDs)
	if err != nil {
		return pricing
	}
	durations := make(map[string][]int)
	otherProvider := make(map[string]bool)
	for _, ability := range abilities {
		if !wanted[ability.Model] || len(groups) > 0 && !slices.Contains(groups, ability.Group) {
			continue
		}
		channel := channels[ability.ChannelId]
		if channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		if !model.IsBlackrxCatalogChannel(channel) {
			otherProvider[ability.Model] = true
			continue
		}
		if upstream, valid := model.BlackrxCatalogUpstreamModel(channel, ability.Model); valid {
			durations[ability.Model] = append(durations[ability.Model], model.BlackrxChannelDurations(channel, upstream)...)
		}
	}
	result := slices.Clone(pricing)
	for index := range result {
		row := &result[index]
		seconds := durations[row.ModelName]
		if otherProvider[row.ModelName] || len(seconds) == 0 {
			continue
		}
		slices.Sort(seconds)
		seconds = slices.Compact(seconds)
		properties, valid := row.Parameters["properties"].(map[string]any)
		if !valid {
			continue
		}
		priorSeconds, valid := properties["seconds"].(map[string]any)
		if !valid {
			continue
		}
		// Intersect again with the actual published schema, which may be
		// narrower than this host's static compatibility schema.
		originalEnum, valid := priorSeconds["enum"].([]int)
		if !valid {
			continue
		}
		seconds = slices.DeleteFunc(seconds, func(value int) bool { return !slices.Contains(originalEnum, value) })
		if len(seconds) == 0 {
			continue
		}
		row.Parameters = maps.Clone(row.Parameters)
		properties = maps.Clone(properties)
		row.Parameters["properties"] = properties
		narrowed := maps.Clone(priorSeconds)
		properties["seconds"] = narrowed
		narrowed["enum"], narrowed["minimum"], narrowed["maximum"] = seconds, seconds[0], seconds[len(seconds)-1]
		if originalDefault, ok := priorSeconds["default"].(int); !ok || !slices.Contains(seconds, originalDefault) {
			narrowed["default"] = seconds[0]
		}
	}
	return result
}

// FetchBlackrxChannelModels powers the existing discovery UI without offering
// unimplemented, unpriced new upstream IDs or deleting historical plugin IDs.
func FetchBlackrxChannelModels(ctx context.Context, channel *model.Channel) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, blackrxCatalogTimeout)
	defer cancel()
	if !model.IsBlackrxCatalogChannel(channel) {
		return nil, errors.New("not a Blackrx video channel")
	}
	plugin, ok := jsplugin.DefaultRegistry.Get("blackrxai-video")
	if !ok {
		return nil, errors.New("Blackrx video plugin is not registered")
	}
	var models []string
	for _, key := range model.BlackrxEnabledKeys(channel) {
		snapshot := refreshBlackrxAccountCatalog(ctx, channel, key)
		if snapshot.VerifiedAt.IsZero() || time.Since(snapshot.VerifiedAt) >= model.BlackrxCatalogTTL {
			return nil, &BlackrxModelAvailabilityError{Code: "upstream_catalog_unverified", HTTPStatus: http.StatusServiceUnavailable}
		}
		for _, name := range snapshot.Models {
			if values, known := model.BlackrxAccountDurations(channel, key, name); slices.Contains(plugin.Meta.Models, name) && (!known || len(values) > 0) {
				models = append(models, name)
			}
		}
	}
	if len(models) == 0 {
		return nil, errors.New("no configured Blackrx video models could be verified")
	}
	slices.Sort(models)
	return slices.Compact(models), nil
}
