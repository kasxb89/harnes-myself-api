package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Bound multipart buffering before ParseMultipartForm allocates or spills to
// disk. Processing has its own shared gate in the asset service.
var mediaV2UploadSlots = make(chan struct{}, 2)

func mediaV2Success(c *gin.Context, data any) {
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
func mediaV2Error(c *gin.Context, err error) {
	status, code, message := http.StatusUnprocessableEntity, "invalid_parameters", "所选参数或参考素材不可用，请保留原任务后调整。"
	switch {
	case errors.Is(err, service.ErrMediaGenerationUploadConfig):
		status, code, message = 503, "upload_unavailable", "当前规格暂不可用，请保留素材并选择其他规格。"
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = 404, "operation_unknown", "未查到该操作的本地回执；请勿据此重复生成。"
	case errors.Is(err, model.ErrMediaGenerationQuoteConflict):
		status, code, message = 409, "operation_conflict", "操作已绑定原请求或报价已更新，请查询原任务。"
	case errors.Is(err, model.ErrMediaGenerationQuoteExpired):
		status, code, message = 409, "quote_expired", "报价已过期，请按原操作重新确认费用。"
	case errors.Is(err, model.ErrMediaGenerationAssetConflict):
		status, code, message = 409, "operation_conflict", "这次上传编号已绑定其他文件。"
	default:
		text := err.Error()
		switch text {
		case "model_not_available":
			status, code, message = 503, text, "当前模型不可用，请选择目录中的可用模型。"
		case "model_version_changed", "quote_changed":
			status, code, message = 409, text, "模型或价格已更新，请按原操作重新确认；不会自动再次生成。"
		case "asset_not_found", "asset_expired":
			status, code, message = 404, "asset_not_found", "参考素材不存在或已过期。"
		case "asset_not_ready", "asset_binding_mismatch":
			status, code, message = 422, text, "参考素材尚未就绪或不属于当前模型。"
		case "model_price_unavailable":
			status, code, message = 503, text, "当前规格暂不可用，请保留素材并选择其他规格。"
		}
	}
	c.Header("Cache-Control", "private, no-store")
	c.AbortWithStatusJSON(status, gin.H{"success": false, "error": gin.H{"code": code, "message": message}, "request_id": c.GetString(common.RequestIdKey)})
}
func decodeMediaV2(c *gin.Context, v any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("invalid request")
	}
	return nil
}

func MediaGenerationModels(c *gin.Context) {
	maxUploadBytes, err := service.MediaGenerationMaxUploadBytes()
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	models, err := service.MediaGenerationModels(c)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	mediaV2Success(c, gin.H{"models": models, "capabilities": gin.H{"media_generation_v2": true, "quote_required": true, "durable_operation_receipt": true, "all_artifacts": true, "same_operation_configuration_requote": true, "material_conditions_v1": true, "max_upload_bytes": maxUploadBytes}})
}

