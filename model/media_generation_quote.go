package model

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrMediaGenerationQuoteInvalid     = errors.New("invalid media generation quote")
	ErrMediaGenerationQuoteConflict    = errors.New("media generation operation conflicts with its original request")
	ErrMediaGenerationQuoteExpired     = errors.New("media generation quote expired")
	ErrMediaGenerationQuoteReplayed    = errors.New("media generation operation already has a task")
	ErrMediaGenerationQuoteUnavailable = errors.New("media generation quote storage is unavailable")
	mediaGenerationQuoteDigest         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	mediaGenerationQuoteOperation      = regexp.MustCompile(`^[A-Za-z0-9_.:-]{16,128}$`)
	mediaGenerationQuoteIdentifier     = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	mediaGenerationQuoteMoney          = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})(\.[0-9]{1,24})?$`)
)

// MediaGenerationQuote freezes one priced intent and its original task binding.
// It owns no wallet balance, reservation or provider-send state. All fields are
// private; callers must explicitly project the public quote response.
type MediaGenerationQuote struct {
	QuoteID            string `gorm:"primaryKey;type:varchar(64)" json:"-"`
	OperationKey       string `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	UserID             int    `gorm:"index" json:"-"`
	TokenID            int    `json:"-"`
	OwnerDigest        string `gorm:"type:varchar(64)" json:"-"`
	ClientOpID         string `gorm:"type:varchar(128)" json:"-"`
	RequestSHA256      string `gorm:"type:varchar(64)" json:"-"`
	MaterialSHA256     string `gorm:"type:varchar(64)" json:"-"`
	Model              string `gorm:"type:varchar(191)" json:"-"`
	Kind               string `gorm:"type:varchar(16)" json:"-"`
	ChannelID          int    `json:"-"`
	UsingGroup         string `gorm:"type:varchar(64)" json:"-"`
	CredentialDigest   string `gorm:"type:varchar(64)" json:"-"`
	ChannelConfigHash  string `gorm:"type:varchar(64)" json:"-"`
	ModelConfigVersion string `gorm:"type:varchar(64)" json:"-"`
	PricingVersion     string `gorm:"type:varchar(64)" json:"-"`
	PluginKey          string `gorm:"type:varchar(64)" json:"-"`
	PluginVersion      string `gorm:"type:varchar(64)" json:"-"`
	PluginSourceHash   string `gorm:"type:varchar(64)" json:"-"`
	SupplierLane       string `gorm:"type:varchar(512)" json:"-"`
	NativeParamsJSON   string `gorm:"type:text" json:"-"`
	UsageFactsJSON     string `gorm:"type:text" json:"-"`
	CostCNY            string `gorm:"type:varchar(64)" json:"-"`
	RetailCNY          string `gorm:"type:varchar(64)" json:"-"`
	ReserveQuota       int64  `json:"-"`
	ExpiresAt          int64  `gorm:"index" json:"-"`
	TaskDBID           int64  `gorm:"index" json:"-"`
	TaskID             string `gorm:"type:varchar(191);index" json:"-"`
	BoundAt            int64  `json:"-"`
	CreatedAt          int64  `json:"-"`
	UpdatedAt          int64  `json:"-"`
}

// Binding is supplied only after authentication and request/material hashing.
// OperationKey may be omitted; a supplied value must match the host HMAC.
type MediaGenerationQuoteBinding struct {
	QuoteID        string
	OperationKey   string
	UserID         int
	TokenID        int
	OwnerDigest    string
	ClientOpID     string
	RequestSHA256  string
	MaterialSHA256 string
}

type MediaGenerationQuoteReplayError struct {
	QuoteID  string
	TaskDBID int64
	TaskID   string
}

func (err *MediaGenerationQuoteReplayError) Error() string {
	return ErrMediaGenerationQuoteReplayed.Error()
}

func (err *MediaGenerationQuoteReplayError) Unwrap() error {
	return ErrMediaGenerationQuoteReplayed
}

