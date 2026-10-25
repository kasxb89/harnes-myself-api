package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The observer's cancellation begins after the actual created event, so slow
// database authentication cannot consume an arbitrary pre-stream deadline.
type h3CancelAfterCreatedRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (recorder *h3CancelAfterCreatedRecorder) Write(data []byte) (int, error) {
	n, err := recorder.ResponseRecorder.Write(data)
	if strings.Contains(string(data), "response.created") {
		recorder.cancel()
	}
	return n, err
}

func newMediaConcurrencyFixture(t *testing.T) *videoQuoteFixture {
	t.Helper()
	f := newVideoQuoteFixture(t)
	require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 871).Update("base_url", "https://blackrx-fixture.invalid").Error)
	source, err := os.ReadFile("../deploy/instance/toapis-image-plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(plugin.Meta.Key) })
	require.NoError(t, f.db.Migrator().DropTable(&model.MediaAdmission{}, &model.MediaPool{}))
	require.NoError(t, model.MigrateMediaAdmission(f.db))
	require.NoError(t, model.MigrateMediaAdmission(f.db))
	return f
}

func mediaConcurrencyTask(f *videoQuoteFixture, id string) *model.Task {
	return &model.Task{TaskID: id, Platform: "blackrxai-video", UserId: f.userID, ChannelId: 871,
		Status: model.TaskStatusUnknown, Progress: "0%", CreatedAt: time.Now().Unix(),
		Properties: model.Properties{OriginModelName: "bt-seedance-2.0", UpstreamModelName: "bt-seedance-2.0"}}
}

func TestMediaConcurrencyPerCredentialBudgetAndDurableUnknown(t *testing.T) {
	f := newMediaConcurrencyFixture(t)
	ctx := context.Background()
	policy, err := model.GetMediaConcurrencyPolicy(ctx)
	require.NoError(t, err)
	assert.Equal(t, model.MediaConcurrencyDefaults{Video: 4, Image: 10}, policy.Defaults)
	var tasks []*model.Task
	for index := range 4 {
		task := mediaConcurrencyTask(f, fmt.Sprintf("task_media_budget_%d", index))
		require.NoError(t, model.AcquireMediaAdmission(ctx, task, "synthetic-unused-blackrx-credential", "video"))
		require.NotEmpty(t, task.PrivateData.MediaAdmissionID)
		tasks = append(tasks, task)
	}
	full := mediaConcurrencyTask(f, "task_media_budget_full")
	assert.ErrorIs(t, model.AcquireMediaAdmission(ctx, full, "synthetic-unused-blackrx-credential", "video"), model.ErrMediaCapacity)
	require.NoError(t, model.SetMediaBillingState(ctx, tasks[0].PrivateData.MediaAdmissionID, "reserved"))
	require.NoError(t, model.MarkMediaSending(ctx, tasks[0].PrivateData.MediaAdmissionID))
	require.NoError(t, model.HoldMediaUnknown(ctx, tasks[0]))
	assert.ErrorIs(t, model.ReleaseMediaUnsent(ctx, tasks[0]), model.ErrMediaAdmissionUnavailable)
	snapshot, err := model.GetMediaConcurrencySnapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snapshot.Pools, 1)
	assert.Equal(t, 4, snapshot.Pools[0].Occupied)
	assert.Equal(t, 0, snapshot.Pools[0].AvailableSlots)
	assert.Nil(t, snapshot.Pools[0].KnownUpstreamLimit, "configured default is not a confirmed supplier capacity")
	// Explicitly unsent work releases exactly one slot; a second release is
	// idempotent and cannot erase the unknown provider operation.
	require.NoError(t, model.ReleaseMediaUnsent(ctx, tasks[1]))
	require.NoError(t, model.ReleaseMediaUnsent(ctx, tasks[1]))
	replacement := mediaConcurrencyTask(f, "task_media_budget_replacement")
	require.NoError(t, model.AcquireMediaAdmission(ctx, replacement, "synthetic-unused-blackrx-credential", "video"))
	assert.ErrorIs(t, model.AcquireMediaAdmission(ctx, full, "synthetic-unused-blackrx-credential", "video"), model.ErrMediaCapacity)
	var wallet model.User
	require.NoError(t, f.db.First(&wallet, f.userID).Error)
	assert.Zero(t, wallet.Quota)
	var logs int64
	require.NoError(t, f.db.Model(&model.Log{}).Count(&logs).Error)
	assert.Zero(t, logs)
}

func TestMediaConcurrencyPolicyCASAndLoweringPreservesActiveTasks(t *testing.T) {
	f := newMediaConcurrencyFixture(t)
	ctx := context.Background()
	for index := range 3 {
		require.NoError(t, model.AcquireMediaAdmission(ctx, mediaConcurrencyTask(f, fmt.Sprintf("task_media_lower_%d", index)), "synthetic-unused-blackrx-credential", "video"))
	}
	current, err := model.GetMediaConcurrencyPolicy(ctx)
	require.NoError(t, err)
	changed := current
	changed.Defaults.Video = 2
	saved, err := model.SaveMediaConcurrencyPolicy(ctx, current.Revision, changed)
	require.NoError(t, err)
	assert.NotEqual(t, current.Revision, saved.Revision)
	_, err = model.SaveMediaConcurrencyPolicy(ctx, current.Revision, current)
	assert.ErrorIs(t, err, model.ErrMediaPolicyConflict)
	assert.ErrorIs(t, model.AcquireMediaAdmission(ctx, mediaConcurrencyTask(f, "task_media_lower_blocked"), "synthetic-unused-blackrx-credential", "video"), model.ErrMediaCapacity)
	snapshot, err := model.GetMediaConcurrencySnapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snapshot.Pools, 1)
	assert.Equal(t, 3, snapshot.Pools[0].Occupied)
	assert.Equal(t, 2, snapshot.Pools[0].EffectiveLimit)
	assert.Zero(t, snapshot.Pools[0].AvailableSlots)
	var unresolved int64
	require.NoError(t, f.db.Model(&model.Task{}).Where("status = ?", model.TaskStatusUnknown).Count(&unresolved).Error)
	assert.EqualValues(t, 3, unresolved)
}

func TestMediaConcurrencyConfigurationUsesAdministratorSessions(t *testing.T) {
	f := newMediaConcurrencyFixture(t)
	require.NoError(t, f.db.Migrator().DropTable(&model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}))
	require.NoError(t, f.db.AutoMigrate(&model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}))
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	previousLimit := common.CriticalRateLimitEnable
	previousSecure := common.SessionCookieSecure
	common.CriticalRateLimitEnable = false
	common.SessionCookieSecure = true
	t.Cleanup(func() { common.CriticalRateLimitEnable = previousLimit; common.SessionCookieSecure = previousSecure })
	credentials := make(map[string]string)
	identities := make(map[string]int)
	for _, user := range []struct {
		name string
		role int
	}{{"root", common.RoleRootUser}, {"admin", common.RoleAdminUser}, {"member", common.RoleCommonUser}} {
		row := model.User{Username: "media-config-" + user.name, AffCode: "mc-" + user.name, Role: user.role, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "default"}
		require.NoError(t, f.db.Create(&row).Error)
		identities[user.name] = row.Id
		bundle, err := service.CreateLoginSession(row.Id, "password", "127.0.0.1", "isolated-media-config")
		require.NoError(t, err)
		credentials[user.name] = bundle.AccessToken
	}
	engine := newAccessTokenScopeTestEngine(t)
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://example.com")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/media-concurrency"}, {http.MethodPut, "/api/media-concurrency"}, {http.MethodPost, "/api/media-concurrency/refresh"},
	} {
		rule, exists := middleware.AccessTokenRouteRule(route.method + " " + route.path)
		require.True(t, exists)
		assert.Equal(t, "session", rule.Kind())
		for _, identity := range []struct {
			name string
			want int
		}{{"anonymous", 401}, {"member", 403}, {"admin", 403}} {
			if identity.name == "admin" && route.method == http.MethodGet {
				identity.want = 200
			}
			t.Run(route.method+identity.name, func(t *testing.T) {
				response := request(route.method, route.path, credentials[identity.name], `{"schema_version":1}`)
				assert.Equal(t, identity.want, response.Code, response.Body.String())
				assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
			})
		}
	}
	current, err := model.GetMediaConcurrencyPolicy(context.Background())
	require.NoError(t, err)
	body := fmt.Sprintf(`{"schema_version":1,"expected_revision":%q,"defaults":{"video":3,"image":8},"pool_overrides":[]}`, current.Revision)
	saved := request(http.MethodPut, "/api/media-concurrency", credentials["root"], body)
	require.Equal(t, 200, saved.Code, saved.Body.String())
	assert.Contains(t, saved.Body.String(), `"video":3`)
	conflict := request(http.MethodPut, "/api/media-concurrency", credentials["root"], body)
	assert.Equal(t, 409, conflict.Code, conflict.Body.String())
	assert.Contains(t, conflict.Body.String(), "MEDIA_CONCURRENCY_POLICY_CONFLICT")
	for _, malformed := range []string{
		`{"schema_version":1,"expected_revision":"unused","defaults":{"video":4,"image":10,"other":1}}`,
		`{"schema_version":1,"expected_revision":"unused","defaults":{"video":0,"image":10},"pool_overrides":[]}`,
		`{"schema_version":1,"expected_revision":"unused","defaults":{"video":4,"image":10},"pool_overrides":[{"pool_id":"invented","limit":2,"secret":"not-allowed"}]}`,
	} {
		response := request(http.MethodPut, "/api/media-concurrency", credentials["root"], malformed)
		assert.Equal(t, 400, response.Code, response.Body.String())
	}
	pat := model.AccessTokenPrefix + strings.Repeat("m", 43)
	credential := model.UserAccessToken{UserId: identities["root"], Name: "isolated media config", TokenHash: model.AccessTokenFingerprint(pat), TokenHint: model.AccessTokenHint(pat)}
	credential.SetScopes([]string{"channel:read", "channel:write"})
	require.NoError(t, f.db.Create(&credential).Error)
	for _, value := range []struct {
		token  string
		status int
	}{{pat, 403}, {"sk-quoteonly", 401}} {
		for _, route := range []struct{ method, path string }{{"GET", "/api/media-concurrency"}, {"PUT", "/api/media-concurrency"}, {"POST", "/api/media-concurrency/refresh"}} {
			response := request(route.method, route.path, value.token, `{"schema_version":1}`)
			assert.Equal(t, value.status, response.Code, response.Body.String())
		}
	}
	unsafe := httptest.NewRequest(http.MethodPut, "/api/media-concurrency", strings.NewReader(body))
	unsafe.Header.Set("Content-Type", "application/json")
	unsafe.Header.Set("Authorization", "Bearer "+credentials["root"])
	unsafe.Header.Set("Origin", "https://untrusted.example")
	unsafeResult := httptest.NewRecorder()
	engine.ServeHTTP(unsafeResult, unsafe)
	assert.Equal(t, 403, unsafeResult.Code)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", identities["root"]).Update("auth_version", 2).Error)
	assert.Equal(t, 401, request(http.MethodGet, "/api/media-concurrency", credentials["root"], "").Code)
}

func TestMediaConcurrencyIndependentConnectionsShareAliasesAndImageBudget(t *testing.T) {
	f := newMediaConcurrencyFixture(t)
	second, err := gorm.Open(f.db.Dialector, &gorm.Config{})
	require.NoError(t, err)
	firstSQL, err := f.db.DB()
	require.NoError(t, err)
	secondSQL, err := second.DB()
	require.NoError(t, err)
	require.NotSame(t, firstSQL, secondSQL)
	t.Cleanup(func() { require.NoError(t, secondSQL.Close()) })
	firstSQL.SetMaxOpenConns(1)
	secondSQL.SetMaxOpenConns(1)
	stores := []model.MediaAdmissionStore{{DB: f.db}, {DB: second}}
	ctx := context.Background()
	for i := range 3 {
		require.NoError(t, stores[i%2].Acquire(ctx, mediaConcurrencyTask(f, fmt.Sprintf("task_shared_%d", i)), "synthetic-unused-blackrx-credential", model.MediaVideo))
	}
	// Two separate SQL pools compete for the final slot. Model aliases and
	// channel declarations cannot multiply one credential's allowance.
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready, done sync.WaitGroup
	ready.Add(2)
	for i := range 2 {
		done.Go(func() {
			ready.Done()
			<-start
			task := mediaConcurrencyTask(f, fmt.Sprintf("task_contending_%d", i))
			task.Properties.OriginModelName = "lec-mj-seedance-2-5-cd-720p"
			task.Properties.UpstreamModelName = task.Properties.OriginModelName
			results <- stores[i].Acquire(ctx, task, "synthetic-unused-blackrx-credential", model.MediaVideo)
		})
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	accepted, rejected := 0, 0
	for outcome := range results {
		if outcome == nil {
			accepted++
		} else {
			require.ErrorIs(t, outcome, model.ErrMediaCapacity)
			rejected++
		}
	}
	assert.Equal(t, 1, accepted)
	assert.Equal(t, 1, rejected)

	// Image and video budgets are independent, while two different image
	// credentials have independent ten-slot budgets.
	for keyIndex := range 2 {
		key := fmt.Sprintf("synthetic-image-%d", keyIndex)
		channel := &model.Channel{Id: 880 + keyIndex, Type: constant.ChannelTypeTaskPlugin, Key: key, Models: "yunshu-internal-gpt-image-2", Group: "default", Status: common.ChannelStatusEnabled}
		baseURL := "https://toapis-fixture.invalid"
		channel.BaseURL = &baseURL
		channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "toapis-image"})
		require.NoError(t, f.db.Create(channel).Error)
		for i := range 10 {
			task := mediaConcurrencyTask(f, fmt.Sprintf("task_image_%d_%d", keyIndex, i))
			task.ChannelId = channel.Id
			task.Platform = "toapis-image"
			task.Properties = model.Properties{OriginModelName: "yunshu-internal-gpt-image-2", UpstreamModelName: "yunshu-internal-gpt-image-2"}
			require.NoError(t, stores[i%2].Acquire(ctx, task, key, model.MediaImage))
		}
		task := mediaConcurrencyTask(f, fmt.Sprintf("task_image_%d_full", keyIndex))
		task.ChannelId, task.Platform = channel.Id, "toapis-image"
		task.Properties = model.Properties{OriginModelName: "yunshu-internal-gpt-image-2", UpstreamModelName: "yunshu-internal-gpt-image-2"}
		assert.ErrorIs(t, stores[1].Acquire(ctx, task, key, model.MediaImage), model.ErrMediaCapacity)
	}
	var slots int64
	require.NoError(t, f.db.Model(&model.MediaAdmission{}).Where("state <> ?", model.MediaReleased).Count(&slots).Error)
	assert.EqualValues(t, 24, slots)
}

