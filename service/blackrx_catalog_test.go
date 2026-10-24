package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func blackrxCatalogFixtureChannel(id int, base, key string) *model.Channel {
	setting := `{"task_plugin_key":"blackrxai-video"}`
	return &model.Channel{Id: id, Type: constant.ChannelTypeTaskPlugin, Key: key, BaseURL: &base, Setting: &setting, Status: common.ChannelStatusEnabled, Group: "default"}
}

func TestBlackrxCatalogDispatchFence(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/models", r.URL.Path)
		if r.Header.Get("Authorization") == "Bearer fixture-key-b" {
			_, _ = w.Write([]byte(`{"data":[{"id":"other-model"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"video-sd25-480p"},{"id":"new-unconfigured-upstream-id"}]}`))
	}))
	t.Cleanup(server.Close)
	channel := blackrxCatalogFixtureChannel(91241, server.URL, "fixture-key-a")
	require.NoError(t, CheckBlackrxModelAvailability(context.Background(), channel, channel.Key, "friendly-alias", "video-sd25-480p"))
	require.NoError(t, CheckBlackrxVideoDuration(context.Background(), channel, channel.Key, "friendly-alias", "video-sd25-480p", 15), "legacy ID-only discovery must not invent a duration restriction")
	var unavailable *BlackrxModelAvailabilityError
	require.ErrorAs(t, CheckBlackrxModelAvailability(context.Background(), channel, channel.Key, "bt-seedance-2.0", ""), &unavailable)
	assert.Equal(t, "model_not_available", unavailable.Code)
	assert.Equal(t, http.StatusBadRequest, unavailable.HTTPStatus)
	assert.Contains(t, unavailable.Error(), "未提交生成、未扣费")
	require.ErrorAs(t, CheckBlackrxModelAvailability(context.Background(), channel, "fixture-key-b", "video-sd25-480p", ""), &unavailable)
	assert.Equal(t, "model_not_available", unavailable.Code)
	assert.EqualValues(t, 2, calls.Load(), "fresh list is reused, account keys never share discovery")
	otherProvider := *channel
	otherProvider.Type = constant.ChannelTypeOpenAI
	require.NoError(t, CheckBlackrxModelAvailability(context.Background(), &otherProvider, channel.Key, "bt-seedance-2.0", ""))
	assert.EqualValues(t, 2, calls.Load())

	channel.ChannelInfo.IsMultiKey = true
	channel.Key = "fixture-key-a\nfixture-key-b"
	require.NoError(t, CheckBlackrxChannelModelAvailability(context.Background(), channel, "friendly-alias", "video-sd25-480p"))
	channel.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled}
	require.ErrorAs(t, CheckBlackrxChannelModelAvailability(context.Background(), channel, "friendly-alias", "video-sd25-480p"), &unavailable)
	assert.Equal(t, "model_not_available", unavailable.Code)
}

