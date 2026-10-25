package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const MediaHistoricalRefund = "refund_original_reservation"
const MediaHistoricalCapacityOnly = "release_capacity_funds_pending"
const MediaHistoricalLegacyGiftRefund = "refund_verified_legacy_gift"

var ErrMediaHistoricalEvidence = errors.New("historical media reconciliation evidence is unconfirmed or changed")

// MediaHistoricalReconciliation is the immutable main-database transaction
// receipt. It is deliberately separate from live HTTP create-rejection proof
// and optional usage/audit log databases.
type MediaHistoricalReconciliation struct {
	ID             int64  `gorm:"primaryKey"`
	TaskDBID       int64  `gorm:"uniqueIndex;not null"`
	TaskID         string `gorm:"type:varchar(191);not null"`
	UserID         int    `gorm:"not null"`
	OperationID    string `gorm:"type:varchar(96);not null"`
	ManifestSHA256 string `gorm:"type:varchar(64);not null"`
	EntrySHA256    string `gorm:"type:varchar(64);not null"`
	Action         string `gorm:"type:varchar(40);not null"`
	OriginalQuota  int    `gorm:"not null"`
	FundingEventID int64  `gorm:"not null"`
	BeforeHashes   string `gorm:"type:text;not null"`
	AfterHashes    string `gorm:"type:text;not null"`
	Evidence       string `gorm:"type:text;not null"`
	CreatedAt      int64  `gorm:"not null"`
	ReceiptSHA256  string `gorm:"type:varchar(64);not null"`
}

type MediaHistoricalFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type MediaHistoricalHostTrace struct {
	OriginalPath   string              `json:"original_path"`
	File           MediaHistoricalFile `json:"file"`
	PreconsumeLine string              `json:"preconsume_line"`
	RejectionLine  string              `json:"rejection_line"`
	ResponseLine   string              `json:"response_line"`
}

// SourceEvidence binds the reviewed original release, not today's plugin or
// channel configuration. Source files are checked against reviewed originals.
type MediaHistoricalSourceEvidence struct {
	RuntimeProvenance MediaHistoricalFile `json:"runtime_provenance"`
	Revision          string              `json:"revision"`
	ReleaseReceipt    MediaHistoricalFile `json:"release_receipt"`
	FundingSource     MediaHistoricalFile `json:"funding_source"`
	BillingSession    MediaHistoricalFile `json:"billing_session"`
	RelayController   MediaHistoricalFile `json:"relay_controller"`
	RelayTask         MediaHistoricalFile `json:"relay_task"`
	Plugin            MediaHistoricalFile `json:"plugin"`
}

type MediaHistoricalEntry struct {
	Identity   MediaHistoricalIdentity         `json:"identity"`
	LegacyGift *MediaHistoricalLegacyGiftProof `json:"legacy_gift,omitempty"`
	TaskDBID   int64                           `json:"task_db_id"`
	Action     string                          `json:"action"`
	Hashes     map[string]string               `json:"hashes"`
	Host       MediaHistoricalHostTrace        `json:"host"`
	Source     MediaHistoricalSourceEvidence   `json:"source"`
}

type MediaHistoricalManifest struct {
	SchemaVersion int                    `json:"schema_version"`
	OperationID   string                 `json:"operation_id"`
	Entries       []MediaHistoricalEntry `json:"entries"`
}

type MediaHistoricalResult struct {
	Status                   string  `json:"status"`
	ManifestSHA256           string  `json:"manifest_sha256"`
	Tasks                    []int64 `json:"tasks"`
	RefundedQuota            int     `json:"refunded_quota"`
	AlreadyApplied           bool    `json:"already_applied"`
	CacheInvalidationPending bool    `json:"cache_invalidation_pending"`
}

type MediaHistoricalIdentity struct {
	CreatedAt        int64  `json:"created_at"`
	ChannelID        int    `json:"channel_id"`
	PluginGeneration uint64 `json:"plugin_generation"`
	TaskID           string `json:"task_id"`
	RequestID        string `json:"request_id"`
	Model            string `json:"model"`
	UserID           int    `json:"user_id"`
	TokenID          int    `json:"token_id"`
	Quota            int    `json:"quota"`
	LogID            int    `json:"log_id"`
	FundingID        int64  `json:"funding_id"`
	EventID          int64  `json:"event_id"`
	AllocationID     int64  `json:"allocation_id"`
	LotID            int64  `json:"lot_id"`
}

type MediaHistoricalLegacyGiftProof struct {
	GiftLogID     int `json:"gift_log_id"`
	GrantQuota    int `json:"grant_quota"`
	ConsumedQuota int `json:"consumed_quota"`
	ConsumeCount  int `json:"consume_count"`
}

func loadMediaHistoricalIdentity(tx *gorm.DB, id int64) (MediaHistoricalIdentity, error) {
	var task Task
	if err := tx.First(&task, id).Error; err != nil {
		return MediaHistoricalIdentity{}, err
	}
	if task.PrivateData.Execution == nil || task.PrivateData.Execution.TaskPlugin == nil {
		return MediaHistoricalIdentity{}, ErrMediaHistoricalEvidence
	}
	identity := MediaHistoricalIdentity{CreatedAt: task.CreatedAt, ChannelID: task.ChannelId, PluginGeneration: task.PrivateData.Execution.TaskPlugin.Generation, TaskID: task.TaskID, RequestID: task.PrivateData.Execution.RequestID, Model: task.Properties.UpstreamModelName, UserID: task.UserId, TokenID: task.PrivateData.TokenId, Quota: task.Quota}
	var log Log
	if err := tx.Where("request_id = ? AND type = ?", identity.RequestID, LogTypeError).First(&log).Error; err != nil {
		return identity, err
	}
	identity.LogID = log.Id
	var requests []WalletFundingRequest
	if err := tx.Where("user_id = ? AND request_id = ? AND purpose = ?", identity.UserID, identity.RequestID, "relay").Find(&requests).Error; err != nil {
		return identity, err
	}
	if len(requests) > 1 {
		return identity, ErrMediaHistoricalEvidence
	}
	if len(requests) == 1 {
		identity.FundingID = requests[0].ID
		identity.EventID = requests[0].LastEventID
		var allocations []WalletFundingAllocation
		if err := tx.Where("funding_request_id = ?", identity.FundingID).Find(&allocations).Error; err != nil {
			return identity, err
		}
		if len(allocations) == 1 {
			identity.AllocationID = allocations[0].ID
			identity.LotID = allocations[0].LotID
		}
	}
	return identity, nil
}

func mediaHistoricalSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CanonicalMediaHistoricalManifest prepares a typed canonical representation.
// Reconcile requires the supplied bytes to match it exactly, rejecting unknown,
// duplicate and omitted members after the operator freezes the explicit SHA256.
func CanonicalMediaHistoricalManifest(data []byte) (MediaHistoricalManifest, []byte, error) {
	var manifest MediaHistoricalManifest
	if len(data) > 1<<20 || common.Unmarshal(data, &manifest) != nil {
		return manifest, nil, ErrMediaHistoricalEvidence
	}
	encoded, err := common.Marshal(manifest)
	if err != nil {
		return manifest, nil, err
	}
	return manifest, encoded, nil
}

// historicalRowsHash reads every physical column, including columns unknown to
// today's struct. NULL, byte/text, numbers and booleans retain distinct tags.
func historicalRowsHash(query *gorm.DB) (string, error) {
	rows, err := query.Rows()
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	all := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			return "", err
		}
		row := make(map[string]any, len(columns))
		for i, value := range values {
			kind := "null"
			var encoded any
			switch v := value.(type) {
			case nil:
			case []byte:
				kind = "bytes"
				encoded = hex.EncodeToString(v)
			case string:
				kind = "text"
				encoded = v
			case int64:
				kind = "integer"
				encoded = strconv.FormatInt(v, 10)
			case float64:
				kind = "float"
				encoded = strconv.FormatFloat(v, 'g', -1, 64)
			case bool:
				kind = "boolean"
				encoded = v
			case time.Time:
				kind = "time"
				encoded = v.UTC().Format(time.RFC3339Nano)
			default:
				return "", fmt.Errorf("unsupported historical SQL type %T", value)
			}
			row[columns[i]] = []any{kind, encoded}
		}
		if len(all) >= 100000 {
			return "", ErrMediaHistoricalEvidence
		}
		all = append(all, row)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	encoded, err := common.Marshal(all)
	if err != nil {
		return "", err
	}
	return mediaHistoricalSHA(encoded), nil
}

func mediaHistoricalHashes(tx *gorm.DB, id int64) (map[string]string, error) {
	identity, err := loadMediaHistoricalIdentity(tx, id)
	if err != nil {
		return nil, err
	}
	queries := map[string]*gorm.DB{
		"task":         tx.Model(&Task{}).Select("*").Where("id = ?", id),
		"admission":    tx.Model(&MediaAdmission{}).Select("*").Where("task_db_id = ?", id),
		"pool":         tx.Model(&MediaPool{}).Select("*").Where("id IN (?)", tx.Model(&MediaAdmission{}).Select("pool_id").Where("task_db_id = ?", id)),
		"channel":      tx.Model(&Channel{}).Select("*").Where("id = ?", identity.ChannelID),
		"user":         tx.Model(&User{}).Select("*").Where("id = ?", identity.UserID),
		"token":        tx.Unscoped().Model(&Token{}).Select("*").Where("id = ?", identity.TokenID),
		"lots":         tx.Model(&WalletFundingLot{}).Select("*").Where("user_id = ?", identity.UserID).Order("id"),
		"requests":     tx.Model(&WalletFundingRequest{}).Select("*").Where("user_id = ? AND (request_id = ? OR request_id = ?)", identity.UserID, identity.RequestID, fmt.Sprintf("legacy-task:%d", id)).Order("id"),
		"events":       tx.Model(&WalletFundingEvent{}).Select("*").Where("user_id = ? AND request_id IN ?", identity.UserID, []string{identity.RequestID, fmt.Sprintf("legacy-task:%d", id)}).Order("id"),
		"allocations":  tx.Model(&WalletFundingAllocation{}).Select("*").Where("funding_request_id IN (?)", tx.Model(&WalletFundingRequest{}).Select("id").Where("user_id = ? AND request_id IN ?", identity.UserID, []string{identity.RequestID, fmt.Sprintf("legacy-task:%d", id)})).Order("id"),
		"logs":         tx.Model(&Log{}).Select("*").Where("request_id = ? OR id = ?", identity.RequestID, identity.LogID).Order("id"),
		"profit":       tx.Model(&ModelProfitBillingRecord{}).Select("*").Where("request_id = ? OR (funding_request_id = ? AND funding_request_id > 0)", identity.RequestID, identity.FundingID).Order("id"),
		"subscription": tx.Model(&SubscriptionPreConsumeRecord{}).Select("*").Where("request_id = ?", identity.RequestID).Order("id"),
	}
	if identity.FundingID == 0 {
		queries["legacy_user_logs"] = tx.Model(&Log{}).Select("*").Where("user_id = ?", identity.UserID).Order("id")
		queries["legacy_user_tasks"] = tx.Model(&Task{}).Select("*").Where("user_id = ?", identity.UserID).Order("id")
		queries["legacy_user_requests"] = tx.Model(&WalletFundingRequest{}).Select("*").Where("user_id = ?", identity.UserID).Order("id")
		queries["legacy_user_events"] = tx.Model(&WalletFundingEvent{}).Select("*").Where("user_id = ?", identity.UserID).Order("id")
		queries["legacy_topups"] = tx.Model(&TopUp{}).Select("*").Where("user_id = ?", identity.UserID).Order("id")
		queries["legacy_redemptions"] = tx.Unscoped().Model(&Redemption{}).Select("*").Where("used_user_id = ?", identity.UserID).Order("id")
		queries["legacy_checkins"] = tx.Model(&Checkin{}).Select("*").Where("user_id = ?", identity.UserID).Order("id")
	}
	queries["pool_nonreleased"] = tx.Model(&MediaAdmission{}).Select("*").Where("pool_id IN (?) AND state <> ?", tx.Model(&MediaAdmission{}).Select("pool_id").Where("task_db_id = ?", id), MediaReleased).Order("id")
	queries["token_logs"] = tx.Model(&Log{}).Select("*").Where("token_id = ?", identity.TokenID).Order("id")
	queries["user_logs"] = tx.Model(&Log{}).Select("*").Where("user_id = ?", identity.UserID).Order("id")
	// The original request log must share the locked main database. Supporting a
	// separate mutable LOG_DB would require an independently frozen log source.
	if LOG_DB != DB {
		return nil, errors.New("historical reconciliation requires the original logs in the main database")
	}
	hashes := make(map[string]string, len(queries))
	for name, query := range queries {
		hash, err := historicalRowsHash(query)
		if err != nil {
			return nil, err
		}
		hashes[name] = hash
	}
	return hashes, nil
}