func TestMediaConcurrencyFourH3CredentialsExposeSixteenAndShareGenericAliases(t *testing.T) {
	f := newH3RouterFixture(t, 4)
	require.NoError(t, f.db.Migrator().DropTable(&model.MediaAdmission{}, &model.MediaPool{}))
	require.NoError(t, model.MigrateMediaAdmission(f.db))
	ctx := context.Background()
	hint := model.GetH3ClientConcurrencyHint()
	require.NotNil(t, hint)
	assert.Equal(t, 16, *hint, "four enabled independent keys each permit four")
	newTask := func(id string, channelID int) *model.Task {
		return &model.Task{TaskID: id, Platform: "hailuo", UserId: 791, ChannelId: channelID, Group: "default", Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"}}
	}
	for keyIndex := range 4 {
		key := fmt.Sprintf("synthetic-h3-account-%d", keyIndex)
		for i := range 4 {
			require.NoError(t, model.AcquireH3Admission(ctx, newTask(fmt.Sprintf("task_four_%d_%d", keyIndex, i), 790+keyIndex), key))
		}
		assert.ErrorIs(t, model.AcquireH3Admission(ctx, newTask(fmt.Sprintf("task_four_full_%d", keyIndex), 790+keyIndex), key), model.ErrH3Capacity)
	}
	assert.Equal(t, 16, f.occupied(t))
	// Raising the platform default cannot bypass H3's known per-key cap.
	policy, err := model.GetMediaConcurrencyPolicy(ctx)
	require.NoError(t, err)
	policy.Defaults.Video = 12
	_, err = model.SaveMediaConcurrencyPolicy(ctx, policy.Revision, policy)
	require.NoError(t, err)
	hint = model.GetH3ClientConcurrencyHint()
	require.NotNil(t, hint)
	assert.Equal(t, 16, *hint)

	source, err := os.ReadFile("../deploy/instance/blackrxai-video-plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(plugin.Meta.Key) })
	channel := &model.Channel{Id: 899, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-h3-account-0", Models: "bt-seedance-2.0", Group: "default", Status: common.ChannelStatusEnabled}
	channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "blackrxai-video"})
	require.NoError(t, f.db.Create(channel).Error)
	task := &model.Task{TaskID: "task_h3_alias_overbook", Platform: "blackrxai-video", UserId: 791, ChannelId: 899, Group: "default", Properties: model.Properties{OriginModelName: "bt-seedance-2.0", UpstreamModelName: "bt-seedance-2.0"}}
	assert.ErrorIs(t, model.AcquireMediaAdmission(ctx, task, channel.Key, model.MediaVideo), model.ErrMediaCapacity, "redeclaring the H3 credential on another video channel must not add capacity")
	beforeWallet, beforeToken := f.balance(t)
	assert.Equal(t, 100000000, beforeWallet)
	assert.Equal(t, 100000000, beforeToken)
	posts, gets := f.counts()
	assert.Zero(t, posts)
	assert.Zero(t, gets)
}

func TestMediaConcurrencyUpgradeReconcilesLegacyUnknownWithoutRepeatingBilling(t *testing.T) {
	f := newMediaConcurrencyFixture(t)
	require.NoError(t, f.db.Migrator().DropTable(&model.MediaAdmission{}, &model.MediaPool{}))
	legacy := mediaConcurrencyTask(f, "task_legacy_media_unknown")
	legacy.Quota = 123
	legacy.Status, legacy.Progress, legacy.FinishTime = model.TaskStatusFailure, "100%", 123456
	legacy.PrivateData = model.TaskPrivateData{Key: "synthetic-unused-blackrx-credential", UpstreamTaskID: "legacy-provider", TokenId: f.userID}
	legacy.SetData(map[string]any{"local_error": "timeout does not prove a provider terminal"})
	require.NoError(t, f.db.Create(legacy).Error)
	var before model.Task
	require.NoError(t, f.db.First(&before, legacy.ID).Error)
	for range 2 {
		require.NoError(t, model.MigrateMediaAdmission(f.db))
	}
	var migrated model.Task
	require.NoError(t, f.db.First(&migrated, legacy.ID).Error)
	assert.Equal(t, before, migrated, "additive migration does not rewrite historical rows")
	for range 2 {
		require.NoError(t, model.RefreshMediaConcurrencyPools(context.Background()))
	}
	require.NoError(t, f.db.First(&migrated, legacy.ID).Error)
	assert.EqualValues(t, model.TaskStatusUnknown, migrated.Status)
	assert.Zero(t, migrated.FinishTime)
	assert.Equal(t, 123, migrated.Quota)
	assert.Equal(t, "legacy-provider", migrated.PrivateData.UpstreamTaskID)
	assert.JSONEq(t, string(before.Data), string(migrated.Data))
	assert.NotEmpty(t, migrated.PrivateData.MediaAdmissionID)
	var admission model.MediaAdmission
	require.NoError(t, f.db.Where("task_db_id = ?", legacy.ID).First(&admission).Error)
	assert.Equal(t, model.MediaUnknown, admission.State)
	assert.Equal(t, "legacy_uncertain", admission.BillingState)
	assert.Positive(t, admission.SendingAt)
	// A restart/migrate/reconcile cannot free an unresolved charged operation.
	for range 2 {
		require.NoError(t, model.MigrateMediaAdmission(f.db))
		require.NoError(t, model.RefreshMediaConcurrencyPools(context.Background()))
	}
	var count int64
	require.NoError(t, f.db.Model(&model.MediaAdmission{}).Where("task_db_id = ?", legacy.ID).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	duplicate := admission
	duplicate.ID = "duplicate-private-ledger"
	assert.Error(t, f.db.Create(&duplicate).Error, "one task cannot acquire two ledgers")
	var wallet model.User
	require.NoError(t, f.db.First(&wallet, f.userID).Error)
	assert.Zero(t, wallet.Quota)
	var logs int64
	require.NoError(t, f.db.Model(&model.Log{}).Count(&logs).Error)
	assert.Zero(t, logs)
}

func TestMediaConcurrencyH3FirstAdmissionImportsSharedCredentialLegacyWork(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	require.NoError(t, f.db.Migrator().DropTable(&model.MediaAdmission{}, &model.MediaPool{}))
	require.NoError(t, model.MigrateMediaAdmission(f.db))
	source, err := os.ReadFile("../deploy/instance/blackrxai-video-plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(plugin.Meta.Key) })
	channel := &model.Channel{Id: 898, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-h3-account-0", Models: "bt-seedance-2.0", Group: "default", Status: common.ChannelStatusEnabled}
	channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "blackrxai-video"})
	require.NoError(t, f.db.Create(channel).Error)
	for i := range 4 {
		task := &model.Task{TaskID: fmt.Sprintf("task_legacy_shared_%d", i), Platform: "blackrxai-video", UserId: 791, ChannelId: channel.Id, Status: model.TaskStatusQueued, PrivateData: model.TaskPrivateData{Key: channel.Key, UpstreamTaskID: fmt.Sprintf("legacy-shared-%d", i)}, Properties: model.Properties{OriginModelName: "bt-seedance-2.0", UpstreamModelName: "bt-seedance-2.0"}}
		require.NoError(t, f.db.Create(task).Error)
	}
	blocked := &model.Task{TaskID: "task_h3_after_shared_legacy", Platform: "hailuo", UserId: 791, ChannelId: 790, Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"}}
	assert.ErrorIs(t, model.AcquireH3Admission(context.Background(), blocked, channel.Key), model.ErrH3Capacity)
	assert.Zero(t, f.occupied(t))
	// Capacity denial may roll back imports; the next actual attempt must still
	// inspect the same unresolved history rather than treating an empty pool as free.
	assert.ErrorIs(t, model.AcquireH3Admission(context.Background(), blocked, channel.Key), model.ErrH3Capacity)
	assert.Zero(t, f.occupied(t))
}

func TestMediaConcurrencyCompletedHistoryDoesNotExhaustLegacyImportWindow(t *testing.T) {
	f := newMediaConcurrencyFixture(t)
	ctx := context.Background()
	first := mediaConcurrencyTask(f, "task_history_first")
	require.NoError(t, model.AcquireMediaAdmission(ctx, first, "synthetic-unused-blackrx-credential", model.MediaVideo))
	require.NoError(t, model.ReleaseMediaUnsent(ctx, first))
	var pool model.MediaPool
	require.NoError(t, f.db.First(&pool).Error)
	// This exact boundary reproduced a permanent outage once the old importer
	// counted all historical rejected tasks toward its 10,000 unresolved limit.
	rows := make([]model.Task, 10001)
	for i := range rows {
		rows[i] = *mediaConcurrencyTask(f, fmt.Sprintf("task_history_released_%d", i))
		rows[i].Status, rows[i].Progress = model.TaskStatusFailure, "100%"
	}
	require.NoError(t, f.db.CreateInBatches(&rows, 100).Error)
	ledgers := make([]model.MediaAdmission, len(rows))
	for i := range rows {
		ledgers[i] = model.MediaAdmission{ID: rows[i].TaskID, TaskDBID: rows[i].ID, PoolID: pool.ID, ChannelID: rows[i].ChannelId, State: model.MediaReleased, BillingState: "not_charged"}
	}
	require.NoError(t, f.db.CreateInBatches(&ledgers, 100).Error)
	// More than the previous global scan cap of known non-media failures must
	// neither block this video pool nor hide a later media history page.
	source, err := os.ReadFile("../plugins/tasks/sunoapi/plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(plugin.Meta.Key) })
	nonMedia := make([]model.Task, 10001)
	for i := range nonMedia {
		nonMedia[i] = model.Task{TaskID: fmt.Sprintf("task_non_media_history_%d", i), Platform: "sunoapi", Status: model.TaskStatusFailure, Progress: "100%", Properties: model.Properties{OriginModelName: "suno_music"}}
	}
	require.NoError(t, f.db.CreateInBatches(&nonMedia, 100).Error)
	require.NoError(t, model.AcquireMediaAdmission(ctx, mediaConcurrencyTask(f, "task_history_next"), "synthetic-unused-blackrx-credential", model.MediaVideo))
	require.NoError(t, f.db.First(&pool).Error)
	assert.Equal(t, 1, pool.Occupied)
}

func TestMediaConcurrencyMergedH3AliasOverrideRemainsEditable(t *testing.T) {
	f := newMediaNativeFixture(t)
	ctx := context.Background()
	task := &model.Task{TaskID: "task_alias_before_h3", Platform: "blackrxai-video", UserId: 791, ChannelId: 880, Properties: model.Properties{OriginModelName: "bt-seedance-2.0", UpstreamModelName: "bt-seedance-2.0"}}
	require.NoError(t, model.AcquireMediaAdmission(ctx, task, "synthetic-media-blackrxai-video", model.MediaVideo))
	var pool model.MediaPool
	require.NoError(t, f.db.First(&pool).Error)
	policy, err := model.GetMediaConcurrencyPolicy(ctx)
	require.NoError(t, err)
	policy.PoolOverrides = []model.MediaPoolOverride{{PoolID: pool.ID, Limit: 2}}
	_, err = model.SaveMediaConcurrencyPolicy(ctx, policy.Revision, policy)
	require.NoError(t, err)
	baseURL := "https://metaso.cn/api/minimax"
	channel := &model.Channel{Id: 790, Type: constant.ChannelTypeMiniMax, Key: "synthetic-media-blackrxai-video", BaseURL: &baseURL, Status: common.ChannelStatusEnabled, Models: "MiniMax-H3", Group: "default"}
	require.NoError(t, f.db.Create(channel).Error)
	require.NoError(t, model.ProvisionH3Account(ctx, "alias-editable", 790))
	require.NoError(t, model.ReconcileH3Account(ctx, "alias-editable"))
	require.NoError(t, model.SetH3AccountEnabled(ctx, "alias-editable", true))
	snapshot, err := model.GetMediaConcurrencySnapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, []model.MediaPoolOverride{{PoolID: "h3:alias-editable", Limit: 2}}, snapshot.Policy.PoolOverrides)
	// Clearing the visible canonical override must clear the older hidden alias.
	snapshot.Policy.PoolOverrides = []model.MediaPoolOverride{}
	_, err = model.SaveMediaConcurrencyPolicy(ctx, snapshot.Policy.Revision, snapshot.Policy)
	require.NoError(t, err)
	snapshot, err = model.GetMediaConcurrencySnapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, snapshot.Policy.PoolOverrides)
	for _, row := range snapshot.Pools {
		if row.PoolID == "h3:alias-editable" {
			assert.Equal(t, 4, row.EffectiveLimit)
			assert.Equal(t, 1, row.Occupied)
		}
	}
}

func TestMediaConcurrencyMultiKeyChannelUsesIndependentSlotsAndFrozenProviderIdentity(t *testing.T) {
	f := newMediaNativeFixture(t)
	var channel model.Channel
	require.NoError(t, f.db.First(&channel, 880).Error)
	channel.Key = "synthetic-media-key-one\nsynthetic-media-key-two"
	channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling}
	require.NoError(t, f.db.Save(&channel).Error)
	model.InitChannelCache()
	ids := make([]string, 0, 8)
	for range 8 {
		response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"two independent fixture keys","seconds":10,"size":"1792x1024"}`)
		require.Equal(t, 200, response.Code, response.Body.String())
		var result struct {
			ID string `json:"id"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.NotEmpty(t, result.ID)
		ids = append(ids, result.ID)
	}
	rejected := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"ninth independent request","seconds":10,"size":"1792x1024"}`)
	assert.Equal(t, 429, rejected.Code, rejected.Body.String())
	var pools []model.MediaPool
	require.NoError(t, f.db.Find(&pools).Error)
	require.Len(t, pools, 2)
	for _, pool := range pools {
		assert.Equal(t, 4, pool.Occupied)
	}
	// Provider IDs are account-local; both accounts can return the same ID.
	perKey := make(map[string]int)
	for _, id := range ids {
		task := f.task(t, id)
		index := perKey[task.PrivateData.Key]
		perKey[task.PrivateData.Key]++
		task.PrivateData.UpstreamTaskID = fmt.Sprintf("provider-same-local-%d", index)
		require.NoError(t, f.db.Model(task).Update("private_data", task.PrivateData).Error)
	}
	f.mu.Lock()
	f.queryMode = "failed"
	f.mu.Unlock()
	service.RunTaskPollingOnce(context.Background(), nil)
	require.NoError(t, f.db.Find(&pools).Error)
	for _, pool := range pools {
		assert.Zero(t, pool.Occupied)
	}
	for _, id := range ids {
		assert.EqualValues(t, model.TaskStatusFailure, f.task(t, id).Status)
	}
}

func TestMediaConcurrencyKeylessLegacyDoesNotGuessRotatedAccount(t *testing.T) {
	f := newMediaNativeFixture(t)
	legacy := &model.Task{TaskID: "task_legacy_missing_frozen_key", Platform: "blackrxai-video", UserId: 791, ChannelId: 880, Status: model.TaskStatusFailure, Progress: "100%", Quota: 123, PrivateData: model.TaskPrivateData{UpstreamTaskID: "legacy-account-local-id"}, Properties: model.Properties{OriginModelName: "bt-seedance-2.0", UpstreamModelName: "bt-seedance-2.0"}}
	require.NoError(t, f.db.Create(legacy).Error)
	// A new declaration of the old credential cannot prove which account
	// owned the historical task after its original channel rotated keys.
	other := &model.Channel{Id: 882, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-previous-account-key", Models: "bt-seedance-2.0", Group: "default", Status: common.ChannelStatusEnabled}
	other.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "blackrxai-video"})
	require.NoError(t, f.db.Create(other).Error)
	attempt := &model.Task{TaskID: "task_after_unknown_rotation", Platform: "blackrxai-video", UserId: 791, ChannelId: 882, Properties: legacy.Properties}
	assert.ErrorIs(t, model.AcquireMediaAdmission(context.Background(), attempt, other.Key, model.MediaVideo), model.ErrMediaLegacyIdentity)
	stored := f.task(t, legacy.TaskID)
	assert.Empty(t, stored.PrivateData.Key)
	assert.Empty(t, stored.PrivateData.MediaAdmissionID)
	get := f.request(t, http.MethodGet, "/v1/tasks/"+legacy.TaskID, "")
	assert.Equal(t, 200, get.Code)
	assert.Contains(t, get.Body.String(), `"status":"UNKNOWN"`)
	assert.NotContains(t, get.Body.String(), `"media_submission_receipt"`)
	require.NoError(t, f.db.Model(legacy).Update("platform", "removed-media-plugin").Error)
	assert.ErrorIs(t, model.AcquireMediaAdmission(context.Background(), attempt, other.Key, model.MediaVideo), model.ErrMediaLegacyIdentity, "removed metadata cannot prove a keyless task was non-media")
	imageLegacy := &model.Task{TaskID: "task_legacy_image_missing_key", Platform: "toapis-image", UserId: 791, ChannelId: 881, Status: model.TaskStatusFailure, Progress: "100%", Quota: 17, PrivateData: model.TaskPrivateData{UpstreamTaskID: "legacy-image-id"}, Properties: model.Properties{OriginModelName: "yunshu-internal-gpt-image-2", UpstreamModelName: "yunshu-internal-gpt-image-2"}}
	require.NoError(t, f.db.Create(imageLegacy).Error)
	native := f.request(t, http.MethodGet, "/toapis/v1/images/generations/"+imageLegacy.TaskID, "")
	assert.Equal(t, 200, native.Code, native.Body.String())
	assert.Contains(t, native.Body.String(), `"status":"queued"`)
	assert.Contains(t, native.Body.String(), `"model":"gpt-image-2"`)
	assert.NotContains(t, native.Body.String(), `"media_submission_receipt"`)
	canonical := f.request(t, http.MethodGet, "/v1/tasks/"+imageLegacy.TaskID, "")
	assert.Contains(t, canonical.Body.String(), `"status":"UNKNOWN"`)
	service.RunTaskPollingOnce(context.Background(), nil)
	f.mu.Lock()
	assert.Zero(t, f.posts)
	assert.Zero(t, f.gets)
	f.mu.Unlock()
}

func TestMediaConcurrencyUnqueryableOldPageDoesNotStarveAcceptedTasks(t *testing.T) {
	f := newMediaNativeFixture(t)
	f.postMode = "malformed"
	unknown := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"unqueryable first task","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 202, unknown.Code, unknown.Body.String())
	f.mu.Lock()
	f.postMode = ""
	f.queryMode = "failed"
	f.mu.Unlock()
	accepted := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"queryable second task","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 200, accepted.Code, accepted.Body.String())
	var result struct {
		ID string `json:"id"`
	}
	require.NoError(t, common.Unmarshal(accepted.Body.Bytes(), &result))
	previous := constant.TaskQueryLimit
	constant.TaskQueryLimit = 1
	t.Cleanup(func() { constant.TaskQueryLimit = previous })
	for range 3 {
		service.RunTaskPollingOnce(context.Background(), nil)
	}
	assert.EqualValues(t, model.TaskStatusFailure, f.task(t, result.ID).Status)
	f.mu.Lock()
	assert.Equal(t, 2, f.posts)
	assert.Equal(t, 1, f.gets)
	f.mu.Unlock()
}

type mediaNativeTransport struct {
	base *url.URL
	next http.RoundTripper
}

func (transport mediaNativeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || (request.URL.Hostname() != "hi.blackrxai.com" && request.URL.Hostname() != "toapis.cn") {
		return nil, fmt.Errorf("media fixture blocked an external target")
	}
	local := request.Clone(request.Context())
	target := *request.URL
	target.Scheme, target.Host = transport.base.Scheme, transport.base.Host
	local.URL, local.Host = &target, transport.base.Host
	response, err := transport.next.RoundTrip(local)
	if response != nil {
		response.Request = request
	}
	return response, err
}

type mediaNativeFixture struct {
	*h3RouterFixture
	mu           sync.Mutex
	posts, gets  int
	postMode     string
	postBody     string
	postStatus   int
	catalogByKey map[string]string
	catalogKeys  []string
	postKeys     []string
	postedBodies []map[string]any
	queryMode    string
}

func newMediaNativeFixture(t *testing.T) *mediaNativeFixture {
	t.Helper()
	base := newH3RouterFixture(t, 0)
	require.NoError(t, base.db.Migrator().DropTable(&model.MediaAdmission{}, &model.MediaPool{}))
	require.NoError(t, model.MigrateMediaAdmission(base.db))
	previousConfig := config.GlobalConfig.ExportAllConfigs()
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(previousConfig)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: `{"blackrxai-video::bt-seedance-2.0":"u(\"seconds\") * 0.02","toapis-image::yunshu-internal-gpt-image-2":"u(\"image_count\") * 0.02"}`}))
	for index, pluginKey := range []string{"blackrxai-video", "toapis-image"} {
		source, err := os.ReadFile("../deploy/instance/" + pluginKey + "-plugin.js")
		require.NoError(t, err)
		plugin, err := jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
		require.NoError(t, err)
		t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(plugin.Meta.Key) })
		channel := &model.Channel{Id: 880 + index, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-media-" + pluginKey, Models: strings.Join(plugin.Meta.Models, ","), Group: "default", Status: common.ChannelStatusEnabled}
		baseURL := "https://hi.blackrxai.com"
		if pluginKey == "toapis-image" {
			baseURL = "https://toapis.cn"
		}
		channel.BaseURL = &baseURL
		channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: pluginKey})
		channel.SetOtherSettings(kitdto.ChannelOtherSettings{DisableTaskPollingSleep: true})
		require.NoError(t, base.db.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(base.db))
	}
	model.InitChannelCache()
	base.engine = gin.New()
	base.engine.Use(middleware.RequestId())
	SetRouter(base.engine, WebAssets{})
	fixture := &mediaNativeFixture{h3RouterFixture: base, queryMode: "queued"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			fixture.catalogKeys = append(fixture.catalogKeys, key)
			if fixture.catalogByKey != nil {
				_, _ = io.WriteString(w, fixture.catalogByKey[key])
				return
			}
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"bt-seedance-2.0","object":"model"}]}`)
			return
		}
		image := strings.Contains(r.URL.Path, "/images/")
		if r.Method == http.MethodPost {
			fixture.posts++
			fixture.postKeys = append(fixture.postKeys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if fixture.catalogByKey != nil {
				var submitted map[string]any
				if common.DecodeJson(r.Body, &submitted) == nil {
					fixture.postedBodies = append(fixture.postedBodies, submitted)
				}
			}
			if fixture.postMode == "transport" {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			}
			if fixture.postStatus != 0 {
				w.WriteHeader(fixture.postStatus)
				_, _ = io.WriteString(w, fixture.postBody)
				return
			}
			if fixture.postMode == "http503" {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"code":"fixture_provider_unavailable","message":"fixture provider temporarily unavailable"}}`)
				return
			}
			if fixture.postMode == "malformed" {
				_, _ = io.WriteString(w, `{"id":`)
				return
			}
			object := "video"
			if image {
				object = "generation.task"
			}
			_, _ = fmt.Fprintf(w, `{"id":"provider-media-%d","object":%q,"status":"queued"}`, fixture.posts, object)
			return
		}
		fixture.gets++
		if fixture.queryMode == "http404" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"message":"temporary lookup missing"}}`)
			return
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		object := "video"
		if image {
			object = "generation.task"
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"object":%q,"status":%q,"error":{"message":"synthetic terminal failure"}}`, id, object, fixture.queryMode)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	for _, client := range []*http.Client{service.GetHttpClient(), service.GetSSRFProtectedHTTPClient()} {
		previousTransport := client.Transport
		client.Transport = mediaNativeTransport{base: baseURL, next: http.DefaultTransport}
		t.Cleanup(func() { client.Transport = previousTransport })
	}
	return fixture
}

func (fixture *mediaNativeFixture) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := fixture.h3RouterFixture.request(t, method, path, body, "h3routeradmin")
	assert.NotContains(t, response.Body.String(), "synthetic-media-")
	if output := os.Getenv("MEDIA_CONCURRENCY_FIXTURE_OUTPUT"); output != "" {
		require.True(t, filepath.IsAbs(output))
		var wire any
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
		var collection []map[string]any
		if previous, err := os.ReadFile(output); err == nil {
			require.NoError(t, common.Unmarshal(previous, &collection))
		} else {
			require.True(t, os.IsNotExist(err))
		}
		collection = append(collection, map[string]any{"test": t.Name(), "method": method, "path": path, "http_status": response.Code, "response": wire})
		encoded, err := common.Marshal(collection)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, encoded, 0o600))
	}
	return response
}

func TestMediaConcurrencyNativeCapacityReceiptsRecoverSameUnchargedTask(t *testing.T) {
	for _, test := range []struct {
		name, path, body, modelName, kind string
		limit                             int
	}{
		{"video", "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"free loopback only","seconds":10,"size":"1792x1024"}`, "bt-seedance-2.0", "video", 4},
		{"image", "/toapis/v1/images/generations", `{"model":"yunshu-internal-gpt-image-2","prompt":"free loopback only","resolution":"1k","n":1}`, "yunshu-internal-gpt-image-2", "image", 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newMediaNativeFixture(t)
			for range test.limit {
				response := f.request(t, http.MethodPost, test.path, test.body)
				require.Equal(t, 200, response.Code, response.Body.String())
			}
			wallet, token := f.balance(t)
			require.Less(t, wallet, 100000000)
			rejected := f.request(t, http.MethodPost, test.path, test.body)
			require.Equal(t, 429, rejected.Code, rejected.Body.String())
			var body struct {
				ID      string                         `json:"id"`
				TaskID  string                         `json:"task_id"`
				Model   string                         `json:"model"`
				Receipt service.MediaSubmissionReceipt `json:"media_submission_receipt"`
			}
			require.NoError(t, common.Unmarshal(rejected.Body.Bytes(), &body))
			require.NotEmpty(t, body.ID)
			assert.Equal(t, body.ID, body.TaskID)
			assert.Equal(t, test.modelName, body.Model)
			assert.Equal(t, 1, body.Receipt.SchemaVersion)
			assert.Equal(t, test.modelName, body.Receipt.Model)
			assert.Equal(t, test.kind, body.Receipt.MediaKind)
			assert.Equal(t, "not_charged", body.Receipt.BillingState)
			assert.False(t, body.Receipt.UpstreamSubmitted)
			assert.True(t, body.Receipt.Resubmittable)
			assert.False(t, body.Receipt.Retryable)
			assert.False(t, body.Receipt.RefundConfirmed)
			assert.NotContains(t, rejected.Body.String(), `"submission_receipt"`)
			get := f.request(t, http.MethodGet, "/v1/tasks/"+body.ID, "")
			require.Equal(t, 200, get.Code, get.Body.String())
			assert.Contains(t, get.Body.String(), `"media_submission_receipt"`)
			assert.Contains(t, get.Body.String(), `"billing_state":"not_charged"`)
			assert.Contains(t, get.Body.String(), `"status":"FAILURE"`)
			failed := f.task(t, body.ID)
			assert.Zero(t, failed.Quota)
			assert.Empty(t, failed.PrivateData.UpstreamTaskID)
			var ledger model.MediaAdmission
			require.NoError(t, f.db.Where("id = ?", failed.PrivateData.MediaAdmissionID).First(&ledger).Error)
			assert.Equal(t, model.MediaReleased, ledger.State)
			assert.Zero(t, ledger.SendingAt)
			walletAfter, tokenAfter := f.balance(t)
			assert.Equal(t, wallet, walletAfter)
			assert.Equal(t, token, tokenAfter)
			f.mu.Lock()
			assert.Equal(t, test.limit, f.posts)
			assert.Zero(t, f.gets)
			f.mu.Unlock()
			other := f.h3RouterFixture.request(t, http.MethodGet, "/v1/tasks/"+body.ID, "", "h3routermember")
			assert.Equal(t, 404, other.Code, other.Body.String())
		})
	}
}

