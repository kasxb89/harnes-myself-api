package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Deliberately enabled abilities must not turn a candidate with approved prices
// but incomplete credential/capability verification into a client-visible offer.
func TestCodeBloomPendingCatalogFenceDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	db, _ := newAuditTestDatabase(t, kind, os.Getenv("TEST_"+strings.ToUpper(kind)+"_DSN"))
	typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
	useLogExportDatabases(t, db, db, typ, typ)
	t.Setenv("LOG_SQL_DSN", "")
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = previousMaster
	t.Cleanup(func() {
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = previousMaster
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.Option{}, &model.Task{}, &model.User{}))
	withSelfUseModeEnabled(t)
	previousRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
		model.InvalidatePricingCache()
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"preserved-chat":1}`))
	model.InvalidatePricingCache()
	channel := model.Channel{Name: "candidate fence fixture", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "synthetic-no-network"}
	require.NoError(t, db.Create(&channel).Error)
	candidates, err := codebloom.Models()
	require.NoError(t, err)
	pending := candidates[:0]
	for _, candidate := range candidates {
		if !candidate.ActivationReady {
			pending = append(pending, candidate)
		}
	}
	candidates = pending
	abilities := []model.Ability{{Group: "default", Model: "preserved-chat", ChannelId: channel.Id, Enabled: true}}
	for _, candidate := range candidates {
		abilities = append(abilities, model.Ability{Group: "default", Model: candidate.ID, ChannelId: channel.Id, Enabled: true})
	}
	require.NoError(t, db.Create(&abilities).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	ListModels(ctx, constant.ChannelTypeOpenAI)
	ids := decodeListModelsResponse(t, recorder)
	require.Len(t, ids, 1, recorder.Body.String())
	assert.Contains(t, ids, "preserved-chat")

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	GetPricing(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Success bool            `json:"success"`
		Data    []model.Pricing `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.Len(t, envelope.Data, 1, recorder.Body.String())
	assert.Equal(t, "preserved-chat", envelope.Data[0].ModelName)
	eligible, err := model.ListClientModelCandidates()
	require.NoError(t, err)
	require.Len(t, eligible, 1)
	assert.Equal(t, "preserved-chat", eligible[0].ID)

	// Even a direct submit that bypasses model selection stops before adaptor,
	// task persistence, options or billing; it cannot fall through to zero price.
	for _, candidate := range candidates {
		t.Run(candidate.ID, func(t *testing.T) {
			ctx := taskSubmissionTestContext()
			info := taskSubmissionRelayInfo(nil)
			info.OriginModelName = candidate.ID
			result, taskErr := relay.RelayTaskSubmit(ctx, info)
			require.Nil(t, result)
			require.NotNil(t, taskErr)
			assert.Equal(t, "model_not_available", taskErr.Code)
			assert.True(t, taskErr.NoRetry)
			assert.Nil(t, info.Billing)
		})
	}
	var unchangedAbilities []model.Ability
	require.NoError(t, db.Order("model").Find(&unchangedAbilities).Error)
	assert.Len(t, unchangedAbilities, len(abilities))
	for _, ability := range unchangedAbilities {
		assert.True(t, ability.Enabled)
	}
	var count int64
	require.NoError(t, db.Model(&model.Option{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&model.Task{}).Count(&count).Error)
	assert.Zero(t, count)

	// The released client consumes single-image retail rows. Only the three
	// qualified n=1 routes may expose this equivalent display unit; the source
	// snapshot and billing metadata must retain their per-request meaning.
	retail := map[string]string{
		"ys-grok-imagine-image-2.0":         "0.25",
		"ys-grok-imagine-image":             "0.2125",
		"ys-gemini-3.1-flash-image-preview": "0.25",
	}
	for id := range retail {
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: id, ChannelId: channel.Id, Enabled: true}).Error)
	}
	model.InvalidatePricingCache()
	cachedBefore, err := common.Marshal(model.GetPricing())
	require.NoError(t, err)
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	GetPricing(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.Len(t, envelope.Data, len(retail)+1, "pending candidates remain hidden")
	for _, row := range envelope.Data {
		price, qualified := retail[row.ModelName]
		if !qualified {
			assert.Equal(t, "preserved-chat", row.ModelName)
			assert.Empty(t, row.RetailPriceRows)
			continue
		}
		assert.Equal(t, []map[string]any{{
			"params":     map[string]any{"mode": "text-to-image", "count": float64(1)},
			"retail_cny": price, "unit": "CNY / image",
		}}, row.RetailPriceRows)
		source := codebloom.ClientPrices(row.ModelName)
		require.Len(t, source, 1)
		assert.Equal(t, "CNY / request", source[0]["unit"])
		assert.Equal(t, "request", source[0]["direction"])
		assert.Equal(t, price, source[0]["retail_cny"])
	}
	cachedAfter, err := common.Marshal(model.GetPricing())
	require.NoError(t, err)
	assert.JSONEq(t, string(cachedBefore), string(cachedAfter), "public display must not mutate cached billing metadata")
	if output := os.Getenv("TEST_CODEBLOOM_PUBLIC_PRICING_OUTPUT"); output != "" {
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		require.NoError(t, err)
		_, err = file.Write(recorder.Body.Bytes())
		require.NoError(t, err)
		require.NoError(t, file.Close())
	}
}