func UploadMediaGenerationAsset(c *gin.Context) {
	a, err := service.MediaGenerationActorFromContext(c)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	maxUploadBytes, err := service.MediaGenerationMaxUploadBytes()
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	select {
	case mediaV2UploadSlots <- struct{}{}:
		defer func() { <-mediaV2UploadSlots }()
	default:
		c.Header("Retry-After", "2")
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"success": false, "error": gin.H{"code": "upload_busy", "message": "素材上传繁忙，请保留原上传编号稍后查询。"}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	// A request context alone does not unblock a slow multipart body read.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes+(1<<20))
	body := c.Request.Body
	stopClosingBody := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopClosingBody()
	if err = c.Request.ParseMultipartForm(1 << 20); err != nil {
		mediaV2Error(c, err)
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	if len(c.Request.MultipartForm.Value) != 2 || len(c.Request.MultipartForm.File) != 1 || len(c.Request.MultipartForm.File["file"]) != 1 || len(c.Request.MultipartForm.Value["model"]) != 1 || len(c.Request.MultipartForm.Value["client_asset_id"]) != 1 {
		mediaV2Error(c, errors.New("invalid upload"))
		return
	}
	name, clientID := c.PostForm("model"), c.PostForm("client_asset_id")
	if c.Request.MultipartForm.File["file"][0].Size > maxUploadBytes {
		mediaV2Error(c, errors.New("invalid upload"))
		return
	}
	if err = selectMediaV2Channel(c, name, nil); err != nil {
		mediaV2Error(c, err)
		return
	}
	file, err := c.Request.MultipartForm.File["file"][0].Open()
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	defer file.Close()
	asset, err := service.StoreMediaGenerationAsset(c.Request.Context(), model.DB, a, name, clientID, file)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	mediaV2Success(c, asset)
}
func GetMediaGenerationAsset(c *gin.Context) {
	a, err := service.MediaGenerationActorFromContext(c)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	v, err := service.FindMediaGenerationAsset(c.Request.Context(), model.DB, a, c.Query("model"), c.Param("client_asset_id"))
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	mediaV2Success(c, v)
}

func QuoteMediaGeneration(c *gin.Context) {
	var request service.MediaGenerationRequest
	if err := decodeMediaV2(c, &request); err != nil {
		mediaV2Error(c, err)
		return
	}
	// Receipt-first precedes channel selection. A retired route or expired asset
	// cannot turn an accepted operation into permission to create another task.
	a, err := service.MediaGenerationActorFromContext(c)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	if request.Parameters == nil {
		request.Parameters = map[string]any{}
	}
	if request.Assets == nil {
		request.Assets = map[string][]string{}
	}
	old, lookupErr := service.LookupMediaGenerationOperation(c.Request.Context(), model.DB, a, request.ClientOperationID)
	if lookupErr == nil && old.TaskID != "" {
		if old.RequestSHA256 != service.MediaGenerationRequestDigest(request) {
			mediaV2Error(c, model.ErrMediaGenerationQuoteConflict)
			return
		}
		writeMediaV2Quote(c, old)
		return
	}
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		mediaV2Error(c, lookupErr)
		return
	}
	if err = selectMediaV2Channel(c, request.Model, nil); err != nil {
		mediaV2Error(c, err)
		return
	}
	quote, err := service.QuoteMediaGeneration(c, request)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	writeMediaV2Quote(c, quote)
}
func writeMediaV2Quote(c *gin.Context, q *model.MediaGenerationQuote) {
	reply, err := service.MediaGenerationQuoteProjection(q)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	raw, _ := common.Marshal(reply)
	var data map[string]any
	_ = common.Unmarshal(raw, &data)
	if q.TaskID != "" {
		task, e := loadMediaV2Task(c, q)
		if e != nil {
			mediaV2Error(c, e)
			return
		}
		projection, e := projectMediaV2Task(task, q.Kind)
		if e != nil {
			mediaV2Error(c, e)
			return
		}
		data["task"] = projection
	}
	mediaV2Success(c, data)
}

func selectMediaV2Channel(c *gin.Context, name string, quote *model.MediaGenerationQuote) error {
	contract, ok := ycapi.LookupMediaContract(name)
	if !ok {
		return errors.New("model_not_available")
	}
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		limits, _ := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyTokenModelLimit)
		if !middleware.TokenModelLimitAllows(limits, name) {
			return errors.New("model_not_available")
		}
	}
	common.SetContextKey(c, constant.ContextKeyOriginalModel, name)
	if err := service.CaptureModelConfiguration(c, name); err != nil {
		return errors.New("model_version_changed")
	}
	path := "/v1/images/generations"
	if contract.Kind == "video" {
		path = "/v1/videos"
	}
	constraints := service.GetChannelConstraints(c)
	constraints.AddFilter(dto.ChannelFilter{Kind: dto.FilterRequestPath, RequestPath: path})
	service.AppendTaskPluginIdentityFilter(c, "ycapi")
	if quote != nil {
		constraints.AddPin(dto.ChannelPin{ChannelId: quote.ChannelID, Source: dto.ChannelPinSource("media_quote"), Rank: 5, RetryMode: dto.PinRetrySingleAttempt})
	}
	channel, _, selectErr := service.SelectChannelForRequest(c, name, &service.RetryParam{Ctx: c, ModelName: name, TokenGroup: common.GetContextKeyString(c, constant.ContextKeyUsingGroup), RequestPath: path, Retry: common.GetPointer(0)})
	if selectErr != nil || channel == nil {
		return errors.New("model_not_available")
	}
	if e := middleware.SetupContextForSelectedChannel(c, channel, name); e != nil {
		return errors.New("model_not_available")
	}
	if quote != nil {
		stored, e := model.GetChannelById(quote.ChannelID, true)
		if e != nil {
			return e
		}
		matched := ""
		for _, key := range stored.GetKeys() {
			if common.GenerateHMAC("media-v2-key:"+key) == quote.CredentialDigest {
				matched = key
				break
			}
		}
		if matched == "" {
			return errors.New("quote_changed")
		}
		common.SetContextKey(c, constant.ContextKeyChannelKey, matched)
	}
	plugin, ok := jsplugin.DefaultRegistry.Get("ycapi")
	if !ok || plugin.Meta.Version != "1.0.4" || !slices.Contains(plugin.Meta.Models, name) {
		return errors.New("model_not_available")
	}
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Plugin: plugin, Generation: jsplugin.DefaultRegistry.Generation()})
	c.Set("task_plugin_key", "ycapi")
	c.Set("platform", "ycapi")
	return nil
}