func TestMediaConcurrencyUncertainProviderNeverRepostsOrReleasesOnQuery404(t *testing.T) {
	f := newMediaNativeFixture(t)
	f.postMode = "malformed"
	post := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"free fixture malformed receipt","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 202, post.Code, post.Body.String())
	var unknown struct {
		ID string `json:"id"`
	}
	require.NoError(t, common.Unmarshal(post.Body.Bytes(), &unknown))
	require.NotEmpty(t, unknown.ID)
	assert.Contains(t, post.Body.String(), service.MediaUnknownCode)
	assert.NotContains(t, post.Body.String(), `"media_submission_receipt"`)
	wallet, token := f.balance(t)
	for range 2 {
		require.NoError(t, model.MigrateMediaAdmission(f.db))
		get := f.request(t, http.MethodGet, "/v1/tasks/"+unknown.ID, "")
		assert.Equal(t, 200, get.Code)
		assert.Contains(t, get.Body.String(), `"status":"UNKNOWN"`)
		service.RunTaskPollingOnce(context.Background(), nil)
	}
	var pool model.MediaPool
	require.NoError(t, f.db.First(&pool).Error)
	assert.Equal(t, 1, pool.Occupied)
	f.mu.Lock()
	assert.Equal(t, 1, f.posts)
	assert.Zero(t, f.gets, "unknown provider ID cannot be invented for a query")
	f.postMode, f.queryMode = "", "http404"
	f.mu.Unlock()
	queued := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"second independent work","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 200, queued.Code, queued.Body.String())
	for range 3 {
		service.RunTaskPollingOnce(context.Background(), nil)
	}
	require.NoError(t, f.db.First(&pool).Error)
	assert.Equal(t, 2, pool.Occupied, "404 and local poll-failure thresholds do not establish provider terminal")
	assert.Less(t, f.task(t, unknown.ID).Quota, wallet)
	walletAfter, tokenAfter := f.balance(t)
	assert.Less(t, walletAfter, wallet)
	assert.Less(t, tokenAfter, token)
	f.mu.Lock()
	f.queryMode = "failed"
	f.mu.Unlock()
	service.RunTaskPollingOnce(context.Background(), nil)
	require.NoError(t, f.db.First(&pool).Error)
	assert.Equal(t, 1, pool.Occupied, "only the known provider task's confirmed terminal releases")
	afterRefund, afterTokenRefund := f.balance(t)
	service.RunTaskPollingOnce(context.Background(), nil)
	finalWallet, finalToken := f.balance(t)
	assert.Equal(t, afterRefund, finalWallet)
	assert.Equal(t, afterTokenRefund, finalToken)
	f.mu.Lock()
	assert.Equal(t, 2, f.posts)
	f.mu.Unlock()
}