// The real retry host and production TaskAdaptor see a post-send transport
// timeout. A synthetic model and neutral meter isolate the protocol fence from
// separate media admission tests without granting any candidate readiness.
func TestCodeBloomImageSendFailureStopsHostRetry(t *testing.T) {
	oldRetry, oldErrorLog := common.RetryTimes, constant.ErrorLogEnabled
	common.RetryTimes, constant.ErrorLogEnabled = 2, false
	t.Cleanup(func() { common.RetryTimes, constant.ErrorLogEnabled = oldRetry, oldErrorLog })
	service.InitHttpClient()
	client := service.GetHttpClient()
	previousTransport := client.Transport
	t.Cleanup(func() { client.Transport = previousTransport })
	withTieredBillingConfig(t, map[string]string{"synthetic-image-model": "tiered_expr"}, map[string]string{"synthetic-image-model": `tier("request", u("units") * 0.1)`})
	for _, key := range []string{"codebloom-image", "legacy-image-fixture"} {
		t.Run(key, func(t *testing.T) {
			source := fmt.Sprintf(`
export const meta={apiVersion:1,key:%q,name:"Offline host failure fixture",version:"1.0.0",author:{name:"Test"},models:["synthetic-image-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/generate",method:"POST",headers:{Authorization:"Bearer synthetic-provider-secret"},body:{model:ctx.model,prompt:"cat"}};}
export function extractUsage(){return {units:1};}
export function parseSubmitResponse(){throw new Error("not reached after timeout");}
export function buildQueryRequest(){throw new Error("no query");}
export function parseTaskResult(){return {status:"UNKNOWN"};}
`, key)
			plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
			require.NoError(t, err)
			c := taskSubmissionTestContext()
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("task_request", map[string]any{"model": "synthetic-image-model", "prompt": "cat"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: pluginruntime.DefaultRegistry.Generation(), Plugin: plugin})
			channel := &model.Channel{Id: 921, Name: "offline image", Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Key: "synthetic-provider-secret", BaseURL: common.GetPointer("https://offline.invalid")}
			channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: key})
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, "synthetic-image-model"))
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			calls := 0
			client.Transport = clientModelPresentationTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "Bearer synthetic-provider-secret", request.Header.Get("Authorization"))
				body, readErr := io.ReadAll(request.Body)
				require.NoError(t, readErr)
				assert.Contains(t, string(body), "synthetic-image-model")
				return nil, context.DeadlineExceeded
			})
			events := []string{}
			billing := &taskSubmissionTestBilling{events: &events}
			info := taskSubmissionRelayInfo(billing)
			info.OriginModelName = "synthetic-image-model"
			info.LockedChannel = channel
			attempts := 0
			var taskErr *dto.TaskError
			if key == "codebloom-image" {
				outcome, submitErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
					attempts++
					return relay.RelayTaskSubmit(c, info)
				})
				require.Nil(t, outcome)
				taskErr = submitErr
				require.NotNil(t, taskErr)
				assert.True(t, taskErr.NoRetry)
				assert.Equal(t, "image_submission_unconfirmed", taskErr.Code)
				assert.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
				assert.Equal(t, 1, attempts)
				assert.Equal(t, 1, calls, "global retry budget must not repeat a possibly accepted image")
				assert.NotContains(t, taskErr.Message, "synthetic-provider-secret")
				assert.NotContains(t, taskErr.Message, "offline.invalid")
				assert.Nil(t, taskErr.Data)
				assert.Equal(t, 1, billing.refunds)
			} else {
				_, taskErr = relay.RelayTaskSubmit(c, info)
				require.NotNil(t, taskErr)
				assert.False(t, taskErr.NoRetry, "other image plugin retry defaults remain unchanged")
				assert.Equal(t, "do_request_failed", taskErr.Code)
				assert.Equal(t, 1, calls)
			}
		})
	}
}