type MediaGenerationQuoteStore struct{ DB *gorm.DB }

type mediaGenerationQuoteContextKey struct{}

// Only the host may attach a binding after quote/owner/request verification.
// It is not populated from plugin JSON or an unverified HTTP header.
func WithMediaGenerationQuoteBinding(ctx context.Context, binding MediaGenerationQuoteBinding) context.Context {
	return context.WithValue(ctx, mediaGenerationQuoteContextKey{}, binding)
}

func MediaGenerationQuoteBindingFromContext(ctx context.Context) (MediaGenerationQuoteBinding, bool) {
	if ctx == nil {
		return MediaGenerationQuoteBinding{}, false
	}
	binding, ok := ctx.Value(mediaGenerationQuoteContextKey{}).(MediaGenerationQuoteBinding)
	return binding, ok
}

// MigrateMediaGenerationQuote is deliberately not wired into global migration.
func MigrateMediaGenerationQuote(db *gorm.DB) error {
	if db == nil {
		return ErrMediaGenerationQuoteUnavailable
	}
	return db.AutoMigrate(&MediaGenerationQuote{})
}

func MediaGenerationOperationKey(binding MediaGenerationQuoteBinding) (string, error) {
	if binding.UserID <= 0 || binding.TokenID <= 0 || !mediaGenerationQuoteDigest.MatchString(binding.OwnerDigest) || !mediaGenerationQuoteOperation.MatchString(binding.ClientOpID) {
		return "", ErrMediaGenerationQuoteInvalid
	}
	encoded, err := common.Marshal(struct {
		UserID      int
		TokenID     int
		OwnerDigest string
		ClientOpID  string
	}{binding.UserID, binding.TokenID, binding.OwnerDigest, binding.ClientOpID})
	if err != nil {
		return "", ErrMediaGenerationQuoteInvalid
	}
	return common.GenerateHMAC("media-generation-operation-v2:" + string(encoded)), nil
}

func mediaGenerationQuoteBinding(binding MediaGenerationQuoteBinding, requireQuote bool) (MediaGenerationQuoteBinding, error) {
	key, err := MediaGenerationOperationKey(binding)
	if err != nil || binding.OperationKey != "" && binding.OperationKey != key ||
		!mediaGenerationQuoteDigest.MatchString(binding.RequestSHA256) || !mediaGenerationQuoteDigest.MatchString(binding.MaterialSHA256) ||
		(requireQuote || binding.QuoteID != "") && !mediaGenerationQuoteIdentifier.MatchString(binding.QuoteID) {
		return binding, ErrMediaGenerationQuoteInvalid
	}
	binding.OperationKey = key
	return binding, nil
}

func mediaGenerationQuoteMatches(quote *MediaGenerationQuote, binding MediaGenerationQuoteBinding) error {
	if quote.UserID != binding.UserID || quote.TokenID != binding.TokenID || quote.OwnerDigest != binding.OwnerDigest || quote.ClientOpID != binding.ClientOpID || quote.OperationKey != binding.OperationKey {
		return gorm.ErrRecordNotFound
	}
	if quote.RequestSHA256 != binding.RequestSHA256 || quote.MaterialSHA256 != binding.MaterialSHA256 || binding.QuoteID != "" && quote.QuoteID != binding.QuoteID {
		return ErrMediaGenerationQuoteConflict
	}
	if (quote.TaskDBID == 0) != (quote.TaskID == "") || quote.TaskDBID < 0 {
		return ErrMediaGenerationQuoteUnavailable
	}
	return nil
}

