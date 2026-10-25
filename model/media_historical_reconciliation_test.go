package model

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Run the identical contract with TEST_MEDIA_HISTORICAL_DIALECT and the
// existing TEST_MYSQL_DSN / TEST_POSTGRES_DSN against actual database engines.
func TestMediaHistoricalReconciliationDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MEDIA_HISTORICAL_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	var driver gorm.Dialector
	switch kind {
	case "sqlite":
		driver = sqlite.Open(filepath.Join(t.TempDir(), "history.db") + "?_pragma=busy_timeout(30000)&_txlock=immediate")
	case "mysql":
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case "postgres":
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported dialect %s", kind)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("mh_%d_", time.Now().UnixNano())}, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldLog, oldType, oldLogType, oldRedis := DB, LOG_DB, common.MainDatabaseType(), common.LogDatabaseType(), common.RedisEnabled
	DB, LOG_DB, common.RedisEnabled = db, db, false
	common.SetDatabaseTypes(common.DatabaseType(kind), common.DatabaseType(kind))
	initCol()
	models := append([]any{&User{}, &Token{}, &Channel{}, &Task{}, &Option{}, &MediaPool{}, &MediaAdmission{}, &MediaHistoricalReconciliation{}, &Log{}, &SubscriptionPreConsumeRecord{}, &TopUp{}, &Redemption{}, &Checkin{}}, WalletFundingModels()...)
	t.Cleanup(func() {
		for i := len(models) - 1; i >= 0; i-- {
			require.NoError(t, db.Migrator().DropTable(models[i]))
		}
		DB, LOG_DB, common.RedisEnabled = oldDB, oldLog, oldRedis
		common.SetDatabaseTypes(oldType, oldLogType)
		initCol()
		require.NoError(t, sqlDB.Close())
	})
	// Upgrade from the existing admission layout, preserve the populated rows,
	// and repeat the normal migration before executing the new operation.
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, db.Migrator().DropTable(&MediaHistoricalReconciliation{}))
	require.NoError(t, MigrateMediaAdmission(db))
	require.NoError(t, MigrateMediaAdmission(db))
	var version string
	query := "SELECT version()"
	if kind == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("historical reconciliation database %s", version)
	fixtureMediaHistorical(t)
	ids := []int64{100, 101, 102, 103}
	manifest, err := InspectMediaHistoricalReconciliation(context.Background(), ids)
	require.NoError(t, err)
	manifest.OperationID = "test-historical-blackrx-20261025"
	for i := range manifest.Entries {
		entry := &manifest.Entries[i]
		entry.Host = fixtureMediaHistoricalHost(t, entry.TaskDBID)
		entry.Source = fixtureMediaHistoricalSource(t, entry.TaskDBID, entry.Host)
		if entry.TaskDBID == 100 {
			entry.Action = MediaHistoricalLegacyGiftRefund
			entry.LegacyGift = &MediaHistoricalLegacyGiftProof{GiftLogID: 11163, GrantQuota: 1000, ConsumedQuota: 9, ConsumeCount: 3}
		}
	}
	encode := func(value MediaHistoricalManifest) ([]byte, string) {
		data, err := common.Marshal(value)
		require.NoError(t, err)
		return data, mediaHistoricalSHA(data)
	}
	data, hash := encode(manifest)
	t.Run("dry_run_never_creates_a_receipt_or_refunds", func(t *testing.T) {
		result, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, false)
		require.NoError(t, err)
		assert.Equal(t, "PLAN_VERIFIED", result.Status)
		var count int64
		require.NoError(t, db.Model(&MediaHistoricalReconciliation{}).Count(&count).Error)
		assert.Zero(t, count)
		var user User
		require.NoError(t, db.First(&user, 144).Error)
		assert.False(t, user.WalletFundingInitialized)
		assert.Equal(t, 980, user.Quota)
	})
	t.Run("wrong_explicit_ids_or_manifest_or_host_refused", func(t *testing.T) {
		_, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, []int64{101}, true)
		assert.Error(t, err)
		_, err = ReconcileMediaHistoricalManifest(context.Background(), data, strings.Repeat("0", 64), ids, true)
		assert.Error(t, err)
		changed := manifest
		changed.Entries = append([]MediaHistoricalEntry{}, manifest.Entries...)
		changed.Entries[0].Host.RejectionLine += " [truncated]"
		bad, badHash := encode(changed)
		_, err = ReconcileMediaHistoricalManifest(context.Background(), bad, badHash, ids, true)
		assert.Error(t, err)
	})
	t.Run("stale_token_snapshot_refuses_without_partial_credits", func(t *testing.T) {
		require.NoError(t, db.Model(&Token{}).Where("id = ?", 148).Update("name", "changed").Error)
		_, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, true)
		assert.Error(t, err)
		require.NoError(t, db.Model(&Token{}).Where("id = ?", 148).Update("name", "harness").Error)
		var count int64
		require.NoError(t, db.Model(&MediaHistoricalReconciliation{}).Count(&count).Error)
		assert.Zero(t, count)
	})
	t.Run("independent_log_store_is_refused_before_mutation", func(t *testing.T) {
		modelLog := LOG_DB
		LOG_DB = db.Session(&gorm.Session{NewDB: true})
		_, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, true)
		LOG_DB = modelLog
		assert.Error(t, err)
		var count int64
		require.NoError(t, db.Model(&MediaHistoricalReconciliation{}).Count(&count).Error)
		assert.Zero(t, count)
	})
	t.Run("pool_changes_require_a_fresh_reviewed_plan", func(t *testing.T) {
		var pool MediaPool
		require.NoError(t, db.First(&pool).Error)
		released := MediaAdmission{ID: "unrelated-new-released-rejection", TaskDBID: 9991, PoolID: pool.ID, ChannelID: 119, State: MediaReleased, BillingState: "not_charged"}
		require.NoError(t, db.Create(&released).Error)
		require.NoError(t, db.Model(&MediaPool{}).Where("id = ?", pool.ID).Update("updated_at", pool.UpdatedAt+1).Error)
		_, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, true)
		assert.Error(t, err)
		require.NoError(t, db.Model(&MediaPool{}).Where("id = ?", pool.ID).Update("updated_at", pool.UpdatedAt).Error)
		// Unrelated released rows are preserved and never become refund targets.
		result, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, false)
		require.NoError(t, err)
		assert.Equal(t, "PLAN_VERIFIED", result.Status)
		extra := MediaAdmission{ID: "unrelated-unreleased", TaskDBID: 9992, PoolID: pool.ID, ChannelID: 119, State: MediaUnknown, BillingState: "reserved", SendingAt: 1}
		require.NoError(t, db.Create(&extra).Error)
		_, err = ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, true)
		assert.Error(t, err)
		require.NoError(t, db.Delete(&extra).Error)
	})
	t.Run("aggregate_token_bound_is_not_reservation_proof", func(t *testing.T) {
		require.NoError(t, db.Model(&Token{}).Where("id = ?", 148).Updates(map[string]any{"used_quota": 32, "remain_quota": -32}).Error)
		changed := manifest
		changed.Entries = append([]MediaHistoricalEntry{}, manifest.Entries...)
		changed.Entries[1].Hashes, err = mediaHistoricalHashes(db, 101)
		require.NoError(t, err)
		bad, badHash := encode(changed)
		_, err = ReconcileMediaHistoricalManifest(context.Background(), bad, badHash, ids, true)
		assert.Error(t, err)
		require.NoError(t, db.Model(&Token{}).Where("id = ?", 148).Updates(map[string]any{"used_quota": 31, "remain_quota": -31}).Error)
	})
	t.Run("legacy_capacity_only_preserves_all_uncertain_funds", func(t *testing.T) {
		sentinel := fmt.Errorf("rollback fixture capacity-only rehearsal")
		err := db.Transaction(func(tx *gorm.DB) error {
			DB, LOG_DB = tx, tx
			defer func() { DB, LOG_DB = db, db }()
			single := manifest
			single.OperationID = "capacity-only-rehearsal"
			single.Entries = append([]MediaHistoricalEntry{}, manifest.Entries[:1]...)
			single.Entries[0].Action = MediaHistoricalCapacityOnly
			single.Entries[0].LegacyGift = nil
			one, oneHash := encode(single)
			result, err := ReconcileMediaHistoricalManifest(context.Background(), one, oneHash, []int64{100}, true)
			require.NoError(t, err)
			assert.Zero(t, result.RefundedQuota)
			var task Task
			var user User
			var admission MediaAdmission
			require.NoError(t, tx.First(&task, 100).Error)
			require.NoError(t, tx.First(&user, 144).Error)
			require.NoError(t, tx.Where("task_db_id = ?", 100).First(&admission).Error)
			assert.Equal(t, TaskStatus(TaskStatusUnknown), task.Status)
			assert.Equal(t, 11, task.Quota)
			assert.Equal(t, 980, user.Quota)
			assert.False(t, user.WalletFundingInitialized)
			assert.Equal(t, "settlement_uncertain", admission.BillingState)
			assert.Equal(t, MediaReleased, admission.State)
			projected, err := MediaHistoricalTaskState(context.Background(), &task)
			require.NoError(t, err)
			require.NotNil(t, projected)
			assert.True(t, projected.FundsPending)
			assert.False(t, projected.Refunded)
			return sentinel
		})
		assert.ErrorIs(t, err, sentinel)
	})
	t.Run("failed_atomic_audit_write_rolls_back_all_four", func(t *testing.T) {
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("historical_receipt_failure", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "MediaHistoricalReconciliation" {
				tx.AddError(fmt.Errorf("injected audit write failure"))
			}
		}))
		_, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, true)
		assert.Error(t, err)
		require.NoError(t, db.Callback().Create().Remove("historical_receipt_failure"))
		for _, entry := range manifest.Entries {
			actual, err := mediaHistoricalHashes(db, entry.TaskDBID)
			require.NoError(t, err)
			assert.Equal(t, entry.Hashes, actual)
		}
	})
	t.Run("all_four_refund_original_gifts_without_false_consumption", func(t *testing.T) {
		result, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, true)
		require.NoError(t, err)
		assert.Equal(t, "APPLIED", result.Status)
		assert.Equal(t, 110, result.RefundedQuota)
		for _, id := range ids {
			identity, _ := fixtureHistoricalIdentity(id)
			var task Task
			var admission MediaAdmission
			var user User
			var token Token
			require.NoError(t, db.First(&task, id).Error)
			require.NoError(t, db.Where("task_db_id = ?", id).First(&admission).Error)
			require.NoError(t, db.First(&user, identity.UserID).Error)
			require.NoError(t, db.First(&token, identity.TokenID).Error)
			assert.Equal(t, TaskStatus(TaskStatusFailure), task.Status)
			assert.Zero(t, task.Quota)
			assert.Equal(t, MediaReleased, admission.State)
			assert.Equal(t, "refunded", admission.BillingState)
			assert.False(t, admission.ProviderTerminal)
			assert.Empty(t, admission.CreateRejection)
			assert.Equal(t, 9, user.UsedQuota)
			assert.Equal(t, 3, user.RequestCount)
			assert.Equal(t, 9, token.UsedQuota)
			assert.Equal(t, -9, token.RemainQuota)
			if id == 100 {
				assert.Equal(t, 991, user.Quota)
				assert.True(t, user.WalletFundingInitialized)
				var raw string
				require.NoError(t, db.Model(&Task{}).Select("private_data").Where("id = ?", id).Row().Scan(&raw))
				var preserved struct {
					FutureFrozenField struct {
						ExactInteger int64 `json:"exact_integer"`
						Flag         *bool `json:"flag"`
					} `json:"future_frozen_field"`
				}
				require.NoError(t, common.UnmarshalJsonStr(raw, &preserved))
				assert.EqualValues(t, int64(9007199254740993), preserved.FutureFrozenField.ExactInteger)
				require.NotNil(t, preserved.FutureFrozenField.Flag)
				assert.False(t, *preserved.FutureFrozenField.Flag)

			} else {
				assert.Equal(t, 1000+identity.Quota, user.Quota)
			}
			projection, err := MediaHistoricalTaskState(context.Background(), &task)
			require.NoError(t, err)
			require.NotNil(t, projection)
			assert.True(t, projection.Refunded)
			assert.False(t, projection.FundsPending)
		}
		var pool MediaPool
		require.NoError(t, db.First(&pool).Error)
		assert.Zero(t, pool.Occupied)
		var profits int64
		require.NoError(t, db.Model(&ModelProfitBillingRecord{}).Count(&profits).Error)
		assert.Zero(t, profits)
		var refunds int64
		require.NoError(t, db.Model(&Log{}).Where("type = ?", LogTypeRefund).Count(&refunds).Error)
		assert.Zero(t, refunds)
	})
	t.Run("replay_after_later_wallet_activity_is_noop", func(t *testing.T) {
		require.NoError(t, db.Model(&User{}).Where("id = ?", 149).Update("quota", gorm.Expr("quota + ?", 100)).Error)
		require.NoError(t, db.Model(&WalletFundingLot{}).Where("id = ?", 501).Update("remaining_quota", gorm.Expr("remaining_quota + ?", 100)).Error)
		require.NoError(t, db.Model(&Token{}).Where("id = ?", 148).Update("name", "legitimate later edit").Error)
		result, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, true)
		require.NoError(t, err)
		assert.Equal(t, "ALREADY_APPLIED", result.Status)
		assert.True(t, result.AlreadyApplied)
		assert.Zero(t, result.RefundedQuota)
	})
	t.Run("tampered_settled_task_refuses_replay", func(t *testing.T) {
		require.NoError(t, db.Model(&Task{}).Where("id = ?", 101).Update("quota", 1).Error)
		_, err := ReconcileMediaHistoricalManifest(context.Background(), data, hash, ids, true)
		assert.Error(t, err)
		require.NoError(t, db.Model(&Task{}).Where("id = ?", 101).Update("quota", 0).Error)
	})
}

