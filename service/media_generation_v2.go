package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type MediaGenerationRequest struct {
	ClientOperationID string                   `json:"client_operation_id"`
	Model             string                   `json:"model"`
	ModelVersion      string                   `json:"model_version"`
	Intent            ycapi.MediaRuntimeIntent `json:"intent"`
	Parameters        map[string]any           `json:"parameters"`
	Assets            map[string][]string      `json:"assets"`
}

type MediaGenerationFrozen struct {
	Request            MediaGenerationRequest `json:"request"`
	Parameters         map[string]any         `json:"parameters"`
	Facts              map[string]any         `json:"facts"`
	CostCNY            string                 `json:"cost_cny"`
	DryRunSHA256       string                 `json:"dry_run_sha256"`
	RetailPolicySHA256 string                 `json:"retail_policy_sha256,omitempty"`
}

type MediaGenerationQuoteReply struct {
	QuoteID           string                   `json:"quote_id"`
	ClientOperationID string                   `json:"client_operation_id"`
	RequestSHA256     string                   `json:"request_sha256"`
	Model             string                   `json:"model"`
	ModelVersion      string                   `json:"model_version"`
	Intent            ycapi.MediaRuntimeIntent `json:"intent"`
	Parameters        map[string]any           `json:"parameters"`
	Assets            map[string][]string      `json:"assets"`
	RetailCNY         string                   `json:"retail_cny"`
	ExpiresAt         int64                    `json:"expires_at"`
	OperationState    string                   `json:"operation_state"`
}

func MediaGenerationDigest(v any) string {
	raw, _ := common.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Configuration versions authorize execution, not a second user operation.
// Everything that expresses the caller's content/material intent stays hashed.
func MediaGenerationRequestDigest(request MediaGenerationRequest) string {
	request.ModelVersion = ""
	return MediaGenerationDigest(request)
}
func mediaGenerationContractSHA(contract ycapi.MediaContract) string {
	return MediaGenerationDigest(contract)
}

func mediaGenerationRecord(name string) (model.PublishedModelConfiguration, ycapi.MediaContract, error) {
	records, err := model.CurrentPublishedModelConfigurations()
	if err != nil {
		return model.PublishedModelConfiguration{}, ycapi.MediaContract{}, err
	}
	record, ok := records[name]
	contract, err := validateMediaGenerationRecord(name, record, ok)
	return record, contract, err
}

func validateMediaGenerationRecord(name string, record model.PublishedModelConfiguration, exists bool) (ycapi.MediaContract, error) {
	contract, known := ycapi.LookupMediaContract(name)
	if !exists || !known || !record.Definition.Enabled || record.Definition.MediaExecution == nil || !contract.Available || model.ValidateModelMediaExecutionConfiguration(record.Definition) != nil || record.Definition.MediaExecution.ContractSHA256 != mediaGenerationContractSHA(contract) {
		return contract, errors.New("model_not_available")
	}
	return contract, nil
}

func MediaGenerationModels(c *gin.Context) ([]map[string]any, error) {
	records, err := model.CurrentPublishedModelConfigurations()
	if err != nil {
		return nil, err
	}
	versions := model.ModelExecutionConfigurationVersions(records)
	plugin, pluginReady := jsplugin.DefaultRegistry.Get("ycapi")
	if !pluginReady || plugin.Meta.Version != "1.0.4" {
		return []map[string]any{}, nil
	}
	models := []map[string]any{}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	groups := []string{group}
	if group == "auto" {
		groups = GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	}
	for id, record := range records {
		contract, e := validateMediaGenerationRecord(id, record, true)
		if e != nil {
			continue
		}
		expression, priced := billing_setting.ResolveTaskBillingExpr("ycapi", id, id)
		usageSchema, _ := plugin.Meta.UsageForModels(id)
		if !priced || !billing_setting.TaskExprCompatible(expression, usageSchema) {
			continue
		}
		if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
			limits, _ := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyTokenModelLimit)
			if !limits[id] {
				continue
			}
		}
		allowed := false
		for _, r := range record.Definition.Routes {
			if r.Enabled && slices.Contains(groups, r.Group) && model.IsChannelEnabledForGroupModel(r.Group, id, r.ChannelID) {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}
		schema, e := mediaGenerationConfiguredSchema(contract, record.Definition.Parameters)
		if e != nil {
			continue
		}
		fields, e := mediaGenerationCatalogAssetFields(contract, schema)
		if e != nil {
			continue
		}
		intents := mediaGenerationCatalogIntents(contract.Kind, record.Definition.MediaExecution.Intents, fields)
		if len(intents) == 0 {
			continue
		}
		models = append(models, map[string]any{"id": id, "display_name": mediaGenerationDisplayName(record.Definition.DisplayName, contract, fields, intents), "kind": contract.Kind, "description": strings.Join(contract.Capabilities, " · "), "enabled": true, "model_version": versions[id], "parameters_schema": schema, "asset_fields": fields, "intents": intents, "pricing": map[string]any{"display": "按所选规格显示费用", "quote_required": true}})
	}
	slices.SortFunc(models, func(a, b map[string]any) int { return strings.Compare(a["id"].(string), b["id"].(string)) })
	return models, nil
}