func TestMediaConcurrencyUnknownPreservesOriginalUpstreamErrorDiagnostics(t *testing.T) {
	f := newMediaNativeFixture(t)
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	f.postMode = "http503"
	response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"free diagnostic fixture","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 202, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), service.MediaUnknownCode)
	assert.NotContains(t, response.Body.String(), `"media_submission_receipt"`)
	var errors []model.Log
	require.NoError(t, f.logDB.Where("type = ?", model.LogTypeError).Find(&errors).Error)
	require.NotEmpty(t, errors)
	matched := false
	for _, entry := range errors {
		if strings.Contains(entry.Content, "503") {
			matched = true
		}
		assert.Zero(t, entry.Quota)
	}
	assert.True(t, matched, "original upstream status remains visible in administrative error logs")
	var channel model.Channel
	require.NoError(t, f.db.First(&channel, 880).Error)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	f.mu.Lock()
	assert.Equal(t, 1, f.posts)
	assert.Zero(t, f.gets)
	f.mu.Unlock()
}

func TestMediaConcurrencyFrozenHistoricalKeySurvivesChannelRotationAndDeletion(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted_%t", deleted), func(t *testing.T) {
			f := newMediaNativeFixture(t)
			ctx := context.Background()
			const originalKey = "synthetic-media-blackrxai-video"
			for i := range 4 {
				legacy := &model.Task{TaskID: fmt.Sprintf("task_frozen_history_%d", i), Platform: "blackrxai-video", UserId: 791, ChannelId: 880, Status: model.TaskStatusQueued,
					PrivateData: model.TaskPrivateData{Key: originalKey, UpstreamTaskID: fmt.Sprintf("provider-history-%d", i)}, Properties: model.Properties{OriginModelName: "bt-seedance-2.0"}}
				require.NoError(t, f.db.Create(legacy).Error)
			}
			if deleted {
				require.NoError(t, f.db.Delete(&model.Channel{}, 880).Error)
			} else {
				require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 880).Update("key", "synthetic-rotated-key").Error)
			}
			for index, key := range []string{originalKey, "synthetic-independent-key"} {
				baseURL := "https://blackrx-fixture.invalid"
				channel := &model.Channel{Id: 882 + index, Type: constant.ChannelTypeTaskPlugin, Key: key, BaseURL: &baseURL, Models: "bt-seedance-2.0", Group: "default", Status: common.ChannelStatusEnabled}
				channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "blackrxai-video"})
				require.NoError(t, f.db.Create(channel).Error)
			}
			require.NoError(t, model.RefreshMediaConcurrencyPools(ctx))
			attempt := &model.Task{TaskID: "task_frozen_history_blocked", Platform: "blackrxai-video", UserId: 791, ChannelId: 882, Properties: model.Properties{OriginModelName: "bt-seedance-2.0"}}
			assert.ErrorIs(t, model.AcquireMediaAdmission(ctx, attempt, originalKey, model.MediaVideo), model.ErrMediaCapacity)
			attempt.TaskID, attempt.ChannelId = "task_independent_allowed", 883
			require.NoError(t, model.AcquireMediaAdmission(ctx, attempt, "synthetic-independent-key", model.MediaVideo))
			var admissions []model.MediaAdmission
			require.NoError(t, f.db.Where("channel_id = ?", 880).Find(&admissions).Error)
			require.Len(t, admissions, 4)
			for _, admission := range admissions {
				assert.Equal(t, model.MediaUnknown, admission.State)
				assert.Equal(t, admissions[0].PoolID, admission.PoolID)
			}
			service.RunTaskPollingOnce(ctx, nil)
			f.mu.Lock()
			assert.Zero(t, f.posts)
			assert.Zero(t, f.gets, "frozen key alone is not original route provenance")
			f.mu.Unlock()
		})
	}
}

type mediaRouteProbeTransport struct {
	requests  *int
	forwarded *bool
	key       string
}

