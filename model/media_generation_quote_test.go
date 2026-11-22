package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// The same focused contracts can run against disposable real databases with
// TEST_MEDIA_GENERATION_QUOTE_DIALECT=mysql|postgres and TEST_*_DSN.
func mediaGenerationQuoteTestStore(t *testing.T) MediaGenerationQuoteStore {
	t.Helper()
	kind := os.Getenv("TEST_MEDIA_GENERATION_QUOTE_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	var driver gorm.Dialector
	switch kind {
	case "sqlite":
		driver = sqlite.Open(filepath.Join(t.TempDir(), "quotes.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	case "mysql":
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case "postgres":
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported quote test dialect %s", kind)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("mgq_%d_", time.Now().UnixNano())}, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	previousType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseType(kind))
	initCol()
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().DropTable(&MediaGenerationUpload{}, &MediaGenerationAsset{}, &MediaGenerationQuote{}))
		require.NoError(t, sqlDB.Close())
		common.SetMainDatabaseType(previousType)
		initCol()
	})
	for range 2 {
		require.NoError(t, MigrateMediaGenerationQuote(db))
		require.NoError(t, MigrateMediaGenerationAsset(db))
		require.NoError(t, MigrateMediaGenerationUpload(db))
	}
	versionQuery := "SELECT version()"
	if kind == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("media quote database: %s %s", kind, version)
	return MediaGenerationQuoteStore{DB: db}
}

func mediaGenerationQuoteFixture(operation string) MediaGenerationQuote {
	return MediaGenerationQuote{
		QuoteID: "mgq_synthetic_" + operation, UserID: 7, TokenID: 11,
		OwnerDigest: strings.Repeat("a", 64), ClientOpID: operation,
		RequestSHA256: strings.Repeat("b", 64), MaterialSHA256: strings.Repeat("c", 64),
		Model: "ycapi-synthetic-video", Kind: "video", ChannelID: 46, UsingGroup: "product",
		CredentialDigest: common.GenerateHMAC("media-v2-key:synthetic-quote-admission-key"), ChannelConfigHash: strings.Repeat("e", 64),
		ModelConfigVersion: strings.Repeat("f", 64), PricingVersion: strings.Repeat("1", 64),
		PluginKey: "ycapi", PluginVersion: "1.0.4", PluginSourceHash: strings.Repeat("2", 64),
		SupplierLane: "synthetic-video@fixture:one", NativeParamsJSON: `{"duration":8,"video_total_duration_sec":21.2}`,
		UsageFactsJSON: `{"seconds":8,"input_video_seconds":21.2}`, CostCNY: "1.25", RetailCNY: "1.75", ReserveQuota: 12345,
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	}
}

func mediaGenerationQuoteTestBinding(quote *MediaGenerationQuote) MediaGenerationQuoteBinding {
	return MediaGenerationQuoteBinding{QuoteID: quote.QuoteID, OperationKey: quote.OperationKey,
		UserID: quote.UserID, TokenID: quote.TokenID, OwnerDigest: quote.OwnerDigest, ClientOpID: quote.ClientOpID,
		RequestSHA256: quote.RequestSHA256, MaterialSHA256: quote.MaterialSHA256}
}

func TestMediaGenerationQuoteAssetUploadRepeatedMigrationPreservesReceipts(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	input := mediaGenerationQuoteFixture("operation-migration-001")
	quote, err := store.SaveQuote(t.Context(), &input)
	require.NoError(t, err)
	asset := MediaGenerationAsset{ID: "ma2_synthetic_receipt", OperationKey: strings.Repeat("3", 64), ClientAssetID: "synthetic-asset-operation", UserID: input.UserID, TokenID: input.TokenID, OwnerDigest: input.OwnerDigest, Model: input.Model, State: "ready", Kind: "video", MIMEType: "video/mp4", Bytes: 1234, SHA256: strings.Repeat("4", 64), Width: 48, Height: 32, DurationSeconds: 1.25, FPS: 24, Format: "mp4", ObjectKey: "asset-00000000-0000-4000-8000-000000000001", CreatedAt: 123, ExpiresAt: 456}
	upload := MediaGenerationUpload{ID: strings.Repeat("5", 64), AssetID: asset.ID, CredentialDigest: input.CredentialDigest, State: "unknown", URL: "https://synthetic.invalid/original-private-file", ProviderFileID: "synthetic-original-upload", CreatedAt: 123, UpdatedAt: 456}
	require.NoError(t, store.DB.Create(&asset).Error)
	require.NoError(t, store.DB.Create(&upload).Error)
	for range 2 {
		require.NoError(t, MigrateMediaGenerationQuote(store.DB))
		require.NoError(t, MigrateMediaGenerationAsset(store.DB))
		require.NoError(t, MigrateMediaGenerationUpload(store.DB))
	}
	var actualAsset MediaGenerationAsset
	var actualUpload MediaGenerationUpload
	require.NoError(t, store.DB.First(&actualAsset, "id = ?", asset.ID).Error)
	require.NoError(t, store.DB.First(&actualUpload, "id = ?", upload.ID).Error)
	assert.Equal(t, asset, actualAsset)
	assert.Equal(t, upload, actualUpload, "unknown original upload state and original coordinates survive repeated migration")
	actualQuote, err := store.GetUnclaimedQuote(t.Context(), mediaGenerationQuoteTestBinding(quote))
	require.NoError(t, err)
	assert.Equal(t, quote, actualQuote)
}

func TestMediaGenerationQuoteLargeUnicodeSnapshotsSurviveMigration(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	prompt := strings.Repeat("云枢", 16_384)
	native, err := common.Marshal(map[string]any{"prompt": prompt, "duration": 8})
	require.NoError(t, err)
	frozen, err := common.Marshal(map[string]any{"request": map[string]any{"parameters": map[string]any{"prompt": prompt}}, "parameters": map[string]any{"prompt": prompt}, "facts": map[string]any{"seconds": 8}})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(native), 90<<10)
	require.GreaterOrEqual(t, len(frozen), 180<<10)
	require.Less(t, len(native), 256<<10, "original semantic request stays within the existing HTTP input bound")
	input := mediaGenerationQuoteFixture("operation-large-unicode")
	input.NativeParamsJSON, input.UsageFactsJSON = LongText(native), LongText(frozen)
	saved, err := store.SaveQuote(t.Context(), &input)
	require.NoError(t, err)
	var migrations bytes.Buffer
	db := store.DB.Session(&gorm.Session{Logger: logger.New(log.New(&migrations, "", 0), logger.Config{LogLevel: logger.Info, ParameterizedQueries: true})})
	for range 2 {
		require.NoError(t, MigrateMediaGenerationQuote(db))
	}
	assert.NotContains(t, strings.ToUpper(migrations.String()), "ALTER TABLE", "an already migrated LongText column must not repeatedly change type")
	actual, err := store.GetUnclaimedQuote(t.Context(), mediaGenerationQuoteTestBinding(saved))
	require.NoError(t, err)
	assert.Equal(t, input.NativeParamsJSON, actual.NativeParamsJSON)
	assert.Equal(t, input.UsageFactsJSON, actual.UsageFactsJSON)
	assert.Equal(t, saved, actual)
	columns, err := store.DB.Migrator().ColumnTypes(&MediaGenerationQuote{})
	require.NoError(t, err)
	expectedType := "text"
	if store.DB.Dialector.Name() == "mysql" {
		expectedType = "longtext"
	}
	checked := 0
	for _, column := range columns {
		if column.Name() == "native_params_json" || column.Name() == "usage_facts_json" {
			assert.Equal(t, expectedType, strings.ToLower(column.DatabaseTypeName()))
			checked++
		}
	}
	assert.Equal(t, 2, checked)
	t.Logf("Unicode quote preserved native=%d bytes frozen=%d bytes as %s", len(native), len(frozen), expectedType)
}