func mediaGenerationDisplayName(name string, contract ycapi.MediaContract, fields, intents []map[string]any) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = map[string]string{"image": "图片模型", "video": "视频模型"}[contract.Kind]
	}
	qualified, material := map[string]bool{}, map[string]bool{}
	for _, intent := range intents {
		if id, ok := intent["id"].(string); ok {
			qualified[id] = true
		}
	}
	for _, field := range fields {
		if kind, ok := field["media_kind"].(string); ok {
			material[kind] = true
		}
	}
	imageGeneration := material["image"] && (qualified["reference"] || qualified["frames"])
	label := ""
	if contract.Kind == "image" {
		switch {
		case qualified["text"] && material["image"] && (qualified["reference"] || qualified["transform"]):
			label = "图生图都可"
		case qualified["text"]:
			label = "只文生图"
		case material["image"] && (qualified["reference"] || qualified["transform"]):
			label = "图片编辑"
		}
	} else if contract.Kind == "video" {
		switch {
		case contract.NativeID == "kling-v3-motion-control" && qualified["transform"] && material["image"] && material["video"]:
			label = "动作复刻"
		case contract.NativeID == "kling-avatar" && qualified["transform"] && material["image"] && material["audio"]:
			label = "数字人视频"
		case slices.Contains([]string{"kling-lip-sync", "minimax-h3-lipsync"}, contract.NativeID) && qualified["transform"] && material["audio"] && (material["image"] || material["video"]):
			label = "口型同步"
		case qualified["text"] && imageGeneration:
			label = "文生／图生视频"
		case imageGeneration:
			label = "图生视频"
		case qualified["text"] && material["video"] && qualified["transform"]:
			label = "文生／视频编辑"
		case material["video"] && qualified["transform"]:
			label = "视频编辑／复刻"
		case qualified["text"] && material["video"] && qualified["reference"]:
			label = "文生／视频参考"
		case qualified["text"]:
			label = "只文生视频"
		}
	}
	if label == "" || strings.Contains(name, label) {
		return name
	}
	return name + "（" + label + "）"
}

func mediaGenerationCatalogAssetFields(contract ycapi.MediaContract, schema map[string]any) ([]map[string]any, error) {
	properties := schema["properties"].(map[string]any)
	rawFields, err := ycapi.AssetFields(contract)
	if err != nil {
		return nil, err
	}
	fields := []map[string]any{}
	for _, f := range rawFields {
		declaration, present := properties[f.Name].(map[string]any)
		if !present {
			continue
		}
		maximum, minimum := 64, f.Minimum
		if f.Maximum != nil {
			maximum = *f.Maximum
		}
		if bound, exists := declaration["maxItems"].(float64); exists {
			maximum = min(maximum, int(bound))
		}
		if bound, exists := declaration["minItems"].(float64); exists {
			minimum = max(minimum, int(bound))
		}
		if slices.Contains(anySlice(schema["required"]), any(f.Name)) {
			minimum = max(minimum, 1)
		}
		if maximum < 1 {
			continue
		}
		if minimum > maximum {
			return nil, errors.New("configured media asset bounds unavailable")
		}
		mimes := f.MIMETypes
		if len(mimes) == 0 {
			mimes = map[string][]string{"image": {"image/png", "image/jpeg", "image/webp", "image/gif"}, "video": {"video/mp4", "video/webm"}, "audio": {"audio/mpeg", "audio/wav", "audio/mp4"}}[f.MediaKind]
		}
		fields = append(fields, map[string]any{"name": f.Name, "media_kind": f.MediaKind, "native_shape": f.Shape, "min_items": minimum, "max_items": maximum, "mime_types": mimes, "constraints": contract.Parameters[f.Name].Native})
	}
	return fields, nil
}

func mediaGenerationCatalogIntents(kind string, configured []string, fields []map[string]any) []map[string]any {
	intents := []map[string]any{}
	for _, intent := range configured {
		if intent != "text" && len(fields) == 0 || intent == "frames" && kind != "video" {
			continue
		}
		intents = append(intents, map[string]any{"id": intent, "label": map[string]string{"text": "文字生成", "reference": "参考素材生成", "frames": "首尾帧／关键帧", "transform": "素材编辑／复刻"}[intent], "requires_assets": intent != "text"})
	}
	return intents
}

