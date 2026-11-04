package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func TestMediaConcurrencyLegacyPolicyAndProviderDefaults(t *testing.T) {
	policy, err := decodeMediaPolicy(`{"schema_version":1,"defaults":{"video":4,"image":10},"pool_overrides":[]}`)
	require.NoError(t, err)
	value, err := mediaPolicyValue(policy)
	require.NoError(t, err)
	assert.Contains(t, value, `"audio":1`)
	assert.Contains(t, value, `"provider_defaults":{"ycapi":{"video":20,"image":20,"audio":20}}`)
	_, err = decodeMediaPolicy(`{"schema_version":1,"defaults":{"video":64,"image":64,"audio":64},"provider_defaults":{"ycapi":{"video":64,"image":64,"audio":64}},"pool_overrides":[]}`)
	require.NoError(t, err)
	for _, invalid := range []string{
		`{}`,
		`{"schema_version":1,"defaults":{"video":null,"image":10},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":null},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10,"audio":0},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10,"audio":null},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":65,"image":10},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10},"provider_defaults":{"ycapi":{"video":0,"image":20,"audio":20}},"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10},"provider_defaults":null,"pool_overrides":[]}`,
		`{"schema_version":1,"defaults":{"video":4,"image":10},"provider_defaults":{},"pool_overrides":[]}`,
	} {
		_, err = decodeMediaPolicy(invalid)
		assert.Error(t, err, invalid)
	}
	assert.Equal(t, 1, mediaEffectiveLimit(policy, "generic-audio", MediaAudio, 0), "other suppliers retain their existing audio default")
}

func TestMediaCapacityExpiryDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MEDIA_CAPACITY_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade=%t", upgrade), func(t *testing.T) {
			var driver gorm.Dialector
			switch kind {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "capacity.db") + "?_pragma=busy_timeout(30000)&_txlock=immediate")
			case "mysql":
				require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
				driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case "postgres":
				require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
				driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
			default:
				t.Fatalf("unsupported dialect %s", kind)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("ce_%d_", time.Now().UnixNano())}, Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			common.SetDatabaseTypes(common.DatabaseType(kind), common.LogDatabaseType())
			initCol()
			models := []any{&Task{}, &Channel{}, &Option{}, &MediaPool{}, &MediaAdmission{}, &H3Account{}, &H3Admission{}, &MediaHistoricalReconciliation{}}
			t.Cleanup(func() {
				for i := len(models) - 1; i >= 0; i-- {
					require.NoError(t, db.Migrator().DropTable(models[i]))
				}
				DB = oldDB
				common.SetDatabaseTypes(oldType, common.LogDatabaseType())
				initCol()
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(models...))
			now := time.Now().Truncate(time.Second)
			var tasks []Task
			for i, state := range []string{MediaReserved, MediaSending, MediaUnknown, MediaActive, MediaReserved, MediaReleased} {
				created := now.Add(-30 * time.Minute).Unix()
				if i == 4 {
					created++ // One second before expiry must retain its slot.
				}
				for _, h3 := range []bool{false, true} {
					task := Task{TaskID: fmt.Sprintf("task_expiry_%d_%t", i, h3), UserId: 7, ChannelId: 1, Status: TaskStatusUnknown, Progress: "0%", Quota: 1234}
					if h3 {
						task.PrivateData.H3AdmissionID = task.TaskID
					} else {
						task.PrivateData.MediaAdmissionID = task.TaskID
						task.PrivateData.MediaKind = MediaImage
					}
					require.NoError(t, db.Create(&task).Error)
					tasks = append(tasks, task)
					sending, billing := int64(0), "preparing"
					if i > 0 && i < 4 {
						sending, billing = created, "reserved"
					}
					if h3 {
						require.NoError(t, db.Create(&H3Admission{ID: task.TaskID, TaskDBID: task.ID, ChannelID: 1, AccountID: "h3-expiry", State: state, BillingState: billing, CreatedAt: created, SendingAt: sending, UpdatedAt: created}).Error)
					} else {
						require.NoError(t, db.Create(&MediaAdmission{ID: task.TaskID, TaskDBID: task.ID, ChannelID: 1, PoolID: "image-expiry", State: state, BillingState: billing, CreatedAt: created, SendingAt: sending, UpdatedAt: created}).Error)
					}
				}
			}
			require.NoError(t, db.Create(&MediaPool{ID: "image-expiry", IdentityDigest: "synthetic-image", MediaKind: MediaImage, Occupied: 5, Reconciled: true}).Error)
			require.NoError(t, db.Create(&H3Account{ID: "h3-expiry", ChannelID: 1, CredentialDigest: "synthetic-h3", Occupied: 5}).Error)
			if upgrade {
				// Recreate the released layout with populated rows before upgrading.
				require.NoError(t, db.Migrator().DropColumn(&MediaAdmission{}, "CapacityReleasedAt"))
				require.NoError(t, db.Migrator().DropColumn(&H3Admission{}, "CapacityReleasedAt"))
			}
			beforeHash, err := historicalRowsHash(db.Model(&MediaAdmission{}).Select("*").Order("id"))
			require.NoError(t, err)
			beforeH3Hash, err := historicalRowsHash(db.Model(&H3Admission{}).Select("*").Order("id"))
			require.NoError(t, err)
			require.NoError(t, MigrateH3Admission(db))
			require.NoError(t, MigrateH3Admission(db))
			afterHash, err := historicalRowsHash(db.Model(&MediaAdmission{}).Select("*").Order("id"))
			require.NoError(t, err)
			assert.Equal(t, beforeHash, afterHash, "migration must preserve frozen financial evidence hashes")
			var version string
			query := "SELECT version()"
			if kind == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("capacity expiry database: %s", version)
			// Existing unique indexes must survive migration, including repeated startup.
			err = db.Create(&MediaAdmission{ID: "duplicate-task", TaskDBID: tasks[0].ID}).Error
			require.Error(t, err)
			err = db.Create(&H3Admission{ID: "duplicate-task", TaskDBID: tasks[1].ID}).Error
			require.Error(t, err)
			ctx := context.Background()
			count, err := ExpireMediaCapacity(ctx, now)
			require.NoError(t, err)
			assert.Equal(t, 8, count)
			expiredHash, err := historicalRowsHash(db.Model(&MediaAdmission{}).Select("*").Order("id"))
			require.NoError(t, err)
			assert.Equal(t, beforeHash, expiredHash, "expiry changes only the capacity marker, preserving financial evidence")
			expiredH3Hash, err := historicalRowsHash(db.Model(&H3Admission{}).Select("*").Order("id"))
			require.NoError(t, err)
			assert.Equal(t, beforeH3Hash, expiredH3Hash)
			count, err = ExpireMediaCapacity(ctx, now)
			require.NoError(t, err)
			assert.Zero(t, count, "repeated passes cannot release a slot twice")
			for i := range tasks {
				var persisted Task
				require.NoError(t, db.First(&persisted, tasks[i].ID).Error)
				assert.Equal(t, tasks[i], persisted, "expiry must preserve task, billing snapshot and original receipt")
			}
			var pool MediaPool
			var account H3Account
			require.NoError(t, db.First(&pool, "id = ?", "image-expiry").Error)
			require.NoError(t, db.First(&account, "id = ?", "h3-expiry").Error)
			assert.Equal(t, 1, pool.Occupied)
			assert.Equal(t, 1, account.Occupied)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return reconcileMediaPool(tx, &pool) }))
			assert.Equal(t, 1, pool.Occupied, "reconciliation cannot reoccupy expired slots")
			// Expired unsent operations remain fenced even if billing finishes late.
			require.NoError(t, db.Model(&MediaAdmission{}).Where("id = ?", tasks[0].TaskID).Update("billing_state", "reserved").Error)
			require.NoError(t, db.Model(&H3Admission{}).Where("id = ?", tasks[1].TaskID).Update("billing_state", "reserved").Error)
			assert.Error(t, MarkMediaSending(ctx, tasks[0].TaskID))
			assert.Error(t, MarkH3Sending(ctx, tasks[1].TaskID))
			for _, index := range []int{4, 5} { // Original UNKNOWN receipts can arrive late.
				task := &tasks[index]
				task.PrivateData.UpstreamTaskID = "original-late-provider-id"
				if index == 4 {
					require.NoError(t, SaveMediaReceipt(ctx, task))
					pollable, err := MediaTaskPollable(ctx, task.TaskID)
					require.NoError(t, err)
					assert.True(t, pollable)
				} else {
					require.NoError(t, SaveH3Receipt(ctx, task))
					pollable, err := H3TaskPollable(ctx, task.TaskID)
					require.NoError(t, err)
					assert.True(t, pollable)
				}
				task.Status, task.Progress = TaskStatusSuccess, "100%"
				task.PrivateData.ResultURL = "https://example.invalid/original-artifact.png"
				var won bool
				if index == 4 {
					won, err = UpdateMediaTaskWithStatus(ctx, task, TaskStatusUnknown, true)
				} else {
					won, err = UpdateH3TaskWithStatus(ctx, task, TaskStatusUnknown, true)
				}
				require.NoError(t, err)
				assert.True(t, won)
				var persisted Task
				require.NoError(t, db.First(&persisted, task.ID).Error)
				assert.Equal(t, TaskStatus(TaskStatusSuccess), persisted.Status)
				assert.Equal(t, task.PrivateData.ResultURL, persisted.PrivateData.ResultURL)
				assert.Equal(t, 1234, persisted.Quota)
			}
			require.NoError(t, db.First(&pool, "id = ?", pool.ID).Error)
			require.NoError(t, db.First(&account, "id = ?", account.ID).Error)
			assert.Equal(t, 1, pool.Occupied, "late completion cannot decrement a replacement's slot")
			assert.Equal(t, 1, account.Occupied)
			// An expired but not yet swept reservation must not cross the send fence.
			require.NoError(t, db.Model(&MediaAdmission{}).Where("id = ?", tasks[8].TaskID).Update("created_at", now.Add(-31*time.Minute).Unix()).Error)
			require.NoError(t, db.Model(&H3Admission{}).Where("id = ?", tasks[9].TaskID).Update("created_at", now.Add(-31*time.Minute).Unix()).Error)
			require.NoError(t, db.Model(&MediaAdmission{}).Where("id = ?", tasks[8].TaskID).Update("billing_state", "reserved").Error)
			require.NoError(t, db.Model(&H3Admission{}).Where("id = ?", tasks[9].TaskID).Update("billing_state", "reserved").Error)
			assert.Error(t, MarkMediaSending(ctx, tasks[8].TaskID))
			assert.Error(t, MarkH3Sending(ctx, tasks[9].TaskID))
		})
	}
}
