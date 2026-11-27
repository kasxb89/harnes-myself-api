package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/plugins"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMediaGenerationV2CapturedOldContractReceiptAfterRefresh(t *testing.T) {
	f := newH3RouterFixture(t, 0)
	isolateNativeMediaFixtureOptions(t)
	isolateNativeMediaFixtureTables(t, f)
	data, err := os.ReadFile("testdata/yc-replica-old-omni-96d99485.json")
	require.NoError(t, err)
	var capture struct {
		SourceCommit       string                         `json:"source_commit"`
		SourceFileSHA      string                         `json:"source_file_sha256"`
		ContractRaw        string                         `json:"contract_raw"`
		ContractRawSHA     string                         `json:"contract_raw_sha256"`
		Request            service.MediaGenerationRequest `json:"request"`
		RequestSHA         string                         `json:"request_sha256"`
		NativeParameters   map[string]any                 `json:"native_parameters"`
		NativeParameterSHA string                         `json:"native_parameters_sha256"`
		TaskID             string                         `json:"task_id"`
		FundingRequestID   string                         `json:"funding_request_id"`
		ReserveQuota       int                            `json:"synthetic_reserve_quota"`
	}
	require.NoError(t, common.Unmarshal(data, &capture))
	require.Equal(t, "96d99485cacde8aba3f7d55794cfe28d364d3086", capture.SourceCommit)
	require.Equal(t, "a540c38073040ed094db89da758d52808b8137f75783a445d0425c31ad65d9cf", capture.SourceFileSHA)
	require.Equal(t, capture.ContractRawSHA, fmt.Sprintf("%x", sha256.Sum256([]byte(capture.ContractRaw))))
	var oldContract ycapi.MediaContract
	require.NoError(t, common.UnmarshalJsonStr(capture.ContractRaw, &oldContract))
	currentContract, ok := ycapi.LookupMediaContract(oldContract.ID)
	require.True(t, ok)
	require.Equal(t, 3, *oldContract.Parameters["videos"].MaxItems)
	require.Equal(t, 1, *currentContract.Parameters["videos"].MaxItems)
	require.NotEqual(t, service.MediaGenerationDigest(oldContract), service.MediaGenerationDigest(currentContract))
	require.Equal(t, capture.RequestSHA, service.MediaGenerationRequestDigest(capture.Request), "the prior request digest is fixed, not rewritten for the new schema")
	native, err := common.Marshal(capture.NativeParameters)
	require.NoError(t, err)
	require.Equal(t, capture.NativeParameterSHA, fmt.Sprintf("%x", sha256.Sum256(native)))
	_, err = ycapi.ValidateMediaContractParams(currentContract.ID, capture.NativeParameters, nil)
	require.Error(t, err, "a new request cannot use the captured two-video selection under the refreshed one-video limit")
	require.Error(t, ycapi.ValidateMediaRuntimeQuantity(currentContract, capture.Request.Parameters), "new video batches are not execution qualified")
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	base := "https://ycapi.token6688.com"
	channel := model.Channel{Id: 981, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-replica-b016-provider", Name: "Synthetic old-layout receipt", Models: oldContract.ID, Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
	channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
	require.NoError(t, channel.Insert())
	model.InitChannelCache()
	snapshot, err := model.GetModelConfigurationSnapshot(oldContract.ID)
	require.NoError(t, err)
	definition := snapshot.Entries[0].Definition
	definition.Kind, definition.Enabled, definition.DisplayName = "video", true, oldContract.Name
	definition.Source, definition.Parameters = "synthetic96d layout compatibility only; not old-binary or paid evidence", nil
	definition.Routes = []model.ModelConfigurationRoute{{ChannelID: channel.Id, Group: "default", UpstreamModel: oldContract.ID, Enabled: true}}
	definition.Pricing = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("synthetic-old-layout", 0.2 / 7.3)`}
	definition.MediaExecution = &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: oldContract.NativeID + "@fixture:only", Intents: ycapi.MediaContractIntents(oldContract), ContractSHA256: service.MediaGenerationDigest(oldContract), QualificationSHA256: service.MediaGenerationDigest("synthetic-old-b016-layout"), CheckedAt: "2026-11-27"}
	require.NoError(t, model.SaveModelConfigurationDraft(snapshot.Revision, snapshot.Entries[0].BaseVersion, definition))
	snapshot, err = model.GetModelConfigurationSnapshot(oldContract.ID)
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(snapshot.Revision, []string{oldContract.ID}, ""))
	frozen := service.MediaGenerationFrozen{Request: capture.Request, Parameters: capture.NativeParameters, Facts: map[string]any{"seconds": float64(3), "input_video_seconds": float64(6), "intent": "reference", "spec": "media-v2"}, CostCNY: "0.02", DryRunSHA256: service.MediaGenerationDigest("synthetic-old-layout-no-vendor-call")}
	frozenBytes, err := common.Marshal(frozen)
	require.NoError(t, err)
	quote := model.MediaGenerationQuote{UserID: 792, TokenID: 792, OwnerDigest: common.GenerateHMAC("media-generation-owner:h3routermember"), ClientOpID: capture.Request.ClientOperationID, RequestSHA256: capture.RequestSHA, MaterialSHA256: service.MediaGenerationDigest(capture.Request.Assets), Model: oldContract.ID, Kind: "video", ChannelID: channel.Id, UsingGroup: "default", CredentialDigest: common.GenerateHMAC("media-v2-key:" + channel.Key), ChannelConfigHash: service.MediaGenerationDigest("synthetic-old-route-b016"), ModelConfigVersion: capture.Request.ModelVersion, PricingVersion: service.MediaGenerationDigest("synthetic-old-price-b016"), PluginKey: "ycapi", PluginVersion: "1.0.4", PluginSourceHash: service.MediaGenerationDigest("synthetic-old-plugin-b016"), SupplierLane: definition.MediaExecution.LaneID, NativeParamsJSON: model.LongText(native), UsageFactsJSON: model.LongText(frozenBytes), CostCNY: "0.02", RetailCNY: "0.2000054", ReserveQuota: int64(capture.ReserveQuota), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	store := model.MediaGenerationQuoteStore{DB: f.db}
	stored, err := store.SaveQuote(t.Context(), &quote)
	require.NoError(t, err)
	require.NoError(t, model.PrepareWalletFundingRequest(792, capture.FundingRequestID, "relay", oldContract.ID, channel.Id))
	task := model.Task{TaskID: capture.TaskID, Platform: "ycapi", UserId: 792, Group: "default", ChannelId: channel.Id, Quota: capture.ReserveQuota, Status: model.TaskStatusInProgress, Progress: "25%", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(), Properties: model.Properties{OriginModelName: oldContract.ID, UpstreamModelName: oldContract.NativeID}, PrivateData: model.TaskPrivateData{Key: channel.Key, UpstreamTaskID: "synthetic-accepted-before-contract-refresh", BillingSource: "wallet", FundingRequestId: capture.FundingRequestID, TokenId: 792, Execution: &model.TaskExecutionSnapshot{ModelConfigVersion: capture.Request.ModelVersion, RequestID: capture.FundingRequestID, RequestPath: "/v1/media-v2/tasks"}}}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		if err := store.BindQuoteToTaskTx(t.Context(), tx, service.MediaGenerationBinding(stored), task.ID, task.TaskID); err != nil {
			return err
		}
		_, err := model.ApplyWalletFundingTargetWithTx(tx, 792, capture.FundingRequestID, "relay", capture.ReserveQuota, true)
		return err
	}))
	var beforeQuote model.MediaGenerationQuote
	require.NoError(t, f.db.Where("quote_id = ?", stored.QuoteID).Take(&beforeQuote).Error)
	beforeTask := f.task(t, task.TaskID)
	walletBefore, tokenBefore := f.balance(t)
	var beforeFunding []model.WalletFundingRequest
	var beforeEvents []model.WalletFundingEvent
	require.NoError(t, f.db.Order("id").Find(&beforeFunding).Error)
	require.NoError(t, f.db.Order("id").Find(&beforeEvents).Error)
	require.Len(t, beforeFunding, 1)
	require.Len(t, beforeEvents, 1)
	// Publish the refreshed contract through the normal CAS path. It cannot
	// rewrite a previously accepted request, quote, task or wallet target.
	snapshot, err = model.GetModelConfigurationSnapshot(oldContract.ID)
	require.NoError(t, err)
	definition = snapshot.Entries[0].Definition
	definition.MediaExecution.ContractSHA256 = service.MediaGenerationDigest(currentContract)
	definition.MediaExecution.QualificationSHA256 = service.MediaGenerationDigest("synthetic-new-b016-contract")
	require.NoError(t, model.SaveModelConfigurationDraft(snapshot.Revision, snapshot.Entries[0].BaseVersion, definition))
	snapshot, err = model.GetModelConfigurationSnapshot(oldContract.ID)
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(snapshot.Revision, []string{oldContract.ID}, ""))
	records, err := model.CurrentPublishedModelConfigurations()
	require.NoError(t, err)
	require.NotEqual(t, capture.Request.ModelVersion, model.ModelExecutionConfigurationVersion(oldContract.ID, records[oldContract.ID]))
	var outbound atomic.Int64
	client, previous := service.GetHttpClient(), service.GetHttpClient().Transport
	client.Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		outbound.Add(1)
		return nil, fmt.Errorf("captured-receipt recovery forbids outbound %s", request.Method)
	})
	t.Cleanup(func() { client.Transport = previous })
	operation := f.request(t, http.MethodGet, "/v1/media-v2/operations/"+capture.Request.ClientOperationID, "", "h3routermember")
	require.Equal(t, http.StatusOK, operation.Code, operation.Body.String())
	var receipt map[string]any
	require.NoError(t, common.Unmarshal(operation.Body.Bytes(), &receipt))
	oldQuote := receipt["data"].(map[string]any)["quote"].(map[string]any)
	assert.Equal(t, capture.RequestSHA, oldQuote["request_sha256"])
	assert.Equal(t, capture.Request.ModelVersion, oldQuote["model_version"])
	assert.Len(t, oldQuote["assets"].(map[string]any)["videos"], 2)
	assert.EqualValues(t, 2, oldQuote["parameters"].(map[string]any)["count"])
	status := f.request(t, http.MethodGet, "/v1/media-v2/tasks/"+capture.TaskID, "", "h3routermember")
	require.Equal(t, http.StatusOK, status.Code, status.Body.String())
	require.Contains(t, status.Body.String(), capture.TaskID)
	requestBytes, err := common.Marshal(capture.Request)
	require.NoError(t, err)
	originalQuote := f.request(t, http.MethodPost, "/v1/media-v2/quotes", string(requestBytes), "h3routermember")
	require.Equal(t, http.StatusOK, originalQuote.Code, originalQuote.Body.String())
	require.Contains(t, originalQuote.Body.String(), stored.QuoteID)
	submit, err := common.Marshal(map[string]any{"quote_id": stored.QuoteID, "client_operation_id": capture.Request.ClientOperationID, "request_sha256": capture.RequestSHA})
	require.NoError(t, err)
	for range 2 {
		recovery := f.request(t, http.MethodPost, "/v1/media-v2/tasks", string(submit), "h3routermember")
		require.Equal(t, http.StatusOK, recovery.Code, recovery.Body.String())
		require.Contains(t, recovery.Body.String(), `"replayed":true`)
		require.Contains(t, recovery.Body.String(), capture.TaskID)
	}
	wrongDigest, err := common.Marshal(map[string]any{"quote_id": stored.QuoteID, "client_operation_id": capture.Request.ClientOperationID, "request_sha256": strings.Repeat("0", 64)})
	require.NoError(t, err)
	rejected := f.request(t, http.MethodPost, "/v1/media-v2/tasks", string(wrongDigest), "h3routermember")
	assert.Equal(t, http.StatusConflict, rejected.Code, "receipt-first must still reject an incorrect original request digest")
	var afterQuote model.MediaGenerationQuote
	require.NoError(t, f.db.Where("quote_id = ?", stored.QuoteID).Take(&afterQuote).Error)
	assert.Equal(t, beforeQuote, afterQuote)
	assert.Equal(t, beforeTask, f.task(t, task.TaskID))
	var afterFunding []model.WalletFundingRequest
	var afterEvents []model.WalletFundingEvent
	require.NoError(t, f.db.Order("id").Find(&afterFunding).Error)
	require.NoError(t, f.db.Order("id").Find(&afterEvents).Error)
	assert.Equal(t, beforeFunding, afterFunding)
	assert.Equal(t, beforeEvents, afterEvents)
	walletAfter, tokenAfter := f.balance(t)
	assert.Equal(t, walletBefore, walletAfter)
	assert.Equal(t, tokenBefore, tokenAfter)
	assert.Zero(t, outbound.Load())
	for _, table := range []any{&model.Task{}, &model.MediaGenerationQuote{}} {
		var count int64
		require.NoError(t, f.db.Model(table).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}
	t.Logf("new75 Go read synthetic96d contract/layout sha=%s request=%s native=%s; not an old-binary or paid receipt", capture.ContractRawSHA, capture.RequestSHA, capture.NativeParameterSHA)
}

func TestMediaGenerationV2AssetReceiptPersistenceBounds(t *testing.T) {
	f := newH3RouterFixture(t, 0)
	isolateNativeMediaFixtureTables(t, f)
	// These are trusted synthetic metadata records only. Real bytes, streaming
	// uploads, probing and supplier multipart forwarding are tested separately.
	original := model.MediaGenerationAsset{UserID: 792, TokenID: 792, OwnerDigest: strings.Repeat("a", 64),
		Model: "ycapi-minimax-h3-image", ClientAssetID: uuid.NewString(), State: "ready", Kind: "image", MIMEType: "image/png",
		Bytes: (50 << 20) + 1, SHA256: strings.Repeat("b", 64), ObjectKey: "synthetic-metadata-no-file-" + uuid.NewString(), Width: 2, Height: 2, Format: "png"}
	first, err := model.InsertMediaGenerationAsset(f.db, original)
	require.NoError(t, err)
	require.NotEmpty(t, first.ID)
	assert.Equal(t, original.Bytes, first.Bytes)
	replayed, err := model.InsertMediaGenerationAsset(f.db, original)
	require.NoError(t, err)
	assert.Equal(t, first.ID, replayed.ID, "original upload UUID must resolve its immutable receipt")
	changed := original
	changed.Bytes++
	_, err = model.InsertMediaGenerationAsset(f.db, changed)
	assert.ErrorIs(t, err, model.ErrMediaGenerationAssetConflict)
	maximum := original
	maximum.ClientAssetID = uuid.NewString()
	maximum.Bytes = model.MaxMediaGenerationAssetBytes
	storedMaximum, err := model.InsertMediaGenerationAsset(f.db, maximum)
	require.NoError(t, err)
	assert.EqualValues(t, 1<<30, storedMaximum.Bytes)
	tooLarge := original
	tooLarge.ClientAssetID = uuid.NewString()
	tooLarge.Bytes = (1 << 30) + 1
	_, err = model.InsertMediaGenerationAsset(f.db, tooLarge)
	assert.ErrorIs(t, err, model.ErrMediaGenerationQuoteInvalid)
	var count int64
	require.NoError(t, f.db.Model(&model.MediaGenerationAsset{}).Count(&count).Error)
	assert.EqualValues(t, 2, count, "same UUID and rejected metadata cannot multiply stored rows")
}

func TestMediaGenerationV2UnqualifiedVideoBatchBeforeSupplierEffects(t *testing.T) {
	f := newH3RouterFixture(t, 0)
	isolateNativeMediaFixtureOptions(t)
	isolateNativeMediaFixtureTables(t, f)
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	contract, ok := ycapi.LookupMediaContract("ycapi-magi-2")
	require.True(t, ok)
	publishSyntheticNativeMediaContracts(t, []ycapi.MediaContract{contract})
	parameters := map[string]any{"mode": "text-to-video", "count": float64(2), "duration": "5", "resolution": "1080p", "prompt": "New synthetic batch boundary"}
	_, err = ycapi.ValidateMediaContractParams(contract.ID, parameters, nil)
	require.NoError(t, err, "native max50 is preserved as declaration, separately from one-output execution qualification")
	require.Error(t, ycapi.ValidateMediaRuntimeQuantity(contract, parameters))
	records, err := model.CurrentPublishedModelConfigurations()
	require.NoError(t, err)
	request := service.MediaGenerationRequest{ClientOperationID: "31694f92-7347-4614-acbc-b495c9834bc4", Model: contract.ID, ModelVersion: model.ModelExecutionConfigurationVersion(contract.ID, records[contract.ID]), Intent: ycapi.MediaIntentText, Parameters: parameters, Assets: map[string][]string{}}
	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	var outbound atomic.Int64
	client, previous := service.GetHttpClient(), service.GetHttpClient().Transport
	client.Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		outbound.Add(1)
		return nil, fmt.Errorf("batch boundary blocks supplier file/estimate/invoke %s", request.URL.Path)
	})
	t.Cleanup(func() { client.Transport = previous })
	walletBefore, tokenBefore := f.balance(t)
	response := f.request(t, http.MethodPost, "/v1/media-v2/quotes", string(encoded), "h3routermember")
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"code":"invalid_parameters"`)
	assert.Zero(t, outbound.Load())
	for _, table := range []any{&model.Task{}, &model.MediaGenerationQuote{}, &model.MediaGenerationUpload{}, &model.WalletFundingRequest{}, &model.WalletFundingEvent{}} {
		var count int64
		require.NoError(t, f.db.Model(table).Count(&count).Error)
		assert.Zero(t, count)
	}
	walletAfter, tokenAfter := f.balance(t)
	assert.Equal(t, walletBefore, walletAfter)
	assert.Equal(t, tokenBefore, tokenAfter)
}