func TestMediaGenerationQuoteImmutableOperationAndIsolation(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	ctx := context.Background()
	input := mediaGenerationQuoteFixture("operation-original-01")
	originalInput := input
	first, err := store.SaveQuote(ctx, &input)
	require.NoError(t, err)
	assert.Equal(t, originalInput, input, "saving must not mutate the caller's proposed quote")
	proposed := input
	proposed.QuoteID = "mgq_synthetic_different_id"
	proposed.CostCNY, proposed.RetailCNY, proposed.ReserveQuota = "0.01", "0.02", 1
	proposed.ExpiresAt += 3600
	duplicate, err := store.SaveQuote(ctx, &proposed)
	require.NoError(t, err)
	assert.Equal(t, first, duplicate, "one operation retains its complete original price and expiry")
	require.NoError(t, MigrateMediaGenerationQuote(store.DB))
	unclaimed, err := store.GetUnclaimedQuote(ctx, mediaGenerationQuoteTestBinding(first))
	require.NoError(t, err)
	assert.Equal(t, first, unclaimed, "repeated migration retains the existing immutable quote")
	_, err = store.FindOperationReceipt(ctx, mediaGenerationQuoteTestBinding(first))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	for _, field := range []string{"request", "material"} {
		t.Run(field+" conflict", func(t *testing.T) {
			changed := input
			binding := mediaGenerationQuoteTestBinding(first)
			if field == "request" {
				changed.RequestSHA256, binding.RequestSHA256 = strings.Repeat("3", 64), strings.Repeat("3", 64)
			} else {
				changed.MaterialSHA256, binding.MaterialSHA256 = strings.Repeat("4", 64), strings.Repeat("4", 64)
			}
			_, err := store.SaveQuote(ctx, &changed)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict)
			_, err = store.GetUnclaimedQuote(ctx, binding)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict)
			_, err = store.FindOperationReceipt(ctx, binding)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict)
			err = store.DB.Transaction(func(tx *gorm.DB) error { return store.BindQuoteToTaskTx(ctx, tx, binding, 50, "task_conflict") })
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict)
		})
	}
	for _, field := range []string{"user", "token", "digest"} {
		t.Run(field+" isolation", func(t *testing.T) {
			binding := mediaGenerationQuoteTestBinding(first)
			binding.OperationKey = "" // recompute the other owner's genuine operation key
			switch field {
			case "user":
				binding.UserID++
			case "token":
				binding.TokenID++
			case "digest":
				binding.OwnerDigest = strings.Repeat("5", 64)
			}
			_, err := store.GetUnclaimedQuote(ctx, binding)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			_, err = store.FindOperationReceipt(ctx, binding)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			err = store.DB.Transaction(func(tx *gorm.DB) error { return store.BindQuoteToTaskTx(ctx, tx, binding, 51, "task_foreign") })
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		})
	}
	otherOwner := input
	otherOwner.UserID++
	otherOwner.QuoteID = "mgq_synthetic_other_owner"
	second, err := store.SaveQuote(ctx, &otherOwner)
	require.NoError(t, err)
	assert.NotEqual(t, first.OperationKey, second.OperationKey, "the same client operation is isolated by owner")
}