// This fixture uses real wallet/token funding and media tables. Its synthetic
// model and intercepted transport cannot enable a catalog candidate or supplier.
func codeBloomFinancialTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	db, _ := newAuditTestDatabase(t, kind, os.Getenv("TEST_"+strings.ToUpper(kind)+"_DSN"))
	typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
	useLogExportDatabases(t, db, db, typ, typ)
	t.Setenv("LOG_SQL_DSN", "")
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = previousMaster
	oldBatch, oldConsume, oldExport, oldErrorLog, oldRetry := common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, constant.ErrorLogEnabled, common.RetryTimes
	common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, constant.ErrorLogEnabled, common.RetryTimes = false, true, false, false, 2
	t.Cleanup(func() {
		common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, constant.ErrorLogEnabled, common.RetryTimes = oldBatch, oldConsume, oldExport, oldErrorLog, oldRetry
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = previousMaster
	})
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.Log{}, &model.Option{}, &model.H3Account{}, &model.H3Admission{}, &model.MediaPool{}, &model.MediaAdmission{}))
	return db
}

func TestCodeBloomImageSendUnknownAdmissionDatabase(t *testing.T) {
	db := codeBloomFinancialTestDatabase(t)
	oldRegistry := pluginruntime.DefaultRegistry
	pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
	t.Cleanup(func() { pluginruntime.DefaultRegistry = oldRegistry })
	const name = "synthetic-codebloom-image"
	plugin, err := pluginruntime.DefaultRegistry.RegisterFactory(`
export const meta={apiVersion:1,key:"codebloom-image",name:"Offline image UNKNOWN fixture",version:"1.0.0",author:{name:"Test"},models:["synthetic-codebloom-image"],fetchMode:"per_task",usageSchema:{image_count:{type:"number",unit:"count"}},protocols:[{name:"openai_image",retainResult:true}]};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/generate",method:"POST",headers:{Authorization:"Bearer "+ctx.apiKey},body:{model:ctx.model,prompt:"cat",n:1}};}
export function extractUsage(){return {image_count:1};}
export function parseSubmitResponse(){throw new Error("not reached without receipt");}
export function buildQueryRequest(){throw new Error("this synchronous image upstream has no retrieval");}
export function parseTaskResult(){return {status:"UNKNOWN"};}
export const protocols={openai_image:{decodeRequest:function(ctx){return {kind:"submit",model:ctx.model,requestBody:ctx.body.value};},render:function(){throw new Error("no verified image");}}};
`, pluginruntime.Options{})
	require.NoError(t, err)
	withTieredBillingConfig(t, map[string]string{name: "tiered_expr"}, map[string]string{name: `tier("request", u("image_count") * 0.1)`})
	initial := int(20 * common.QuotaPerUnit)
	reserved := common.QuotaRound(0.1 * common.QuotaPerUnit)
	user := model.User{Username: "cb-unknown-owner", AffCode: "cb-unknown-owner", Quota: initial, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "synthetic-relay-token", Name: "offline", RemainQuota: initial}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "offline CodeBloom host fixture", Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Key: "synthetic-provider-secret", BaseURL: common.GetPointer("https://offline.invalid"), Models: name, Group: "default"}
	channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "codebloom-image"})
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: name, ChannelId: channel.Id, Enabled: true}).Error)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"synthetic-codebloom-image","prompt":"cat","n":1}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(common.RequestIdKey, "cb-image-unknown-request")
	c.Set("task_request", map[string]any{"model": name, "prompt": "cat", "n": 1})
	generation := pluginruntime.DefaultRegistry.Generation()
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: generation, Plugin: plugin})
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{Generation: generation, Plugin: plugin, Protocol: pluginruntime.ProtocolOpenAIImage, Model: name})
	common.SetContextKey(c, constant.ContextKeyUserId, user.Id)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserQuota, initial)
	common.SetContextKey(c, constant.ContextKeyTokenId, token.Id)
	common.SetContextKey(c, constant.ContextKeyTokenKey, token.Key)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, name))
	info := taskSubmissionRelayInfo(nil)
	info.UserId, info.UserGroup, info.UsingGroup, info.TokenGroup = user.Id, "default", "default", "default"
	info.OriginModelName, info.RequestId = name, "cb-image-unknown-request"
	info.TokenId, info.TokenKey = token.Id, token.Key
	info.UserSetting.BillingPreference = "wallet_only"
	info.PublicTaskID = model.GenerateTaskID()
	info.LockedChannel = &channel
	service.InitHttpClient()
	client := service.GetHttpClient()
	oldTransport := client.Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	sends := 0
	client.Transport = clientModelPresentationTransport(func(request *http.Request) (*http.Response, error) {
		sends++
		assert.Equal(t, "Bearer synthetic-provider-secret", request.Header.Get("Authorization"))
		payload, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		assert.Contains(t, string(payload), name)
		return nil, context.DeadlineExceeded
	})
	attempts := 0
	var attemptErr *dto.TaskError
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		attempts++
		result, err := relay.RelayTaskSubmit(c, info)
		attemptErr = err
		return result, err
	})
	require.Nil(t, taskErr, "durable UNKNOWN is a recoverable task outcome, not a known failure")
	require.NotNil(t, outcome)
	require.NotNil(t, attemptErr)
	assert.True(t, attemptErr.NoRetry)
	assert.Equal(t, "image_submission_unconfirmed", attemptErr.Code)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, 1, sends)
	media := service.MediaSubmissionFromContext(c)
	require.NotNil(t, media)
	assert.True(t, media.MayHaveSent)
	assert.True(t, media.Unknown)
	assert.False(t, media.SlotReleased)
	assert.False(t, media.RefundConfirmed)
	require.NotNil(t, info.Billing)
	assert.True(t, info.Billing.NeedsRefund(), "original reserve remains outstanding until a verified outcome")
	var task model.Task
	require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&task).Error)
	assert.EqualValues(t, model.TaskStatusUnknown, task.Status)
	assert.Equal(t, reserved, task.Quota)
	assert.Zero(t, task.SubmitTime)
	assert.Zero(t, task.FinishTime)
	assert.Empty(t, task.PrivateData.UpstreamTaskID)
	assert.False(t, task.PrivateData.ResultDiscarded)
	assert.Empty(t, task.Data)
	var admission model.MediaAdmission
	require.NoError(t, db.Where("id = ?", task.PrivateData.MediaAdmissionID).First(&admission).Error)
	assert.Equal(t, model.MediaUnknown, admission.State)
	assert.Positive(t, admission.SendingAt)
	assert.Equal(t, "reserved", admission.BillingState)
	assert.False(t, admission.ProviderTerminal)
	var pool model.MediaPool
	require.NoError(t, db.Where("id = ?", admission.PoolID).First(&pool).Error)
	assert.Equal(t, 1, pool.Occupied)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, initial-reserved, user.Quota)
	assert.Equal(t, initial-reserved, token.RemainQuota)
	assert.Equal(t, reserved, token.UsedQuota)
	var funding model.WalletFundingRequest
	require.NoError(t, db.Where("user_id = ? AND request_id = ?", user.Id, info.RequestId).First(&funding).Error)
	assert.EqualValues(t, reserved, funding.Quota)
	var events []model.WalletFundingEvent
	require.NoError(t, db.Where("funding_request_id = ?", funding.ID).Order("id").Find(&events).Error)
	require.Len(t, events, 1)
	assert.EqualValues(t, reserved, events[0].DeltaQuota)
	var consumeCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumeCount).Error)
	assert.Zero(t, consumeCount, "no final successful consumption is fabricated")
	presentTaskSubmission(c, outcome)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	var body map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, "unknown", body["status"])
	assert.Equal(t, false, body["retryable"])
	assert.Equal(t, false, body["resubmittable"])
	assert.NotContains(t, recorder.Body.String(), "synthetic-provider-secret")
	assert.NotContains(t, recorder.Body.String(), "not_charged")
	assert.NotContains(t, recorder.Body.String(), "refunded")
	for range 2 {
		result := httptest.NewRecorder()
		query, _ := gin.CreateTestContext(result)
		query.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID, nil)
		query.Set("id", user.Id)
		query.Params = gin.Params{{Key: "key", Value: task.TaskID}}
		GetTask(query)
		require.Equal(t, http.StatusOK, result.Code, result.Body.String())
		assert.NotContains(t, result.Body.String(), "provider_terminal_confirmed")
		assert.NotContains(t, result.Body.String(), "not_charged")
		assert.NotContains(t, result.Body.String(), "refunded")
	}
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.Where("id = ?", pool.ID).First(&pool).Error)
	var afterEvents []model.WalletFundingEvent
	require.NoError(t, db.Where("funding_request_id = ?", funding.ID).Order("id").Find(&afterEvents).Error)
	assert.Equal(t, initial-reserved, user.Quota)
	assert.Equal(t, initial-reserved, token.RemainQuota)
	assert.Equal(t, events, afterEvents, "repeated recovery GET cannot refund or re-debit uncertain work")
	assert.Equal(t, 1, pool.Occupied)
	assert.Equal(t, 1, sends)
}