func (transport mediaRouteProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	*transport.requests++
	*transport.forwarded = *transport.forwarded || request.Header.Get("Authorization") == "Bearer "+transport.key
	id := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
	body := fmt.Sprintf(`{"id":%q,"object":"video","status":"failed","error":{"message":"synthetic unrelated route terminal"}}`, id)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestMediaConcurrencyPollRejectsChangedExecutionRoute(t *testing.T) {
	for _, change := range []string{"base_url", "type", "plugin", "proxy", "organization", "auth_header", "missing_provenance", "display_only"} {
		t.Run(change, func(t *testing.T) {
			f := newMediaNativeFixture(t)
			accepted := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"synthetic local fixture","seconds":10,"size":"1792x1024"}`)
			require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
			var response struct {
				ID string `json:"id"`
			}
			require.NoError(t, common.Unmarshal(accepted.Body.Bytes(), &response))
			task := f.task(t, response.ID)
			require.NotEmpty(t, task.PrivateData.RouteDigest)
			var original model.Channel
			require.NoError(t, f.db.First(&original, 880).Error)
			changed := original
			switch change {
			case "base_url":
				value := "https://unrelated-route.invalid"
				changed.BaseURL = &value
			case "type":
				changed.Type = constant.ChannelTypeNewAPI
			case "plugin":
				setting := changed.GetSetting()
				setting.TaskPluginKey = "toapis-image"
				changed.SetSetting(setting)
			case "proxy":
				setting := changed.GetSetting()
				setting.Proxy = "http://unrelated-proxy.invalid"
				changed.SetSetting(setting)
			case "organization":
				value := "unrelated-org"
				changed.OpenAIOrganization = &value
			case "auth_header":
				value := `{"Authorization":"Bearer unrelated-auth"}`
				changed.HeaderOverride = &value
			case "missing_provenance":
				task.PrivateData.RouteDigest = ""
				require.NoError(t, f.db.Model(&model.Task{}).Where("id = ?", task.ID).Update("private_data", task.PrivateData).Error)
			case "display_only":
				changed.Name = "Renamed display label"
				other := changed.GetOtherSettings()
				other.DisableTaskPollingSleep = false
				changed.SetOtherSettings(other)
			}
			require.NoError(t, f.db.Save(&changed).Error)
			model.InitChannelCache()
			wallet, token := f.balance(t)
			f.mu.Lock()
			f.queryMode = "failed"
			f.mu.Unlock()
			observed, forwarded := 0, false
			clients := []*http.Client{service.GetHttpClient(), service.GetSSRFProtectedHTTPClient()}
			transports := make([]http.RoundTripper, len(clients))
			for i, client := range clients {
				transports[i] = client.Transport
				client.Transport = mediaRouteProbeTransport{requests: &observed, forwarded: &forwarded, key: task.PrivateData.Key}
			}
			t.Cleanup(func() {
				for i, client := range clients {
					client.Transport = transports[i]
				}
			})
			service.RunTaskPollingOnce(context.Background(), nil)
			var pool model.MediaPool
			require.NoError(t, f.db.First(&pool).Error)
			stored := f.task(t, response.ID)
			f.mu.Lock()
			if change == "display_only" {
				assert.Equal(t, 1, observed)
				assert.Zero(t, pool.Occupied)
				assert.EqualValues(t, model.TaskStatusFailure, stored.Status)
			} else {
				assert.Zero(t, observed, "a different route must never receive any query or supply a terminal")
				assert.False(t, forwarded, "the frozen bearer must never reach a changed route")
				assert.Equal(t, 1, pool.Occupied)
				assert.EqualValues(t, model.TaskStatusUnknown, stored.Status)
			}
			assert.Equal(t, 1, f.posts)
			f.mu.Unlock()
			if change == "display_only" {
				return
			}
			afterWallet, afterToken := f.balance(t)
			assert.Equal(t, wallet, afterWallet)
			assert.Equal(t, token, afterToken)
			if change == "missing_provenance" {
				return
			}
			for i, client := range clients {
				client.Transport = transports[i]
			}
			require.NoError(t, f.db.Save(&original).Error)
			model.InitChannelCache()
			service.RunTaskPollingOnce(context.Background(), nil)
			require.NoError(t, f.db.First(&pool).Error)
			assert.Zero(t, pool.Occupied, "restoring the original route allows normal same-task recovery")
			assert.EqualValues(t, model.TaskStatusFailure, f.task(t, response.ID).Status)
		})
	}
}

func TestMediaConcurrencyHistoricalTerminalSnapshotsOnlyExcludeCapacity(t *testing.T) {
	for _, platform := range []string{"blackrxai-video", "toapis-image"} {
		for _, evidence := range []string{"exact", "numeric_id", "other_id", "other_model", "host_error", "wrong_object", "running", "no_error_code", "plugin_conflict", "missing_provenance", "unknown_version", "wrong_path", "new_ledger"} {
			t.Run(platform+"/"+evidence, func(t *testing.T) {
				f := newMediaNativeFixture(t)
				kind, modelName, object, wireModel, errorCode, channelID := model.MediaVideo, "bt-seedance-2.0", "video", "bt-seedance-2.0", "video_generation_failed", 880
				if platform == "toapis-image" {
					kind, modelName, object, wireModel, errorCode, channelID = model.MediaImage, "yunshu-internal-gpt-image-2", "generation.task", "gpt-image-2", "generation_failed", 881
				}
				legacy := &model.Task{TaskID: "task_legacy_recorded_terminal", Platform: constant.TaskPlatform(platform), UserId: 791, ChannelId: channelID, Status: model.TaskStatusFailure, Progress: "100%", Quota: 19,
					PrivateData: model.TaskPrivateData{UpstreamTaskID: "original-provider-id"}, Properties: model.Properties{OriginModelName: modelName, UpstreamModelName: modelName}}
				version, path := "1.0.2", "/v1/videos"
				if platform == "toapis-image" {
					version, path = "1.2.0", "/toapis/v1/images/generations"
				}
				legacy.PrivateData.Execution = &model.TaskExecutionSnapshot{RequestID: "original-request-id", RequestPath: path, TaskPlugin: &model.TaskPluginSnapshot{Key: platform, APIVersion: 1, Version: version}}
				envelope := map[string]any{"id": "original-provider-id", "object": object, "model": wireModel, "status": "failed", "error": map[string]any{"code": errorCode}}
				switch evidence {
				case "numeric_id":
					envelope["id"] = 12345
				case "other_id":
					envelope["id"] = "other-provider-task"
				case "other_model":
					envelope["model"] = "other-model"
				case "host_error":
					envelope["code"] = "task_timeout"
				case "wrong_object":
					envelope["object"] = "unrecognized.task"
				case "running":
					envelope["status"] = "queued"
				case "no_error_code":
					envelope["error"] = map[string]any{"message": "local timeout"}
				case "plugin_conflict":
					legacy.PrivateData.Execution.TaskPlugin.Key = "unrelated-plugin"
				case "missing_provenance":
					legacy.PrivateData.Execution = nil
				case "unknown_version":
					legacy.PrivateData.Execution.TaskPlugin.Version = "99.0.0"
				case "wrong_path":
					legacy.PrivateData.Execution.RequestPath = "/unrelated/path"
				case "new_ledger":
					legacy.PrivateData.MediaAdmissionID = legacy.TaskID
				}
				legacy.SetData(envelope)
				require.NoError(t, f.db.Create(legacy).Error)
				before := f.task(t, legacy.TaskID)
				wallet, token := f.balance(t)
				if evidence == "new_ledger" {
					// Already gated work cannot release a slot based on this old-data exception.
					var channel model.Channel
					require.NoError(t, f.db.First(&channel, channelID).Error)
					require.NoError(t, f.db.Model(legacy).Update("status", model.TaskStatusUnknown).Error)
					require.NoError(t, f.db.Create(&model.MediaPool{ID: "existing-pool", IdentityDigest: "unrelated-pool-identity", MediaKind: kind, Occupied: 1, Reconciled: true}).Error)
					require.NoError(t, f.db.Create(&model.MediaAdmission{ID: legacy.TaskID, TaskDBID: legacy.ID, PoolID: "existing-pool", ChannelID: channelID, State: model.MediaUnknown, SendingAt: 1}).Error)
					service.RunTaskPollingOnce(context.Background(), nil)
					var pool model.MediaPool
					require.NoError(t, f.db.Where("id = ?", "existing-pool").First(&pool).Error)
					assert.Equal(t, 1, pool.Occupied)
					assert.False(t, model.MediaLegacyProviderTerminalConfirmed(legacy))
				} else {
					var channel model.Channel
					require.NoError(t, f.db.First(&channel, channelID).Error)
					attempt := &model.Task{TaskID: "task_after_historical_terminal", Platform: legacy.Platform, UserId: 791, ChannelId: channelID, Properties: legacy.Properties}
					err := model.AcquireMediaAdmission(context.Background(), attempt, channel.Key, kind)
					if evidence == "exact" {
						require.NoError(t, err)
					} else {
						assert.ErrorIs(t, err, model.ErrMediaLegacyIdentity)
					}
					assert.Equal(t, before, f.task(t, legacy.TaskID), "capacity classification never rewrites old evidence, key, route or billing")
				}
				afterWallet, afterToken := f.balance(t)
				assert.Equal(t, wallet, afterWallet)
				assert.Equal(t, token, afterToken)
				f.mu.Lock()
				assert.Zero(t, f.posts)
				assert.Zero(t, f.gets)
				f.mu.Unlock()
			})
		}
	}
}

func TestMediaConcurrencyContentDoesNotRebindAcceptedCredential(t *testing.T) {
	f := newMediaNativeFixture(t)
	var original model.Channel
	require.NoError(t, f.db.First(&original, 880).Error)
	organization, headers := "original-organization", `{"X-Provider-Auth":"original-auth-setting"}`
	original.OpenAIOrganization, original.HeaderOverride = &organization, &headers
	require.NoError(t, f.db.Save(&original).Error)
	model.RecordBlackrxCatalog(&original, original.Key, []string{"bt-seedance-2.0"}, time.Now())
	model.InitChannelCache()
	accepted := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"local content regression","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	var response struct {
		ID string `json:"id"`
	}
	require.NoError(t, common.Unmarshal(accepted.Body.Bytes(), &response))
	task := f.task(t, response.ID)
	require.NoError(t, f.db.Model(task).Update("status", model.TaskStatusSuccess).Error)
	require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 880).Update("base_url", "https://changed-content-route.invalid").Error)
	model.InitChannelCache()
	observed, forwarded := 0, false
	for _, client := range []*http.Client{service.GetHttpClient(), service.GetSSRFProtectedHTTPClient()} {
		previous := client.Transport
		client.Transport = mediaRouteProbeTransport{requests: &observed, forwarded: &forwarded, key: task.PrivateData.Key}
		t.Cleanup(func() { client.Transport = previous })
	}
	content := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts/video/content", "")
	assert.Equal(t, http.StatusInternalServerError, content.Code, content.Body.String())
	assert.Zero(t, observed)
	assert.False(t, forwarded)
	assert.NotContains(t, content.Body.String(), task.PrivateData.Key)
}

func TestMediaConcurrencyHistoricalSavedURLSurvivesDeletedChannelWithoutCredentials(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	originalSetting := *system_setting.GetFetchSetting()
	system_setting.GetFetchSetting().AllowPrivateIp = true
	system_setting.GetFetchSetting().AllowedPorts = []string{"1-65535"}
	t.Cleanup(func() { *system_setting.GetFetchSetting() = originalSetting })
	calls, leaked := 0, false
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		leaked = leaked || r.Header.Get("Authorization") != "" || r.Header.Get("X-Provider-Auth") != ""
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.WriteString(w, "saved-signed-video")
	}))
	t.Cleanup(cdn.Close)
	for _, client := range []*http.Client{service.GetHttpClient(), service.GetSSRFProtectedHTTPClient()} {
		previous := client.Transport
		client.Transport = http.DefaultTransport
		t.Cleanup(func() { client.Transport = previous })
	}
	legacy := &model.Task{TaskID: "task_legacy_saved_video", Platform: "hailuo", UserId: 791, ChannelId: 790, Status: model.TaskStatusSuccess, Progress: "100%", Properties: model.Properties{OriginModelName: "MiniMax-H3"},
		PrivateData: model.TaskPrivateData{UpstreamTaskID: "legacy-original-provider", Execution: &model.TaskExecutionSnapshot{TaskPlugin: &model.TaskPluginSnapshot{Key: "hailuo", APIVersion: 1, Version: "1.0.0"}}}}
	legacy.SetData(map[string]any{"task": map[string]any{"id": "legacy-original-provider", "status": "succeeded", "content": map[string]any{"url": cdn.URL + "/signed-video"}}})
	require.NoError(t, f.db.Create(legacy).Error)
	require.NoError(t, f.db.Delete(&model.Channel{}, 790).Error)
	model.InitChannelCache()
	content := f.request(t, http.MethodGet, "/v1/tasks/"+legacy.TaskID+"/artifacts/video/content", "", "h3routeradmin")
	require.Equal(t, http.StatusOK, content.Code, content.Body.String())
	assert.Equal(t, "saved-signed-video", content.Body.String())
	assert.Equal(t, 1, calls)
	assert.False(t, leaked)
	stored := f.task(t, legacy.TaskID)
	assert.Empty(t, stored.PrivateData.Key)
	assert.Empty(t, stored.PrivateData.RouteDigest)
}

func TestMediaCreateRejectionSupportedModelContract(t *testing.T) {
	source, err := os.ReadFile("../deploy/instance/blackrxai-video-plugin.js")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Unregister(plugin.Meta.Key)) })
	require.Equal(t, model.BlackrxCreateRejectionSourceHash, plugin.SourceHash)
	for _, modelName := range plugin.Meta.Models {
		t.Run(modelName, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"error":{"message":"模型不存在：%s","type":"invalid_request_error"}}`, modelName))
			assert.True(t, model.IsBlackrxModelMissingCreateBody(body, modelName))
			assert.False(t, model.IsBlackrxModelMissingCreateBody(body, modelName+"-unlisted"))
			otherModel := "bt-seedance-2.0"
			if modelName == otherModel {
				otherModel = "sd2-hn2-480-pro"
			}
			assert.False(t, model.IsBlackrxModelMissingCreateBody(body, otherModel), "another supported model is still a mismatched refusal")
		})
	}
	for _, unsupported := range []string{"", "video-sd25-720p", "other-model", "BT-seedance-2.0", "sd2-hn2-480-pro "} {
		body := []byte(fmt.Sprintf(`{"error":{"message":"模型不存在：%s","type":"invalid_request_error"}}`, unsupported))
		assert.False(t, model.IsBlackrxModelMissingCreateBody(body, unsupported))
	}
}