func TestMediaGenerationQuoteBindingRollbackAndExpiredReceipt(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	ctx := context.Background()
	input := mediaGenerationQuoteFixture("operation-bind-0001")
	quote, err := store.SaveQuote(ctx, &input)
	require.NoError(t, err)
	binding := mediaGenerationQuoteTestBinding(quote)
	rolledBack := errors.New("synthetic task/admission creation rolled back")
	err = store.DB.Transaction(func(tx *gorm.DB) error {
		if err := store.BindQuoteToTaskTx(ctx, tx, binding, 101, "task_rolled_back"); err != nil {
			return err
		}
		return rolledBack
	})
	require.ErrorIs(t, err, rolledBack)
	unclaimed, err := store.GetUnclaimedQuote(ctx, binding)
	require.NoError(t, err)
	assert.Zero(t, unclaimed.TaskDBID)
	assert.Empty(t, unclaimed.TaskID)
	assert.Zero(t, unclaimed.BoundAt)
	require.NoError(t, store.DB.Transaction(func(tx *gorm.DB) error { return store.BindQuoteToTaskTx(ctx, tx, binding, 102, "task_original") }))
	require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Where("quote_id = ?", quote.QuoteID).Update("expires_at", time.Now().Unix()-1).Error)
	receipt, err := store.FindOperationReceipt(ctx, binding)
	require.NoError(t, err)
	assert.Equal(t, int64(102), receipt.TaskDBID)
	assert.Equal(t, "task_original", receipt.TaskID)
	_, err = store.GetUnclaimedQuote(ctx, binding)
	require.ErrorIs(t, err, ErrMediaGenerationQuoteReplayed)
	var replay *MediaGenerationQuoteReplayError
	require.ErrorAs(t, err, &replay)
	assert.Equal(t, "task_original", replay.TaskID)
	err = store.DB.Transaction(func(tx *gorm.DB) error {
		return store.BindQuoteToTaskTx(ctx, tx, binding, 103, "task_forbidden_second")
	})
	require.ErrorIs(t, err, ErrMediaGenerationQuoteReplayed)
	require.ErrorAs(t, err, &replay)
	assert.Equal(t, int64(102), replay.TaskDBID)
	assert.Equal(t, "task_original", replay.TaskID)
	changed := binding
	changed.RequestSHA256 = strings.Repeat("9", 64)
	_, err = store.FindOperationReceipt(ctx, changed)
	require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "an expired accepted operation cannot be reused with a different request")

	expiring := mediaGenerationQuoteFixture("operation-expire-01")
	quote, err = store.SaveQuote(ctx, &expiring)
	require.NoError(t, err)
	require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Where("quote_id = ?", quote.QuoteID).Update("expires_at", time.Now().Unix()-1).Error)
	binding = mediaGenerationQuoteTestBinding(quote)
	_, err = store.GetUnclaimedQuote(ctx, binding)
	require.ErrorIs(t, err, ErrMediaGenerationQuoteExpired)
	err = store.DB.Transaction(func(tx *gorm.DB) error { return store.BindQuoteToTaskTx(ctx, tx, binding, 104, "task_expired") })
	require.ErrorIs(t, err, ErrMediaGenerationQuoteExpired)
	_, err = store.FindOperationReceipt(ctx, binding)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestMediaGenerationQuoteConcurrentInsertAndBind(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	ctx := context.Background()
	first := mediaGenerationQuoteFixture("operation-concurrent-01")
	second := first
	second.QuoteID, second.RetailCNY = "mgq_synthetic_competing_quote", "2.00"
	start := make(chan struct{})
	type saved struct {
		quote *MediaGenerationQuote
		err   error
	}
	saves := make(chan saved, 2)
	for _, candidate := range []MediaGenerationQuote{first, second} {
		go func() {
			<-start
			quote, err := store.SaveQuote(ctx, &candidate)
			saves <- saved{quote, err}
		}()
	}
	close(start)
	a, b := <-saves, <-saves
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	require.Equal(t, a.quote, b.quote)
	var count int64
	require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	binding := mediaGenerationQuoteTestBinding(a.quote)
	start = make(chan struct{})
	type bound struct {
		id  int64
		err error
	}
	binds := make(chan bound, 2)
	for _, id := range []int64{201, 202} {
		go func() {
			<-start
			err := store.DB.Transaction(func(tx *gorm.DB) error {
				return store.BindQuoteToTaskTx(ctx, tx, binding, id, fmt.Sprintf("task_%d", id))
			})
			binds <- bound{id, err}
		}()
	}
	close(start)
	x, y := <-binds, <-binds
	if x.err != nil {
		x, y = y, x
	}
	require.NoError(t, x.err, "one original task wins the quote claim")
	require.ErrorIs(t, y.err, ErrMediaGenerationQuoteReplayed)
	var replay *MediaGenerationQuoteReplayError
	require.ErrorAs(t, y.err, &replay)
	assert.Equal(t, x.id, replay.TaskDBID)
	assert.Equal(t, fmt.Sprintf("task_%d", x.id), replay.TaskID)
	receipt, err := store.FindOperationReceipt(ctx, binding)
	require.NoError(t, err)
	assert.Equal(t, x.id, receipt.TaskDBID)
}