// Existing administrator controls restrict the native contract. Native field
// names and wire types stay unchanged; controls cannot grant a new protocol.
func mediaGenerationControlField(contract ycapi.MediaContract, name string) string {
	if _, exists := contract.Parameters[name]; exists {
		return name
	}
	aliases := map[string][]string{
		"seconds": {"duration"}, "duration": {"seconds"},
		"n": {"image_count", "count"}, "count": {"image_count", "n"}, "image_count": {"n", "count"},
		"ratio": {"aspect_ratio"}, "aspect_ratio": {"ratio"},
		"reference_images": {"images"}, "input_images": {"images"},
		"reference_videos": {"videos"}, "reference_audios": {"audios"},
	}
	for _, candidate := range aliases[name] {
		if _, exists := contract.Parameters[candidate]; exists {
			return candidate
		}
	}
	return ""
}

func validateMediaGenerationControls(contract ycapi.MediaContract, controls, params map[string]any) error {
	if controls == nil {
		return nil
	}
	values := map[string]any{}
	fields, _ := controls["properties"].(map[string]any)
	for name := range fields {
		native := mediaGenerationControlField(contract, name)
		if native == "" {
			return errors.New("invalid configured media control")
		}
		if value, exists := params[native]; exists {
			values[name] = value
		}
	}
	// The generic validator consumes JSON number/array shapes, including native
	// numeric enum strings. No reference is removed or shortened by validation.
	raw, err := common.Marshal(values)
	if err != nil || common.Unmarshal(raw, &values) != nil {
		return errors.New("invalid configured media control")
	}
	return model.ModelParameterValues(controls, values)
}

func mediaGenerationConfiguredSchema(contract ycapi.MediaContract, controls map[string]any) (map[string]any, error) {
	schema, ok := ycapi.MediaContractSchema(contract.ID)
	if !ok {
		return nil, errors.New("model_not_available")
	}
	// Native declarations retain the supplier's batch range. Executable video
	// controls stay single-item until the billing facts meter a video batch.
	if contract.Kind == "video" {
		if count, exists := schema["properties"].(map[string]any)["count"].(map[string]any); exists {
			count["maximum"] = float64(1)
		}
	}
	if controls == nil {
		return schema, nil
	}
	raw, err := common.Marshal(schema)
	if err != nil || common.Unmarshal(raw, &schema) != nil {
		return nil, errors.New("invalid media schema")
	}
	properties := schema["properties"].(map[string]any)
	configured, _ := controls["properties"].(map[string]any)
	for name, value := range configured {
		native := mediaGenerationControlField(contract, name)
		field, exists := properties[native].(map[string]any)
		if native == "" || !exists {
			return nil, errors.New("invalid configured media control")
		}
		if value == false {
			delete(properties, native)
			continue
		}
		control, valid := value.(map[string]any)
		if !valid {
			return nil, errors.New("invalid configured media control")
		}
		field, err = mediaGenerationNarrowField(field, control)
		if err != nil {
			return nil, err
		}
		if field["maxItems"] == float64(0) {
			delete(properties, native)
		} else {
			properties[native] = field
		}
	}
	// Top-level disabled controls leave no required or asset declaration behind.
	required := []any{}
	for _, list := range []any{schema["required"], controls["required"]} {
		for _, rawName := range anySlice(list) {
			name, _ := rawName.(string)
			if native := mediaGenerationControlField(contract, name); native != "" {
				name = native
			}
			if _, exists := properties[name]; exists && !slices.Contains(required, any(name)) {
				required = append(required, name)
			}
		}
	}
	schema["required"] = required
	assets := []any{}
	for _, rawAsset := range anySlice(schema["x-media-assets"]) {
		asset, _ := rawAsset.(map[string]any)
		field, present := properties[fmt.Sprint(asset["name"])].(map[string]any)
		if !present {
			continue
		}
		for property, key := range map[string]string{"minItems": "min", "maxItems": "max"} {
			if bound, exists := field[property]; exists {
				asset[key] = bound
			}
		}
		if slices.Contains(required, asset["name"]) {
			minimum, _ := asset["min"].(float64)
			asset["min"] = max(minimum, float64(1))
		}
		assets = append(assets, asset)
	}
	schema["x-media-assets"] = assets
	if alternatives, explicit := controls["oneOf"].([]any); explicit {
		branches := []any{}
		for _, rawBranch := range alternatives {
			branch, valid := rawBranch.(map[string]any)
			if !valid {
				return nil, errors.New("invalid configured media combination")
			}
			fields := map[string]any{}
			usable := true
			for name, value := range branch["properties"].(map[string]any) {
				native := mediaGenerationControlField(contract, name)
				if native == "" {
					return nil, errors.New("invalid configured media control")
				}
				field, present := properties[native].(map[string]any)
				if !present {
					continue
				}
				if value == false {
					fields[native] = false
					continue
				}
				control, valid := value.(map[string]any)
				if !valid {
					return nil, errors.New("invalid configured media combination")
				}
				narrowed, err := mediaGenerationNarrowField(field, control)
				if err != nil {
					usable = false
					break
				}
				fields[native] = narrowed
			}
			branchRequired := []any{}
			for _, rawName := range anySlice(branch["required"]) {
				name, _ := rawName.(string)
				name = mediaGenerationControlField(contract, name)
				if _, present := properties[name]; !present || fields[name] == false {
					usable = false
					break
				}
				branchRequired = append(branchRequired, name)
			}
			if usable {
				branches = append(branches, map[string]any{"type": "object", "properties": fields, "required": branchRequired})
			}
		}
		if len(branches) == 0 {
			return nil, errors.New("configured media combinations unavailable")
		}
		schema["oneOf"] = branches
	}
	return schema, nil
}