// SaveQuote is an immutable insert. The same operation and request returns its
// original quote, even when a newer proposed price/expiry differs. It never
// refreshes an expired quote or replaces an accepted operation.
func (store MediaGenerationQuoteStore) SaveQuote(ctx context.Context, quote *MediaGenerationQuote) (*MediaGenerationQuote, error) {
	if store.DB == nil {
		return nil, ErrMediaGenerationQuoteUnavailable
	}
	if quote == nil {
		return nil, ErrMediaGenerationQuoteInvalid
	}
	binding, err := mediaGenerationQuoteBinding(MediaGenerationQuoteBinding{OperationKey: quote.OperationKey, UserID: quote.UserID, TokenID: quote.TokenID, OwnerDigest: quote.OwnerDigest, ClientOpID: quote.ClientOpID, RequestSHA256: quote.RequestSHA256, MaterialSHA256: quote.MaterialSHA256}, false)
	if err != nil {
		return nil, err
	}
	var stored MediaGenerationQuote
	if err := store.DB.WithContext(ctx).Where("operation_key = ?", binding.OperationKey).Take(&stored).Error; err == nil {
		if err := mediaGenerationQuoteMatches(&stored, binding); err != nil {
			return nil, err
		}
		return &stored, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	candidate := *quote
	candidate.OperationKey = binding.OperationKey
	if candidate.QuoteID == "" {
		candidate.QuoteID = "mgq_" + uuid.NewString()
	}
	if err := validateMediaGenerationQuote(candidate); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if candidate.ExpiresAt <= now {
		return nil, ErrMediaGenerationQuoteExpired
	}
	candidate.CreatedAt, candidate.UpdatedAt = now, now
	// The unique operation key serializes concurrent first inserts on all three
	// databases. DoNothing also avoids aborting a PostgreSQL transaction.
	if err := store.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
		return nil, err
	}
	if err := store.DB.WithContext(ctx).Where("operation_key = ?", binding.OperationKey).Take(&stored).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMediaGenerationQuoteConflict // quote ID belongs to another operation
	} else if err != nil {
		return nil, err
	}
	if err := mediaGenerationQuoteMatches(&stored, binding); err != nil {
		return nil, err
	}
	return &stored, nil
}

// RenewUnboundQuote explicitly replaces a never-accepted quote after expiry or
// a changed model/channel/credential fingerprint. Request/material identity
// cannot change. The original quote ID, expiry and fingerprints form the CAS;
// accepted receipts take precedence over a stale quote ID or expiry.
func (store MediaGenerationQuoteStore) RenewUnboundQuote(ctx context.Context, binding MediaGenerationQuoteBinding, expectedExpiresAt int64, replacement *MediaGenerationQuote) (*MediaGenerationQuote, error) {
	if store.DB == nil {
		return nil, ErrMediaGenerationQuoteUnavailable
	}
	binding, err := mediaGenerationQuoteBinding(binding, true)
	if err != nil || replacement == nil {
		return nil, ErrMediaGenerationQuoteInvalid
	}
	var renewed MediaGenerationQuote
	err = store.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Take the SQLite writer lock before reading; on MySQL/PG this also
		// serializes a concurrent admission bind against this exact operation.
		if err := tx.Model(&MediaGenerationQuote{}).Where("operation_key = ?", binding.OperationKey).
			UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		var original MediaGenerationQuote
		if err := lockForUpdate(tx).Where("operation_key = ?", binding.OperationKey).Take(&original).Error; err != nil {
			return err
		}
		receiptBinding := binding
		receiptBinding.QuoteID = ""
		if err := mediaGenerationQuoteMatches(&original, receiptBinding); err != nil {
			return err
		}
		if original.TaskDBID != 0 {
			return &MediaGenerationQuoteReplayError{original.QuoteID, original.TaskDBID, original.TaskID}
		}
		if err := mediaGenerationQuoteMatches(&original, binding); err != nil {
			return err
		}
		now := time.Now().Unix()
		if original.ExpiresAt != expectedExpiresAt {
			return ErrMediaGenerationQuoteConflict
		}
		candidate := *replacement
		candidate.OperationKey = binding.OperationKey
		configurationChanged := candidate.ModelConfigVersion != original.ModelConfigVersion || candidate.ChannelConfigHash != original.ChannelConfigHash || candidate.CredentialDigest != original.CredentialDigest
		if original.ExpiresAt > now && !configurationChanged {
			return ErrMediaGenerationQuoteConflict
		}
		if candidate.UserID != original.UserID || candidate.TokenID != original.TokenID || candidate.OwnerDigest != original.OwnerDigest ||
			candidate.ClientOpID != original.ClientOpID || candidate.RequestSHA256 != original.RequestSHA256 || candidate.MaterialSHA256 != original.MaterialSHA256 ||
			candidate.Model != original.Model || candidate.Kind != original.Kind || candidate.UsingGroup != original.UsingGroup || candidate.QuoteID == original.QuoteID {
			return ErrMediaGenerationQuoteConflict
		}
		if err := validateMediaGenerationQuote(candidate); err != nil {
			return err
		}
		if candidate.ExpiresAt <= now {
			return ErrMediaGenerationQuoteExpired
		}
		candidate.CreatedAt, candidate.UpdatedAt = now, now
		result := tx.Model(&MediaGenerationQuote{}).
			Where("quote_id = ? AND operation_key = ? AND task_db_id = 0 AND task_id = '' AND expires_at = ?", original.QuoteID, binding.OperationKey, expectedExpiresAt).
			Where("model_config_version = ? AND channel_config_hash = ? AND credential_digest = ?", original.ModelConfigVersion, original.ChannelConfigHash, original.CredentialDigest).
			Select("*").Updates(&candidate)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrMediaGenerationQuoteConflict
		}
		renewed = candidate
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &renewed, nil
}