func TestMediaGenerationQuoteRejectsInvalidFrozenValues(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	for _, test := range []struct {
		name   string
		change func(*MediaGenerationQuote)
	}{
		{"negative reserve", func(q *MediaGenerationQuote) { q.ReserveQuota = -1 }},
		{"oversized reserve", func(q *MediaGenerationQuote) { q.ReserveQuota = int64(common.MaxQuota) + 1 }},
		{"negative amount", func(q *MediaGenerationQuote) { q.RetailCNY = "-0.1" }},
		{"nonfinite amount", func(q *MediaGenerationQuote) { q.CostCNY = "NaN" }},
		{"unbounded exponent", func(q *MediaGenerationQuote) { q.RetailCNY = "1e999999999" }},
		{"missing material identity", func(q *MediaGenerationQuote) { q.MaterialSHA256 = "" }},
		{"forged operation key", func(q *MediaGenerationQuote) { q.OperationKey = strings.Repeat("0", 64) }},
		{"nonobject native params", func(q *MediaGenerationQuote) { q.NativeParamsJSON = `[]` }},
		{"null frozen facts", func(q *MediaGenerationQuote) { q.UsageFactsJSON = `null` }},
		{"already bound proposal", func(q *MediaGenerationQuote) { q.TaskDBID, q.TaskID = 301, "task_untrusted" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			quote := mediaGenerationQuoteFixture("operation-invalid-01")
			test.change(&quote)
			_, err := store.SaveQuote(context.Background(), &quote)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteInvalid)
		})
	}
	var count int64
	require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestMediaGenerationQuoteExplicitRenewal(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	ctx := context.Background()
	input := mediaGenerationQuoteFixture("operation-renew-001")
	original, err := store.SaveQuote(ctx, &input)
	require.NoError(t, err)
	binding := mediaGenerationQuoteTestBinding(original)
	replacement := input
	replacement.QuoteID = "mgq_synthetic_renewed"
	replacement.CostCNY, replacement.RetailCNY = "2.00", "2.50"
	replacement.PricingVersion = strings.Repeat("7", 64)
	_, err = store.RenewUnboundQuote(ctx, binding, original.ExpiresAt, &replacement)
	require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "a live quote must not be silently repriced")
	expired := time.Now().Unix() - 1
	require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Where("quote_id = ?", original.QuoteID).Update("expires_at", expired).Error)
	for _, field := range []string{"request", "material", "model", "owner", "quote"} {
		t.Run(field+" renewal conflict", func(t *testing.T) {
			changed := replacement
			switch field {
			case "request":
				changed.RequestSHA256 = strings.Repeat("3", 64)
			case "material":
				changed.MaterialSHA256 = strings.Repeat("4", 64)
			case "model":
				changed.Model = "another-model"
			case "owner":
				changed.UserID++
			case "quote":
				changed.QuoteID = original.QuoteID
			}
			_, err := store.RenewUnboundQuote(ctx, binding, expired, &changed)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict)
		})
	}
	_, err = store.RenewUnboundQuote(ctx, binding, expired-1, &replacement)
	require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "renew requires the exact previous expiry")
	renewed, err := store.RenewUnboundQuote(ctx, binding, expired, &replacement)
	require.NoError(t, err)
	assert.Equal(t, original.OperationKey, renewed.OperationKey)
	assert.Equal(t, replacement.QuoteID, renewed.QuoteID)
	assert.Equal(t, "2.50", renewed.RetailCNY)
	_, err = store.GetUnclaimedQuote(ctx, binding)
	require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "a superseded quote is changed, never an absent operation")
	err = store.DB.Transaction(func(tx *gorm.DB) error { return store.BindQuoteToTaskTx(ctx, tx, binding, 400, "task_old_quote") })
	require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict)
	newBinding := mediaGenerationQuoteTestBinding(renewed)
	require.NoError(t, store.DB.Transaction(func(tx *gorm.DB) error { return store.BindQuoteToTaskTx(ctx, tx, newBinding, 401, "task_renewed") }))
	require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Where("quote_id = ?", renewed.QuoteID).Update("expires_at", expired).Error)
	replacement.QuoteID = "mgq_forbidden_after_accept"
	_, err = store.RenewUnboundQuote(ctx, newBinding, expired, &replacement)
	require.ErrorIs(t, err, ErrMediaGenerationQuoteReplayed, "accepted work can never be renewed")
	receipt, err := store.FindOperationReceipt(ctx, newBinding)
	require.NoError(t, err)
	assert.Equal(t, "task_renewed", receipt.TaskID)
}