func SubmitMediaGeneration(c *gin.Context) {
	var request struct {
		QuoteID           string `json:"quote_id"`
		ClientOperationID string `json:"client_operation_id"`
		RequestSHA256     string `json:"request_sha256"`
	}
	if err := decodeMediaV2(c, &request); err != nil {
		mediaV2Error(c, err)
		return
	}
	a, err := service.MediaGenerationActorFromContext(c)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	q, err := service.LookupMediaGenerationOperation(c.Request.Context(), model.DB, a, request.ClientOperationID)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	if request.RequestSHA256 != q.RequestSHA256 {
		mediaV2Error(c, model.ErrMediaGenerationQuoteConflict)
		return
	}
	if q.TaskID != "" {
		writeMediaV2Task(c, q, true)
		return
	}
	if q.QuoteID != request.QuoteID {
		mediaV2Error(c, errors.New("quote_changed"))
		return
	}
	if q.ExpiresAt <= time.Now().Unix() {
		mediaV2Error(c, model.ErrMediaGenerationQuoteExpired)
		return
	}
	if err = selectMediaV2Channel(c, q.Model, q); err != nil {
		mediaV2Error(c, err)
		return
	}
	if err = service.AttachMediaGenerationQuote(c, q); err != nil {
		mediaV2Error(c, err)
		return
	}
	var frozen service.MediaGenerationFrozen
	if common.UnmarshalJsonStr(string(q.UsageFactsJSON), &frozen) != nil {
		mediaV2Error(c, errors.New("quote unavailable"))
		return
	}
	body := map[string]any{}
	for k, v := range frozen.Parameters {
		body[k] = v
	}
	body["model"] = q.Model
	c.Set("task_request", body)
	info, apiErr := relaycommon.GenRelayInfo(c, kittypes.RelayFormatTask, nil, nil)
	if apiErr != nil {
		mediaV2Error(c, apiErr)
		return
	}
	info.OriginModelName = q.Model
	lockedChannel, channelErr := model.GetChannelById(q.ChannelID, true)
	if channelErr != nil {
		mediaV2Error(c, channelErr)
		return
	}
	info.LockedChannel = lockedChannel
	// The existing execute pipeline owns funding, durable send fences, provider
	// receipts, settlement and unknown outcomes. No second wallet exists here.
	_, submitErr := executeTaskSubmission(c, info)
	actual, lookupErr := service.LookupMediaGenerationOperation(c.Request.Context(), model.DB, a, request.ClientOperationID)
	if lookupErr == nil && actual.TaskID != "" {
		writeMediaV2Task(c, actual, false)
		return
	}
	if submitErr != nil {
		mediaV2Error(c, errors.New(submitErr.Code))
		return
	}
	mediaV2Error(c, errors.New("operation_unknown"))
}
func GetMediaGenerationOperation(c *gin.Context) {
	a, err := service.MediaGenerationActorFromContext(c)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	q, err := service.LookupMediaGenerationOperation(c.Request.Context(), model.DB, a, c.Param("client_operation_id"))
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	data := gin.H{"client_operation_id": q.ClientOpID, "quote_id": q.QuoteID, "state": "quoted"}
	if quote, projectionErr := service.MediaGenerationQuoteProjection(q); projectionErr == nil {
		data["quote"] = quote
	} else {
		mediaV2Error(c, projectionErr)
		return
	}
	if q.TaskID != "" {
		t, e := loadMediaV2Task(c, q)
		if e != nil {
			mediaV2Error(c, e)
			return
		}
		p, e := projectMediaV2Task(t, q.Kind)
		if e != nil {
			mediaV2Error(c, e)
			return
		}
		data["state"] = "accepted"
		data["task"] = p
		encodedQuote, encodeErr := common.Marshal(data["quote"])
		if encodeErr != nil {
			mediaV2Error(c, encodeErr)
			return
		}
		var completeQuote map[string]any
		if decodeErr := common.Unmarshal(encodedQuote, &completeQuote); decodeErr != nil {
			mediaV2Error(c, decodeErr)
			return
		}
		completeQuote["task"] = p
		data["quote"] = completeQuote

	}
	mediaV2Success(c, data)
}
func GetMediaGenerationTask(c *gin.Context) {
	a, err := service.MediaGenerationActorFromContext(c)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	var q model.MediaGenerationQuote
	if err = model.DB.WithContext(c.Request.Context()).Where("task_id = ? AND user_id = ? AND token_id = ? AND owner_digest = ?", c.Param("task_id"), a.UserID, a.TokenID, a.OwnerDigest).Take(&q).Error; err != nil {
		mediaV2Error(c, err)
		return
	}
	t, err := loadMediaV2Task(c, &q)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	p, err := projectMediaV2Task(t, q.Kind)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	mediaV2Success(c, gin.H{"task": p})
}
func loadMediaV2Task(c *gin.Context, q *model.MediaGenerationQuote) (*model.Task, error) {
	t, exists, err := model.GetByTaskId(c.GetInt("id"), q.TaskID)
	if err != nil || !exists || t == nil || t.ID != q.TaskDBID {
		return nil, gorm.ErrRecordNotFound
	}
	return t, nil
}
func writeMediaV2Task(c *gin.Context, q *model.MediaGenerationQuote, replayed bool) {
	t, err := loadMediaV2Task(c, q)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	p, err := projectMediaV2Task(t, q.Kind)
	if err != nil {
		mediaV2Error(c, err)
		return
	}
	mediaV2Success(c, gin.H{"task": p, "replayed": replayed})
}
func projectMediaV2Task(t *model.Task, kind string) (map[string]any, error) {
	status := "pending"
	final := false
	switch t.Status {
	case model.TaskStatusInProgress:
		status = "running"
	case model.TaskStatusSuccess:
		status = "succeeded"
		final = true
	case model.TaskStatusFailure:
		status = "failed"
		final = true
	case model.TaskStatusUnknown:
		status = "unknown"
	}
	artifacts := []map[string]any{}
	if t.Status == model.TaskStatusSuccess {
		items, err := projectTaskArtifacts(t)
		if err != nil {
			return nil, err
		}
		for i, item := range items {
			u, err := service.BuildTaskArtifactContentURL(t.TaskID, item.Key)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, map[string]any{"key": item.Key, "type": item.Type, "mime_type": item.MimeType, "order": i, "content_url": u})
		}
		if len(items) == 0 {
			return nil, errors.New("artifact_not_ready")
		}
	}
	p := map[string]any{"id": t.TaskID, "model": t.Properties.OriginModelName, "kind": kind, "status": status, "is_final": final, "artifacts": artifacts, "created_at": t.CreatedAt, "updated_at": t.UpdatedAt}
	if n, err := strconv.ParseFloat(strings.TrimSuffix(t.Progress, "%"), 64); err == nil && n >= 0 && n <= 100 {
		p["progress"] = n
	}
	if status == "failed" {
		code, message := "generation_failed", "本次生成未完成，请查看原任务记录。"
		switch t.PrivateData.SubmissionErrorCode {
		case "insufficient_user_quota":
			code, message = "insufficient_quota", "余额或订阅额度不足，本次任务未发送、未扣费。"
		case "pre_consume_token_quota_failed":
			code, message = "insufficient_token_quota", "API 额度不足，本次任务未发送、未扣费。"
		case "media_account_capacity_exhausted":
			code, message = "media_capacity_full", "当前生成并发已满，本次任务未发送、未扣费。"
		}
		p["failure"] = map[string]any{"code": code, "message": message}
		if code != "generation_failed" {
			p["submission_state"] = "not_submitted"
			p["charged"] = false
		}
	}
	return p, nil
}