// A configured field intersects the native menu/range; it never changes the
// provider's value type, and invalid constants cannot manufacture a new option.
func mediaGenerationNarrowField(native, control map[string]any) (map[string]any, error) {
	field := map[string]any{}
	for key, value := range native {
		field[key] = value
	}
	convert := func(value any) any {
		if native["type"] == "string" {
			return fmt.Sprint(value)
		}
		return value
	}
	allowed := func(declaration map[string]any, value any) bool {
		value = convert(value)
		if native["type"] == "integer" {
			number, valid := value.(float64)
			if !valid || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
				return false
			}
		}
		if native["type"] == "boolean" {
			if _, valid := value.(bool); !valid {
				return false
			}
		}
		if constant, exists := declaration["const"]; exists && !reflect.DeepEqual(value, convert(constant)) {
			return false
		}
		if menu, exists := declaration["enum"].([]any); exists && !slices.ContainsFunc(menu, func(item any) bool { return reflect.DeepEqual(value, convert(item)) }) {
			return false
		}
		if number, err := strconv.ParseFloat(fmt.Sprint(value), 64); err == nil {
			if minimum, exists := declaration["minimum"].(float64); exists && number < minimum {
				return false
			}
			if maximum, exists := declaration["maximum"].(float64); exists && number > maximum {
				return false
			}
		}
		return true
	}
	for _, bound := range []string{"minimum", "minItems", "maximum", "maxItems"} {
		if native["type"] == "string" && (bound == "minimum" || bound == "maximum") {
			continue
		}
		if number, exists := control[bound].(float64); exists {
			old, bounded := field[bound].(float64)
			if !bounded || strings.HasPrefix(bound, "min") && number > old || strings.HasPrefix(bound, "max") && number < old {
				field[bound] = number
			}
		}
	}
	for _, bounds := range [][2]string{{"minimum", "maximum"}, {"minItems", "maxItems"}} {
		minimum, lower := field[bounds[0]].(float64)
		maximum, upper := field[bounds[1]].(float64)
		if lower && upper && minimum > maximum {
			return nil, errors.New("configured media bounds unavailable")
		}
	}
	if constant, exists := control["const"]; exists {
		constant = convert(constant)
		if !allowed(native, constant) || !allowed(control, constant) {
			return nil, errors.New("configured media constant unavailable")
		}
		field["const"], field["enum"] = constant, []any{constant}
	} else {
		menu, nativeMenu := native["enum"].([]any)
		if !nativeMenu {
			menu, _ = control["enum"].([]any)
		}
		if menu != nil {
			choices := []any{}
			for _, option := range menu {
				if allowed(native, option) && allowed(control, option) {
					choices = append(choices, convert(option))
				}
			}
			if len(choices) == 0 {
				return nil, errors.New("configured media choices unavailable")
			}
			field["enum"] = choices
		}
	}
	if value, exists := control["default"]; exists {
		if !allowed(field, value) || !allowed(control, value) {
			return nil, errors.New("configured media default unavailable")
		}
		field["default"] = convert(value)
	} else if value, exists := field["default"]; exists && (!allowed(field, value) || !allowed(control, value)) {
		delete(field, "default")
	}
	return field, nil
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func LookupMediaGenerationOperation(ctx context.Context, db *gorm.DB, a MediaGenerationActor, opID string) (*model.MediaGenerationQuote, error) {
	key, err := model.MediaGenerationOperationKey(model.MediaGenerationQuoteBinding{UserID: a.UserID, TokenID: a.TokenID, OwnerDigest: a.OwnerDigest, ClientOpID: opID})
	if err != nil {
		return nil, err
	}
	var quote model.MediaGenerationQuote
	if err := db.WithContext(ctx).Where("operation_key = ? AND user_id = ? AND token_id = ? AND owner_digest = ?", key, a.UserID, a.TokenID, a.OwnerDigest).Take(&quote).Error; err != nil {
		return nil, err
	}
	return &quote, nil
}
func MediaGenerationBinding(q *model.MediaGenerationQuote) model.MediaGenerationQuoteBinding {
	return model.MediaGenerationQuoteBinding{QuoteID: q.QuoteID, OperationKey: q.OperationKey, UserID: q.UserID, TokenID: q.TokenID, OwnerDigest: q.OwnerDigest, ClientOpID: q.ClientOpID, RequestSHA256: q.RequestSHA256, MaterialSHA256: q.MaterialSHA256}
}
func MediaGenerationQuoteProjection(q *model.MediaGenerationQuote) (MediaGenerationQuoteReply, error) {
	var frozen MediaGenerationFrozen
	if q == nil || common.UnmarshalJsonStr(string(q.UsageFactsJSON), &frozen) != nil || frozen.Request.Model != q.Model {
		return MediaGenerationQuoteReply{}, errors.New("quote unavailable")
	}
	state := "quoted"
	if q.TaskID != "" {
		state = "accepted"
	}
	publicParams := map[string]any{}
	for key, value := range frozen.Parameters {
		publicParams[key] = value
	}
	if contract, exists := ycapi.LookupMediaContract(q.Model); exists {
		if fields, fieldErr := ycapi.AssetFields(contract); fieldErr == nil {
			for _, field := range fields {
				delete(publicParams, field.Name)
			}
		} else {
			return MediaGenerationQuoteReply{}, fieldErr
		}
	}
	return MediaGenerationQuoteReply{q.QuoteID, q.ClientOpID, q.RequestSHA256, q.Model, q.ModelConfigVersion, frozen.Request.Intent, publicParams, frozen.Request.Assets, q.RetailCNY, q.ExpiresAt, state}, nil
}