func TestBlackrxCatalogBadDiscoveryPreservesVerifiedState(t *testing.T) {
	for index, test := range []struct {
		name string
		code int
		body string
	}{
		{"empty", 200, `{"data":[]}`},
		{"missing", 200, `{}`},
		{"null", 200, `{"data":null}`},
		{"malformed", 200, `{"data":[`},
		{"bad id", 200, `{"data":[{"id":" live "}]}`},
		{"unauthorized", 401, `{"error":"fixture"}`},
		{"provider unavailable", 503, `{}`},
		{"oversized", 200, strings.Repeat("x", blackrxCatalogMaxBody+1)},
		{"duration mismatch", 200, `{"data":[{"id":"original-model","durations":[15],"duration_policy":{"mode":"fixed","values":[10],"default":10}}]}`},
		{"duration missing policy", 200, `{"data":[{"id":"original-model","durations":[15]}]}`},
		{"duration unknown mode", 200, `{"data":[{"id":"original-model","durations":[15],"duration_policy":{"mode":"unknown","values":[15],"default":15}}]}`},
		{"duration invalid range", 200, `{"data":[{"id":"original-model","durations":[15],"duration_policy":{"mode":"range","min":15,"max":30,"step":0,"default":15}}]}`},
		{"duration out of range", 200, `{"data":[{"id":"original-model","durations":[3601],"duration_policy":{"mode":"fixed","values":[3601],"default":3601}}]}`},
		{"duration invalid default", 200, `{"data":[{"id":"original-model","durations":[15],"duration_policy":{"mode":"fixed","values":[15],"default":10}}]}`},
		{"duplicate model", 200, `{"data":[{"id":"original-model"},{"id":"original-model"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			channel := blackrxCatalogFixtureChannel(92000+index, server.URL, "bad-response-fixture")
			original := time.Now().Add(-2 * model.BlackrxCatalogTTL)
			model.RecordBlackrxCatalog(channel, channel.Key, []string{"original-model"}, original)
			var unavailable *BlackrxModelAvailabilityError
			require.ErrorAs(t, CheckBlackrxModelAvailability(context.Background(), channel, channel.Key, "original-model", ""), &unavailable)
			assert.Equal(t, "upstream_catalog_unverified", unavailable.Code)
			assert.Equal(t, http.StatusServiceUnavailable, unavailable.HTTPStatus)
			snapshot := model.ReadBlackrxCatalog(channel, channel.Key)
			assert.Equal(t, []string{"original-model"}, snapshot.Models)
			assert.Equal(t, original, snapshot.VerifiedAt)
			require.Error(t, CheckBlackrxModelAvailability(context.Background(), channel, channel.Key, "original-model", ""))
			assert.EqualValues(t, 1, calls.Load(), "failed discovery is rate limited")
			// Restart/cache-loss and key rotation must not turn a missing list
			// into permission to send a paid generation.
			require.ErrorAs(t, CheckBlackrxModelAvailability(context.Background(), channel, "new-key-fixture", "original-model", ""), &unavailable)
			assert.Equal(t, "upstream_catalog_unverified", unavailable.Code)
		})
	}
}

func TestBlackrxCatalogNoRedirectAndConcurrentCoalescing(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	t.Cleanup(redirect.Close)
	channel := blackrxCatalogFixtureChannel(93001, redirect.URL, "redirect-fixture")
	var unavailable *BlackrxModelAvailabilityError
	require.ErrorAs(t, CheckBlackrxModelAvailability(context.Background(), channel, channel.Key, "video-sd25-480p", ""), &unavailable)
	assert.Equal(t, "upstream_catalog_unverified", unavailable.Code)
	assert.Zero(t, targetCalls.Load(), "credentials must not follow a discovery redirect")

	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		_, _ = w.Write([]byte(`{"data":[{"id":"video-sd25-480p"}]}`))
	}))
	t.Cleanup(server.Close)
	channel = blackrxCatalogFixtureChannel(93002, server.URL, "concurrent-fixture")
	var workers sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		workers.Go(func() {
			results <- CheckBlackrxModelAvailability(context.Background(), channel, channel.Key, "video-sd25-480p", "")
		})
	}
	<-started
	close(release)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	assert.EqualValues(t, 1, calls.Load())
}

// TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN / TEST_POSTGRES_DSN select the real
// engine. All fixture tables have a unique prefix and are removed on cleanup.
func TestBlackrxCatalogDirectoryDatabase(t *testing.T) {
	var driver gorm.Dialector = sqlite.Open(":memory:")
	dialect := common.DatabaseTypeSQLite
	switch os.Getenv("TEST_TASK_DB_DIALECT") {
	case "mysql":
		dialect = common.DatabaseTypeMySQL
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case "postgres":
		dialect = common.DatabaseTypePostgreSQL
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("catalog_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	models := []any{&model.Channel{}, &model.Ability{}, &model.Model{}}
	require.NoError(t, db.AutoMigrate(models...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRegistry, oldPrices := jsplugin.DefaultRegistry, ratio_setting.ModelPrice2JSONString()
	model.DB, common.MemoryCacheEnabled = db, false
	common.SetMainDatabaseType(dialect)
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = oldDB, oldMemory
		common.SetDatabaseTypes(oldMain, oldLog)
		jsplugin.DefaultRegistry = oldRegistry
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		if oldMemory && oldDB != nil && oldDB.Migrator().HasTable(&model.Channel{}) && oldDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
	})
	source, err := os.ReadFile("../deploy/instance/blackrxai-video-plugin.js")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"video-sd25-480p":1,"bt-seedance-2.0":1,"sd2-th-2.0-720p":1,"friendly-alias":1}`))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		if r.Header.Get("Authorization") == "Bearer catalog-directory-fixture-b" {
			_, _ = w.Write([]byte(`{"data":[{"id":"sd2-th-2.0-720p","durations":[15],"duration_policy":{"mode":"fixed","values":[15],"default":15}}]}`))
			return
		}
		// The 10-second-only fixed policy is the observed upstream conflict.
		// The shorter range is a synthetic future policy to prove menus and
		// explicit requests can be narrowed without modifying prices.
		_, _ = w.Write([]byte(`{"data":[{"id":"video-sd25-480p","durations":[15,16,17,18,19,20],"duration_policy":{"mode":"range","min":15,"max":20,"step":1,"default":20}},{"id":"sd2-th-2.0-720p","durations":[10],"duration_policy":{"mode":"fixed","values":[10],"default":10}},{"id":"unimplemented-model"}]}`))
	}))
	t.Cleanup(server.Close)
	channel := blackrxCatalogFixtureChannel(94001, server.URL, "catalog-directory-fixture")
	channel.Models = "video-sd25-480p,bt-seedance-2.0,friendly-alias,unimplemented-model,sd2-th-2.0-720p"
	mapping := `{"friendly-alias":"intermediate-alias","intermediate-alias":"video-sd25-480p"}`
	channel.ModelMapping = &mapping
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	var before model.Channel
	require.NoError(t, db.First(&before, channel.Id).Error)
	RefreshBlackrxCatalog(context.Background())
	assert.Equal(t, []string{"video-sd25-480p", "friendly-alias"}, FilterUnavailableCatalogModels(strings.Split(channel.Models, ","), []string{"default"}))
	candidates, err := model.ListClientModelCandidates()
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	assert.Equal(t, "friendly-alias", candidates[0].ID)
	assert.Equal(t, "video-sd25-480p", candidates[1].ID)
	var durationError *BlackrxModelAvailabilityError
	for _, seconds := range []int{10, 15} {
		require.ErrorAs(t, CheckBlackrxVideoDuration(context.Background(), channel, channel.Key, "sd2-th-2.0-720p", "", seconds), &durationError)
		assert.Equal(t, "video_duration_unavailable", durationError.Code)
		assert.Equal(t, http.StatusBadRequest, durationError.HTTPStatus)
	}
	require.NoError(t, CheckBlackrxVideoDuration(context.Background(), channel, channel.Key, "friendly-alias", "video-sd25-480p", 20))
	require.ErrorAs(t, CheckBlackrxVideoDuration(context.Background(), channel, channel.Key, "friendly-alias", "video-sd25-480p", 30), &durationError)
	assert.Equal(t, "video_duration_unavailable", durationError.Code)
	parameters := map[string]any{"properties": map[string]any{"seconds": map[string]any{
		"type": "integer", "enum": []int{15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30}, "minimum": 15, "maximum": 30, "default": 30,
	}}}
	pricing := []model.Pricing{{ModelName: "video-sd25-480p", Parameters: parameters}, {ModelName: "friendly-alias", Parameters: parameters}}
	narrowed := NarrowBlackrxPricingParameters(pricing, []string{"default"})
	for _, row := range narrowed {
		seconds := row.Parameters["properties"].(map[string]any)["seconds"].(map[string]any)
		assert.Equal(t, []int{15, 16, 17, 18, 19, 20}, seconds["enum"])
		assert.Equal(t, 20, seconds["maximum"])
		assert.Equal(t, 15, seconds["default"], "an invalid default is replaced only in the display schema, never in a request")
	}
	assert.Equal(t, 30, parameters["properties"].(map[string]any)["seconds"].(map[string]any)["default"], "do not mutate the pricing cache or another row sharing its schema")
	// A reader cannot mutate the cached policy for subsequent submissions.
	readCopy := model.ReadBlackrxCatalog(channel, channel.Key)
	readCopy.DurationPolicies["video-sd25-480p"].Values[0] = 30
	require.Error(t, CheckBlackrxVideoDuration(context.Background(), channel, channel.Key, "friendly-alias", "video-sd25-480p", 30))
	discovered, err := FetchBlackrxChannelModels(context.Background(), channel)
	require.NoError(t, err)
	assert.Equal(t, []string{"video-sd25-480p"}, discovered, "new provider IDs are not automatically published")
	var after model.Channel
	require.NoError(t, db.First(&after, channel.Id).Error)
	assert.Equal(t, before, after, "discovery must not rewrite configured models, mappings, keys, or prices")
	// Per-account policy is not borrowed: another enabled key may support
	// 15s, but the original 10s-only account must continue rejecting it.
	multiKey := *channel
	multiKey.Key = channel.Key + "\ncatalog-directory-fixture-b"
	multiKey.ChannelInfo.IsMultiKey = true
	require.NoError(t, CheckBlackrxVideoDuration(context.Background(), &multiKey, "catalog-directory-fixture-b", "sd2-th-2.0-720p", "", 15))
	assert.Equal(t, []int{15}, model.BlackrxChannelDurations(&multiKey, "sd2-th-2.0-720p"))
	require.Error(t, CheckBlackrxVideoDuration(context.Background(), &multiKey, channel.Key, "sd2-th-2.0-720p", "", 15))

	// Another supplier with the same client-facing ID remains routable. Its
	// private group must not make the model available to default-group users.
	other := model.Channel{Id: 94002, Type: constant.ChannelTypeOpenAI, Key: "other-fixture", Status: common.ChannelStatusEnabled, Group: "private", Models: "bt-seedance-2.0"}
	require.NoError(t, db.Create(&other).Error)
	require.NoError(t, other.AddAbilities(nil))
	assert.Empty(t, FilterUnavailableCatalogModels([]string{"bt-seedance-2.0"}, []string{"default"}))
	assert.Equal(t, []string{"bt-seedance-2.0"}, FilterUnavailableCatalogModels([]string{"bt-seedance-2.0"}, []string{"private"}))
	t.Run("warm cache key changes cannot mutate catalog snapshots", func(t *testing.T) {
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
			"key": multiKey.Key, "channel_info": model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{1: common.ChannelStatusEnabled}},
		}).Error)
		common.MemoryCacheEnabled = true
		defer func() { common.MemoryCacheEnabled = false }()
		model.InitChannelCache()
		owned, err := model.BlackrxCatalogChannelSnapshots(context.Background(), []int{channel.Id})
		require.NoError(t, err)
		require.Len(t, model.BlackrxEnabledKeys(owned[channel.Id]), 2)
		// Pause the real key-status writer after its cache mutation, before
		// database persistence. Catalog readers must see independent DB rows,
		// never this shared map while the channel polling lock is held.
		cacheChanged, resumePersistence := make(chan struct{}), make(chan struct{})
		var resume sync.Once
		defer func() { resume.Do(func() { close(resumePersistence) }) }()
		var paused atomic.Bool
		callback := "catalog_pause_key_status_persistence"
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
			_, singleChannel := tx.Statement.Dest.(*model.Channel)
			if tx.Statement.Table == db.NamingStrategy.TableName("Channel") && singleChannel && paused.CompareAndSwap(false, true) {
				close(cacheChanged)
				<-resumePersistence
			}
		}))
		defer func() { require.NoError(t, db.Callback().Query().Remove(callback)) }()
		changed := make(chan bool, 1)
		go func() {
			changed <- model.UpdateChannelStatus(channel.Id, "catalog-directory-fixture-b", common.ChannelStatusManuallyDisabled, "local catalog concurrency regression")
		}()
		<-cacheChanged
		assert.Equal(t, []string{"sd2-th-2.0-720p"}, FilterUnavailableCatalogModels([]string{"sd2-th-2.0-720p"}, []string{"default"}))
		assert.Len(t, model.BlackrxEnabledKeys(owned[channel.Id]), 2, "a previously read account snapshot cannot change with the shared cache")
		current := NarrowBlackrxPricingParameters(pricing, []string{"default"})
		assert.Equal(t, 20, current[0].Parameters["properties"].(map[string]any)["seconds"].(map[string]any)["maximum"])
		resume.Do(func() { close(resumePersistence) })
		require.True(t, <-changed)
		assert.Empty(t, FilterUnavailableCatalogModels([]string{"sd2-th-2.0-720p"}, []string{"default"}), "committed key disable is reflected without reading mutable cache maps")
		require.True(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "local channel pause before enabling key"))
		require.True(t, model.UpdateChannelStatus(channel.Id, "catalog-directory-fixture-b", common.ChannelStatusEnabled, ""))
		assert.Equal(t, []string{"sd2-th-2.0-720p"}, FilterUnavailableCatalogModels([]string{"sd2-th-2.0-720p"}, []string{"default"}))
	})
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
}