func TestMediaCreateRejectionExactResponseRefundAndRecovery(t *testing.T) {
	for _, modelName := range []string{"bt-seedance-2.0", "sd2-hn2-480-pro", "sd2-th-2.0-720p"} {
		for _, test := range []struct {
			name, body string
			status     int
			rejected   bool
		}{
			{"exact", `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 404, true},
			{"whitespace", ` { "error" : { "type" : "invalid_request_error", "message" : "模型不存在：bt-seedance-2.0" } } `, 404, true},
			{"extra_id", `{"id":"already-created","error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 404, false},
			{"nested_id", `{"error":{"id":"already-created","message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 404, false},
			{"receipt", `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"},"media_submission_receipt":{"billing_state":"refunded"}}`, 404, false},
			{"duplicate", `{"error":{"message":"accepted","message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 404, false},
			{"duplicate_error", `{"error":{"id":"already-created"},"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 404, false},
			{"escaped_model", `{"error":{"message":"模型不存在：bt-seedance-2\u002e0","type":"invalid_request_error"}}`, 404, false},
			{"html", `<html>模型不存在：bt-seedance-2.0</html>`, 404, false},
			{"other_model", `{"error":{"message":"模型不存在：other-model","type":"invalid_request_error"}}`, 404, false},
			{"other_type", `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"server_error"}}`, 404, false},
			{"contradiction", `{"status":"queued","error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 404, false},
			{"truncated", `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}`, 404, false},
			{"503", `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 503, false},
			{"429", `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 429, false},
			{"2xx_bad", `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`, 200, false},
		} {
			t.Run(modelName+"/"+test.name, func(t *testing.T) {
				f := newMediaNativeFixture(t)
				seconds := 10
				accountKey := "synthetic-media-blackrxai-video"
				if modelName != "bt-seedance-2.0" {
					seconds = 15
					accountKey += "-" + modelName
					require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 880).Update("key", accountKey).Error)
					model.InitChannelCache()
					pricing, err := common.Marshal(map[string]string{"blackrxai-video::" + modelName: `u("seconds") * 0.02`})
					require.NoError(t, err)
					require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: string(pricing)}))
					f.catalogByKey = map[string]string{accountKey: fmt.Sprintf(`{"data":[{"id":%q}]}`, modelName)}
				}
				f.postStatus, f.postBody = test.status, strings.ReplaceAll(test.body, "bt-seedance-2.0", modelName)
				beforeWallet, beforeToken := f.balance(t)
				response := f.request(t, http.MethodPost, "/v1/videos", fmt.Sprintf(`{"model":%q,"prompt":"free loopback create refusal","seconds":%d,"size":"1792x1024"}`, modelName, seconds))
				var wire struct {
					ID      string                          `json:"id"`
					Receipt *service.MediaSubmissionReceipt `json:"media_submission_receipt"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
				require.NotEmpty(t, wire.ID, response.Body.String())
				task := f.task(t, wire.ID)
				var admission model.MediaAdmission
				require.NoError(t, f.db.Where("id = ?", wire.ID).First(&admission).Error)
				assert.Positive(t, admission.SendingAt)
				assert.False(t, admission.ProviderTerminal)
				assert.Equal(t, accountKey, task.PrivateData.Key)
				assert.NotEmpty(t, task.PrivateData.RouteDigest)
				assert.Empty(t, task.PrivateData.UpstreamTaskID)
				var pool model.MediaPool
				require.NoError(t, f.db.Where("id = ?", admission.PoolID).First(&pool).Error)
				afterWallet, afterToken := f.balance(t)
				if test.rejected {
					assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
					require.NotNil(t, wire.Receipt)
					assert.Equal(t, service.MediaRejectedCode, wire.Receipt.SubmissionCode)
					assert.Equal(t, "not_submitted", wire.Receipt.SubmissionState)
					assert.Equal(t, "refunded", wire.Receipt.BillingState)
					assert.True(t, wire.Receipt.RefundConfirmed)
					assert.True(t, wire.Receipt.ProviderResponseReceived)
					assert.True(t, wire.Receipt.UpstreamSubmitted, "a confirmed create refusal is not a not-sent proof")
					if test.name == "exact" {
						t.Logf("API_HANDLER_FIXTURE rejected-create %s", response.Body.String())
					}
					assert.Equal(t, "upstream_model_not_available", wire.Receipt.RejectionReason)
					assert.Equal(t, model.MediaReleased, admission.State)
					assert.Equal(t, "refunded", admission.BillingState)
					assert.Zero(t, pool.Occupied)
					assert.Zero(t, task.Quota)
					assert.EqualValues(t, model.TaskStatusFailure, task.Status)
					assert.Equal(t, beforeWallet, afterWallet)
					assert.Equal(t, beforeToken, afterToken)
					var eventsBefore int64
					require.NoError(t, f.db.Model(&model.WalletFundingEvent{}).Where("user_id = ? AND request_id = ?", task.UserId, task.PrivateData.FundingRequestId).Count(&eventsBefore).Error)
					assert.EqualValues(t, 2, eventsBefore, "exactly one reservation and its reversal")
					require.NoError(t, model.RefundMediaCreateRejection(context.Background(), task))
					require.NoError(t, model.RefundMediaCreateRejection(context.Background(), task))
					var eventsAfter int64
					require.NoError(t, f.db.Model(&model.WalletFundingEvent{}).Where("user_id = ? AND request_id = ?", task.UserId, task.PrivateData.FundingRequestId).Count(&eventsAfter).Error)
					assert.Equal(t, eventsBefore, eventsAfter, "recovery cannot append another refund")
				} else {
					assert.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
					assert.Nil(t, wire.Receipt)
					assert.Equal(t, model.MediaUnknown, admission.State)
					assert.Empty(t, admission.CreateRejection)
					assert.Equal(t, 1, pool.Occupied)
					assert.Less(t, afterWallet, beforeWallet)
					assert.Less(t, afterToken, beforeToken)
					assert.Error(t, model.RefundMediaCreateRejection(context.Background(), task))
				}
				for range 2 {
					require.NoError(t, model.MigrateMediaAdmission(f.db))
					get := f.request(t, http.MethodGet, "/v1/tasks/"+wire.ID, "")
					require.Equal(t, 200, get.Code)
					if test.rejected {
						assert.Contains(t, get.Body.String(), `"provider_response_received":true`)
						assert.Contains(t, get.Body.String(), `"status":"FAILURE"`)
						if test.name == "exact" {
							t.Logf("API_HANDLER_FIXTURE rejected-get %s", get.Body.String())
						}
					} else {
						assert.NotContains(t, get.Body.String(), `"media_submission_receipt"`)
						assert.Contains(t, get.Body.String(), `"status":"UNKNOWN"`)
					}
				}
				f.mu.Lock()
				assert.Equal(t, 1, f.posts)
				assert.Zero(t, f.gets)
				f.mu.Unlock()
				finalWallet, finalToken := f.balance(t)
				assert.Equal(t, afterWallet, finalWallet)
				assert.Equal(t, afterToken, finalToken)
			})
		}
	}
}

func TestMediaCreateRejectionRefundFailureIsAtomicAndRecoverable(t *testing.T) {
	for _, failure := range []string{"proof_write", "token_refund", "subscription_refund"} {
		t.Run(failure, func(t *testing.T) {
			f := newMediaNativeFixture(t)
			f.postStatus = 404
			f.postBody = `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`
			if failure == "subscription_refund" {
				require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("setting", `{"billing_preference":"subscription_only"}`).Error)
				plan := model.SubscriptionPlan{Id: 791, Title: "local rejection test", TotalAmount: 100000000, QuotaResetPeriod: "never"}
				require.NoError(t, f.db.Create(&plan).Error)
				sub := model.UserSubscription{Id: 791, UserId: 791, PlanId: 791, AmountTotal: 100000000, Status: "active", StartTime: time.Now().Unix(), EndTime: time.Now().Add(time.Hour).Unix()}
				require.NoError(t, f.db.Create(&sub).Error)
			}
			beforeWallet, beforeToken := f.balance(t)
			callback := "media_create_rejection_" + failure
			require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok {
					return
				}
				fail := failure == "proof_write" && tx.Statement.Table == "media_admissions" && values["create_rejection"] != nil || failure == "token_refund" && tx.Statement.Table == "tokens" && values["remain_quota"] != nil && values["accessed_time"] == nil || failure == "subscription_refund" && tx.Statement.Table == "subscription_pre_consume_records" && values["status"] == "refunded"
				if fail {
					tx.AddError(fmt.Errorf("synthetic %s failure", failure))
				}
			}))
			response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"free atomic refund fixture","seconds":10,"size":"1792x1024"}`)
			require.NoError(t, f.db.Callback().Update().Remove(callback))
			require.Equal(t, 202, response.Code, response.Body.String())
			var wire struct {
				ID string `json:"id"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
			task := f.task(t, wire.ID)
			var admission model.MediaAdmission
			require.NoError(t, f.db.Where("id = ?", wire.ID).First(&admission).Error)
			assert.Equal(t, model.MediaUnknown, admission.State)
			assert.Positive(t, admission.SendingAt)
			assert.False(t, admission.ProviderTerminal)
			assert.EqualValues(t, model.TaskStatusUnknown, task.Status)
			assert.Positive(t, task.Quota)
			wallet, token := f.balance(t)
			assert.Less(t, token, beforeToken)
			if failure == "subscription_refund" {
				assert.Equal(t, beforeWallet, wallet)
			} else {
				assert.Less(t, wallet, beforeWallet)
			}
			if failure == "proof_write" {
				assert.Empty(t, admission.CreateRejection)
				assert.Equal(t, "reserved", admission.BillingState)
				assert.Error(t, model.RefundMediaCreateRejection(context.Background(), task))
			} else {
				assert.NotEmpty(t, admission.CreateRejection)
				assert.Equal(t, "refund_pending", admission.BillingState)
				pending, err := model.PendingMediaCreateRejectionTasks(context.Background(), 100)
				require.NoError(t, err)
				require.Len(t, pending, 1)
				assert.Equal(t, task.TaskID, pending[0].TaskID)
				errors := make(chan error, 2)
				var wait sync.WaitGroup
				for range 2 {
					wait.Go(func() { errors <- model.RefundMediaCreateRejection(context.Background(), task) })
				}
				wait.Wait()
				close(errors)
				for err := range errors {
					require.NoError(t, err)
				}
				require.NoError(t, model.RefundMediaCreateRejection(context.Background(), task))
				wallet, token = f.balance(t)
				assert.Equal(t, beforeWallet, wallet)
				assert.Equal(t, beforeToken, token)
				get := f.request(t, http.MethodGet, "/v1/tasks/"+wire.ID, "")
				assert.Contains(t, get.Body.String(), `"billing_state":"refunded"`)
				var pool model.MediaPool
				require.NoError(t, f.db.Where("id = ?", admission.PoolID).First(&pool).Error)
				assert.Zero(t, pool.Occupied)
			}
			f.mu.Lock()
			assert.Equal(t, 1, f.posts)
			assert.Zero(t, f.gets)
			f.mu.Unlock()
		})
	}
}

func TestMediaCreateRejectionMigrationPreservesOriginalUnknownTasks(t *testing.T) {
	f := newMediaNativeFixture(t)
	response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"synthetic pre-upgrade accepted task","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	var wire struct {
		ID string `json:"id"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
	before := f.task(t, wire.ID)
	var admission model.MediaAdmission
	require.NoError(t, f.db.Where("id = ?", wire.ID).First(&admission).Error)
	// Previous released schema has no host create-refusal proof. Upgrading it
	// must never reinterpret an old sent task as unsent or reset its capacity.
	require.NoError(t, f.db.Migrator().DropColumn(&model.MediaAdmission{}, "CreateRejection"))
	for range 2 {
		require.NoError(t, model.MigrateMediaAdmission(f.db))
	}
	require.True(t, f.db.Migrator().HasColumn(&model.MediaAdmission{}, "CreateRejection"))
	after := f.task(t, wire.ID)
	assert.Equal(t, before, after)
	var upgraded model.MediaAdmission
	require.NoError(t, f.db.Where("id = ?", wire.ID).First(&upgraded).Error)
	assert.Equal(t, admission, upgraded)
	assert.Empty(t, upgraded.CreateRejection)
	receipt, terminal, err := service.ProjectMediaTaskSubmission(context.Background(), after)
	require.NoError(t, err)
	assert.Nil(t, receipt)
	assert.False(t, terminal)
	assert.Error(t, model.RefundMediaCreateRejection(context.Background(), after))
	var pool model.MediaPool
	require.NoError(t, f.db.Where("id = ?", admission.PoolID).First(&pool).Error)
	assert.Equal(t, 1, pool.Occupied)
	var duplicate model.MediaAdmission = upgraded
	duplicate.ID = "other-upgrade-task"
	assert.Error(t, f.db.Create(&duplicate).Error, "task-to-admission uniqueness must survive both upgrade runs")
}

func TestMediaCreateRejectionRestoresOriginalPaidAndGiftLots(t *testing.T) {
	f := newMediaNativeFixture(t)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("quota", 0).Error)
	cash := decimal.RequireFromString("0.064")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return model.CreditWalletFundingWithTx(tx, 791, 40000, model.WalletFundingPaid, "synthetic-verified-recharge", &cash)
	}))
	require.NoError(t, model.CreditUserGiftQuota(791, 100000, "synthetic-registration-gift"))
	var beforeLots []model.WalletFundingLot
	require.NoError(t, f.db.Where("user_id = ? AND remaining_quota > 0", 791).Order("id").Find(&beforeLots).Error)
	require.Len(t, beforeLots, 2)
	f.postStatus, f.postBody = 404, `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`
	beforeWallet, beforeToken := f.balance(t)
	response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"synthetic mixed funding refusal","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 400, response.Code, response.Body.String())
	var wire struct {
		ID string `json:"id"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
	task := f.task(t, wire.ID)
	require.NoError(t, model.RefundMediaCreateRejection(context.Background(), task))
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, beforeWallet, afterWallet)
	assert.Equal(t, beforeToken, afterToken)
	var afterLots []model.WalletFundingLot
	require.NoError(t, f.db.Where("user_id = ? AND remaining_quota > 0", 791).Order("id").Find(&afterLots).Error)
	assert.Equal(t, beforeLots, afterLots)
	funding, err := model.GetWalletFundingSnapshot(791, task.PrivateData.FundingRequestId, "relay")
	require.NoError(t, err)
	assert.Zero(t, funding.Quota)
	assert.Zero(t, funding.PaidQuota)
	assert.Zero(t, funding.GiftQuota)
	var user model.User
	require.NoError(t, f.db.First(&user, 791).Error)
	assert.Zero(t, user.UsedQuota)
	var channel model.Channel
	require.NoError(t, f.db.First(&channel, 880).Error)
	assert.Zero(t, channel.UsedQuota)
}