func MediaGenerationRouteFromContext(c *gin.Context) (MediaNativeRoute, error) {
	route := MediaNativeRoute{ChannelID: common.GetContextKeyInt(c, constant.ContextKeyChannelId), BaseURL: common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl), Credential: common.GetContextKeyString(c, constant.ContextKeyChannelKey)}
	channel, err := model.GetChannelById(route.ChannelID, true)
	if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled || channel.Type != constant.ChannelTypeTaskPlugin || channel.GetSetting().TaskPluginKey != "ycapi" || channel.GetBaseURL() != route.BaseURL {
		return route, errors.New("model_not_available")
	}
	route.Settings = channel.GetSetting()
	route.CredentialDigest = common.GenerateHMAC("media-v2-key:" + route.Credential)
	route.ConfigHash = MediaGenerationDigest([]any{channel.Type, channel.BaseURL, channel.Group, channel.Models, channel.Setting, channel.OtherSettings, channel.ModelMapping, channel.ParamOverride, channel.HeaderOverride})
	if channel.ParamOverride != nil && *channel.ParamOverride != "" && *channel.ParamOverride != "{}" || channel.HeaderOverride != nil && *channel.HeaderOverride != "" && *channel.HeaderOverride != "{}" {
		return route, errors.New("media route override cannot be quoted")
	}
	if _, err = mediaNativeHTTPClient(route); err != nil {
		return route, err
	}
	return route, nil
}