func fixtureMediaHistorical(t *testing.T) {
	t.Helper()
	key := "historical-test-upstream-key"
	base := "https://hi.blackrxai.com"
	setting := `{"task_plugin_key":"blackrxai-video"}`
	require.NoError(t, DB.Create(&Channel{Id: 119, Type: 61, Status: 1, Key: key, BaseURL: &base, Setting: &setting}).Error)
	pool := MediaPool{ID: "test-historical-pool", IdentityDigest: mediaIdentityDigest(key, MediaVideo), MediaKind: MediaVideo, Occupied: 4, Reconciled: true}
	require.NoError(t, DB.Create(&pool).Error)
	for _, id := range []int64{100, 101, 102, 103} {
		identity, _ := fixtureHistoricalIdentity(id)
		created := int64(1792762979) + id
		user := User{Id: identity.UserID, Username: fmt.Sprintf("mh-user-%d", identity.UserID), Password: "not-a-login", AffCode: fmt.Sprintf("mh-aff-%d", identity.UserID), Status: 1, Role: 1, Quota: 1000, UsedQuota: 9, RequestCount: 3, WalletFundingInitialized: true}
		if id == 100 {
			user.Quota = 980
			user.WalletFundingInitialized = false
		}
		require.NoError(t, DB.Create(&user).Error)
		token := Token{Id: identity.TokenID, UserId: identity.UserID, Key: fmt.Sprintf("mh-token-%d", id), Name: "harness", UsedQuota: identity.Quota + 9, RemainQuota: -identity.Quota - 9, UnlimitedQuota: true, Status: 1}
		require.NoError(t, DB.Create(&token).Error)
		task := Task{ID: id, TaskID: identity.TaskID, UserId: identity.UserID, ChannelId: 119, Platform: "blackrxai-video", Quota: identity.Quota, Status: TaskStatusUnknown, CreatedAt: created, Properties: Properties{OriginModelName: identity.Model, UpstreamModelName: identity.Model}, PrivateData: TaskPrivateData{MediaAdmissionID: identity.TaskID, MediaKind: MediaVideo, Key: key, RouteDigest: "frozen-historical-route", BillingSource: "wallet", TokenId: identity.TokenID, Execution: &TaskExecutionSnapshot{RequestID: identity.RequestID, RequestPath: "/v1/videos", TaskPlugin: &TaskPluginSnapshot{Key: "blackrxai-video", Version: "1.0.2", APIVersion: 1, Generation: 13}}}}
		if id != 100 {
			task.PrivateData.FundingRequestId = identity.RequestID
		}
		require.NoError(t, DB.Create(&task).Error)
		if id == 100 {
			raw, err := common.Marshal(task.PrivateData)
			require.NoError(t, err)
			unknown := strings.TrimSuffix(string(raw), "}") + `,"future_frozen_field":{"exact_integer":9007199254740993,"flag":false}}`
			require.NoError(t, DB.Model(&Task{}).Where("id = ?", id).Update("private_data", unknown).Error)
		}

		require.NoError(t, DB.Create(&MediaAdmission{ID: identity.TaskID, TaskDBID: id, PoolID: pool.ID, ChannelID: 119, State: MediaUnknown, BillingState: "reserved", SendingAt: created, CreatedAt: created}).Error)
		body := fmt.Sprintf(`{"error":{"message":"模型不存在：%s","type":"invalid_request_error"}}`, identity.Model)
		other := fmt.Sprintf(`{"status_code":404,"task_id":"%s","request_path":"/v1/videos","admin_info":{"request_policy":[{"attempt":1,"channel_id":119,"decision":{"action":"attempt"}},{"attempt":1,"channel_id":119,"status":404,"error_code":"fail_to_fetch_task","decision":{"action":"failure"}},{"attempt":1,"channel_id":119,"status":404,"error_code":"fail_to_fetch_task","decision":{"action":"stop","reason":"media_submission_unknown"}}]}}`, identity.TaskID)
		require.NoError(t, DB.Create(&Log{Id: identity.LogID, UserId: identity.UserID, ChannelId: 119, TokenId: identity.TokenID, Type: LogTypeError, RequestId: identity.RequestID, ModelName: identity.Model, CreatedAt: created + 2, Content: "status_code=404, " + body, Other: other}).Error)
		if id == 100 {
			continue
		}
		for n := range 3 {
			require.NoError(t, DB.Create(&Log{Id: 30000 + int(id)*10 + n, UserId: identity.UserID, TokenId: identity.TokenID, Type: LogTypeConsume, Quota: 3, ModelName: "deepseek-flash", RequestId: fmt.Sprintf("prior-consumption-%d-%d", id, n)}).Error)
		}
		require.NoError(t, DB.Create(&WalletFundingLot{ID: identity.LotID, UserID: identity.UserID, Source: WalletFundingGift, SourceKey: "phone-registration", OriginalQuota: int64(1000 + identity.Quota + 9), RemainingQuota: 1000, OriginalCashCNY: "0", RemainingCashCNY: "0", CashKnown: true}).Error)
		require.NoError(t, DB.Create(&WalletFundingRequest{ID: identity.FundingID, UserID: identity.UserID, IdentityHash: walletFundingIdentity(identity.RequestID, "relay"), RequestID: identity.RequestID, Purpose: "relay", ModelName: identity.Model, InitialModelName: identity.Model, ChannelID: 119, Quota: int64(identity.Quota), LastEventID: identity.EventID}).Error)
		require.NoError(t, DB.Create(&WalletFundingAllocation{ID: identity.AllocationID, FundingRequestID: identity.FundingID, LotID: identity.LotID, Quota: int64(identity.Quota), CashCNY: "0"}).Error)
		require.NoError(t, DB.Create(&WalletFundingEvent{ID: identity.EventID, UserID: identity.UserID, FundingRequestID: identity.FundingID, RequestID: identity.RequestID, Purpose: "relay", Operation: "target", DeltaQuota: int64(identity.Quota), GiftQuota: int64(identity.Quota), TargetQuota: int64(identity.Quota), KnownCashRevenueCNY: "0", CreatedAt: created}).Error)
	}
	require.NoError(t, DB.Create(&Log{Id: 11163, UserId: 144, Type: LogTypeSystem, Other: `{"balance_change":{"category":"registration_bonus","registration_source":"phone","quota_delta":1000,"quota_before":0,"quota_after":1000}}`}).Error)
	for i := range 3 {
		quota := 1
		quota = 3
		require.NoError(t, DB.Create(&Log{Id: 22000 + i, UserId: 144, TokenId: 143, Type: LogTypeConsume, Quota: quota, ModelName: "deepseek-flash", RequestId: fmt.Sprintf("historical-consumption-%d", i)}).Error)
	}
}