func validateMediaGenerationQuote(quote MediaGenerationQuote) error {
	if !mediaGenerationQuoteIdentifier.MatchString(quote.QuoteID) || !validClientModelID(quote.Model) || len(quote.Model) > 191 ||
		quote.Kind != "image" && quote.Kind != "video" && quote.Kind != "audio" || quote.ChannelID <= 0 ||
		quote.UsingGroup == "" || len(quote.UsingGroup) > 64 || quote.UsingGroup != strings.TrimSpace(quote.UsingGroup) || strings.ContainsFunc(quote.UsingGroup, unicode.IsControl) ||
		!mediaGenerationQuoteDigest.MatchString(quote.CredentialDigest) || !mediaGenerationQuoteDigest.MatchString(quote.ChannelConfigHash) ||
		quote.ModelConfigVersion != "" && !mediaGenerationQuoteDigest.MatchString(quote.ModelConfigVersion) ||
		!mediaGenerationQuoteDigest.MatchString(quote.PricingVersion) || !mediaGenerationQuoteDigest.MatchString(quote.PluginSourceHash) ||
		quote.PluginKey == "" || len(quote.PluginKey) > 64 || strings.ContainsFunc(quote.PluginKey, unicode.IsControl) ||
		quote.PluginVersion == "" || len(quote.PluginVersion) > 64 || strings.ContainsFunc(quote.PluginVersion, unicode.IsControl) ||
		quote.SupplierLane == "" || len(quote.SupplierLane) > 512 || quote.SupplierLane != strings.TrimSpace(quote.SupplierLane) || strings.ContainsFunc(quote.SupplierLane, unicode.IsControl) ||
		!mediaGenerationQuoteMoney.MatchString(quote.CostCNY) || !mediaGenerationQuoteMoney.MatchString(quote.RetailCNY) ||
		quote.ReserveQuota < 0 || quote.ReserveQuota > common.MaxQuota || quote.TaskDBID != 0 || quote.TaskID != "" || quote.BoundAt != 0 {
		return ErrMediaGenerationQuoteInvalid
	}
	for _, raw := range []string{quote.NativeParamsJSON, quote.UsageFactsJSON} {
		var value map[string]common.RawMessage
		if len(raw) == 0 || len(raw) > 1<<20 || common.UnmarshalJsonStr(raw, &value) != nil || value == nil {
			return ErrMediaGenerationQuoteInvalid
		}
	}
	return nil
}