func QuoteMediaGeneration(c *gin.Context, request MediaGenerationRequest) (*model.MediaGenerationQuote, error) {
	a, err := MediaGenerationActorFromContext(c)
	if err != nil {
		return nil, err
	}
	if request.Parameters == nil {
		request.Parameters = map[string]any{}
	}
	if request.Assets == nil {
		request.Assets = map[string][]string{}
	}
	requestSHA := MediaGenerationRequestDigest(request)
	old, err := LookupMediaGenerationOperation(c.Request.Context(), model.DB, a, request.ClientOperationID)
	if err == nil {
		if old.RequestSHA256 != requestSHA {
			return nil, model.ErrMediaGenerationQuoteConflict
		}
		if old.TaskID != "" {
			return old, nil
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	record, contract, err := mediaGenerationRecord(request.Model)
	if err != nil {
		return nil, err
	}
	version := model.ModelExecutionConfigurationVersion(request.Model, record)
	if request.ModelVersion != version {
		return nil, errors.New("model_version_changed")
	}
	if !slices.Contains(record.Definition.MediaExecution.Intents, string(request.Intent)) {
		return nil, errors.New("invalid_parameters")
	}
	// Receipt-first recovery above remains independent of today's qualification.
	// Reject an unmetered batch before even uploading its materials to a vendor.
	if err = ycapi.ValidateMediaRuntimeQuantity(contract, request.Parameters); err != nil {
		return nil, err
	}
	route, err := MediaGenerationRouteFromContext(c)
	if err != nil {
		return nil, err
	}
	if old != nil && old.ExpiresAt > time.Now().Unix() && old.ModelConfigVersion == version && old.ChannelConfigHash == route.ConfigHash && old.CredentialDigest == route.CredentialDigest {
		return old, nil
	}
	bindings, err := ReadMediaGenerationAssets(c.Request.Context(), model.DB, a, request.Model, request.Assets)
	if err != nil {
		return nil, err
	}
	materialSHA := MediaGenerationDigest(bindings)
	normalized, metadata, err := prepareMediaNativeParameters(c.Request.Context(), model.DB, route, request.Model, request.Parameters, record.Definition.Parameters, bindings, request.Intent)
	if err != nil {
		return nil, err
	}
	native, err := ycapi.BuildMediaRuntimeRequest(request.Model, normalized, record.Definition.MediaExecution.LaneID, "quote-not-a-generation", request.Intent)
	if err != nil {
		return nil, err
	}
	// The account quote is a free operation. It validates this exact native
	// selection and its real material quantities before any task or wallet exists.
	estimateParams := map[string]any{}
	for key, value := range native.Params {
		estimateParams[key] = value
	}
	var videoSeconds float64
	for _, assets := range bindings {
		for _, asset := range assets {
			if asset.Kind == "video" {
				videoSeconds += asset.DurationSeconds
			}
		}
	}
	if videoSeconds > 0 {
		estimateParams["video_total_duration_sec"] = videoSeconds
	}
	estimate, err := mediaNativeJSON(c.Request.Context(), route, "/v1/pricing-estimate", map[string]any{"model": contract.NativeID, "modality": contract.Kind, "params": estimateParams, "pinned_lane_id": native.LaneID, "include_all_channels": true})
	if err != nil {
		return nil, err
	}
	cost, err := mediaGenerationNativeCost(estimate, native.LaneID)
	if err != nil {
		return nil, err
	}
	native.ClientRequestID = "free-validate-" + request.ClientOperationID
	dryRunSHA, err := VerifyMediaNativeDryRun(c.Request.Context(), route, native, metadata)
	if err != nil {
		return nil, err
	}
	facts := mediaGenerationUsage(contract, normalized, metadata, request.Intent)
	plugin, ok := jsplugin.DefaultRegistry.Get("ycapi")
	if !ok || plugin.Meta.Version != "1.0.4" {
		return nil, errors.New("media runtime unavailable")
	}
	expr, ok := billing_setting.ResolveTaskBillingExpr("ycapi", request.Model, request.Model)
	if !ok {
		return nil, errors.New("model_price_unavailable")
	}
	schema, _ := plugin.Meta.UsageForModels(request.Model)
	if !billing_setting.TaskExprCompatible(expr, schema) {
		return nil, errors.New("model_price_unavailable")
	}
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	ratio := GetUserGroupRatio(common.GetContextKeyString(c, constant.ContextKeyUserGroup), usingGroup)
	quota, retail, pricingVersion, err := mediaGenerationRetail(expr, facts, ratio)
	if err != nil {
		return nil, err
	}
	policySHA, err := validateMediaGenerationRetailCost(record.Definition, contract, normalized, facts, metadata, expr, cost, retail, ratio)
	if err != nil {
		return nil, err
	}
	frozen := MediaGenerationFrozen{Request: request, Parameters: normalized, Facts: facts, CostCNY: cost.String(), DryRunSHA256: dryRunSHA, RetailPolicySHA256: policySHA}
	if policySHA != "" {
		pricingVersion = MediaGenerationDigest([]any{pricingVersion, policySHA})
	}
	rawFacts, _ := common.Marshal(frozen)
	rawParams, _ := common.Marshal(normalized)
	candidate := &model.MediaGenerationQuote{QuoteID: "mgq_" + uuid.NewString(), UserID: a.UserID, TokenID: a.TokenID, OwnerDigest: a.OwnerDigest, ClientOpID: request.ClientOperationID, RequestSHA256: requestSHA, MaterialSHA256: materialSHA, Model: request.Model, Kind: contract.Kind, ChannelID: route.ChannelID, UsingGroup: usingGroup, CredentialDigest: route.CredentialDigest, ChannelConfigHash: route.ConfigHash, ModelConfigVersion: version, PricingVersion: pricingVersion, PluginKey: "ycapi", PluginVersion: plugin.Meta.Version, PluginSourceHash: plugin.SourceHash, SupplierLane: native.LaneID, NativeParamsJSON: model.LongText(rawParams), UsageFactsJSON: model.LongText(rawFacts), CostCNY: cost.String(), RetailCNY: retail.String(), ReserveQuota: int64(quota), ExpiresAt: time.Now().Add(2 * time.Minute).Unix()}
	// No price/default version may change while this request was quoted.
	records, err := model.CurrentPublishedModelConfigurations()
	if err != nil || model.ModelExecutionConfigurationVersion(request.Model, records[request.Model]) != version {
		return nil, errors.New("model_version_changed")
	}
	store := model.MediaGenerationQuoteStore{DB: model.DB}
	if old != nil {
		return store.RenewUnboundQuote(c.Request.Context(), MediaGenerationBinding(old), old.ExpiresAt, candidate)
	}
	return store.SaveQuote(c.Request.Context(), candidate)
}

func mediaGenerationNativeCost(response map[string]any, expectedLane string) (decimal.Decimal, error) {
	if data, ok := response["data"].(map[string]any); ok {
		response = data
	}
	if adjustments, ok := response["param_adjustments"].([]any); ok && len(adjustments) > 0 {
		return decimal.Zero, errors.New("native parameters changed")
	}
	if mode, _ := response["billing_mode"].(string); mode != "fixed_amount" {
		return decimal.Zero, errors.New("unsupported variable native meter")
	}
	heads := 0
	if channels, exists := response["channels"].([]any); exists {
		for _, raw := range channels {
			row, valid := raw.(map[string]any)
			if !valid {
				return decimal.Zero, errors.New("native route unavailable")
			}
			if head, _ := row["is_delivery_head"].(bool); head {
				heads++
				if row["lane"] != expectedLane {
					return decimal.Zero, errors.New("native route changed")
				}
			}
		}
	}
	if heads != 1 {
		return decimal.Zero, errors.New("native route unavailable")
	}
	value, ok := response["effective_total_rmb"]
	if !ok {
		return decimal.Zero, errors.New("native cost unavailable")
	}
	cost, err := decimal.NewFromString(fmt.Sprint(value))
	if err != nil || !cost.IsPositive() {
		return decimal.Zero, errors.New("native cost unavailable")
	}
	return cost, nil
}
func mediaGenerationRetail(expr string, facts map[string]any, ratio float64) (int, decimal.Decimal, string, error) {
	if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) || common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) || operation_setting.USDExchangeRate <= 0 || math.IsNaN(operation_setting.USDExchangeRate) || math.IsInf(operation_setting.USDExchangeRate, 0) {
		return 0, decimal.Zero, "", errors.New("invalid price conversion")
	}
	snap := &billingexpr.BillingSnapshot{ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: ratio, QuotaPerUnit: common.QuotaPerUnit, TaskUsageBilling: true, BillingTime: time.Now().Unix()}
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
	if err != nil || result.Clamp != nil || result.ActualQuotaAfterGroup <= 0 {
		return 0, decimal.Zero, "", errors.New("model_price_unavailable")
	}
	retail := decimal.NewFromInt(int64(result.ActualQuotaAfterGroup)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromFloat(operation_setting.USDExchangeRate))
	return result.ActualQuotaAfterGroup, retail, MediaGenerationDigest([]any{expr, ratio, common.QuotaPerUnit, operation_setting.USDExchangeRate}), nil
}