func fixtureMediaHistoricalFile(t *testing.T, data []byte, name string) MediaHistoricalFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0600))
	return MediaHistoricalFile{Path: path, SHA256: mediaHistoricalSHA(data)}
}

func fixtureMediaHistoricalSource(t *testing.T, id int64, host MediaHistoricalHostTrace) MediaHistoricalSourceEvidence {
	t.Helper()
	revision := "24364344e0a121bff4cbb8c5448926553c040904"
	if id == 100 {
		revision = "27992b8e5d9ff59e39de32d902400685e0a7c021"
	}
	// This intentionally uses the real, frozen historical sources. An arbitrary
	// string or today's modified parser cannot stand in for their hash proof.
	read := func(path string) MediaHistoricalFile {
		data, err := exec.Command("git", "show", revision+":"+path).Output()
		require.NoError(t, err)
		return fixtureMediaHistoricalFile(t, data, strings.ReplaceAll(path, "/", "-"))
	}
	var task Task
	require.NoError(t, DB.First(&task, id).Error)
	provenance := fmt.Sprintf(`{"historical_runtimes":[{"revision":"%s","started_at":"%s","finished_at":"%s"}],"log_files":[{"path":"%s","sha256":"%s"}]}`, revision, time.Unix(task.CreatedAt-60, 0).UTC().Format(time.RFC3339Nano), time.Unix(task.CreatedAt+60, 0).UTC().Format(time.RFC3339Nano), host.OriginalPath, host.File.SHA256)
	return MediaHistoricalSourceEvidence{RuntimeProvenance: fixtureMediaHistoricalFile(t, []byte(provenance), "runtime.json"), Revision: revision, ReleaseReceipt: fixtureMediaHistoricalFile(t, []byte(revision), "release.txt"), FundingSource: read("service/funding_source.go"), BillingSession: read("service/billing_session.go"), RelayController: read("controller/relay.go"), RelayTask: read("relay/relay_task.go"), Plugin: read("deploy/instance/blackrxai-video-plugin.js")}
}