func (store MediaGenerationQuoteStore) FindOperationReceipt(ctx context.Context, binding MediaGenerationQuoteBinding) (*MediaGenerationQuote, error) {
	if store.DB == nil {
		return nil, ErrMediaGenerationQuoteUnavailable
	}
	binding, err := mediaGenerationQuoteBinding(binding, false)
	if err != nil {
		return nil, err
	}
	var quote MediaGenerationQuote
	if err := store.DB.WithContext(ctx).Where("operation_key = ?", binding.OperationKey).Take(&quote).Error; err != nil {
		return nil, err
	}
	if err := mediaGenerationQuoteMatches(&quote, binding); err != nil {
		return nil, err
	}
	if quote.TaskDBID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &quote, nil // accepted receipts survive quote expiry
}

func (store MediaGenerationQuoteStore) GetUnclaimedQuote(ctx context.Context, binding MediaGenerationQuoteBinding) (*MediaGenerationQuote, error) {
	if store.DB == nil {
		return nil, ErrMediaGenerationQuoteUnavailable
	}
	binding, err := mediaGenerationQuoteBinding(binding, true)
	if err != nil {
		return nil, err
	}
	var quote MediaGenerationQuote
	if err := store.DB.WithContext(ctx).Where("operation_key = ?", binding.OperationKey).Take(&quote).Error; err != nil {
		return nil, err
	}
	if err := mediaGenerationQuoteMatches(&quote, binding); err != nil {
		return nil, err
	}
	if quote.TaskDBID != 0 {
		return nil, &MediaGenerationQuoteReplayError{quote.QuoteID, quote.TaskDBID, quote.TaskID}
	}
	if quote.ExpiresAt <= time.Now().Unix() {
		return nil, ErrMediaGenerationQuoteExpired
	}
	return &quote, nil
}

// BindQuoteToTaskTx must share the caller's existing Task/MediaAdmission
// transaction. Only a successful commit authorizes continuing to reservation;
// a replay error always means return the original task without another send.
func (store MediaGenerationQuoteStore) BindQuoteToTaskTx(ctx context.Context, tx *gorm.DB, binding MediaGenerationQuoteBinding, taskDBID int64, taskID string) error {
	if tx == nil {
		return ErrMediaGenerationQuoteUnavailable
	}
	binding, err := mediaGenerationQuoteBinding(binding, true)
	if err != nil || taskDBID <= 0 || taskID == "" || len(taskID) > 191 || taskID != strings.TrimSpace(taskID) || strings.ContainsFunc(taskID, unicode.IsControl) {
		return ErrMediaGenerationQuoteInvalid
	}
	now := time.Now().Unix()
	// Write before reading: this obtains the SQLite writer lock without a
	// read-to-write upgrade race. UPDATE also takes the row lock on MySQL/PG.
	result := tx.WithContext(ctx).Model(&MediaGenerationQuote{}).
		Where("quote_id = ? AND operation_key = ? AND user_id = ? AND token_id = ? AND owner_digest = ? AND client_op_id = ? AND request_sha256 = ? AND material_sha256 = ? AND task_db_id = 0 AND task_id = '' AND expires_at > ?", binding.QuoteID, binding.OperationKey, binding.UserID, binding.TokenID, binding.OwnerDigest, binding.ClientOpID, binding.RequestSHA256, binding.MaterialSHA256, now).
		Updates(map[string]any{"task_db_id": taskDBID, "task_id": taskID, "bound_at": now, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var quote MediaGenerationQuote
	if err := lockForUpdate(tx.WithContext(ctx)).Where("operation_key = ?", binding.OperationKey).Take(&quote).Error; err != nil {
		return err
	}
	if err := mediaGenerationQuoteMatches(&quote, binding); err != nil {
		return err
	}
	if quote.TaskDBID != 0 {
		return &MediaGenerationQuoteReplayError{quote.QuoteID, quote.TaskDBID, quote.TaskID}
	}
	if quote.ExpiresAt <= now {
		return ErrMediaGenerationQuoteExpired
	}
	return ErrMediaGenerationQuoteUnavailable
}