// The exported catalog is the normal handler's original response bytes. Its
// local lane/qualification/tariff bindings must never be mistaken for upstream
// paid qualification, production configuration or an audio-model inventory.
func TestMediaGenerationV2NativeCatalogProjectionAndMatrix(t *testing.T) {
	f := newH3RouterFixture(t, 0)
	isolateNativeMediaFixtureOptions(t)
	isolateNativeMediaFixtureTables(t, f)
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	contracts, err := ycapi.MediaContracts()
	require.NoError(t, err)
	require.Len(t, contracts, 75)
	var outbound atomic.Int64
	client, oldTransport := service.GetHttpClient(), service.GetHttpClient().Transport
	client.Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		outbound.Add(1)
		return nil, fmt.Errorf("static native catalog denies outbound %s %s", request.Method, request.URL.Host)
	})
	t.Cleanup(func() { client.Transport = oldTransport })
	walletBefore, tokenBefore := f.balance(t)
	names := publishSyntheticNativeMediaContracts(t, contracts)
	catalog, matrix := nativeMediaFixtureProjection(t, f, contracts)
	require.Len(t, names, 75)
	// Normal token restrictions and publication changes must affect the actual
	// handler, rather than being simulated by editing the exported JSON.
	chosen := names[0]
	require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 792).Updates(map[string]any{"model_limits_enabled": true, "model_limits": chosen}).Error)
	limited := f.request(t, http.MethodGet, "/v1/media-v2/models", "", "h3routermember")
	require.Equal(t, http.StatusOK, limited.Code, limited.Body.String())
	var tokenView struct {
		Data struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(limited.Body.Bytes(), &tokenView))
	require.Len(t, tokenView.Data.Models, 1)
	assert.Equal(t, chosen, tokenView.Data.Models[0].ID)
	require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 792).Updates(map[string]any{"model_limits_enabled": false, "model_limits": ""}).Error)
	snapshot, err := model.GetModelConfigurationSnapshot(chosen)
	require.NoError(t, err)
	original := snapshot.Entries[0].Definition
	for _, gate := range []string{"disabled", "contract_mismatch", "task_price_missing"} {
		t.Run(gate, func(t *testing.T) {
			current, err := model.GetModelConfigurationSnapshot(chosen)
			require.NoError(t, err)
			definition := current.Entries[0].Definition
			switch gate {
			case "disabled":
				definition.Enabled = false
			case "contract_mismatch":
				binding := *definition.MediaExecution
				binding.ContractSHA256 = strings.Repeat("b", 64)
				definition.MediaExecution = &binding
			case "task_price_missing":
				definition.Pricing = model.PricingValues{"ModelPrice": 0.2, "billing_setting.billing_mode": "ratio"}
			}
			require.NoError(t, model.SaveModelConfigurationDraft(current.Revision, current.Entries[0].BaseVersion, definition))
			current, err = model.GetModelConfigurationSnapshot(chosen)
			require.NoError(t, err)
			require.NoError(t, model.PublishModelConfigurations(current.Revision, []string{chosen}, ""))
			response := f.request(t, http.MethodGet, "/v1/media-v2/models", "", "h3routermember")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var result struct {
				Data struct {
					Models []struct {
						ID string `json:"id"`
					} `json:"models"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.Len(t, result.Data.Models, len(contracts)-1)
			assert.False(t, slices.ContainsFunc(result.Data.Models, func(row struct {
				ID string `json:"id"`
			}) bool {
				return row.ID == chosen
			}))
			current, err = model.GetModelConfigurationSnapshot(chosen)
			require.NoError(t, err)
			require.NoError(t, model.SaveModelConfigurationDraft(current.Revision, current.Entries[0].BaseVersion, original))
			current, err = model.GetModelConfigurationSnapshot(chosen)
			require.NoError(t, err)
			require.NoError(t, model.PublishModelConfigurations(current.Revision, []string{chosen}, ""))
		})
	}
	t.Run("retail_policy_old_dashboard_omission_and_explicit_revocation", func(t *testing.T) {
		name := "ycapi-gpt-image-2.6-flare"
		contract, ok := ycapi.LookupMediaContract(name)
		require.True(t, ok)
		normalized, err := ycapi.ValidateMediaContractParams(name, map[string]any{"mode": "text-to-image", "prompt": "Local synthetic configuration preservation"}, nil)
		require.NoError(t, err)
		specification := service.MediaGenerationRetailSpecificationSHA(contract, normalized, map[string]any{"image_count": float64(1), "spec": "media-v2", "intent": "text"}, nil)
		require.NotEmpty(t, specification)
		current, err := model.GetModelConfigurationSnapshot(name)
		require.NoError(t, err)
		definition := current.Entries[0].Definition
		binding := definition.MediaExecution
		expression, ok := definition.Pricing["billing_setting.billing_expr"].(string)
		require.True(t, ok)
		policy := &model.ModelMediaRetailPolicy{SchemaVersion: 1, Policy: "official_cap", ExpressionSHA256: billingexpr.ExprHashString(expression),
			LaneID: binding.LaneID, ContractSHA256: binding.ContractSHA256, QualificationSHA256: binding.QualificationSHA256,
			EvidenceSHA256: service.MediaGenerationDigest("synthetic-policy-preservation-only"), CheckedAt: "2026-11-27",
			Rules: []model.ModelMediaRetailPolicyRule{{SpecificationSHA256: specification, OfficialTotalCNY: "0.2", OfficialModel: contract.NativeID, SourceURL: "https://example.com/official-prices"}}}
		for _, action := range []string{"save", "omit_execution", "omit_nested_policy", "explicit_disabled"} {
			current, err = model.GetModelConfigurationSnapshot(name)
			require.NoError(t, err)
			definition = current.Entries[0].Definition
			expected := policy
			switch action {
			case "save":
				definition.MediaExecution.RetailPolicy = policy
			case "omit_execution":
				definition.MediaExecution = nil
			case "omit_nested_policy":
				definition.MediaExecution.RetailPolicy = nil
			case "explicit_disabled":
				expected = &model.ModelMediaRetailPolicy{SchemaVersion: 1, Policy: "disabled"}
				definition.MediaExecution.RetailPolicy = expected
			}
			require.NoError(t, model.SaveModelConfigurationDraft(current.Revision, current.Entries[0].BaseVersion, definition), action)
			current, err = model.GetModelConfigurationSnapshot(name)
			require.NoError(t, err)
			require.NotNil(t, current.Entries[0].Draft)
			require.Equal(t, expected, current.Entries[0].Draft.Definition.MediaExecution.RetailPolicy, action)
			require.NoError(t, model.PublishModelConfigurations(current.Revision, []string{name}, ""), action)
			persisted, err := model.GetModelConfigurationSnapshot(name)
			require.NoError(t, err)
			assert.Equal(t, expected, persisted.Entries[0].Definition.MediaExecution.RetailPolicy, "normal JSON draft/publish persistence: %s", action)
		}
	})
	// Bind any exported versions to the final normal-published test state, which
	// now includes the explicit policy revocation instead of its former absence.
	catalog, matrix = nativeMediaFixtureProjection(t, f, contracts)
	walletAfter, tokenAfter := f.balance(t)
	assert.Equal(t, walletBefore, walletAfter)
	assert.Equal(t, tokenBefore, tokenAfter)
	assert.Zero(t, outbound.Load(), "catalog projection must not contact suppliers")
	for _, table := range []any{&model.Task{}, &model.WalletFundingRequest{}, &model.WalletFundingEvent{}} {
		var count int64
		require.NoError(t, f.db.Model(table).Count(&count).Error)
		assert.Zero(t, count, "static catalog cannot create tasks or wallet events")
	}
	matrix["verified_catalog_gates"] = []string{"token_model_limit", "disabled", "contract_mismatch", "task_price_missing"}
	matrix["retail_policy_persistence"] = []string{"normal_draft_publish", "old_execution_omission_preserved", "old_nested_policy_omission_preserved", "explicit_disabled_revoked"}
	matrix["outbound_requests"] = outbound.Load()
	if output := os.Getenv("YC_NATIVE_MEDIA_STATIC_OUTPUT"); output != "" {
		require.False(t, t.Failed(), "failed checks cannot export a successful static fixture")
		writeNativeMediaProjection(t, output, catalog, matrix)
	}
}

func nativeMediaFixtureProjection(t *testing.T, f *h3RouterFixture, contracts []ycapi.MediaContract) ([]byte, map[string]any) {
	t.Helper()
	response := f.request(t, http.MethodGet, "/v1/media-v2/models", "", "h3routermember")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Models       []map[string]any `json:"models"`
			Capabilities map[string]any   `json:"capabilities"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	for _, private := range []string{"\"lane_id\"", "\"qualification_sha256\"", "\"contract_sha256\"", "synthetic-v2-provider-"} {
		assert.NotContains(t, response.Body.String(), private, "public catalog must not expose execution credentials or qualification bindings")
	}
	public := map[string]map[string]any{}
	for _, row := range payload.Data.Models {
		id, ok := row["id"].(string)
		require.True(t, ok)
		require.NotContains(t, public, id)
		public[id] = row
	}
	published, err := model.CurrentPublishedModelConfigurations()
	require.NoError(t, err)
	plugin, ok := jsplugin.DefaultRegistry.Get("ycapi")
	require.True(t, ok)
	kinds := map[string]int{"image": 0, "video": 0, "audio": 0}
	availability := map[string]int{"available": 0, "unavailable": 0}
	rows := make([]map[string]any, 0, len(contracts))
	for _, contract := range contracts {
		kinds[contract.Kind]++
		if contract.Available {
			availability["available"]++
		} else {
			availability["unavailable"]++
		}
		record, bound := published[contract.ID]
		require.True(t, bound, contract.ID)
		schema, _ := plugin.Meta.UsageForModels(contract.ID)
		expression, priced := billing_setting.ResolveTaskBillingExpr("ycapi", contract.ID, contract.ID)
		require.True(t, priced, contract.ID)
		require.True(t, billing_setting.TaskExprCompatible(expression, schema), contract.ID)
		visible, present := public[contract.ID]
		unavailable := []map[string]any{}
		for name, parameter := range contract.Parameters {
			for _, option := range parameter.Options {
				if option.Available != nil && !*option.Available {
					unavailable = append(unavailable, map[string]any{"field": name, "value": option.Value})
				}
			}
		}
		row := map[string]any{"id": contract.ID, "name": contract.Name, "kind": contract.Kind,
			"source_available": contract.Available, "catalog_visible": present, "synthetic_only": true,
			"real_paid_qualification": false, "production_activated": false, "supplier_runtime_tested": false,
			"generation_tested_by_static_projection": false,
			"source_contract_sha256":                 service.MediaGenerationDigest(contract), "declared_intents": ycapi.MediaContractIntents(contract),
			"explicitly_unavailable_options": unavailable, "execution": record.Definition.MediaExecution,
			"billing": map[string]any{"expression": expression, "schema": schema, "compatible": true, "kind": "synthetic-test-tariff-not-real-price"}}
		selectableModes := []any{}
		for _, value := range contract.Parameters["mode"].Enum {
			if !slices.ContainsFunc(unavailable, func(option map[string]any) bool { return option["field"] == "mode" && option["value"] == value }) {
				selectableModes = append(selectableModes, value)
			}
		}
		row["native_modes"] = map[string]any{"declared": contract.Parameters["mode"].Enum, "selectable": selectableModes}
		if contract.Available && record.Definition.Enabled {
			require.True(t, present, contract.ID)
			assert.Equal(t, contract.Kind, visible["kind"])
			properties := visible["parameters_schema"].(map[string]any)["properties"].(map[string]any)
			if contract.Kind == "video" {
				if _, declared := contract.Parameters["count"]; declared {
					countField, exists := properties["count"].(map[string]any)
					require.True(t, exists, contract.ID)
					assert.EqualValues(t, 1, countField["maximum"], "video batch metering is not qualified")
				} else {
					assert.NotContains(t, properties, "count", "a fixed single-source processor must not gain an invented batch control")
				}
				row["qualified_count_maximum"], row["video_batch_qualified"] = 1, false
			}
			for name, parameter := range contract.Parameters {
				field, exists := properties[name].(map[string]any)
				require.True(t, exists, "%s.%s", contract.ID, name)
				native, err := common.Marshal(parameter.Native)
				require.NoError(t, err)
				projected, err := common.Marshal(field["x-ycapi-contract"])
				require.NoError(t, err)
				assert.JSONEq(t, string(native), string(projected), "%s.%s native metadata must survive the real handler", contract.ID, name)
				for _, option := range parameter.Options {
					if option.Available != nil && !*option.Available {
						choices, _ := field["enum"].([]any)
						assert.NotContains(t, choices, option.Value, "%s.%s must not offer an unavailable choice", contract.ID, name)
					}
				}
			}
			fields, err := ycapi.AssetFields(contract)
			require.NoError(t, err)
			actualFields := visible["asset_fields"].([]any)
			require.Len(t, actualFields, len(fields), contract.ID)
			for i, field := range fields {
				actual := actualFields[i].(map[string]any)
				assert.Equal(t, field.Name, actual["name"])
				assert.Equal(t, field.MediaKind, actual["media_kind"])
				assert.Equal(t, field.Shape, actual["native_shape"])
			}
			row["projected_intents"], row["asset_fields"] = visible["intents"], visible["asset_fields"]
			if contract.NativeID == "grok-imagine-video" {
				assert.Contains(t, record.Definition.MediaExecution.Intents, "transform", "fresh public extend with a video role may have an explicit synthetic binding")
				assert.Contains(t, properties["mode"].(map[string]any)["enum"], "extend")
				assert.True(t, slices.ContainsFunc(fields, func(field ycapi.MediaAssetField) bool { return field.Name == "videos" && field.MediaKind == "video" }))
			}
		}
		rows = append(rows, row)
	}
	assert.Equal(t, 31, kinds["image"])
	assert.Equal(t, 44, kinds["video"])
	assert.Zero(t, kinds["audio"], "decoder forward compatibility cannot invent an audio contract")
	assert.Len(t, public, availability["available"])
	for _, name := range []string{"ycapi-magi-2", "ycapi-magi-2-special"} {
		require.Contains(t, public, name)
		assert.Equal(t, "video", public[name]["kind"], "new Magi declarations are video candidates only")
	}
	return bytes.Clone(response.Body.Bytes()), map[string]any{"schema": 1, "scope": "yc-replica-b016cea5",
		"status": "ACTUAL_GO_SYNTHETIC_CATALOG_NOT_PAID_QUALIFICATION", "model_count": len(contracts),
		"kinds": kinds, "source_availability": availability, "capabilities": payload.Data.Capabilities,
		"plugin_version": plugin.Meta.Version, "models": rows, "conditions_source": "Fresh native75 has no top-level conditions; per-field unknown native JSON is compared losslessly"}
}

func writeNativeMediaProjection(t *testing.T, output string, catalog []byte, matrix map[string]any) {
	t.Helper()
	require.False(t, t.Failed(), "failed checks cannot export a successful static fixture")
	require.True(t, filepath.IsAbs(output), "static fixture export requires an explicit absolute output")
	require.NoError(t, os.MkdirAll(output, 0700))
	encoded, err := common.Marshal(matrix)
	require.NoError(t, err)
	files := map[string][]byte{"catalog.json": catalog, "matrix.json": encoded}
	for name, data := range files {
		file, err := os.OpenFile(filepath.Join(output, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err, "export is immutable; select a fresh output directory")
		_, err = file.Write(data)
		require.NoError(t, err)
		require.NoError(t, file.Close())
	}
	ready := map[string]any{"schema": 1, "mode": "actual-go-static-native-media-projection", "catalog": filepath.Join(output, "catalog.json"), "catalog_sha256": fmt.Sprintf("%x", sha256.Sum256(catalog)), "matrix": filepath.Join(output, "matrix.json"), "matrix_sha256": fmt.Sprintf("%x", sha256.Sum256(encoded)), "models": matrix["model_count"], "kinds": matrix["kinds"], "synthetic_only": true, "paid_qualification": false, "source_binding": os.Getenv("YC_NATIVE_MEDIA_SOURCE_BINDING"), "long_lived_fixture_started": false}
	data, err := common.Marshal(ready)
	require.NoError(t, err)
	file, err := os.OpenFile(filepath.Join(output, "STATIC-FIXTURES-READY.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	_, err = file.Write(data)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func TestMediaGenerationV2ActualRouterQuoteReferenceAndReceiptRecovery(t *testing.T) {
	for _, kind := range []string{"image", "video"} {
		t.Run(kind, func(t *testing.T) { testMediaGenerationV2RouterReferenceReceipt(t, kind, "") })
	}
}
func TestMediaGenerationV2RejectedOriginalOperationsNeverChargeOrMultiplyTasks(t *testing.T) {
	for _, condition := range []string{"capacity", "balance"} {
		t.Run(condition, func(t *testing.T) { testMediaGenerationV2RouterReferenceReceipt(t, "image", condition) })
	}
}
func testMediaGenerationV2RouterReferenceReceipt(t *testing.T, kind, condition string) {
	f := newH3RouterFixture(t, 1)
	isolateNativeMediaFixtureTables(t, f)
	common.OptionMapRWMutex.Lock()
	previousPublished, hadPublished := common.OptionMap[model.ModelConfigurationKey]
	delete(common.OptionMap, model.ModelConfigurationKey)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadPublished {
			common.OptionMap[model.ModelConfigurationKey] = previousPublished
		} else {
			delete(common.OptionMap, model.ModelConfigurationKey)
		}
		common.OptionMapRWMutex.Unlock()
	})
	t.Setenv("MEDIA_ASSET_STORAGE_DIR", t.TempDir())
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	name := "ycapi-gpt-image-2.6-flare"
	if kind == "video" {
		name = "ycapi-seedance-2-5"
	}
	contract, ok := ycapi.LookupMediaContract(name)
	require.True(t, ok)
	base := "https://ycapi.token6688.com"
	ch := model.Channel{Id: 981, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-v2-provider-key", Name: "V2 isolated local channel", Models: name, Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
	ch.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
	ch.SetOtherSettings(kitdto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	require.NoError(t, ch.Insert())
	model.InitChannelCache()
	snapshot, err := model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.Len(t, snapshot.Entries, 1)
	entry := snapshot.Entries[0]
	definition := entry.Definition
	definition.Kind = kind
	definition.Enabled = true
	definition.DisplayName = "GPT Image 2.6（图生图都可）"
	definition.Source = "isolated synthetic v2 qualification only"
	definition.Parameters = nil
	definition.Routes = []model.ModelConfigurationRoute{{ChannelID: ch.Id, Group: "default", UpstreamModel: name, Enabled: true}}
	definition.MediaExecution = &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: contract.NativeID + "@fixture:only", Intents: ycapi.MediaContractIntents(contract), QualificationSHA256: strings.Repeat("a", 64), ContractSHA256: service.MediaGenerationDigest(contract), CheckedAt: "2026-11-23"}
	quantity := "image_count"
	if kind == "video" {
		quantity = "seconds"
	}
	definition.Pricing = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": fmt.Sprintf(`tier("synthetic", u("%s") * (0.2 / %g))`, quantity, operation_setting.USDExchangeRate)}
	require.NoError(t, model.SaveModelConfigurationDraft(snapshot.Revision, entry.BaseVersion, definition))
	snapshot, err = model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(snapshot.Revision, []string{name}, ""))
	legacySave, err := model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	legacyDefinition := legacySave.Entries[0].Definition
	legacyDefinition.MediaExecution = nil
	require.NoError(t, model.SaveModelConfigurationDraft(legacySave.Revision, legacySave.Entries[0].BaseVersion, legacyDefinition))
	legacySave, err = model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.Equal(t, definition.MediaExecution, legacySave.Entries[0].Draft.Definition.MediaExecution, "older dashboard omission cannot delist native execution")
	model.RefreshPricing()
	var posts, uploads, quotes atomic.Int64
	client := service.GetHttpClient()
	oldTransport := client.Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	oldAddress := system_setting.TaskPublicAddress
	system_setting.TaskPublicAddress = "https://gateway.media-fixture/harness"
	t.Cleanup(func() { system_setting.TaskPublicAddress = oldAddress })
	fetch := system_setting.GetFetchSetting()
	oldProtection := fetch.EnableSSRFProtection
	fetch.EnableSSRFProtection = false
	t.Cleanup(func() { fetch.EnableSSRFProtection = oldProtection })
	var outputPNG bytes.Buffer
	require.NoError(t, png.Encode(&outputPNG, image.NewNRGBA(image.Rect(0, 0, 32, 24))))
	var outputGIF bytes.Buffer
	require.NoError(t, gif.Encode(&outputGIF, image.NewPaletted(image.Rect(0, 0, 32, 24), color.Palette{color.Black, color.White}), nil))
	mediaBytes := map[string][]byte{"/main.png": outputPNG.Bytes(), "/motion.gif": outputGIF.Bytes(), "/layer.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="24"><rect width="32" height="24" fill="blue"/></svg>`)}
	mimeTypes := map[string]string{"/main.png": "image/png", "/motion.gif": "image/gif", "/layer.svg": "image/svg+xml"}
	if kind == "video" {
		path := filepath.Join(t.TempDir(), "result.mp4")
		require.NoError(t, exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=48x32:r=24:d=0.5", "-c:v", "mpeg4", "-f", "mp4", "-y", path).Run())
		data, e := os.ReadFile(path)
		require.NoError(t, e)
		mediaBytes["/clip.mp4"] = data
		mimeTypes["/clip.mp4"] = "video/mp4"
	}
	var referenceSHA string
	var envelopes []map[string]any
	client.Transport = imageUploadRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "cdn.media-fixture" {
			require.Empty(t, r.Header.Get("Authorization"), "provider credentials must not reach artifact storage")
			data, ok := mediaBytes[r.URL.Path]
			require.True(t, ok)
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", mimeTypes[r.URL.Path])
			http.ServeContent(rec, r, filepath.Base(r.URL.Path), time.Unix(1, 0), bytes.NewReader(data))
			return rec.Result(), nil
		}
		require.Equal(t, "ycapi.token6688.com", r.URL.Host, "all outgoing requests must be synthetic and intercepted")
		body := ""
		status := 200
		switch r.URL.Path {
		case "/v1/files":
			uploads.Add(1)
			require.NoError(t, r.ParseMultipartForm(2<<20))
			file, _, e := r.FormFile("file")
			require.NoError(t, e)
			actual, e := io.ReadAll(file)
			require.NoError(t, e)
			file.Close()
			referenceSHA = fmt.Sprintf("%x", sha256.Sum256(actual))
			body = fmt.Sprintf(`{"id":"file_fixture","object":"file","url":"https://assets.token6688.com/sha256/%s.png","content_type":"image/png","bytes":%d}`, referenceSHA, len(actual))
			status = 201
		case "/v1/pricing-estimate":
			quotes.Add(1)
			var request map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &request))
			require.Equal(t, definition.MediaExecution.LaneID, request["pinned_lane_id"])
			params := request["params"].(map[string]any)
			require.Len(t, params["images"], 1)
			body = fmt.Sprintf(`{"effective_total_rmb":0.02,"billing_mode":"fixed_amount","param_adjustments":[],"channels":[{"lane":%q,"is_delivery_head":true}]}`, definition.MediaExecution.LaneID)
		case "/api/v1/model-runtime/dry-run":
			var request map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &request))
			nativeParams := request["params"]
			bodyBytes, _ := common.Marshal(map[string]any{"object": "model_runtime.dry_run", "would_call_vendor": false, "pinned_lane": definition.MediaExecution.LaneID, "modality": kind, "wire": map[string]any{"channel_id": definition.MediaExecution.LaneID, "method": "POST", "body": nativeParams}})
			body = string(bodyBytes)
		case "/api/v1/model-runtime/invoke":
			posts.Add(1)
			var request map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &request))
			envelopes = append(envelopes, request)
			if kind == "image" {
				require.Equal(t, "edit", request["operation"])
			} else {
				require.NotContains(t, request, "operation")
				require.Equal(t, "video", request["modality"])
			}
			require.Equal(t, definition.MediaExecution.LaneID, request["lane_id"])
			params := request["params"].(map[string]any)
			require.Len(t, params["images"], 1)
			require.Contains(t, fmt.Sprint(params["images"]), referenceSHA)
			body = fmt.Sprintf(`{"task_id":"v2_original_provider_task","status":"pending","is_final":false,"model":{"public_model":%q}}`, contract.UpstreamPublicID)
		case "/v1/tasks/v2_original_provider_task":
			outputs := []map[string]any{}
			if kind == "image" {
				for _, path := range []string{"/main.png", "/motion.gif", "/layer.svg"} {
					outputs = append(outputs, map[string]any{"url": "https://cdn.media-fixture" + path, "type": "image", "mime_type": mimeTypes[path]})
				}
			} else {
				outputs = append(outputs, map[string]any{"url": "https://cdn.media-fixture/clip.mp4", "type": "video", "mime_type": "video/mp4"})
			}
			raw, _ := common.Marshal(map[string]any{"task_id": "v2_original_provider_task", "status": "completed", "is_final": true, "model": map[string]any{"public_model": contract.UpstreamPublicID}, "data": outputs})
			body = string(raw)
		default:
			return nil, fmt.Errorf("fixture denies unexpected outbound %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	catalog := f.request(t, "GET", "/v1/media-v2/models", "", "h3routermember")
	require.Equal(t, 200, catalog.Code, catalog.Body.String())
	var result map[string]any
	require.NoError(t, common.Unmarshal(catalog.Body.Bytes(), &result))
	models := result["data"].(map[string]any)["models"].([]any)
	require.Len(t, models, 1)
	version := models[0].(map[string]any)["model_version"]
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 512, 512))))
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	require.NoError(t, writer.WriteField("model", name))
	require.NoError(t, writer.WriteField("client_asset_id", "8deaa249-bc33-4fd4-bc68-42603e962d01"))
	file, err := writer.CreateFormFile("file", "reference.png")
	require.NoError(t, err)
	_, err = file.Write(picture.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	r := httptest.NewRequest("POST", "/v1/media-v2/assets", &upload)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Authorization", "Bearer sk-h3routermember")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
	asset := result["data"].(map[string]any)
	require.NotContains(t, asset, "object_key")
	require.NotContains(t, asset, "owner_digest")
	require.NotContains(t, asset, "url")
	request := map[string]any{"client_operation_id": "646b0609-72ba-4682-bc61-7c02a160b14c", "model": name, "model_version": version, "intent": "reference", "parameters": map[string]any{"mode": "image-edit", "prompt": "保留参考图形状，换成蓝色背景"}, "assets": map[string]any{"images": []any{asset["asset_id"]}}}
	if kind == "video" {
		request["intent"] = "frames"
		request["parameters"] = map[string]any{"mode": "first-frame", "duration": "5", "prompt": "保留参考画面，镜头缓慢向前移动"}
	}
	encoded, _ := common.Marshal(request)
	quote := f.request(t, "POST", "/v1/media-v2/quotes", string(encoded), "h3routermember")
	require.Equal(t, 200, quote.Code, quote.Body.String())
	require.NoError(t, common.Unmarshal(quote.Body.Bytes(), &result))
	q := result["data"].(map[string]any)
	if kind == "image" {
		require.Equal(t, "0.2000054", q["retail_cny"])
	} else {
		require.Equal(t, "0.9999978", q["retail_cny"], "the quote includes exact existing quota rounding")
	}
	require.NotContains(t, q, "cost_cny")
	require.NotContains(t, q, "lane_id")
	require.NotContains(t, q, "credits")
	recovery := f.request(t, "GET", "/v1/media-v2/operations/646b0609-72ba-4682-bc61-7c02a160b14c", "", "h3routermember")
	require.Equal(t, 200, recovery.Code, recovery.Body.String())
	require.Contains(t, recovery.Body.String(), q["quote_id"])
	require.Contains(t, recovery.Body.String(), "retail_cny")
	if condition == "" {
		// A new configuration authorizes a new free quote, not a second user
		// operation. Keep the original body/material and rotate only the version.
		oldQuoteID, oldRequestSHA := q["quote_id"], q["request_sha256"]
		latest, e := model.GetModelConfigurationSnapshot(name)
		require.NoError(t, e)
		changed := latest.Entries[0].Definition
		changed.Parameters = map[string]any{"properties": map[string]any{"count": map[string]any{"type": "integer", "maximum": float64(1)}}}
		require.NoError(t, model.SaveModelConfigurationDraft(latest.Revision, latest.Entries[0].BaseVersion, changed))
		latest, e = model.GetModelConfigurationSnapshot(name)
		require.NoError(t, e)
		require.NoError(t, model.PublishModelConfigurations(latest.Revision, []string{name}, ""))
		records, e := model.CurrentPublishedModelConfigurations()
		require.NoError(t, e)
		request["model_version"] = model.ModelExecutionConfigurationVersion(name, records[name])
		require.NotEqual(t, version, request["model_version"])
		refreshedBody, e := common.Marshal(request)
		require.NoError(t, e)
		refreshed := f.request(t, "POST", "/v1/media-v2/quotes", string(refreshedBody), "h3routermember")
		require.Equal(t, 200, refreshed.Code, refreshed.Body.String())
		require.NoError(t, common.Unmarshal(refreshed.Body.Bytes(), &result))
		q = result["data"].(map[string]any)
		require.NotEqual(t, oldQuoteID, q["quote_id"])
		require.Equal(t, oldRequestSHA, q["request_sha256"])
		require.Zero(t, posts.Load(), "explicit quote refresh never generates")
	}
	submit := map[string]any{"quote_id": q["quote_id"], "client_operation_id": request["client_operation_id"], "request_sha256": q["request_sha256"]}
	wire, _ := common.Marshal(submit)
	first := f.request(t, "POST", "/v1/media-v2/tasks", string(wire), "h3routermember")
	require.Equal(t, 200, first.Code, first.Body.String())
	require.NoError(t, common.Unmarshal(first.Body.Bytes(), &result))
	task := result["data"].(map[string]any)["task"].(map[string]any)
	require.NotEmpty(t, task["id"])
	require.Equal(t, task["id"], envelopes[0]["client_request_id"])
	expectedRows := int64(1)
	if condition != "" {
		if condition == "capacity" {
			policy, e := model.GetMediaConcurrencyPolicy(t.Context())
			require.NoError(t, e)
			defaults := policy.ProviderDefaults["ycapi"]
			defaults.Image = 1
			policy.ProviderDefaults["ycapi"] = defaults
			_, e = model.SaveMediaConcurrencyPolicy(t.Context(), policy.Revision, policy)
			require.NoError(t, e)
		} else {
			require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 792).Update("quota", 0).Error)
		}
		rejectedRequest := map[string]any{}
		for k, v := range request {
			rejectedRequest[k] = v
		}
		rejectedRequest["client_operation_id"] = "b80e9581-79df-49b7-9faa-40c3d93415f6"
		payload, _ := common.Marshal(rejectedRequest)
		quoteReply := f.request(t, "POST", "/v1/media-v2/quotes", string(payload), "h3routermember")
		require.Equal(t, 200, quoteReply.Code, quoteReply.Body.String())
		var decoded map[string]any
		require.NoError(t, common.Unmarshal(quoteReply.Body.Bytes(), &decoded))
		failedQuote := decoded["data"].(map[string]any)
		submitPayload, _ := common.Marshal(map[string]any{"quote_id": failedQuote["quote_id"], "client_operation_id": rejectedRequest["client_operation_id"], "request_sha256": failedQuote["request_sha256"]})
		refused := f.request(t, "POST", "/v1/media-v2/tasks", string(submitPayload), "h3routermember")
		require.Equal(t, 200, refused.Code, refused.Body.String())
		require.NoError(t, common.Unmarshal(refused.Body.Bytes(), &decoded))
		refusedTask := decoded["data"].(map[string]any)["task"].(map[string]any)
		require.Equal(t, "failed", refusedTask["status"], refused.Body.String())
		require.Equal(t, "not_submitted", refusedTask["submission_state"])
		require.Equal(t, false, refusedTask["charged"])
		expectedCode := "media_capacity_full"
		if condition == "balance" {
			expectedCode = "insufficient_quota"
		}
		require.Equal(t, expectedCode, refusedTask["failure"].(map[string]any)["code"])
		refusedAgain := f.request(t, "POST", "/v1/media-v2/tasks", string(submitPayload), "h3routermember")
		require.Equal(t, 200, refusedAgain.Code, refusedAgain.Body.String())
		require.Contains(t, refusedAgain.Body.String(), refusedTask["id"])
		require.Contains(t, refusedAgain.Body.String(), `"replayed":true`)
		failedRow := f.task(t, refusedTask["id"].(string))
		require.Zero(t, failedRow.Quota)
		require.Empty(t, failedRow.PrivateData.UpstreamTaskID)
		require.EqualValues(t, 1, posts.Load(), "rejected operations must not reach provider")
		var consumeCount int64
		require.NoError(t, f.logDB.Model(&model.Log{}).Where("user_id = ? AND type = ?", 792, model.LogTypeConsume).Count(&consumeCount).Error)
		require.EqualValues(t, 1, consumeCount, "only the first successful synthetic request is charged")
		expectedRows = 2
	}
	acceptedOperation := f.request(t, "GET", "/v1/media-v2/operations/646b0609-72ba-4682-bc61-7c02a160b14c", "", "h3routermember")
	require.Equal(t, 200, acceptedOperation.Code, acceptedOperation.Body.String())
	var acceptedEnvelope map[string]any
	require.NoError(t, common.Unmarshal(acceptedOperation.Body.Bytes(), &acceptedEnvelope))
	acceptedData := acceptedEnvelope["data"].(map[string]any)
	require.Equal(t, acceptedData["task"], acceptedData["quote"].(map[string]any)["task"], "accepted nested quote must carry the identical original task")
	// Receipt-first survives expired quote, disabled model/route and local files.
	require.NoError(t, f.db.Model(&model.MediaGenerationQuote{}).Where("client_op_id = ?", request["client_operation_id"]).Update("expires_at", 1).Error)
	again := f.request(t, "POST", "/v1/media-v2/tasks", string(wire), "h3routermember")
	require.Equal(t, 200, again.Code, again.Body.String())
	require.Contains(t, again.Body.String(), `"replayed":true`)
	require.Contains(t, again.Body.String(), task["id"])
	denied := f.request(t, "GET", "/v1/media-v2/operations/646b0609-72ba-4682-bc61-7c02a160b14c", "", "h3routeradmin")
	require.Equal(t, 404, denied.Code, denied.Body.String())
	service.RunTaskPollingOnce(context.Background(), nil)
	statusReply := f.request(t, "GET", "/v1/media-v2/tasks/"+task["id"].(string), "", "h3routermember")
	require.Equal(t, 200, statusReply.Code, statusReply.Body.String())
	require.NoError(t, common.Unmarshal(statusReply.Body.Bytes(), &result))
	completed := result["data"].(map[string]any)["task"].(map[string]any)
	require.Equal(t, "succeeded", completed["status"], statusReply.Body.String())
	artifacts := completed["artifacts"].([]any)
	expectedCount := 3
	if kind == "video" {
		expectedCount = 1
	}
	require.Len(t, artifacts, expectedCount)
	for _, raw := range artifacts {
		artifact := raw.(map[string]any)
		key := artifact["key"].(string)
		endpoint := "/v1/tasks/" + task["id"].(string) + "/artifacts/" + key + "/content"
		download := f.request(t, "GET", endpoint, "", "h3routermember")
		require.Equal(t, 200, download.Code, download.Body.String())
		require.NotEmpty(t, download.Body.Bytes())
		head := f.request(t, "HEAD", endpoint, "", "h3routermember")
		require.Equal(t, 200, head.Code, head.Body.String())
		require.Zero(t, head.Body.Len())
		rr := httptest.NewRequest("GET", endpoint, nil)
		rr.Header.Set("Authorization", "Bearer sk-h3routermember")
		rr.Header.Set("Range", "bytes=0-15")
		rw := httptest.NewRecorder()
		f.engine.ServeHTTP(rw, rr)
		require.Equal(t, 206, rw.Code, rw.Body.String())
		require.Len(t, rw.Body.Bytes(), 16)
		require.Equal(t, download.Body.Bytes()[:16], rw.Body.Bytes())
	}
	require.EqualValues(t, 1, posts.Load())
	require.EqualValues(t, 1, uploads.Load())
	expectedQuotes := int64(1)
	if condition == "" {
		expectedQuotes++
	}
	if condition != "" {
		expectedQuotes = 2
	}
	require.EqualValues(t, expectedQuotes, quotes.Load())
	var count int64
	require.NoError(t, f.db.Model(&model.Task{}).Where("user_id = ? AND platform = ?", 792, "ycapi").Count(&count).Error)
	require.EqualValues(t, expectedRows, count)
	var admissions int64
	require.NoError(t, f.db.Model(&model.MediaAdmission{}).Where("channel_id = ?", ch.Id).Count(&admissions).Error)
	require.EqualValues(t, expectedRows, admissions)
}