func TestMediaCreateRejectionRejectsChangedPluginSource(t *testing.T) {
	f := newMediaNativeFixture(t)
	source, err := os.ReadFile("../deploy/instance/blackrxai-video-plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.DefaultRegistry.Register(string(source)+"\n// a different, unreviewed implementation", jsplugin.Options{})
	require.NoError(t, err)
	assert.NotEqual(t, model.BlackrxCreateRejectionSourceHash, plugin.SourceHash)
	f.postStatus, f.postBody = 404, `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`
	beforeWallet, beforeToken := f.balance(t)
	response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"synthetic unreviewed plugin refusal","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 202, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), `"media_submission_receipt"`)
	var wire struct {
		ID string `json:"id"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
	task := f.task(t, wire.ID)
	require.NotNil(t, task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, plugin.SourceHash, task.PrivateData.Execution.TaskPlugin.SourceHash)
	assert.Error(t, model.RefundMediaCreateRejection(context.Background(), task))
	afterWallet, afterToken := f.balance(t)
	assert.Less(t, afterWallet, beforeWallet)
	assert.Less(t, afterToken, beforeToken)
	pending, err := model.PendingMediaCreateRejectionTasks(context.Background(), 100)
	require.NoError(t, err)
	assert.Empty(t, pending)
	f.mu.Lock()
	assert.Equal(t, 1, f.posts)
	assert.Zero(t, f.gets)
	f.mu.Unlock()
}

func TestMediaCreateRejectionZeroPriceSubscriptionRestoresActualMinimum(t *testing.T) {
	f := newMediaNativeFixture(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: `{"blackrxai-video::bt-seedance-2.0":"u(\"seconds\") * 0"}`}))
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("setting", `{"billing_preference":"subscription_only"}`).Error)
	plan := model.SubscriptionPlan{Id: 791, Title: "zero-price rejection fixture", TotalAmount: 1000, QuotaResetPeriod: "never"}
	require.NoError(t, f.db.Create(&plan).Error)
	sub := model.UserSubscription{Id: 791, UserId: 791, PlanId: 791, AmountTotal: 1000, Status: "active", StartTime: time.Now().Unix(), EndTime: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, f.db.Create(&sub).Error)
	f.postStatus, f.postBody = 404, `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`
	beforeWallet, beforeToken := f.balance(t)
	response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"free subscription minimum rejection","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, 400, response.Code, response.Body.String())
	var record model.SubscriptionPreConsumeRecord
	require.NoError(t, f.db.Where("user_id = ?", 791).First(&record).Error)
	assert.EqualValues(t, 1, record.PreConsumed)
	assert.Equal(t, "refunded", record.Status)
	require.NoError(t, f.db.First(&sub, 791).Error)
	assert.Zero(t, sub.AmountUsed)
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, beforeWallet, afterWallet)
	assert.Equal(t, beforeToken, afterToken)
	assert.Contains(t, response.Body.String(), `"refund_confirmed":true`)
}

type mediaRejectionResponseTransport struct {
	next     http.RoundTripper
	mutation string
}

func (transport mediaRejectionResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if response == nil || request.Method != http.MethodPost {
		return response, err
	}
	cloned := request.Clone(request.Context())
	copied := *request.URL
	cloned.URL = &copied
	response.Request = cloned
	switch transport.mutation {
	case "wrong_host":
		cloned.URL.Host = "unrelated.invalid"
	case "wrong_path":
		cloned.URL.Path = "/v1/videos/not-a-create"
	case "wrong_method":
		cloned.Method = http.MethodGet
	case "receipt_header":
		response.Header.Set("X-Task-Id", "already-accepted")
	case "html_content_type":
		response.Header.Set("Content-Type", "text/html")
	}
	return response, err
}

func TestMediaCreateRejectionUntrustedTransportEvidenceKeepsOriginalReservation(t *testing.T) {
	for _, mutation := range []string{"transport", "wrong_host", "wrong_path", "wrong_method", "receipt_header", "html_content_type"} {
		t.Run(mutation, func(t *testing.T) {
			f := newMediaNativeFixture(t)
			if mutation == "transport" {
				f.postMode = "transport"
			} else {
				f.postStatus, f.postBody = 404, `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`
				for _, client := range []*http.Client{service.GetHttpClient(), service.GetSSRFProtectedHTTPClient()} {
					previous := client.Transport
					client.Transport = mediaRejectionResponseTransport{next: previous, mutation: mutation}
					t.Cleanup(func() { client.Transport = previous })
				}
			}
			beforeWallet, beforeToken := f.balance(t)
			response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"synthetic original transport evidence","seconds":10,"size":"1792x1024"}`)
			require.Equal(t, 202, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), `"media_submission_receipt"`)
			var wire struct {
				ID string `json:"id"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
			task := f.task(t, wire.ID)
			var admission model.MediaAdmission
			require.NoError(t, f.db.Where("id = ?", task.TaskID).First(&admission).Error)
			assert.Equal(t, model.MediaUnknown, admission.State)
			assert.Empty(t, admission.CreateRejection)
			assert.Positive(t, admission.SendingAt)
			assert.Error(t, model.RefundMediaCreateRejection(context.Background(), task))
			wallet, token := f.balance(t)
			assert.Less(t, wallet, beforeWallet)
			assert.Less(t, token, beforeToken)
			f.mu.Lock()
			assert.Equal(t, 1, f.posts)
			assert.Zero(t, f.gets)
			f.mu.Unlock()
		})
	}
}

func TestMediaCreateRejectionSubscriptionRecoveryNeverCreditsAnotherQuotaPeriod(t *testing.T) {
	for _, reset := range []string{"admin_keep_dates", "admin_advance_dates", "automatic_due", "legacy_missing_epoch"} {
		t.Run(reset, func(t *testing.T) {
			f := newMediaNativeFixture(t)
			require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("setting", `{"billing_preference":"subscription_only"}`).Error)
			plan := model.SubscriptionPlan{Id: 791, Title: "quota cycle refusal fixture", TotalAmount: 100000000, QuotaResetPeriod: model.SubscriptionResetNever}
			if reset == "automatic_due" {
				plan.QuotaResetPeriod = model.SubscriptionResetCustom
				plan.QuotaResetCustomSeconds = 3600
			}
			require.NoError(t, f.db.Create(&plan).Error)
			model.InvalidateSubscriptionPlanCache(plan.Id)
			now := time.Now().Unix()
			sub := model.UserSubscription{Id: 791, UserId: 791, PlanId: 791, AmountTotal: 100000000, Status: "active", StartTime: now - 600, EndTime: now + 7200, LastResetTime: now - 600, NextResetTime: now + 3000}
			require.NoError(t, f.db.Create(&sub).Error)
			f.postStatus, f.postBody = 404, `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`
			const callback = "media-rejection-cycle-delayed-refund"
			require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]any)
				if ok && tx.Statement.Table == "tokens" && values["remain_quota"] != nil && values["accessed_time"] == nil {
					tx.AddError(fmt.Errorf("synthetic refund delay before cycle change"))
				}
			}))
			response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"synthetic delayed original cycle refund","seconds":10,"size":"1792x1024"}`)
			require.NoError(t, f.db.Callback().Update().Remove(callback))
			require.Equal(t, 202, response.Code, response.Body.String())
			var wire struct {
				ID string `json:"id"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
			task := f.task(t, wire.ID)
			var admission model.MediaAdmission
			require.NoError(t, f.db.Where("id = ?", task.TaskID).First(&admission).Error)
			require.NotEmpty(t, admission.CreateRejection)
			require.Equal(t, "refund_pending", admission.BillingState)
			var original model.SubscriptionPreConsumeRecord
			require.NoError(t, f.db.Where("request_id = ?", task.PrivateData.Execution.RequestID).First(&original).Error)
			require.NotNil(t, original.QuotaEpoch)
			require.NoError(t, f.db.First(&sub, 791).Error)
			oldEpoch := sub.QuotaEpoch
			oldLast, oldNext := sub.LastResetTime, sub.NextResetTime
			switch reset {
			case "admin_keep_dates", "admin_advance_dates":
				_, err := model.AdminResetUserSubscriptionsByPlan(791, 791, reset == "admin_advance_dates")
				require.NoError(t, err)
			case "automatic_due":
				require.NoError(t, f.db.Model(&model.UserSubscription{}).Where("id = ?", 791).Updates(map[string]any{"last_reset_time": now - 7200, "next_reset_time": now - 1}).Error)
				count, err := model.ResetDueSubscriptions(10)
				require.NoError(t, err)
				require.Equal(t, 1, count)
			case "legacy_missing_epoch":
				require.NoError(t, f.db.Model(&model.SubscriptionPreConsumeRecord{}).Where("id = ?", original.Id).Update("quota_epoch", nil).Error)
			}
			require.NoError(t, f.db.First(&sub, 791).Error)
			if reset != "legacy_missing_epoch" {
				assert.Greater(t, sub.QuotaEpoch, oldEpoch)
				assert.Zero(t, sub.AmountUsed)
			}
			if reset == "admin_keep_dates" {
				assert.Equal(t, oldLast, sub.LastResetTime)
				assert.Equal(t, oldNext, sub.NextResetTime)
			}
			if reset != "legacy_missing_epoch" {
				_, err := model.PreConsumeUserSubscription("new-cycle-"+task.TaskID, 791, "bt-seedance-2.0", 0, 200000)
				require.NoError(t, err)
			}
			require.NoError(t, f.db.First(&sub, 791).Error)
			newUsed := sub.AmountUsed
			wallet, token := f.balance(t)
			for range 2 {
				assert.Error(t, model.RefundMediaCreateRejection(context.Background(), task))
			}
			var after model.UserSubscription
			require.NoError(t, f.db.First(&after, 791).Error)
			assert.Equal(t, newUsed, after.AmountUsed)
			assert.Equal(t, sub.QuotaEpoch, after.QuotaEpoch)
			afterWallet, afterToken := f.balance(t)
			assert.Equal(t, wallet, afterWallet)
			assert.Equal(t, token, afterToken)
			require.NoError(t, f.db.Where("id = ?", task.TaskID).First(&admission).Error)
			assert.Equal(t, "refund_pending", admission.BillingState)
			assert.Equal(t, model.MediaUnknown, admission.State)
			get := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "")
			assert.Contains(t, get.Body.String(), `"status":"UNKNOWN"`)
			assert.NotContains(t, get.Body.String(), `"media_submission_receipt"`)
		})
	}
}

func TestMediaCreateRejectionUpgradeLeavesHistoricalSubscriptionEpochUnproven(t *testing.T) {
	f := newMediaNativeFixture(t)
	sub := model.UserSubscription{Id: 791, UserId: 791, PlanId: 791, AmountTotal: 1000000, AmountUsed: 123, Status: "active", StartTime: 1, EndTime: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, f.db.Create(&sub).Error)
	record := model.SubscriptionPreConsumeRecord{RequestId: "legacy-refusal-reservation", UserId: 791, UserSubscriptionId: 791, PreConsumed: 123, Status: "consumed"}
	require.NoError(t, f.db.Create(&record).Error)
	require.NoError(t, f.db.Migrator().DropColumn(&model.UserSubscription{}, "QuotaEpoch"))
	require.NoError(t, f.db.Migrator().DropColumn(&model.SubscriptionPreConsumeRecord{}, "QuotaEpoch"))
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(&model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
		require.NoError(t, model.MigrateMediaAdmission(f.db))
	}
	var upgraded model.UserSubscription
	require.NoError(t, f.db.First(&upgraded, 791).Error)
	assert.Equal(t, sub, upgraded)
	var reservation model.SubscriptionPreConsumeRecord
	require.NoError(t, f.db.First(&reservation, record.Id).Error)
	assert.Equal(t, record, reservation)
	assert.Nil(t, reservation.QuotaEpoch)
}

func TestMediaCreateRejectionSubscriptionResetEpochNeverWraps(t *testing.T) {
	for _, mode := range []string{"administrator", "automatic"} {
		t.Run(mode, func(t *testing.T) {
			f := newMediaNativeFixture(t)
			plan := model.SubscriptionPlan{Id: 791, Title: "epoch upper-bound fixture", TotalAmount: 1000000, QuotaResetPeriod: model.SubscriptionResetCustom, QuotaResetCustomSeconds: 3600}
			require.NoError(t, f.db.Create(&plan).Error)
			model.InvalidateSubscriptionPlanCache(plan.Id)
			now := time.Now().Unix()
			sub := model.UserSubscription{Id: 791, UserId: 791, PlanId: 791, AmountTotal: 1000000, AmountUsed: 123, QuotaEpoch: 1<<63 - 1, Status: "active", StartTime: now - 7200, EndTime: now + 7200, LastResetTime: now - 7200, NextResetTime: now - 1}
			require.NoError(t, f.db.Create(&sub).Error)
			if mode == "administrator" {
				_, err := model.AdminResetUserSubscriptionsByPlan(791, 791, false)
				require.ErrorContains(t, err, "epoch cannot advance")
			} else {
				_, err := model.ResetDueSubscriptions(10)
				require.ErrorContains(t, err, "epoch cannot advance")
			}
			var after model.UserSubscription
			require.NoError(t, f.db.First(&after, 791).Error)
			assert.Equal(t, sub, after)
		})
	}
}

func TestMediaCatalogCandidateSelectionBeforeAnyAdmissionOrCharge(t *testing.T) {
	const duration10 = `{"data":[{"id":"bt-seedance-2.0","durations":[10],"duration_policy":{"mode":"fixed","values":[10],"default":10}}]}`
	const duration15 = `{"data":[{"id":"bt-seedance-2.0","durations":[15],"duration_policy":{"mode":"fixed","values":[15],"default":15}}]}`
	const otherModel = `{"data":[{"id":"other-upstream-model"}]}`
	for _, test := range []struct {
		name                                                          string
		keyCount                                                      int
		firstCatalog                                                  string
		allUnavailable, disabledSecond, otherChannel, providerUnknown bool
		wantStatus                                                    int
		wantCode                                                      string
		wantCatalogs                                                  int
	}{
		{name: "key_a_15_key_b_10", keyCount: 2, firstCatalog: duration15, wantStatus: 200, wantCatalogs: 2},
		{name: "key_a_model_missing_key_b_available", keyCount: 2, firstCatalog: otherModel, wantStatus: 200, wantCatalogs: 2},
		{name: "key_a_catalog_unknown_key_b_available", keyCount: 2, firstCatalog: `{"data":[]}`, wantStatus: 200, wantCatalogs: 2},
		{name: "all_keys_duration_unavailable", keyCount: 2, firstCatalog: duration15, allUnavailable: true, wantStatus: 400, wantCode: "video_duration_unavailable", wantCatalogs: 2},
		{name: "all_keys_catalog_unknown", keyCount: 2, firstCatalog: `{"data":[]}`, allUnavailable: true, wantStatus: 503, wantCode: "upstream_catalog_unverified", wantCatalogs: 2},
		{name: "disabled_key_is_never_used", keyCount: 2, firstCatalog: duration15, disabledSecond: true, wantStatus: 400, wantCode: "video_duration_unavailable", wantCatalogs: 1},
		{name: "bounded_sixteen_candidates", keyCount: 20, firstCatalog: duration15, allUnavailable: true, wantStatus: 400, wantCode: "video_duration_unavailable", wantCatalogs: 16},
		{name: "other_eligible_channel", keyCount: 1, firstCatalog: duration15, otherChannel: true, wantStatus: 200, wantCatalogs: 2},
		{name: "actual_provider_unknown_never_tries_second_key", keyCount: 2, firstCatalog: duration10, providerUnknown: true, wantStatus: 202, wantCatalogs: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newMediaNativeFixture(t)
			keys := make([]string, test.keyCount)
			f.catalogByKey = make(map[string]string)
			for index := range keys {
				keys[index] = fmt.Sprintf("synthetic-catalog-%s-%d", test.name, index)
				f.catalogByKey[keys[index]] = duration10
				if index == 0 || test.allUnavailable {
					f.catalogByKey[keys[index]] = test.firstCatalog
				}
			}
			var original model.Channel
			require.NoError(t, f.db.First(&original, 880).Error)
			original.Key = strings.Join(keys, "\n")
			original.ChannelInfo = model.ChannelInfo{IsMultiKey: len(keys) > 1, MultiKeySize: len(keys), MultiKeyMode: constant.MultiKeyModePolling}
			if test.disabledSecond {
				original.ChannelInfo.MultiKeyStatusList = map[int]int{1: common.ChannelStatusManuallyDisabled}
			}
			priority := int64(100)
			original.Priority = &priority
			require.NoError(t, f.db.Save(&original).Error)
			require.NoError(t, original.UpdateAbilities(nil))
			expectedKey := keys[0]
			if len(keys) > 1 {
				expectedKey = keys[1]
			}
			if test.otherChannel {
				alternate := original
				alternate.Id = 882
				alternate.Name = "synthetic other eligible account"
				alternate.Key = "synthetic-catalog-eligible-other-channel"
				alternate.ChannelInfo = model.ChannelInfo{}
				zero := int64(0)
				alternate.Priority = &zero
				require.NoError(t, f.db.Create(&alternate).Error)
				require.NoError(t, alternate.AddAbilities(f.db))
				f.catalogByKey[alternate.Key] = duration10
				expectedKey = alternate.Key
			}
			if test.providerUnknown {
				f.postMode = "http503"
				expectedKey = keys[0]
			}
			model.InitChannelCache()
			beforeWallet, beforeToken := f.balance(t)
			response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"free synthetic exact duration candidate","seconds":10,"size":"1792x1024"}`)
			t.Logf("catalog candidate API status=%d response=%s", response.Code, response.Body.String())
			require.Equal(t, test.wantStatus, response.Code, response.Body.String())
			if test.wantCode != "" {
				assert.Contains(t, response.Body.String(), `"code":"`+test.wantCode+`"`)
				assert.NotContains(t, response.Body.String(), service.MediaCapacityCode)
			}
			f.mu.Lock()
			assert.Len(t, f.catalogKeys, test.wantCatalogs)
			catalogKeys := append([]string(nil), f.catalogKeys...)
			postKeys := append([]string(nil), f.postKeys...)
			postedBodies := append([]map[string]any(nil), f.postedBodies...)
			assert.Zero(t, f.gets)
			f.mu.Unlock()
			assert.Equal(t, keys[0], catalogKeys[0], "first selected key must really fail its own preflight")
			afterWallet, afterToken := f.balance(t)
			var taskCount, admissionCount, requestCount int64
			require.NoError(t, f.db.Model(&model.Task{}).Count(&taskCount).Error)
			require.NoError(t, f.db.Model(&model.MediaAdmission{}).Count(&admissionCount).Error)
			require.NoError(t, f.db.Model(&model.WalletFundingRequest{}).Count(&requestCount).Error)
			if test.wantStatus == 200 || test.providerUnknown {
				require.Equal(t, []string{expectedKey}, postKeys)
				require.Len(t, postedBodies, 1)
				assert.Equal(t, float64(10), postedBodies[0]["seconds"])
				assert.Equal(t, "bt-seedance-2.0", postedBodies[0]["model"])
				assert.Less(t, afterWallet, beforeWallet)
				assert.Less(t, afterToken, beforeToken)
				assert.EqualValues(t, 1, taskCount)
				assert.EqualValues(t, 1, admissionCount)
				assert.EqualValues(t, 1, requestCount)
				var task model.Task
				require.NoError(t, f.db.First(&task).Error)
				assert.Equal(t, expectedKey, task.PrivateData.Key)
			} else {
				assert.Empty(t, postKeys)
				assert.Equal(t, beforeWallet, afterWallet)
				assert.Equal(t, beforeToken, afterToken)
				assert.Zero(t, taskCount)
				assert.Zero(t, admissionCount)
				assert.Zero(t, requestCount)
			}
		})
	}
}