// InspectMediaHistoricalReconciliation is read-only: it does not migrate,
// initialize wallets, register pools, refresh providers or acquire admissions.
func InspectMediaHistoricalReconciliation(ctx context.Context, ids []int64) (MediaHistoricalManifest, error) {
	initCol()
	if len(ids) < 1 || len(ids) > 4 || !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != len(ids) || ids[0] <= 0 {
		return MediaHistoricalManifest{}, ErrMediaHistoricalEvidence
	}
	manifest := MediaHistoricalManifest{SchemaVersion: 1, Entries: []MediaHistoricalEntry{}}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			hashes, err := mediaHistoricalHashes(tx, id)
			if err != nil {
				return err
			}
			action := MediaHistoricalRefund
			identity, err := loadMediaHistoricalIdentity(tx, id)
			if err != nil {
				return err
			}
			if identity.FundingID == 0 {
				action = MediaHistoricalCapacityOnly
			}
			manifest.Entries = append(manifest.Entries, MediaHistoricalEntry{TaskDBID: id, Identity: identity, Action: action, Hashes: hashes})
		}
		return nil
	})
	return manifest, err
}

func readMediaHistoricalFile(file MediaHistoricalFile, maxBytes int64) ([]byte, error) {
	if len(file.SHA256) != 64 || !strings.HasPrefix(file.Path, "/") {
		return nil, ErrMediaHistoricalEvidence
	}
	info, err := os.Lstat(file.Path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBytes {
		return nil, ErrMediaHistoricalEvidence
	}
	handle, err := os.Open(file.Path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	actual, err := handle.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, ErrMediaHistoricalEvidence
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() || mediaHistoricalSHA(data) != file.SHA256 {
		return nil, ErrMediaHistoricalEvidence
	}
	return data, nil
}

func validateMediaHistoricalSource(entry MediaHistoricalEntry, identity MediaHistoricalIdentity) error {
	source := entry.Source
	revision := "24364344e0a121bff4cbb8c5448926553c040904"
	if entry.Action == MediaHistoricalLegacyGiftRefund || entry.Action == MediaHistoricalCapacityOnly {
		revision = "27992b8e5d9ff59e39de32d902400685e0a7c021"
	}
	if source.Revision != revision {
		return ErrMediaHistoricalEvidence
	}
	release, err := readMediaHistoricalFile(source.ReleaseReceipt, 4<<20)
	if err != nil {
		return err
	}
	if !bytes.Contains(release, []byte(revision)) {
		return ErrMediaHistoricalEvidence
	}
	runtimeData, err := readMediaHistoricalFile(source.RuntimeProvenance, 4<<20)
	if err != nil {
		return err
	}
	var provenance struct {
		Runtimes []struct {
			Revision   string `json:"revision"`
			StartedAt  string `json:"started_at"`
			FinishedAt string `json:"finished_at"`
		} `json:"historical_runtimes"`
		Logs []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"log_files"`
	}
	if common.Unmarshal(runtimeData, &provenance) != nil {
		return ErrMediaHistoricalEvidence
	}
	matchedRuntime, matchedLog := 0, 0
	for _, runtime := range provenance.Runtimes {
		start, startErr := time.Parse(time.RFC3339Nano, runtime.StartedAt)
		finish, finishErr := time.Parse(time.RFC3339Nano, runtime.FinishedAt)
		if startErr != nil || finishErr != nil {
			return ErrMediaHistoricalEvidence
		}
		created := time.Unix(identity.CreatedAt, 0)
		if !created.Before(start) && created.Before(finish) {
			if runtime.Revision != revision {
				return ErrMediaHistoricalEvidence
			}
			matchedRuntime++
		}
	}
	for _, log := range provenance.Logs {
		if log.Path == entry.Host.OriginalPath && log.SHA256 == entry.Host.File.SHA256 {
			matchedLog++
		}
	}
	if matchedRuntime != 1 || matchedLog != 1 {
		return ErrMediaHistoricalEvidence
	}
	plugin, err := readMediaHistoricalFile(source.Plugin, 1<<20)
	if err != nil {
		return err
	}
	if mediaHistoricalSHA(plugin) != BlackrxCreateRejectionSourceHash {
		return ErrMediaHistoricalEvidence
	}
	if entry.Action != MediaHistoricalCapacityOnly {
		files := []struct {
			file     MediaHistoricalFile
			expected string
		}{
			{source.FundingSource, "1b103236299286558ddb3cd729232407862de52461235cc1abafb4d50b88655c"},
			{source.BillingSession, "4f92c7abde81f1e479688c27d293d86a85bdd9115df1da2507d0edb8482aeecf"},
			{source.RelayController, "7227360565e06c5212c4e2c33410766378a592b43b5c0485e2be9aa812e920e0"},
			{source.RelayTask, "c065219446c579d9a484d75bf5a092dadfc6f2f4c6dab4add63c744372219f93"},
		}
		if entry.Action == MediaHistoricalLegacyGiftRefund {
			files[0].expected = "a2f9acf9729f37e5b20523ff820cbc0b8af707c373d1068985f9734e149f3f2a"
			files[1].expected = "6394c9016a66f7d5e500c2fb32fd0dc605b0e43596d89acce7d20f8721770ad3"
			files[2].expected = "f9af64ec34fd16e21da0574c4cf4a844a44c928afd549563e0249cf7aa01da54"
		}
		for _, file := range files {
			data, err := readMediaHistoricalFile(file.file, 4<<20)
			if err != nil {
				return err
			}
			if mediaHistoricalSHA(data) != file.expected {
				return ErrMediaHistoricalEvidence
			}
		}
	}
	data, err := readMediaHistoricalFile(entry.Host.File, 256<<20)
	if err != nil {
		return err
	}
	pre, rejection, response := entry.Host.PreconsumeLine, entry.Host.RejectionLine, entry.Host.ResponseLine
	if strings.ContainsAny(pre+rejection+response, "\r\n") || len(pre) > 8192 || len(rejection) > 8192 || len(response) > 8192 {
		return ErrMediaHistoricalEvidence
	}
	if !strings.Contains(pre, identity.RequestID+" | user "+strconv.Itoa(identity.UserID)+" needs pre-consume ") || !strings.HasSuffix(strings.TrimRight(pre, " \t"), " (funding=wallet)") ||
		!strings.Contains(rejection, identity.RequestID+" | record error log: userId="+strconv.Itoa(identity.UserID)+", channelId="+strconv.Itoa(identity.ChannelID)+", modelName="+identity.Model+",") ||
		!strings.Contains(rejection, "content=status_code=404, ") || !strings.Contains(response, " | "+identity.RequestID+" | 202 |") || !strings.HasSuffix(response, "POST /v1/videos") {
		return ErrMediaHistoricalEvidence
	}
	// Require each complete, unique line and the original ordering, rather than
	// accepting a truncated grep excerpt or a request ID found elsewhere.
	positions := []int{}
	for _, line := range []string{pre, rejection, response} {
		needle := append([]byte(line), '\n')
		if bytes.Count(data, needle) != 1 {
			return ErrMediaHistoricalEvidence
		}
		positions = append(positions, bytes.Index(data, needle))
	}
	if positions[0] >= positions[1] || positions[1] >= positions[2] {
		return ErrMediaHistoricalEvidence
	}
	return nil
}

func validateMediaHistoricalOriginal(tx *gorm.DB, entry MediaHistoricalEntry) (*Task, *MediaAdmission, *MediaPool, error) {
	identity := entry.Identity
	if entry.TaskDBID <= 0 || identity.Quota <= 0 || identity.Quota > common.MaxWalletQuota || identity.RequestID == "" || identity.UserID <= 0 || identity.TokenID <= 0 {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var task Task
	var admission MediaAdmission
	var pool MediaPool
	if err := lockForUpdate(tx).First(&task, entry.TaskDBID).Error; err != nil {
		return nil, nil, nil, err
	}
	if err := lockForUpdate(tx).Where("task_db_id = ?", task.ID).First(&admission).Error; err != nil {
		return nil, nil, nil, err
	}
	if err := lockForUpdate(tx).Where("id = ?", admission.PoolID).First(&pool).Error; err != nil {
		return nil, nil, nil, err
	}
	execution := task.PrivateData.Execution
	if task.CreatedAt != identity.CreatedAt || task.TaskID != identity.TaskID || task.UserId != identity.UserID || task.ChannelId != identity.ChannelID || task.Platform != "blackrxai-video" || task.Quota != identity.Quota || task.Status != TaskStatusUnknown || task.FinishTime != 0 || task.SubmitTime != 0 || task.StartTime != 0 || len(task.Data) > 0 && string(task.Data) != "null" ||
		task.Properties.OriginModelName != identity.Model || task.Properties.UpstreamModelName != identity.Model || task.PrivateData.Key == "" || task.PrivateData.RouteDigest == "" || task.PrivateData.TokenId != identity.TokenID || task.PrivateData.BillingSource != "wallet" || task.PrivateData.SubscriptionId != 0 || task.PrivateData.UpstreamTaskID != "" || task.PrivateData.ResultURL != "" || task.PrivateData.MediaAdmissionID != identity.TaskID || task.PrivateData.MediaKind != MediaVideo ||
		execution == nil || execution.RequestID != identity.RequestID || execution.RequestPath != "/v1/videos" || execution.TaskPlugin == nil || execution.TaskPlugin.Key != "blackrxai-video" || execution.TaskPlugin.Version != "1.0.2" || execution.TaskPlugin.APIVersion != 1 || execution.TaskPlugin.Generation != identity.PluginGeneration || identity.PluginGeneration == 0 || execution.TaskPlugin.SourceHash != "" && execution.TaskPlugin.SourceHash != BlackrxCreateRejectionSourceHash ||
		admission.ID != identity.TaskID || admission.ChannelID != identity.ChannelID || admission.State != MediaUnknown || admission.BillingState != "reserved" || admission.ProviderTerminal || admission.SendingAt <= 0 || admission.SendingAt != task.CreatedAt || admission.CreateRejection != "" || pool.IdentityDigest != mediaIdentityDigest(task.PrivateData.Key, MediaVideo) || pool.MediaKind != MediaVideo || !pool.Reconciled {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var channel Channel
	if err := lockForUpdate(tx).First(&channel, identity.ChannelID).Error; err != nil {
		return nil, nil, nil, err
	}
	if channel.GetBaseURL() != "https://hi.blackrxai.com" || channel.Type != 61 || !mediaChannelHasKey(&channel, task.PrivateData.Key, false) {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var log Log
	if err := tx.Where("id = ?", identity.LogID).First(&log).Error; err != nil {
		return nil, nil, nil, err
	}
	if log.UserId != identity.UserID || log.ChannelId != identity.ChannelID || log.TokenId != identity.TokenID || log.Type != LogTypeError || log.Quota != 0 || log.RequestId != identity.RequestID || log.UpstreamRequestId != "" || log.ModelName != identity.Model || log.CreatedAt < task.CreatedAt || log.CreatedAt > task.CreatedAt+90 || !strings.HasPrefix(log.Content, "status_code=404, ") {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	body := strings.TrimPrefix(log.Content, "status_code=404, ")
	if !IsBlackrxModelMissingCreateBody([]byte(body), identity.Model) || !strings.Contains(entry.Host.RejectionLine, "content="+log.Content) {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var other struct {
		Status int    `json:"status_code"`
		TaskID string `json:"task_id"`
		Path   string `json:"request_path"`
		Admin  struct {
			Policy []struct {
				Attempt  int    `json:"attempt"`
				Channel  int    `json:"channel_id"`
				Status   int    `json:"status"`
				Code     string `json:"error_code"`
				Decision struct {
					Action string `json:"action"`
					Reason string `json:"reason"`
				} `json:"decision"`
			} `json:"request_policy"`
		} `json:"admin_info"`
	}
	if common.UnmarshalJsonStr(log.Other, &other) != nil || other.Status != 404 || other.TaskID != task.TaskID || other.Path != "/v1/videos" || len(other.Admin.Policy) != 3 {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	for i, policy := range other.Admin.Policy {
		if policy.Attempt != 1 || policy.Channel != identity.ChannelID {
			return nil, nil, nil, ErrMediaHistoricalEvidence
		}
		if i > 0 && (policy.Status != 404 || policy.Code != "fail_to_fetch_task") {
			return nil, nil, nil, ErrMediaHistoricalEvidence
		}
	}
	if other.Admin.Policy[0].Decision.Action != "attempt" || other.Admin.Policy[1].Decision.Action != "failure" || other.Admin.Policy[2].Decision.Action != "stop" || other.Admin.Policy[2].Decision.Reason != "media_submission_unknown" {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var count int64
	if err := tx.Model(&Log{}).Where("request_id = ? AND id <> ?", identity.RequestID, identity.LogID).Count(&count).Error; err != nil {
		return nil, nil, nil, err
	}
	if count != 0 {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	if err := tx.Model(&ModelProfitBillingRecord{}).Where("request_id = ? OR (funding_request_id = ? AND funding_request_id > 0)", identity.RequestID, identity.FundingID).Count(&count).Error; err != nil {
		return nil, nil, nil, err
	}
	if count != 0 {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	if err := tx.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", identity.RequestID).Count(&count).Error; err != nil {
		return nil, nil, nil, err
	}
	if count != 0 {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	if entry.Action == MediaHistoricalCapacityOnly {
		if identity.FundingID != 0 || task.PrivateData.FundingRequestId != "" {
			return nil, nil, nil, ErrMediaHistoricalEvidence
		}
		return &task, &admission, &pool, nil
	}
	if entry.Action == MediaHistoricalLegacyGiftRefund {
		if identity.FundingID != 0 || task.PrivateData.FundingRequestId != "" {
			return nil, nil, nil, ErrMediaHistoricalEvidence
		}
		if err := validateMediaHistoricalLegacyGift(tx, entry); err != nil {
			return nil, nil, nil, err
		}
		return &task, &admission, &pool, nil
	}
	if identity.FundingID <= 0 || task.PrivateData.FundingRequestId != identity.RequestID {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var user User
	if err := lockForUpdate(tx).First(&user, identity.UserID).Error; err != nil {
		return nil, nil, nil, err
	}
	if !user.WalletFundingInitialized {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var request WalletFundingRequest
	if err := lockForUpdate(tx).Where("id = ?", identity.FundingID).First(&request).Error; err != nil {
		return nil, nil, nil, err
	}
	if request.UserID != identity.UserID || request.RequestID != identity.RequestID || request.Purpose != "relay" || request.IdentityHash != walletFundingIdentity(identity.RequestID, "relay") || request.Quota != int64(identity.Quota) || request.LastEventID != identity.EventID || request.LegacyBaselineImported || request.ModelName != identity.Model || request.InitialModelName != identity.Model || request.ChannelID != identity.ChannelID {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var events []WalletFundingEvent
	if err := lockForUpdate(tx).Where("funding_request_id = ?", request.ID).Find(&events).Error; err != nil {
		return nil, nil, nil, err
	}
	if len(events) != 1 {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	event := events[0]
	if event.ID != identity.EventID || event.UserID != identity.UserID || event.RequestID != identity.RequestID || event.Purpose != "relay" || event.Operation != "target" || event.DeltaQuota != int64(identity.Quota) || event.TargetQuota != int64(identity.Quota) || event.GiftQuota != int64(identity.Quota) || event.PaidQuota != 0 || event.UnknownQuota != 0 || event.CashUnknownQuota != 0 || event.KnownCashRevenueCNY != "0" || event.CreatedAt != task.CreatedAt {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var allocations []WalletFundingAllocation
	if err := lockForUpdate(tx).Where("funding_request_id = ?", request.ID).Find(&allocations).Error; err != nil {
		return nil, nil, nil, err
	}
	if len(allocations) != 1 || allocations[0].ID != identity.AllocationID || allocations[0].LotID != identity.LotID || allocations[0].Quota != int64(identity.Quota) || allocations[0].CashCNY != "0" {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var lot WalletFundingLot
	if err := lockForUpdate(tx).First(&lot, identity.LotID).Error; err != nil {
		return nil, nil, nil, err
	}
	if lot.UserID != identity.UserID || lot.Source != WalletFundingGift || !lot.CashKnown || lot.OriginalCashCNY != "0" || lot.RemainingCashCNY != "0" || lot.RemainingQuota < 0 || lot.OriginalQuota < int64(identity.Quota) || lot.RemainingQuota > lot.OriginalQuota-int64(identity.Quota) {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	// Original atomic pre-consume source, one funding event, the immutable task
	// token identity and this whole token snapshot jointly prove ownership. The
	// aggregate >= check below is only an additional bound, never the proof.
	var token Token
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", identity.TokenID, identity.UserID).First(&token).Error; err != nil {
		return nil, nil, nil, err
	}
	if !token.UnlimitedQuota || token.UsedQuota < identity.Quota || token.RemainQuota != -token.UsedQuota {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	var tokenLogs []Log
	if err := tx.Where("token_id = ? AND type IN ?", identity.TokenID, []int{LogTypeConsume, LogTypeRefund}).Find(&tokenLogs).Error; err != nil {
		return nil, nil, nil, err
	}
	var successful int64
	for _, record := range tokenLogs {
		if record.UserId != identity.UserID || record.Type != LogTypeConsume || record.Quota < 0 {
			return nil, nil, nil, ErrMediaHistoricalEvidence
		}
		successful += int64(record.Quota)
	}
	if successful+int64(identity.Quota) != int64(token.UsedQuota) {
		return nil, nil, nil, ErrMediaHistoricalEvidence
	}
	return &task, &admission, &pool, nil
}

func historicalReceiptHash(receipt MediaHistoricalReconciliation) string {
	receipt.ID = 0
	receipt.ReceiptSHA256 = ""
	data, _ := common.Marshal(receipt)
	return mediaHistoricalSHA(data)
}

func verifyHistoricalReceipt(tx *gorm.DB, receipt MediaHistoricalReconciliation, checkPool bool) error {
	if historicalReceiptHash(receipt) != receipt.ReceiptSHA256 {
		return ErrMediaHistoricalEvidence
	}
	var expected map[string]string
	if common.UnmarshalJsonStr(receipt.AfterHashes, &expected) != nil {
		return ErrMediaHistoricalEvidence
	}
	var task Task
	if err := tx.First(&task, receipt.TaskDBID).Error; err != nil {
		return err
	}
	if task.PrivateData.Execution == nil {
		return ErrMediaHistoricalEvidence
	}
	queries := map[string]*gorm.DB{
		"task":        tx.Model(&Task{}).Select("*").Where("id = ?", receipt.TaskDBID),
		"admission":   tx.Model(&MediaAdmission{}).Select("*").Where("task_db_id = ?", receipt.TaskDBID),
		"requests":    tx.Model(&WalletFundingRequest{}).Select("*").Where("user_id = ? AND request_id IN ?", task.UserId, []string{task.PrivateData.Execution.RequestID, fmt.Sprintf("legacy-task:%d", task.ID)}).Order("id"),
		"events":      tx.Model(&WalletFundingEvent{}).Select("*").Where("user_id = ? AND request_id IN ?", task.UserId, []string{task.PrivateData.Execution.RequestID, fmt.Sprintf("legacy-task:%d", task.ID)}).Order("id"),
		"allocations": tx.Model(&WalletFundingAllocation{}).Select("*").Where("funding_request_id IN (?)", tx.Model(&WalletFundingRequest{}).Select("id").Where("user_id = ? AND request_id IN ?", task.UserId, []string{task.PrivateData.Execution.RequestID, fmt.Sprintf("legacy-task:%d", task.ID)})).Order("id"),
	}
	for key, query := range queries {
		hash, err := historicalRowsHash(query)
		if err != nil {
			return err
		}
		if expected[key] == "" || expected[key] != hash {
			return ErrMediaHistoricalEvidence
		}
	}
	if !checkPool {
		return nil
	}
	var count int64
	if err := tx.Model(&MediaAdmission{}).Where("pool_id IN (?) AND state <> ?", tx.Model(&MediaAdmission{}).Select("pool_id").Where("task_db_id = ?", receipt.TaskDBID), MediaReleased).Count(&count).Error; err != nil {
		return err
	}
	var pool MediaPool
	if err := tx.Where("id IN (?)", tx.Model(&MediaAdmission{}).Select("pool_id").Where("task_db_id = ?", receipt.TaskDBID)).First(&pool).Error; err != nil {
		return err
	}
	if pool.Occupied != int(count) {
		return ErrMediaHistoricalEvidence
	}
	return nil
}

// ReconcileMediaHistoricalManifest validates all entries before changing any.
// apply=false executes the same evidence checks without any row mutation. It
// never adopts missing legacy funding, sends provider requests or writes usage
// consumption/refund projections. The normal server migration creates receipts.
func ReconcileMediaHistoricalManifest(ctx context.Context, data []byte, expectedSHA string, ids []int64, apply bool) (*MediaHistoricalResult, error) {
	initCol()
	manifest, canonical, err := CanonicalMediaHistoricalManifest(data)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(bytes.TrimSpace(data), canonical) || mediaHistoricalSHA(canonical) != expectedSHA || manifest.SchemaVersion != 1 || len(manifest.OperationID) < 8 || len(manifest.OperationID) > 96 || len(manifest.Entries) < 1 || len(manifest.Entries) > 4 || len(ids) != len(manifest.Entries) {
		return nil, ErrMediaHistoricalEvidence
	}
	manifestIDs := []int64{}
	for _, entry := range manifest.Entries {
		manifestIDs = append(manifestIDs, entry.TaskDBID)
	}
	if !slices.IsSorted(manifestIDs) || len(slices.Compact(slices.Clone(manifestIDs))) != len(ids) || !slices.Equal(ids, manifestIDs) {
		return nil, ErrMediaHistoricalEvidence
	}
	for _, entry := range manifest.Entries {
		identity := entry.Identity
		ok := entry.TaskDBID > 0 && identity.UserID > 0 && identity.TokenID > 0 && identity.Quota > 0
		if !ok || (entry.Action != MediaHistoricalRefund && entry.Action != MediaHistoricalCapacityOnly && entry.Action != MediaHistoricalLegacyGiftRefund) || identity.FundingID == 0 && entry.Action != MediaHistoricalCapacityOnly && entry.Action != MediaHistoricalLegacyGiftRefund || identity.FundingID > 0 && entry.Action != MediaHistoricalRefund {
			return nil, ErrMediaHistoricalEvidence
		}
		if err = validateMediaHistoricalSource(entry, identity); err != nil {
			return nil, err
		}
	}
	result := &MediaHistoricalResult{Status: "PLAN_VERIFIED", ManifestSHA256: expectedSHA, Tasks: ids}
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if apply {
			if !tx.Migrator().HasTable(&MediaHistoricalReconciliation{}) {
				return errors.New("historical receipt schema must be installed by the normal server migration")
			}
			if _, err := lockMediaPolicy(tx); err != nil {
				return err
			}
		}
		old := []MediaHistoricalReconciliation{}
		if tx.Migrator().HasTable(&MediaHistoricalReconciliation{}) {
			if err := tx.Where("task_db_id IN ?", ids).Order("task_db_id").Find(&old).Error; err != nil {
				return err
			}
		}
		if len(old) > 0 {
			if len(old) != len(ids) {
				return ErrMediaHistoricalEvidence
			}
			for i, receipt := range old {
				encoded, _ := common.Marshal(manifest.Entries[i])
				if receipt.ManifestSHA256 != expectedSHA || receipt.OperationID != manifest.OperationID || receipt.EntrySHA256 != mediaHistoricalSHA(encoded) {
					return ErrMediaHistoricalEvidence
				}
				if err := verifyHistoricalReceipt(tx, receipt, true); err != nil {
					return err
				}
			}
			result.Status = "ALREADY_APPLIED"
			result.AlreadyApplied = true
			return nil
		}
		tasks := []*Task{}
		admissions := []*MediaAdmission{}
		for _, entry := range manifest.Entries {
			identity := entry.Identity
			var lockedUser User
			if err := lockForUpdate(tx).First(&lockedUser, identity.UserID).Error; err != nil {
				return err
			}
			actual, err := mediaHistoricalHashes(tx, entry.TaskDBID)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(actual, entry.Hashes) {
				return ErrMediaHistoricalEvidence
			}
			task, admission, pool, err := validateMediaHistoricalOriginal(tx, entry)
			if err != nil {
				return fmt.Errorf("task %d original evidence: %w", entry.TaskDBID, err)
			}
			lockedHashes, err := mediaHistoricalHashes(tx, entry.TaskDBID)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(lockedHashes, entry.Hashes) {
				return ErrMediaHistoricalEvidence
			}
			var occupied int64
			if err = tx.Model(&MediaAdmission{}).Where("pool_id = ? AND state <> ?", pool.ID, MediaReleased).Count(&occupied).Error; err != nil {
				return err
			}
			if pool.Occupied != int(occupied) || occupied <= 0 {
				return ErrMediaHistoricalEvidence
			}
			tasks = append(tasks, task)
			admissions = append(admissions, admission)
		}
		if !apply {
			return nil
		}
		for i, entry := range manifest.Entries {
			task, admission := tasks[i], admissions[i]
			identity := entry.Identity
			fundingEventID := int64(0)
			now := time.Now().Unix()
			if entry.Action != MediaHistoricalCapacityOnly {
				requestID := identity.RequestID
				if entry.Action == MediaHistoricalLegacyGiftRefund {
					requestID = fmt.Sprintf("legacy-task:%d", task.ID)
					if err := reconstructMediaHistoricalLegacyGift(tx, task, entry); err != nil {
						return err
					}
				}
				funding, err := ApplyWalletFundingTargetWithTx(tx, identity.UserID, requestID, "relay", 0, false)
				if err != nil {
					return err
				}
				if !funding.Applied || funding.AppliedDelta != -identity.Quota || funding.Delta.GiftQuota != -int64(identity.Quota) || funding.Delta.PaidQuota != 0 || funding.Delta.UnknownQuota != 0 || funding.Total.Quota != 0 {
					return ErrMediaHistoricalEvidence
				}
				fundingEventID = funding.EventID
				changed := tx.Model(&Token{}).Where("id = ? AND user_id = ? AND used_quota >= ?", identity.TokenID, identity.UserID, identity.Quota).Updates(map[string]any{"remain_quota": gorm.Expr("remain_quota + ?", identity.Quota), "used_quota": gorm.Expr("used_quota - ?", identity.Quota)})
				if changed.Error != nil {
					return changed.Error
				}
				if changed.RowsAffected != 1 {
					return ErrMediaHistoricalEvidence
				}
				if err = tx.Model(&Task{}).Where("id = ? AND status = ? AND quota = ?", task.ID, TaskStatusUnknown, identity.Quota).Updates(map[string]any{"status": TaskStatusFailure, "quota": 0, "progress": "100%", "finish_time": now, "fail_reason": "Upstream rejected video creation because the model is unavailable"}).Error; err != nil {
					return err
				}
			}
			billing := "refunded"
			if entry.Action == MediaHistoricalCapacityOnly {
				billing = "settlement_uncertain"
			}
			if err := tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Update("billing_state", billing).Error; err != nil {
				return err
			}
			if err := releaseMediaAdmission(tx, admission); err != nil {
				return err
			}
			after, err := mediaHistoricalHashes(tx, task.ID)
			if err != nil {
				return err
			}
			beforeJSON, _ := common.Marshal(entry.Hashes)
			afterJSON, _ := common.Marshal(after)
			entryJSON, _ := common.Marshal(entry)
			receipt := MediaHistoricalReconciliation{TaskDBID: task.ID, TaskID: task.TaskID, UserID: task.UserId, OperationID: manifest.OperationID, ManifestSHA256: expectedSHA, EntrySHA256: mediaHistoricalSHA(entryJSON), Action: entry.Action, OriginalQuota: identity.Quota, FundingEventID: fundingEventID, BeforeHashes: string(beforeJSON), AfterHashes: string(afterJSON), Evidence: string(entryJSON), CreatedAt: now}
			receipt.ReceiptSHA256 = historicalReceiptHash(receipt)
			if err = tx.Create(&receipt).Error; err != nil {
				return err
			}
			if err = verifyHistoricalReceipt(tx, receipt, true); err != nil {
				return err
			}
			if entry.Action != MediaHistoricalCapacityOnly {
				result.RefundedQuota += identity.Quota
			}
		}
		result.Status = "APPLIED"
		return nil
	})
	if err != nil {
		return nil, err
	}
	if apply {
		for _, entry := range manifest.Entries {
			identity := entry.Identity
			if err := invalidateUserCache(identity.UserID); err != nil {
				result.CacheInvalidationPending = true
			}
			if err := InvalidateUserTokensCache(identity.UserID); err != nil {
				result.CacheInvalidationPending = true
			}
		}
	}
	return result, nil
}

type MediaHistoricalProjection struct {
	Refunded     bool
	FundsPending bool
}

// MediaHistoricalTaskState projects only a verified main-database historical
// receipt. It never publishes the old error log as a fresh HTTP wire proof.
func MediaHistoricalTaskState(ctx context.Context, task *Task) (*MediaHistoricalProjection, error) {
	if task == nil || task.ID <= 0 || task.PrivateData.MediaAdmissionID == "" {
		return nil, nil
	}
	if !DB.Migrator().HasTable(&MediaHistoricalReconciliation{}) {
		return nil, nil
	}
	var receipt MediaHistoricalReconciliation
	err := DB.WithContext(ctx).Where("task_db_id = ? AND task_id = ? AND user_id = ?", task.ID, task.TaskID, task.UserId).First(&receipt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = verifyHistoricalReceipt(DB.WithContext(ctx), receipt, false); err != nil {
		return nil, err
	}
	var persisted Task
	if err := DB.WithContext(ctx).First(&persisted, receipt.TaskDBID).Error; err != nil {
		return nil, err
	}
	if receipt.TaskID != persisted.TaskID || receipt.UserID != persisted.UserId {
		return nil, ErrMediaHistoricalEvidence
	}
	*task = persisted
	return &MediaHistoricalProjection{Refunded: receipt.Action == MediaHistoricalRefund || receipt.Action == MediaHistoricalLegacyGiftRefund, FundsPending: receipt.Action == MediaHistoricalCapacityOnly}, nil
}

// A legacy gift refund requires a complete original grant/debit/balance chain.
// The manifest supplies its exact private identity and complete frozen rowsets.
func validateMediaHistoricalLegacyGift(tx *gorm.DB, entry MediaHistoricalEntry) error {
	identity := entry.Identity
	proof := entry.LegacyGift
	if proof == nil || proof.GrantQuota <= 0 || proof.GrantQuota > common.MaxWalletQuota || proof.ConsumedQuota < 0 || proof.ConsumedQuota > proof.GrantQuota || proof.ConsumeCount <= 0 {
		return ErrMediaHistoricalEvidence
	}
	var user User
	if err := lockForUpdate(tx).First(&user, identity.UserID).Error; err != nil {
		return err
	}
	if user.WalletFundingInitialized || user.UsedQuota != proof.ConsumedQuota || user.RequestCount != proof.ConsumeCount {
		return ErrMediaHistoricalEvidence
	}
	var token Token
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", identity.TokenID, identity.UserID).First(&token).Error; err != nil {
		return err
	}
	if !token.UnlimitedQuota || token.UsedQuota != proof.ConsumedQuota+identity.Quota || token.RemainQuota != -token.UsedQuota {
		return ErrMediaHistoricalEvidence
	}
	tables := []*gorm.DB{
		tx.Model(&WalletFundingRequest{}).Where("user_id = ?", identity.UserID), tx.Model(&WalletFundingEvent{}).Where("user_id = ?", identity.UserID), tx.Model(&WalletFundingLot{}).Where("user_id = ?", identity.UserID),
		tx.Model(&TopUp{}).Where("user_id = ?", identity.UserID), tx.Unscoped().Model(&Redemption{}).Where("used_user_id = ?", identity.UserID), tx.Model(&Checkin{}).Where("user_id = ?", identity.UserID), tx.Model(&ModelProfitBillingRecord{}).Where("user_id = ?", identity.UserID),
	}
	for _, query := range tables {
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrMediaHistoricalEvidence
		}
	}
	var tasks []Task
	if err := tx.Where("user_id = ?", identity.UserID).Find(&tasks).Error; err != nil {
		return err
	}
	if len(tasks) != 1 || tasks[0].ID != entry.TaskDBID {
		return ErrMediaHistoricalEvidence
	}
	var logs []Log
	if err := tx.Where("user_id = ?", identity.UserID).Order("id").Find(&logs).Error; err != nil {
		return err
	}
	if len(logs) != proof.ConsumeCount+2 {
		return ErrMediaHistoricalEvidence
	}
	consumed, count, gifts, refusals := int64(0), 0, 0, 0
	for _, log := range logs {
		switch log.Type {
		case LogTypeConsume:
			if log.TokenId != identity.TokenID || log.Quota < 0 || log.ModelName == "" || log.RequestId == "" {
				return ErrMediaHistoricalEvidence
			}
			consumed += int64(log.Quota)
			count++
		case LogTypeSystem:
			var other struct {
				Change struct {
					Category string `json:"category"`
					Source   string `json:"registration_source"`
					Delta    int    `json:"quota_delta"`
					Before   int    `json:"quota_before"`
					After    int    `json:"quota_after"`
				} `json:"balance_change"`
			}
			if log.Id != proof.GiftLogID || log.TokenId != 0 || common.UnmarshalJsonStr(log.Other, &other) != nil || other.Change.Category != "registration_bonus" || other.Change.Source != "phone" || other.Change.Delta != proof.GrantQuota || other.Change.Before != 0 || other.Change.After != proof.GrantQuota {
				return ErrMediaHistoricalEvidence
			}
			gifts++
		case LogTypeError:
			if log.Id != identity.LogID {
				return ErrMediaHistoricalEvidence
			}
			refusals++
		default:
			return ErrMediaHistoricalEvidence
		}
	}
	var tokenLogs []Log
	if err := tx.Where("token_id = ? AND type IN ?", identity.TokenID, []int{LogTypeConsume, LogTypeRefund}).Find(&tokenLogs).Error; err != nil {
		return err
	}
	if len(tokenLogs) != proof.ConsumeCount {
		return ErrMediaHistoricalEvidence
	}
	for _, record := range tokenLogs {
		if record.UserId != identity.UserID || record.Type != LogTypeConsume {
			return ErrMediaHistoricalEvidence
		}
	}
	if count != proof.ConsumeCount || consumed != int64(proof.ConsumedQuota) || gifts != 1 || refusals != 1 || int64(user.Quota)+consumed+int64(identity.Quota) != int64(proof.GrantQuota) {
		return ErrMediaHistoricalEvidence
	}
	return nil
}

// Record the proven legacy gift and still-held reservation without changing
// the wallet. The normal source-ledger target then reverses that reservation.
func reconstructMediaHistoricalLegacyGift(tx *gorm.DB, task *Task, entry MediaHistoricalEntry) error {
	if err := validateMediaHistoricalLegacyGift(tx, entry); err != nil {
		return err
	}
	identity := entry.Identity
	proof := entry.LegacyGift
	var user User
	if err := tx.First(&user, identity.UserID).Error; err != nil {
		return err
	}
	lot := WalletFundingLot{UserID: identity.UserID, Source: WalletFundingGift, SourceKey: fmt.Sprintf("historical-registration-log:%d", proof.GiftLogID), OriginalQuota: int64(proof.GrantQuota), RemainingQuota: int64(user.Quota), OriginalCashCNY: "0", RemainingCashCNY: "0", CashKnown: true, CreatedAt: user.CreatedAt}
	if err := tx.Create(&lot).Error; err != nil {
		return err
	}
	requestID := fmt.Sprintf("legacy-task:%d", entry.TaskDBID)
	request := WalletFundingRequest{UserID: identity.UserID, IdentityHash: walletFundingIdentity(requestID, "relay"), RequestID: requestID, Purpose: "relay", ModelName: identity.Model, InitialModelName: identity.Model, ChannelID: task.ChannelId, Quota: int64(identity.Quota), LegacyBaselineImported: true, CreatedAt: task.CreatedAt, UpdatedAt: task.CreatedAt}
	if err := tx.Create(&request).Error; err != nil {
		return err
	}
	if err := tx.Create(&WalletFundingAllocation{FundingRequestID: request.ID, LotID: lot.ID, Quota: int64(identity.Quota), CashCNY: "0"}).Error; err != nil {
		return err
	}
	if err := tx.Model(&User{}).Where("id = ? AND wallet_funding_initialized = ?", identity.UserID, false).Update("wallet_funding_initialized", true).Error; err != nil {
		return err
	}
	// Preserve any frozen private fields unknown to this binary. Only the
	// proved funding identity is added; typed re-marshalling could drop them.
	var raw any
	if err := tx.Model(&Task{}).Select("private_data").Where("id = ?", task.ID).Row().Scan(&raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if common.Unmarshal(jsonScanBytes(raw), &fields) != nil || fields == nil {
		return ErrMediaHistoricalEvidence
	}
	if _, present := fields["funding_request_id"]; present {
		return ErrMediaHistoricalEvidence
	}
	encodedIdentity, err := common.Marshal(requestID)
	if err != nil {
		return err
	}
	fields["funding_request_id"] = encodedIdentity
	encodedPrivate, err := common.Marshal(fields)
	if err != nil {
		return err
	}
	task.PrivateData.FundingRequestId = requestID
	return tx.Model(&Task{}).Where("id = ?", task.ID).Update("private_data", string(encodedPrivate)).Error
}

// PrepareMediaHistoricalReconciliationSchema is an explicit operator step,
// after validating the exact private plan. Inspect/plan never invoke it.
// Only this receipt table is installed; no wallet/pool/task migration runs.
func PrepareMediaHistoricalReconciliationSchema(ctx context.Context, data []byte, expectedSHA string, ids []int64) error {
	if _, err := ReconcileMediaHistoricalManifest(ctx, data, expectedSHA, ids, false); err != nil {
		return err
	}
	return DB.WithContext(ctx).AutoMigrate(&MediaHistoricalReconciliation{})
}