func TestMediaGenerationQuoteConcurrentExpiredBindAndRenew(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	ctx := context.Background()
	input := mediaGenerationQuoteFixture("operation-renew-race")
	original, err := store.SaveQuote(ctx, &input)
	require.NoError(t, err)
	expired := time.Now().Unix() - 1
	require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Where("quote_id = ?", original.QuoteID).Update("expires_at", expired).Error)
	binding := mediaGenerationQuoteTestBinding(original)
	replacement := input
	replacement.QuoteID = "mgq_synthetic_race_renewed"
	start := make(chan struct{})
	renewErr, bindErr := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		_, err := store.RenewUnboundQuote(ctx, binding, expired, &replacement)
		renewErr <- err
	}()
	go func() {
		<-start
		bindErr <- store.DB.Transaction(func(tx *gorm.DB) error {
			return store.BindQuoteToTaskTx(ctx, tx, binding, 500, "task_forbidden_expired")
		})
	}()
	close(start)
	require.NoError(t, <-renewErr)
	err = <-bindErr
	assert.True(t, errors.Is(err, ErrMediaGenerationQuoteExpired) || errors.Is(err, ErrMediaGenerationQuoteConflict), "old quote must fail regardless of lock winner: %v", err)
	current, err := store.GetUnclaimedQuote(ctx, mediaGenerationQuoteTestBinding(&replacement))
	require.NoError(t, err)
	assert.Zero(t, current.TaskDBID)
	assert.Empty(t, current.TaskID)
}