// MediaGenerationRetailSpecificationSHA binds audited price evidence to every
// native selection, service-derived quantity and ordered probed measurements,
// without content, asset URLs, file identities or credentials.
func MediaGenerationRetailSpecificationSHA(contract ycapi.MediaContract, params, facts map[string]any, metadata map[string]ycapi.MediaAssetMetadata) string {
	fields, err := ycapi.AssetFields(contract)
	if err != nil {
		return ""
	}
	assets := map[string]bool{}
	measurements := map[string][]map[string]any{}
	for _, field := range fields {
		assets[field.Name] = true
		value, present := params[field.Name]
		if !present {
			continue
		}
		var refs []string
		switch value := value.(type) {
		case string:
			refs = []string{value}
		case []string:
			refs = value
		case []any:
			for _, item := range value {
				ref, ok := item.(string)
				if !ok {
					return ""
				}
				refs = append(refs, ref)
			}
		default:
			return ""
		}
		for _, ref := range refs {
			meta, ok := metadata[ref]
			if !ok || meta.Kind != field.MediaKind || meta.MIMEType == "" || meta.Width < 0 || meta.Height < 0 || meta.DurationSeconds < 0 || math.IsNaN(meta.DurationSeconds) || math.IsInf(meta.DurationSeconds, 0) || meta.FPS < 0 || math.IsNaN(meta.FPS) || math.IsInf(meta.FPS, 0) || meta.Kind != "audio" && (meta.Width == 0 || meta.Height == 0) || meta.Kind != "image" && meta.DurationSeconds == 0 || meta.Kind == "video" && meta.FPS == 0 {
				return ""
			}
			measurements[field.Name] = append(measurements[field.Name], map[string]any{"kind": meta.Kind, "mime_type": meta.MIMEType, "width": meta.Width, "height": meta.Height, "duration_seconds": meta.DurationSeconds, "fps": meta.FPS})
		}
	}
	selection := map[string]any{}
	for name, value := range params {
		if name == "prompt" || name == "negative_prompt" || assets[name] {
			continue
		}
		field, known := contract.Parameters[name]
		// An opaque extra string has no reviewed price vocabulary. Do not hash
		// possible content/credentials or silently treat it as a wildcard.
		if !known || field.Type == "string" && value != "" {
			return ""
		}
		selection[name] = value
	}
	return MediaGenerationDigest([]any{contract.ID, selection, facts, measurements})
}