func fixtureMediaHistoricalHost(t *testing.T, id int64) MediaHistoricalHostTrace {
	t.Helper()
	identity, _ := fixtureHistoricalIdentity(id)
	var log Log
	require.NoError(t, DB.Where("id = ?", identity.LogID).First(&log).Error)
	trace := MediaHistoricalHostTrace{PreconsumeLine: fmt.Sprintf("[INFO] original | %s | user %d needs pre-consume ¥3.00 (funding=wallet)", identity.RequestID, identity.UserID), RejectionLine: fmt.Sprintf("[INFO] original | %s | record error log: userId=%d, channelId=119, modelName=%s, tokenName=harness, content=%s", identity.RequestID, identity.UserID, identity.Model, log.Content), ResponseLine: fmt.Sprintf("[GIN] original | relay | %s | 202 | 2s | 127.0.0.1 | POST /v1/videos", identity.RequestID)}
	trace.File = fixtureMediaHistoricalFile(t, []byte(trace.PreconsumeLine+"\n"+trace.RejectionLine+"\n"+trace.ResponseLine+"\n"), "original-host.log")
	trace.OriginalPath = trace.File.Path
	return trace
}

func fixtureHistoricalIdentity(id int64) (MediaHistoricalIdentity, bool) {
	switch id {
	case 100:
		return MediaHistoricalIdentity{ChannelID: 119, PluginGeneration: 13, TaskID: "task_synthetic_legacy", RequestID: "synthetic-original-legacy", Model: "sd2-hn2-480-pro", UserID: 144, TokenID: 143, Quota: 11, LogID: 11229}, true
	case 101:
		return MediaHistoricalIdentity{ChannelID: 119, PluginGeneration: 13, TaskID: "task_synthetic_modern_a", RequestID: "synthetic-original-a", Model: "bt-seedance-2.0", UserID: 149, TokenID: 148, Quota: 22, LogID: 11230, FundingID: 201, EventID: 301, AllocationID: 401, LotID: 501}, true
	case 102:
		return MediaHistoricalIdentity{ChannelID: 119, PluginGeneration: 13, TaskID: "task_synthetic_modern_b", RequestID: "synthetic-original-b", Model: "sd2-th-2.0-720p", UserID: 150, TokenID: 151, Quota: 33, LogID: 11231, FundingID: 202, EventID: 302, AllocationID: 402, LotID: 502}, true
	case 103:
		return MediaHistoricalIdentity{ChannelID: 119, PluginGeneration: 13, TaskID: "task_synthetic_modern_c", RequestID: "synthetic-original-c", Model: "bt-seedance-2.0", UserID: 138, TokenID: 137, Quota: 44, LogID: 11232, FundingID: 203, EventID: 303, AllocationID: 403, LotID: 503}, true
	}
	return MediaHistoricalIdentity{}, false
}