func TestMediaGenerationQuoteRefreshesLiveConfigurationOnly(t *testing.T) {
	for _, field := range []string{"model_version", "channel_configuration", "credential"} {
		t.Run(field, func(t *testing.T) {
			store := mediaGenerationQuoteTestStore(t)
			input := mediaGenerationQuoteFixture("operation-live-refresh-" + field)
			original, err := store.SaveQuote(t.Context(), &input)
			require.NoError(t, err)
			binding := mediaGenerationQuoteTestBinding(original)
			replacement := input
			replacement.QuoteID = "mgq_live_configuration_replacement"
			replacement.ExpiresAt += 60
			_, err = store.RenewUnboundQuote(t.Context(), binding, original.ExpiresAt, &replacement)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "unchanged live configuration cannot renew only its ID/TTL")
			switch field {
			case "model_version":
				replacement.ModelConfigVersion = strings.Repeat("7", 64)
			case "channel_configuration":
				replacement.ChannelConfigHash = strings.Repeat("8", 64)
			case "credential":
				replacement.CredentialDigest = strings.Repeat("9", 64)
			}
			replacement.CostCNY, replacement.RetailCNY, replacement.ReserveQuota = "2.00", "2.50", 23456
			_, err = store.RenewUnboundQuote(t.Context(), binding, original.ExpiresAt+1, &replacement)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "refresh still compares the exact previous expiry")
			changedBody := replacement
			changedBody.RequestSHA256 = strings.Repeat("3", 64)
			_, err = store.RenewUnboundQuote(t.Context(), binding, original.ExpiresAt, &changedBody)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "a version refresh does not permit a changed semantic request")
			refreshed, err := store.RenewUnboundQuote(t.Context(), binding, original.ExpiresAt, &replacement)
			require.NoError(t, err)
			assert.Equal(t, replacement.QuoteID, refreshed.QuoteID)
			assert.Equal(t, original.OperationKey, refreshed.OperationKey)
			assert.Equal(t, original.RequestSHA256, refreshed.RequestSHA256)
			assert.Equal(t, original.MaterialSHA256, refreshed.MaterialSHA256)
			assert.Zero(t, refreshed.TaskDBID)
			_, err = store.GetUnclaimedQuote(t.Context(), binding)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "superseded live quote cannot be submitted")
			_, err = store.RenewUnboundQuote(t.Context(), binding, original.ExpiresAt, &replacement)
			require.ErrorIs(t, err, ErrMediaGenerationQuoteConflict, "a stale refresh cannot replace the winning quote")
		})
	}
}