func validateMediaGenerationRetailCost(def model.ModelConfiguration, contract ycapi.MediaContract, params, facts map[string]any, metadata map[string]ycapi.MediaAssetMetadata, expr string, cost, retail decimal.Decimal, ratio float64) (string, error) {
	if !retail.LessThan(cost) {
		return "", nil
	}
	// Discounted loss, an unknown combination, and ordinary unreviewed tariffs
	// remain blocked. This policy never calculates or replaces a retail price.
	if ratio < 1 || def.MediaExecution == nil || model.ValidateModelMediaExecutionConfiguration(def) != nil {
		return "", errors.New("model_price_unavailable")
	}
	policy := def.MediaExecution.RetailPolicy
	if policy == nil || policy.Policy != "official_cap" || policy.ExpressionSHA256 != billingexpr.ExprHashString(expr) || policy.ContractSHA256 != mediaGenerationContractSHA(contract) {
		return "", errors.New("model_price_unavailable")
	}
	specification := MediaGenerationRetailSpecificationSHA(contract, params, facts, metadata)
	for _, rule := range policy.Rules {
		if rule.SpecificationSHA256 != specification {
			continue
		}
		official, err := decimal.NewFromString(rule.OfficialTotalCNY)
		if err != nil || !cost.GreaterThan(official) {
			break
		}
		baseQuota, _, _, err := mediaGenerationRetail(expr, facts, 1)
		if err != nil {
			break
		}
		capQuota, clamp := common.QuotaFromDecimalChecked(official.Div(decimal.NewFromFloat(operation_setting.USDExchangeRate)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
		if clamp == nil && capQuota > 0 && baseQuota == capQuota {
			return MediaGenerationDigest(policy), nil
		}
		break
	}
	return "", errors.New("model_price_unavailable")
}

func mediaGenerationUsage(contract ycapi.MediaContract, params map[string]any, assets map[string]ycapi.MediaAssetMetadata, intent ycapi.MediaRuntimeIntent) map[string]any {
	facts := map[string]any{"input_video_seconds": float64(0), "input_audio_seconds": float64(0), "input_image_count": float64(0), "intent": string(intent), "resolution": "", "quality": "", "mode": "", "aspect_ratio": "", "spec": "media-v2", "generate_audio": false, "draft": false}
	fields, _ := ycapi.AssetFields(contract)
	for _, field := range fields {
		var refs []string
		if values, ok := params[field.Name].([]string); ok {
			refs = values
		} else if values, ok := params[field.Name].([]any); ok {
			for _, value := range values {
				if ref, valid := value.(string); valid {
					refs = append(refs, ref)
				}
			}
		}
		for _, ref := range refs {
			meta := assets[ref]
			switch meta.Kind {
			case "image":
				facts["input_image_count"] = facts["input_image_count"].(float64) + 1
			case "video":
				facts["input_video_seconds"] = facts["input_video_seconds"].(float64) + meta.DurationSeconds
			case "audio":
				facts["input_audio_seconds"] = facts["input_audio_seconds"].(float64) + meta.DurationSeconds
			}
		}
	}

	for _, key := range []string{"generate_audio", "draft"} {
		if value, ok := params[key].(bool); ok {
			facts[key] = value
		}
	}
	for _, k := range []string{"resolution", "quality", "mode", "aspect_ratio"} {
		if v, ok := params[k].(string); ok {
			facts[k] = v
		}
	}
	if contract.Kind == "image" {
		n := float64(1)
		for _, k := range []string{"n", "count", "image_count"} {
			if value, ok := params[k].(float64); ok {
				n = value
			}
		}
		facts["image_count"] = n
	} else {
		seconds := float64(0)
		for _, key := range []string{"duration", "seconds"} {
			if raw, ok := params[key]; ok {
				d, _ := decimal.NewFromString(fmt.Sprint(raw))
				seconds = d.InexactFloat64()
				break
			}
		}
		if seconds <= 0 {
			seconds = facts["input_video_seconds"].(float64)
			if seconds <= 0 {
				seconds = facts["input_audio_seconds"].(float64)
			}
		}
		facts["seconds"] = seconds
	}
	return facts
}