// The history transaction is covered across all three databases in model.
// This fixture supplies its committed audit row to the actual GET presenter,
// proving that the existing strict desktop rejection contract stays unchanged.
func TestMediaHistoricalReconciliationGETPreservesDesktopReceiptContract(t *testing.T) {
	f := newMediaNativeFixture(t)
	f.postStatus = http.StatusNotFound
	f.postBody = `{"error":{"message":"模型不存在：bt-seedance-2.0","type":"invalid_request_error"}}`
	response := f.request(t, http.MethodPost, "/v1/videos", `{"model":"bt-seedance-2.0","prompt":"synthetic historical projection","seconds":10,"size":"1792x1024"}`)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	task := f.task(t, created.ID)
	// The ordinary router fixture disables usage logging. Supply the original
	// log store explicitly for this committed-history projection fixture.
	require.NoError(t, f.db.AutoMigrate(&model.Log{}, &model.SubscriptionPreConsumeRecord{}))
	previousLogDB := model.LOG_DB
	model.LOG_DB = f.db
	defer func() { model.LOG_DB = previousLogDB }()
	require.NoError(t, f.db.Create(&model.Log{Id: 1, UserId: task.UserId, ChannelId: task.ChannelId, TokenId: task.PrivateData.TokenId, ModelName: task.Properties.OriginModelName, Type: model.LogTypeError, RequestId: task.PrivateData.Execution.RequestID}).Error)
	var admission model.MediaAdmission
	require.NoError(t, f.db.Where("task_db_id = ?", task.ID).First(&admission).Error)
	var original model.MediaCreateRejection
	require.NoError(t, common.UnmarshalJsonStr(admission.CreateRejection, &original))
	require.NoError(t, f.db.Model(&model.MediaAdmission{}).Where("id = ?", admission.ID).Update("create_rejection", nil).Error)
	before := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "")
	require.Equal(t, http.StatusOK, before.Code, before.Body.String())
	assert.Contains(t, before.Body.String(), `"status":"UNKNOWN"`)
	assert.NotContains(t, before.Body.String(), `"media_submission_receipt"`)
	snapshot, err := model.InspectMediaHistoricalReconciliation(context.Background(), []int64{task.ID})
	require.NoError(t, err)
	hashes, err := common.Marshal(snapshot.Entries[0].Hashes)
	require.NoError(t, err)
	receipt := model.MediaHistoricalReconciliation{TaskDBID: task.ID, TaskID: task.TaskID, UserID: task.UserId, OperationID: "synthetic-committed-historical-audit", ManifestSHA256: strings.Repeat("a", 64), EntrySHA256: strings.Repeat("b", 64), Action: model.MediaHistoricalRefund, OriginalQuota: original.OriginalQuota, BeforeHashes: string(hashes), AfterHashes: string(hashes), Evidence: "synthetic operator receipt fixture", CreatedAt: time.Now().Unix()}
	encoded, err := common.Marshal(receipt)
	require.NoError(t, err)
	sum := sha256.Sum256(encoded)
	receipt.ReceiptSHA256 = hex.EncodeToString(sum[:])
	require.NoError(t, f.db.Create(&receipt).Error)
	get := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "")
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	var recovered struct {
		Status  string                          `json:"status"`
		Receipt *service.MediaSubmissionReceipt `json:"media_submission_receipt"`
	}
	require.NoError(t, common.Unmarshal(get.Body.Bytes(), &recovered))
	require.NotNil(t, recovered.Receipt)
	assert.Equal(t, "FAILURE", recovered.Status)
	assert.Equal(t, service.MediaRejectedCode, recovered.Receipt.SubmissionCode)
	assert.Equal(t, "not_submitted", recovered.Receipt.SubmissionState)
	assert.Equal(t, "refunded", recovered.Receipt.BillingState)
	assert.Equal(t, "upstream_model_not_available", recovered.Receipt.RejectionReason)
	assert.True(t, recovered.Receipt.RefundConfirmed)
	assert.True(t, recovered.Receipt.ProviderResponseReceived)
	assert.True(t, recovered.Receipt.UpstreamSubmitted)
	assert.True(t, recovered.Receipt.Resubmittable)
	assert.NotContains(t, get.Body.String(), "historical_reconciliation")
	assert.NotContains(t, get.Body.String(), "funds_pending_review")
	assert.NotContains(t, get.Body.String(), "provider_terminal_confirmed")
	admissionID := admission.ID
	admission = model.MediaAdmission{}
	require.NoError(t, f.db.Where("id = ?", admissionID).First(&admission).Error)
	assert.Empty(t, admission.CreateRejection)
	assert.False(t, admission.ProviderTerminal)
	t.Logf("API_HANDLER_FIXTURE historical-reconciled-get %s", get.Body.String())
	f.mu.Lock()
	assert.Equal(t, 1, f.posts)
	assert.Zero(t, f.gets)
	f.mu.Unlock()
}