func TestMediaGenerationQuoteConcurrentLiveBindAndRefresh(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	for attempt := range 8 {
		input := mediaGenerationQuoteFixture(fmt.Sprintf("operation-live-race-%02d", attempt))
		original, err := store.SaveQuote(t.Context(), &input)
		require.NoError(t, err)
		binding := mediaGenerationQuoteTestBinding(original)
		replacement := input
		replacement.QuoteID = fmt.Sprintf("mgq_live_race_refreshed_%02d", attempt)
		replacement.ModelConfigVersion = strings.Repeat("7", 64)
		replacement.RetailCNY = "2.50"
		start := make(chan struct{})
		type renewalResult struct {
			quote *MediaGenerationQuote
			err   error
		}
		renewals, binds := make(chan renewalResult, 1), make(chan error, 1)
		originalTaskID := fmt.Sprintf("task_live_original_%02d", attempt)
		go func() {
			<-start
			quote, err := store.RenewUnboundQuote(t.Context(), binding, original.ExpiresAt, &replacement)
			renewals <- renewalResult{quote, err}
		}()
		go func() {
			<-start
			binds <- store.DB.Transaction(func(tx *gorm.DB) error {
				return store.BindQuoteToTaskTx(t.Context(), tx, binding, int64(600+attempt), originalTaskID)
			})
		}()
		close(start)
		renewed, bindErr := <-renewals, <-binds
		if bindErr == nil {
			require.ErrorIs(t, renewed.err, ErrMediaGenerationQuoteReplayed)
			var replay *MediaGenerationQuoteReplayError
			require.ErrorAs(t, renewed.err, &replay)
			assert.Equal(t, originalTaskID, replay.TaskID)
		} else {
			require.NoError(t, renewed.err)
			require.ErrorIs(t, bindErr, ErrMediaGenerationQuoteConflict, "old quote loses to an explicit refresh without binding a task")
			require.Zero(t, renewed.quote.TaskDBID)
			require.NoError(t, store.DB.Transaction(func(tx *gorm.DB) error {
				return store.BindQuoteToTaskTx(t.Context(), tx, mediaGenerationQuoteTestBinding(renewed.quote), int64(700+attempt), originalTaskID)
			}))
		}
		identity := binding
		identity.QuoteID = ""
		receipt, err := store.FindOperationReceipt(t.Context(), identity)
		require.NoError(t, err)
		assert.Equal(t, originalTaskID, receipt.TaskID)
		replacement.QuoteID += "_again"
		replacement.ModelConfigVersion = strings.Repeat("8", 64)
		_, err = store.RenewUnboundQuote(t.Context(), binding, original.ExpiresAt+1, &replacement)
		require.ErrorIs(t, err, ErrMediaGenerationQuoteReplayed, "accepted operation wins even over a stale original quote ID and expiry")
		var replay *MediaGenerationQuoteReplayError
		require.ErrorAs(t, err, &replay)
		assert.Equal(t, receipt.TaskDBID, replay.TaskDBID)
		assert.Equal(t, receipt.TaskID, replay.TaskID)
		after, err := store.FindOperationReceipt(t.Context(), identity)
		require.NoError(t, err)
		assert.Equal(t, receipt, after, "accepted original task and its price stay immutable")
		var count int64
		require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Where("operation_key = ?", receipt.OperationKey).Count(&count).Error)
		assert.Equal(t, int64(1), count)
	}
}