func TestCodeBloomMappedAliasRefundsExistingReservationDatabase(t *testing.T) {
	db := codeBloomFinancialTestDatabase(t)
	initial := int(20 * common.QuotaPerUnit)
	reserve := common.QuotaRound(0.1 * common.QuotaPerUnit)
	user := model.User{Username: "cb-alias-owner", AffCode: "cb-alias-owner", Quota: initial, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "synthetic-alias-token", Name: "offline", RemainQuota: initial}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "offline alias fixture", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "synthetic-provider-secret", BaseURL: common.GetPointer("https://offline.invalid"), ModelMapping: common.GetPointer(`{"ordinary-alias":"ys-gpt-6.1-sol","ys-gpt-6.1-sol":"gpt-6.1-sol"}`)}
	require.NoError(t, db.Create(&channel).Error)
	c := taskSubmissionTestContext()
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, "ordinary-alias"))
	common.SetContextKey(c, constant.ContextKeyUserId, user.Id)
	common.SetContextKey(c, constant.ContextKeyTokenId, token.Id)
	common.SetContextKey(c, constant.ContextKeyTokenKey, token.Key)
	info := taskSubmissionRelayInfo(nil)
	info.UserId, info.OriginModelName, info.RequestId = user.Id, "ordinary-alias", "cb-alias-reserve"
	info.TokenId, info.TokenKey = token.Id, token.Key
	info.UserSetting.BillingPreference = "wallet_only"
	info.Request = &kitdto.GeneralOpenAIRequest{Model: "ordinary-alias"}
	require.Nil(t, service.PreConsumeBilling(c, reserve, info))
	require.NotNil(t, info.Billing)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, initial-reserve, user.Quota, "test reaches a real existing reserve before the channel mapping is inspected")
	service.InitHttpClient()
	client := service.GetHttpClient()
	oldTransport := client.Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	sends := 0
	client.Transport = clientModelPresentationTransport(func(*http.Request) (*http.Response, error) { sends++; return nil, context.DeadlineExceeded })
	apiErr := relay.TextHelper(c, info)
	require.NotNil(t, apiErr)
	assert.Equal(t, "model_not_available", string(apiErr.GetErrorCode()))
	assert.True(t, kittypes.IsSkipRetryError(apiErr))
	assert.Equal(t, "The selected model is not available", apiErr.Error())
	assert.Equal(t, 0, sends)
	require.NotNil(t, relay.RefundFailedRequestBilling(c, info, apiErr))
	require.NotNil(t, relay.RefundFailedRequestBilling(c, info, apiErr))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, initial, user.Quota)
	assert.Equal(t, initial, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	var funding model.WalletFundingRequest
	require.NoError(t, db.Where("user_id = ? AND request_id = ?", user.Id, info.RequestId).First(&funding).Error)
	assert.Zero(t, funding.Quota)
	var events []model.WalletFundingEvent
	require.NoError(t, db.Where("funding_request_id = ?", funding.ID).Order("id").Find(&events).Error)
	require.Len(t, events, 2)
	assert.EqualValues(t, reserve, events[0].DeltaQuota)
	assert.EqualValues(t, -reserve, events[1].DeltaQuota)
	assert.Equal(t, 0, sends)
}

func TestCodeBloomMappedTaskAliasStopsBeforeBilling(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta={apiVersion:1,key:"mapping-fence-fixture",name:"Offline mapping fixture",version:"1.0.0",author:{name:"Test"},models:["ordinary-alias"],fetchMode:"per_task"};
export function buildSubmitRequest(){throw new Error("validation must not be reached");}
export function parseSubmitResponse(){return {};}
export function buildQueryRequest(){return {};}
export function parseTaskResult(){return {status:"UNKNOWN"};}
`, pluginruntime.Options{})
	require.NoError(t, err)
	c := taskSubmissionTestContext()
	c.Set("model_mapping", `{"ordinary-alias":"ys-gpt-image-2k","ys-gpt-image-2k":"gpt-image-2k"}`)
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
	info := taskSubmissionRelayInfo(nil)
	info.OriginModelName = "ordinary-alias"
	result, taskErr := relay.RelayTaskSubmit(c, info)
	require.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_mapping_failed", taskErr.Code)
	assert.Equal(t, "The selected model is not available", taskErr.Message)
	assert.Nil(t, info.Billing)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}, decideTaskRetry(c, taskErr, 2))
}