func TestMediaGenerationQuoteAcquireAdmissionAtomicity(t *testing.T) {
	store := mediaGenerationQuoteTestStore(t)
	ctx := context.Background()
	models := []any{&Task{}, &Channel{}, &Option{}, &MediaPool{}, &MediaAdmission{}, &MediaHistoricalReconciliation{}, &H3Account{}, &H3Admission{}}
	require.NoError(t, store.DB.AutoMigrate(models...))
	t.Cleanup(func() {
		for i := len(models) - 1; i >= 0; i-- {
			require.NoError(t, store.DB.Migrator().DropTable(models[i]))
		}
	})
	previousRegistry := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() { jsplugin.DefaultRegistry = previousRegistry })
	_, err := jsplugin.DefaultRegistry.Register(`export const meta = {
apiVersion:1,key:"quote-fixture",name:"Quote Fixture",version:"1.0.0",author:{name:"Test"},
models:["ycapi-synthetic-video"],fetchMode:"per_task",protocols:["openai_video"]};
export const protocols = {openai_video:{decodeRequest(ctx){return {kind:"submit",model:ctx.model,requestBody:ctx.body.value};},render(){return {};}}};
export function buildSubmitRequest(){return {url:"https://quote.fixture/jobs",method:"POST",body:{}};}
export function parseSubmitResponse(ctx,response){return {taskId:ctx.publicTaskId,taskData:response.body};}
export function buildQueryRequest(){return {url:"https://quote.fixture/jobs/original",method:"GET"};}
export function parseTaskResult(){return {status:"UNKNOWN"};}
export function listArtifacts(){return [];}
export function buildContentRequest(){return {url:"https://quote.fixture/original.mp4",method:"GET"};}`, jsplugin.Options{})
	require.NoError(t, err)
	key, otherKey, base, setting := "synthetic-quote-admission-key", "synthetic-other-account-key", "https://quote.fixture", `{"task_plugin_key":"quote-fixture"}`
	require.NoError(t, store.DB.Create(&Channel{Id: 46, Type: constant.ChannelTypeTaskPlugin, Key: key + "\n" + otherKey, ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2}, BaseURL: &base, Setting: &setting, Status: common.ChannelStatusEnabled, Models: "ycapi-synthetic-video", Group: "product"}).Error)
	input := mediaGenerationQuoteFixture("operation-admission-1")
	quote, err := store.SaveQuote(ctx, &input)
	require.NoError(t, err)
	binding := mediaGenerationQuoteTestBinding(quote)
	quoteContext := WithMediaGenerationQuoteBinding(ctx, binding)
	admissions := MediaAdmissionStore{DB: store.DB}
	newTask := func(id string) *Task {
		return &Task{TaskID: id, UserId: quote.UserID, Group: quote.UsingGroup, ChannelId: quote.ChannelID, Platform: "quote-fixture", Properties: Properties{OriginModelName: quote.Model, UpstreamModelName: quote.Model}}
	}
	wrongAccount := newTask("task_wrong_quoted_account")
	require.ErrorIs(t, admissions.Acquire(quoteContext, wrongAccount, otherKey, MediaVideo), ErrMediaGenerationQuoteConflict, "another enabled key in the same channel cannot claim this quote")
	assert.Zero(t, wrongAccount.ID)
	assert.Empty(t, wrongAccount.PrivateData.MediaAdmissionID)
	for _, model := range []any{&Task{}, &MediaAdmission{}, &MediaPool{}} {
		var count int64
		require.NoError(t, store.DB.Model(model).Count(&count).Error)
		assert.Zero(t, count, "a different quoted account cannot create a task, admission or pool")
	}
	_, err = store.GetUnclaimedQuote(ctx, binding)
	require.NoError(t, err)
	rollback := errors.New("synthetic admission persistence failure")
	require.NoError(t, store.DB.Callback().Create().Before("gorm:create").Register("quote_admission_rollback", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "MediaAdmission" {
			tx.AddError(rollback)
		}
	}))
	failed := newTask("task_admission_rolled_back")
	require.ErrorIs(t, admissions.Acquire(quoteContext, failed, key, MediaVideo), rollback)
	require.NoError(t, store.DB.Callback().Create().Remove("quote_admission_rollback"))
	assert.Zero(t, failed.ID)
	assert.Empty(t, failed.PrivateData.MediaAdmissionID)
	for _, model := range []any{&Task{}, &MediaAdmission{}, &MediaPool{}} {
		var count int64
		require.NoError(t, store.DB.Model(model).Count(&count).Error)
		assert.Zero(t, count, "failed admission rolls back task, quote binding and pool")
	}
	_, err = store.GetUnclaimedQuote(ctx, binding)
	require.NoError(t, err)

	start := make(chan struct{})
	type admissionResult struct {
		task *Task
		err  error
	}
	results := make(chan admissionResult, 2)
	for _, id := range []string{"task_admission_a", "task_admission_b"} {
		go func() {
			<-start
			task := newTask(id)
			results <- admissionResult{task, admissions.Acquire(quoteContext, task, key, MediaVideo)}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil {
		a, b = b, a
	}
	require.NoError(t, a.err)
	require.ErrorIs(t, b.err, ErrMediaGenerationQuoteReplayed)
	assert.Zero(t, b.task.ID)
	assert.Empty(t, b.task.PrivateData.MediaAdmissionID)
	for _, model := range []any{&Task{}, &MediaAdmission{}, &MediaPool{}} {
		var count int64
		require.NoError(t, store.DB.Model(model).Count(&count).Error)
		assert.Equal(t, int64(1), count)
	}
	var pool MediaPool
	require.NoError(t, store.DB.First(&pool).Error)
	assert.Equal(t, 1, pool.Occupied)
	require.NoError(t, store.DB.Model(&pool).Update("known_limit", 1).Error)
	err = admissions.Acquire(quoteContext, newTask("task_replay_when_pool_full"), key, MediaVideo)
	require.ErrorIs(t, err, ErrMediaGenerationQuoteReplayed, "capacity must not hide an already accepted receipt")
	receipt, err := store.FindOperationReceipt(ctx, binding)
	require.NoError(t, err)
	assert.Equal(t, a.task.ID, receipt.TaskDBID)
	assert.Equal(t, a.task.TaskID, receipt.TaskID)
	require.NoError(t, store.DB.Model(&MediaGenerationQuote{}).Where("quote_id = ?", quote.QuoteID).Update("expires_at", time.Now().Unix()-1).Error)
	_, err = store.FindOperationReceipt(ctx, binding)
	require.NoError(t, err, "accepted operation survives expiry")
	// The optional context changes no legacy unquoted acquisition behavior.
	require.NoError(t, store.DB.Model(&pool).Update("known_limit", 0).Error)
	require.NoError(t, admissions.Acquire(ctx, newTask("task_legacy_without_quote"), key, MediaVideo))
}
