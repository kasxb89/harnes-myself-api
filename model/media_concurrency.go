package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MediaConcurrencyOption = "MediaConcurrencyPolicy"
const MediaVideo = "video"
const MediaImage = "image"
const MediaReserved = "reserved"
const MediaSending = "sending"
const MediaActive = "active"
const MediaUnknown = "unknown"
const MediaReleased = "released"

var ErrMediaCapacity = errors.New("upstream media account capacity exhausted")
var ErrMediaAdmissionUnavailable = errors.New("media admission is unavailable")
var ErrMediaPolicyConflict = errors.New("media concurrency policy changed")
var ErrInvalidMediaConcurrencyPolicy = errors.New("invalid media concurrency policy")
var ErrMediaLegacyIdentity = errors.New("historical media account identity is unconfirmed")

type MediaConcurrencyDefaults struct {
	Video int `json:"video"`
	Image int `json:"image"`
}

type MediaPoolOverride struct {
	PoolID string `json:"pool_id"`
	Limit  int    `json:"limit"`
}

type MediaConcurrencyPolicy struct {
	SchemaVersion int                      `json:"schema_version"`
	Defaults      MediaConcurrencyDefaults `json:"defaults"`
	PoolOverrides []MediaPoolOverride      `json:"pool_overrides"`
	Revision      string                   `json:"revision,omitempty"`
}

// MediaPool keeps credential equality private. The random ID is safe for the
// administrator to use in configuration; it is never a hash of an API key.
type MediaPool struct {
	ID             string `gorm:"primaryKey;type:varchar(64)" json:"-"`
	IdentityDigest string `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	MediaKind      string `gorm:"type:varchar(8)" json:"-"`
	KnownLimit     int    `json:"-"`
	Occupied       int    `json:"-"`
	Reconciled     bool   `json:"-"`
	UpdatedAt      int64  `json:"-"`
}

// MediaAdmission never expires. A disconnected request, a local timeout or a
// lost provider receipt cannot prove that a paid upstream operation stopped.
type MediaAdmission struct {
	ID               string `gorm:"primaryKey;type:varchar(191)" json:"-"`
	TaskDBID         int64  `gorm:"uniqueIndex" json:"-"`
	PoolID           string `gorm:"type:varchar(64);index" json:"-"`
	ChannelID        int    `gorm:"index" json:"-"`
	State            string `gorm:"type:varchar(20);index" json:"-"`
	BillingState     string `gorm:"type:varchar(24)" json:"-"`
	ProviderTerminal bool   `json:"-"`
	SendingAt        int64  `json:"-"`
	CreatedAt        int64  `json:"-"`
	UpdatedAt        int64  `json:"-"`
}

type MediaPoolSnapshot struct {
	PoolID             string `json:"pool_id"`
	Label              string `json:"label"`
	MediaKind          string `json:"media_kind"`
	ChannelIDs         []int  `json:"channel_ids"`
	ConfiguredLimit    int    `json:"configured_limit"`
	KnownUpstreamLimit *int   `json:"known_upstream_limit"`
	EffectiveLimit     int    `json:"effective_limit"`
	Occupied           int    `json:"occupied"`
	Unknown            int    `json:"unknown"`
	Enabled            bool   `json:"enabled"`
	Reconciled         bool   `json:"reconciled"`
	AvailableSlots     int    `json:"available_slots"`
}

type MediaConcurrencySnapshot struct {
	Policy     MediaConcurrencyPolicy `json:"policy"`
	Pools      []MediaPoolSnapshot    `json:"pools"`
	ObservedAt int64                  `json:"observed_at"`
}

func defaultMediaConcurrencyPolicy() MediaConcurrencyPolicy {
	return MediaConcurrencyPolicy{SchemaVersion: 1, Defaults: MediaConcurrencyDefaults{Video: 4, Image: 10}, PoolOverrides: []MediaPoolOverride{}}
}

func MigrateMediaAdmission(db *gorm.DB) error {
	if err := db.AutoMigrate(&Option{}, &MediaPool{}, &MediaAdmission{}); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error { _, err := mediaPolicyForUpdate(tx); return err })
}

func mediaPolicyValue(policy MediaConcurrencyPolicy) (string, error) {
	policy.Revision = ""
	policy.PoolOverrides = append([]MediaPoolOverride{}, policy.PoolOverrides...)
	slices.SortFunc(policy.PoolOverrides, func(a, b MediaPoolOverride) int { return strings.Compare(a.PoolID, b.PoolID) })
	data, err := common.Marshal(policy)
	return string(data), err
}

func decodeMediaPolicy(value string) (MediaConcurrencyPolicy, error) {
	var policy MediaConcurrencyPolicy
	if err := common.UnmarshalJsonStr(value, &policy); err != nil {
		return policy, ErrMediaAdmissionUnavailable
	}
	if err := validateMediaPolicy(policy); err != nil {
		return policy, err
	}
	sum := sha256.Sum256([]byte(value))
	policy.Revision = hex.EncodeToString(sum[:])
	return policy, nil
}

func validateMediaPolicy(policy MediaConcurrencyPolicy) error {
	if policy.SchemaVersion != 1 || policy.Defaults.Video < 1 || policy.Defaults.Video > 16 || policy.Defaults.Image < 1 || policy.Defaults.Image > 64 || len(policy.PoolOverrides) > 128 {
		return ErrInvalidMediaConcurrencyPolicy
	}
	seen := make(map[string]struct{}, len(policy.PoolOverrides))
	for _, override := range policy.PoolOverrides {
		if override.PoolID == "" || len(override.PoolID) > 68 || override.Limit < 1 || override.Limit > 64 {
			return ErrInvalidMediaConcurrencyPolicy
		}
		if _, duplicate := seen[override.PoolID]; duplicate {
			return ErrInvalidMediaConcurrencyPolicy
		}
		seen[override.PoolID] = struct{}{}
	}
	return nil
}

// A write to the single option row serializes both configuration CAS and all
// acquisitions across processes. It is the first transaction statement on
// SQLite, avoiding read-snapshot upgrades. No process cache is authoritative.
func mediaPolicyForUpdate(tx *gorm.DB) (MediaConcurrencyPolicy, error) {
	row, err := lockMediaPolicy(tx)
	if err != nil {
		return MediaConcurrencyPolicy{}, err
	}
	return decodeMediaPolicy(row.Value)
}

// Releasing already accepted work must remain possible even if somebody
// bypassed the policy API and stored a malformed option. Locking and policy
// validation are deliberately separate for that reason.
func lockMediaPolicy(tx *gorm.DB) (Option, error) {
	value, err := mediaPolicyValue(defaultMediaConcurrencyPolicy())
	if err != nil {
		return Option{}, err
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: MediaConcurrencyOption, Value: value}).Error; err != nil {
		return Option{}, err
	}
	if err := tx.Model(&Option{}).Where(commonKeyCol+" = ?", MediaConcurrencyOption).UpdateColumn("value", gorm.Expr("value")).Error; err != nil {
		return Option{}, err
	}
	var row Option
	if err := tx.Where(commonKeyCol+" = ?", MediaConcurrencyOption).First(&row).Error; err != nil {
		return Option{}, err
	}
	return row, nil
}

func GetMediaConcurrencyPolicy(ctx context.Context) (MediaConcurrencyPolicy, error) {
	if DB == nil {
		return MediaConcurrencyPolicy{}, ErrMediaAdmissionUnavailable
	}
	var row Option
	err := DB.WithContext(ctx).Where(commonKeyCol+" = ?", MediaConcurrencyOption).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		value, marshalErr := mediaPolicyValue(defaultMediaConcurrencyPolicy())
		if marshalErr != nil {
			return MediaConcurrencyPolicy{}, marshalErr
		}
		return decodeMediaPolicy(value)
	}
	if err != nil {
		return MediaConcurrencyPolicy{}, err
	}
	return decodeMediaPolicy(row.Value)
}

func SaveMediaConcurrencyPolicy(ctx context.Context, expectedRevision string, policy MediaConcurrencyPolicy) (MediaConcurrencyPolicy, error) {
	if err := validateMediaPolicy(policy); err != nil {
		return MediaConcurrencyPolicy{}, err
	}
	var saved MediaConcurrencyPolicy
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := mediaPolicyForUpdate(tx)
		if err != nil {
			return err
		}
		if expectedRevision == "" || expectedRevision != current.Revision {
			return ErrMediaPolicyConflict
		}
		var channels []Channel
		var pools []MediaPool
		var accounts []H3Account
		if err := tx.Limit(2001).Find(&channels).Error; err != nil || len(channels) > 2000 {
			return ErrMediaAdmissionUnavailable
		}
		if err := tx.Limit(2001).Find(&pools).Error; err != nil || len(pools) > 2000 {
			return ErrMediaAdmissionUnavailable
		}
		if err := tx.Limit(101).Find(&accounts).Error; err != nil || len(accounts) > 100 {
			return ErrMediaAdmissionUnavailable
		}
		policy = canonicalMediaPolicy(policy, channels, pools, accounts, true)
		for _, override := range policy.PoolOverrides {
			kind := ""
			if accountID, h3 := strings.CutPrefix(override.PoolID, "h3:"); h3 {
				var count int64
				if err := tx.Model(&H3Account{}).Where("id = ?", accountID).Count(&count).Error; err != nil || count != 1 {
					return ErrInvalidMediaConcurrencyPolicy
				}
				kind = MediaVideo
			} else {
				var pool MediaPool
				if err := tx.Where("id = ?", override.PoolID).First(&pool).Error; err != nil {
					return ErrInvalidMediaConcurrencyPolicy
				}
				kind = pool.MediaKind
			}
			if (kind == MediaVideo && override.Limit > 16) || (kind != MediaVideo && kind != MediaImage) {
				return ErrInvalidMediaConcurrencyPolicy
			}
		}
		value, err := mediaPolicyValue(policy)
		if err != nil {
			return err
		}
		if err := tx.Model(&Option{}).Where(commonKeyCol+" = ?", MediaConcurrencyOption).Update("value", value).Error; err != nil {
			return err
		}
		saved, err = decodeMediaPolicy(value)
		return err
	})
	if err == nil {
		InvalidatePricingCache()
	}
	return saved, err
}

// A shared H3/generic credential has one visible configuration row. Present
// and save its overrides under that canonical ID, so a hidden older alias
// cannot silently retain a lower limit after the administrator edits it.
func canonicalMediaPolicy(policy MediaConcurrencyPolicy, channels []Channel, pools []MediaPool, accounts []H3Account, preferCanonical bool) MediaConcurrencyPolicy {
	aliases := make(map[string]string)
	for _, account := range accounts {
		for _, channel := range channels {
			if channel.Id != account.ChannelID || channel.ChannelInfo.IsMultiKey || h3CredentialDigest(channel.Key) != account.CredentialDigest {
				continue
			}
			for _, pool := range pools {
				if pool.MediaKind == MediaVideo && pool.IdentityDigest == mediaIdentityDigest(channel.Key, MediaVideo) {
					aliases[pool.ID] = "h3:" + account.ID
				}
			}
		}
	}
	explicit := make(map[string]int, len(policy.PoolOverrides))
	for _, override := range policy.PoolOverrides {
		explicit[override.PoolID] = override.Limit
	}
	canonical := make(map[string]int, len(explicit))
	for _, override := range policy.PoolOverrides {
		id := override.PoolID
		if target, linked := aliases[id]; linked {
			id = target
			if limit, exists := explicit[target]; exists && preferCanonical {
				canonical[target] = limit
				continue
			}
		}
		limit := override.Limit
		if previous, exists := canonical[id]; exists && !preferCanonical {
			limit = min(limit, previous)
		}
		canonical[id] = limit
	}
	policy.PoolOverrides = make([]MediaPoolOverride, 0, len(canonical))
	for id, limit := range canonical {
		policy.PoolOverrides = append(policy.PoolOverrides, MediaPoolOverride{PoolID: id, Limit: limit})
	}
	slices.SortFunc(policy.PoolOverrides, func(a, b MediaPoolOverride) int { return strings.Compare(a.PoolID, b.PoolID) })
	return policy
}

func mediaEffectiveLimit(policy MediaConcurrencyPolicy, poolID, kind string, known int) int {
	limit := policy.Defaults.Video
	if kind == MediaImage {
		limit = policy.Defaults.Image
	}
	for _, override := range policy.PoolOverrides {
		if override.PoolID == poolID {
			limit = override.Limit
			break
		}
	}
	if known > 0 {
		limit = min(limit, known)
	}
	return limit
}

func mediaIdentityDigest(key, kind string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

func mediaLinkedH3Account(tx *gorm.DB, key string) (*H3Account, error) {
	var account H3Account
	err := tx.Where("credential_digest = ?", h3CredentialDigest(key)).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func mediaSharedVideoLimit(tx *gorm.DB, policy MediaConcurrencyPolicy, pool *MediaPool, key string) (int, int, error) {
	limit := mediaEffectiveLimit(policy, pool.ID, pool.MediaKind, pool.KnownLimit)
	if pool.MediaKind != MediaVideo {
		return limit, 0, nil
	}
	account, err := mediaLinkedH3Account(tx, key)
	if err != nil || account == nil {
		return limit, 0, err
	}
	if !account.Enabled || !account.Reconciled || account.Limit != H3AccountLimit {
		return 0, 0, ErrMediaAdmissionUnavailable
	}
	var channel Channel
	if err := tx.Where("id = ?", account.ChannelID).First(&channel).Error; err != nil || channel.Status != common.ChannelStatusEnabled || channel.ChannelInfo.IsMultiKey || h3CredentialDigest(channel.Key) != account.CredentialDigest || h3ConfigurationDigest(&channel) != account.ConfigurationDigest {
		return 0, 0, ErrMediaAdmissionUnavailable
	}
	return min(limit, mediaEffectiveLimit(policy, "h3:"+account.ID, MediaVideo, H3AccountLimit)), account.Occupied, nil
}

func mediaChannelHasKey(channel *Channel, key string, enabledOnly bool) bool {
	if enabledOnly && channel.Status != common.ChannelStatusEnabled {
		return false
	}
	for i, candidate := range channel.GetKeys() {
		if strings.TrimSpace(candidate) != strings.TrimSpace(key) {
			continue
		}
		status, explicit := channel.ChannelInfo.MultiKeyStatusList[i]
		if !enabledOnly || !channel.ChannelInfo.IsMultiKey || !explicit || status == common.ChannelStatusEnabled {
			return true
		}
	}
	return false
}

// TaskMediaKind resolves exact plugin metadata; substrings in model names do
// not establish a media capability. Ambiguous legacy metadata fails closed.
func TaskMediaKind(task *Task) string {
	if task == nil {
		return ""
	}
	if task.PrivateData.MediaKind == MediaImage || task.PrivateData.MediaKind == MediaVideo {
		return task.PrivateData.MediaKind
	}
	plugin, ok := mediaTaskPlugin(task.Platform)
	if !ok {
		return ""
	}
	schema, _ := plugin.Meta.UsageForModels(task.Properties.UpstreamModelName, task.Properties.OriginModelName)
	_, image := schema["image_count"]
	_, seconds := schema["seconds"]
	if image && !seconds {
		return MediaImage
	}
	if seconds && !image {
		return MediaVideo
	}
	if !image {
		for _, claim := range plugin.Meta.Protocols {
			if claim.Name == "openai_video" && (len(claim.Models) == 0 || slices.Contains(claim.Models, task.Properties.UpstreamModelName) || slices.Contains(claim.Models, task.Properties.OriginModelName)) {
				return MediaVideo
			}
			if claim.Name == "openai_image" && (len(claim.Models) == 0 || slices.Contains(claim.Models, task.Properties.UpstreamModelName) || slices.Contains(claim.Models, task.Properties.OriginModelName)) {
				return MediaImage
			}
		}
	}
	return ""
}

// TaskRequiresMediaRouteBinding preserves the known product media identity
// even if a later registry generation removes its schema or protocol claims.
func TaskRequiresMediaRouteBinding(task *Task) bool {
	if task == nil {
		return false
	}
	if task.PrivateData.MediaAdmissionID != "" || task.PrivateData.H3AdmissionID != "" || task.PrivateData.RouteDigest != "" || TaskMediaKind(task) != "" || IsH3Task(task) {
		return true
	}
	keys := []string{string(task.Platform)}
	if execution := task.PrivateData.Execution; execution != nil && execution.TaskPlugin != nil {
		keys = append(keys, execution.TaskPlugin.Key)
	}
	for _, key := range keys {
		if key == "blackrxai-video" || key == "toapis-image" || key == "hailuo" || key == "35" {
			return true
		}
	}
	return false
}

func ensureMediaPool(tx *gorm.DB, key, kind string) (*MediaPool, error) {
	if strings.TrimSpace(key) == "" || (kind != MediaVideo && kind != MediaImage) {
		return nil, ErrMediaAdmissionUnavailable
	}
	if err := guardMediaLegacyIdentity(tx, kind); err != nil {
		return nil, err
	}
	candidate := MediaPool{ID: "media_" + strings.TrimPrefix(GenerateTaskID(), "task_"), IdentityDigest: mediaIdentityDigest(key, kind), MediaKind: kind, UpdatedAt: time.Now().Unix()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
		return nil, err
	}
	var pool MediaPool
	if err := tx.Where("identity_digest = ?", candidate.IdentityDigest).First(&pool).Error; err != nil {
		return nil, err
	}
	// Import again before each acquisition: adding another channel with the
	// same credential must not introduce previously invisible legacy work.
	if err := reconcileMediaPool(tx, &pool); err != nil {
		return nil, err
	}
	return &pool, nil
}

func MediaLegacyIdentityUnknown(task *Task) bool {
	if task == nil || IsH3Task(task) || task.Status == TaskStatusSuccess || strings.TrimSpace(task.PrivateData.Key) != "" {
		return false
	}
	_, known := mediaTaskPlugin(task.Platform)
	return TaskRequiresMediaRouteBinding(task) || !known
}

func mediaTaskPlugin(platform constant.TaskPlatform) (*jsplugin.LoadedPlugin, bool) {
	generation := jsplugin.DefaultRegistry.Generation()
	if plugin, known := generation.Get(string(platform)); known {
		return plugin, true
	}
	channelType, err := strconv.Atoi(string(platform))
	if err != nil {
		return nil, false
	}
	return generation.GetByChannelType(channelType)
}

// Without an immutable selected credential, a historical channel's current
// key cannot establish which upstream account still owns the paid work. A key
// rotation can otherwise cause a different channel to overbook the old key.
// MediaLegacyProviderTerminalConfirmed recognizes only the historical wire
// snapshots whose failed state the old host persisted from these providers.
// It excludes stopped work from capacity reconciliation; it does not attest to
// billing, authorize resubmission, or reconstruct a credential/execution route.
func MediaLegacyProviderTerminalConfirmed(task *Task) bool {
	if task == nil || task.Status != TaskStatusFailure || task.PrivateData.MediaAdmissionID != "" || task.PrivateData.H3AdmissionID != "" || task.PrivateData.UpstreamTaskID == "" {
		return false
	}
	platform := string(task.Platform)
	if platform != "blackrxai-video" && platform != "toapis-image" {
		return false
	}
	execution := task.PrivateData.Execution
	if execution == nil || execution.RequestID == "" || execution.TaskPlugin == nil || execution.TaskPlugin.Key != platform || execution.TaskPlugin.APIVersion != 1 {
		return false
	}
	version := execution.TaskPlugin.Version
	if platform == "blackrxai-video" {
		if execution.RequestPath != "/v1/videos" || !slices.Contains([]string{"1.0.0", "1.0.2"}, version) {
			return false
		}
	} else if execution.RequestPath != "/toapis/v1/images/generations" || !slices.Contains([]string{"1.1.0", "1.2.0"}, version) {
		return false
	}
	var envelope map[string]any
	if common.Unmarshal(task.Data, &envelope) != nil || envelope == nil {
		return false
	}
	for _, field := range []string{"success", "data", "code", "status_code", "task", "task_id", "submission_receipt", "media_submission_receipt", "provider_terminal_confirmed"} {
		if _, present := envelope[field]; present {
			return false
		}
	}
	id, _ := envelope["id"].(string)
	status, _ := envelope["status"].(string)
	object, _ := envelope["object"].(string)
	modelName, _ := envelope["model"].(string)
	providerError, _ := envelope["error"].(map[string]any)
	errorCode, _ := providerError["code"].(string)
	if id == "" || id != task.PrivateData.UpstreamTaskID {
		return false
	}
	upstreamModel := task.Properties.UpstreamModelName
	if upstreamModel == "" {
		upstreamModel = task.Properties.OriginModelName
	}
	if platform == "blackrxai-video" {
		return (task.PrivateData.MediaKind == "" || task.PrivateData.MediaKind == MediaVideo) && object == "video" && (status == "failed" || status == "cancelled") && errorCode == "video_generation_failed" && upstreamModel != "" && modelName == upstreamModel
	}
	return (task.PrivateData.MediaKind == "" || task.PrivateData.MediaKind == MediaImage) && object == "generation.task" && status == "failed" && errorCode == "generation_failed" && task.Properties.OriginModelName == "yunshu-internal-gpt-image-2" && upstreamModel == "yunshu-internal-gpt-image-2" && modelName == "gpt-image-2"
}

func guardMediaLegacyIdentity(tx *gorm.DB, kind string) error {
	return visitUnledgeredMediaHistory(tx, func(task *Task) error {
		if IsH3Task(task) || strings.TrimSpace(task.PrivateData.Key) != "" || MediaLegacyProviderTerminalConfirmed(task) {
			return nil
		}
		resolvedKind := TaskMediaKind(task)
		_, knownPlugin := mediaTaskPlugin(task.Platform)
		if resolvedKind == kind || resolvedKind == "" && (!knownPlugin || TaskRequiresMediaRouteBinding(task)) {
			return ErrMediaLegacyIdentity
		}
		return nil
	})
}

// Page by the immutable primary key rather than clipping all historical task
// kinds to one window. Importing a page removes rows from the unledgered set.
func visitUnledgeredMediaHistory(tx *gorm.DB, visit func(*Task) error) error {
	var cursor int64
	for {
		var tasks []Task
		if err := lockForUpdate(tx).Where("id > ? AND (status IS NULL OR status <> ?)", cursor, TaskStatusSuccess).
			Where("id NOT IN (?)", tx.Model(&MediaAdmission{}).Select("task_db_id").Where("task_db_id IS NOT NULL")).
			Where("id NOT IN (?)", tx.Model(&H3Admission{}).Select("task_db_id").Where("task_db_id IS NOT NULL")).
			Order("id").Limit(500).Find(&tasks).Error; err != nil {
			return err
		}
		for i := range tasks {
			if err := visit(&tasks[i]); err != nil {
				return err
			}
			cursor = tasks[i].ID
		}
		if len(tasks) < 500 {
			return nil
		}
	}
}

func reconcileMediaPool(tx *gorm.DB, pool *MediaPool) error {
	if err := visitUnledgeredMediaHistory(tx, func(task *Task) error {
		if task.PrivateData.H3AdmissionID != "" || IsH3Task(task) || MediaLegacyProviderTerminalConfirmed(task) || strings.TrimSpace(task.PrivateData.Key) == "" || mediaIdentityDigest(task.PrivateData.Key, pool.MediaKind) != pool.IdentityDigest {
			return nil
		}
		kind := TaskMediaKind(task)
		if kind == "" {
			if _, known := mediaTaskPlugin(task.Platform); !known || TaskRequiresMediaRouteBinding(task) {
				return ErrMediaLegacyIdentity
			}
			return nil
		}
		if kind != pool.MediaKind {
			return nil
		}
		id := fmt.Sprintf("legacy-media-%d", task.ID)
		admission := MediaAdmission{ID: id, TaskDBID: task.ID, PoolID: pool.ID, ChannelID: task.ChannelId, State: MediaUnknown, BillingState: "legacy_uncertain", SendingAt: 1, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}
		if err := tx.Create(&admission).Error; err != nil {
			return err
		}
		task.PrivateData.MediaAdmissionID = id
		task.PrivateData.MediaKind = pool.MediaKind
		// Legacy local FAILURE is not evidence that provider work stopped.
		if err := tx.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{"private_data": task.PrivateData, "status": TaskStatusUnknown, "progress": "0%", "finish_time": 0}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	var occupied int64
	if err := tx.Model(&MediaAdmission{}).Where("pool_id = ? AND state <> ?", pool.ID, MediaReleased).Count(&occupied).Error; err != nil {
		return err
	}
	pool.Occupied = int(occupied)
	pool.Reconciled = true
	return tx.Model(pool).Updates(map[string]any{"occupied": occupied, "reconciled": true, "updated_at": time.Now().Unix()}).Error
}

func AcquireMediaAdmission(ctx context.Context, task *Task, selectedKey, mediaKind string) error {
	return (MediaAdmissionStore{DB: DB}).Acquire(ctx, task, selectedKey, mediaKind)
}

type MediaAdmissionStore struct{ DB *gorm.DB }

func (store MediaAdmissionStore) Acquire(ctx context.Context, task *Task, selectedKey, mediaKind string) error {
	if store.DB == nil || task == nil || task.TaskID == "" || task.ChannelId <= 0 || task.PrivateData.H3AdmissionID != "" || task.PrivateData.MediaAdmissionID != "" {
		return ErrMediaAdmissionUnavailable
	}
	target := task
	candidate := *task
	task = &candidate
	err := store.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		policy, err := mediaPolicyForUpdate(tx)
		if err != nil {
			return err
		}
		var channel Channel
		if err := tx.Where("id = ?", task.ChannelId).First(&channel).Error; err != nil || !mediaChannelHasKey(&channel, selectedKey, true) {
			return ErrMediaAdmissionUnavailable
		}
		pool, err := ensureMediaPool(tx, selectedKey, mediaKind)
		if err != nil {
			return err
		}
		limit, linkedOccupied, err := mediaSharedVideoLimit(tx, policy, pool, selectedKey)
		if err != nil {
			return err
		}
		result := tx.Model(&MediaPool{}).Where("id = ? AND reconciled = ? AND occupied < ?", pool.ID, true, limit-linkedOccupied).Updates(map[string]any{"occupied": gorm.Expr("occupied + 1"), "updated_at": time.Now().Unix()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrMediaCapacity
		}
		routeDigest := MediaTaskRouteDigest(&channel, task.Platform)
		if routeDigest == "" || task.PrivateData.RouteDigest != "" && task.PrivateData.RouteDigest != routeDigest {
			return ErrMediaAdmissionUnavailable
		}
		task.PrivateData.RouteDigest = routeDigest
		task.Status = TaskStatusUnknown
		task.SubmitTime = 0
		task.Progress = "0%"
		task.PrivateData.Key = selectedKey
		task.PrivateData.MediaAdmissionID = task.TaskID
		task.PrivateData.MediaKind = mediaKind
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		return tx.Create(&MediaAdmission{ID: task.TaskID, TaskDBID: task.ID, PoolID: pool.ID, ChannelID: task.ChannelId, State: MediaReserved, BillingState: "not_started", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}).Error
	})
	if err == nil {
		*target = candidate
	}
	return err
}

// RecordMediaCapacityRejection records a final, proven-unsent refusal only
// after the caller exhausted its eligible pools. It does not occupy a slot.
func RecordMediaCapacityRejection(ctx context.Context, task *Task, selectedKey, mediaKind string) error {
	if task == nil || task.TaskID == "" || task.ChannelId <= 0 || task.ID != 0 {
		return ErrMediaAdmissionUnavailable
	}
	target := task
	candidate := *task
	task = &candidate
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := mediaPolicyForUpdate(tx); err != nil {
			return err
		}
		var channel Channel
		if err := tx.Where("id = ?", task.ChannelId).First(&channel).Error; err != nil || !mediaChannelHasKey(&channel, selectedKey, true) {
			return ErrMediaAdmissionUnavailable
		}
		pool, err := ensureMediaPool(tx, selectedKey, mediaKind)
		if err != nil {
			return err
		}
		task.Status = TaskStatusFailure
		task.Quota = 0
		task.SubmitTime = 0
		task.Progress = "100%"
		task.FinishTime = time.Now().Unix()
		task.FailReason = "Upstream media account capacity exhausted; submission was not sent"
		task.PrivateData = TaskPrivateData{MediaAdmissionID: task.TaskID, MediaKind: mediaKind, Key: selectedKey, Execution: task.PrivateData.Execution}
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		return tx.Create(&MediaAdmission{ID: task.TaskID, TaskDBID: task.ID, PoolID: pool.ID, ChannelID: task.ChannelId, State: MediaReleased, BillingState: "not_charged", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}).Error
	})
	if err == nil {
		*target = candidate
	}
	return err
}

func MarkMediaSending(ctx context.Context, id string) error {
	result := DB.WithContext(ctx).Model(&MediaAdmission{}).Where("id = ? AND state = ? AND sending_at = 0 AND billing_state = ?", id, MediaReserved, "reserved").Updates(map[string]any{"state": MediaSending, "sending_at": time.Now().Unix(), "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrMediaAdmissionUnavailable
	}
	return nil
}

func SetMediaBillingState(ctx context.Context, id, state string) error {
	if state == "proven_uncharged" {
		state = "not_charged"
	}
	if !slices.Contains([]string{"preparing", "reserved", "not_charged", "settlement_uncertain"}, state) {
		return ErrMediaAdmissionUnavailable
	}
	result := DB.WithContext(ctx).Model(&MediaAdmission{}).Where("id = ? AND state = ?", id, MediaReserved).Updates(map[string]any{"billing_state": state, "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var admission MediaAdmission
		if err := DB.WithContext(ctx).Where("id = ? AND state = ? AND billing_state = ?", id, MediaReserved, state).First(&admission).Error; err != nil {
			return ErrMediaAdmissionUnavailable
		}
	}
	return nil
}

func SaveMediaBillingReservation(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ? AND channel_id = ? AND state = ? AND sending_at = 0", task.PrivateData.MediaAdmissionID, task.ID, task.ChannelId, MediaReserved).First(&admission).Error; err != nil {
			return err
		}
		if err := tx.Model(&Task{}).Where("id = ? AND task_id = ? AND user_id = ? AND status = ?", task.ID, task.TaskID, task.UserId, TaskStatusUnknown).Updates(map[string]any{"private_data": task.PrivateData, "quota": task.Quota}).Error; err != nil {
			return err
		}
		return tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Updates(map[string]any{"billing_state": "reserved", "updated_at": time.Now().Unix()}).Error
	})
}

func MediaBillingNeedsReview(ctx context.Context, id string) (bool, error) {
	var admission MediaAdmission
	if err := DB.WithContext(ctx).Select("billing_state").Where("id = ?", id).First(&admission).Error; err != nil {
		return true, err
	}
	return admission.BillingState == "preparing" || admission.BillingState == "legacy_uncertain" || admission.BillingState == "settlement_uncertain", nil
}

func MarkMediaBillingUncertain(ctx context.Context, id string) error {
	result := DB.WithContext(ctx).Model(&MediaAdmission{}).Where("id = ?", id).Update("billing_state", "settlement_uncertain")
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrMediaAdmissionUnavailable
	}
	return nil
}

// ConfirmMediaRefund records a completed funding refund; it never performs a
// wallet credit. Until this commit, a pre-send failure stays financially unknown.
func ConfirmMediaRefund(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ? AND channel_id = ?", task.PrivateData.MediaAdmissionID, task.ID, task.ChannelId).First(&admission).Error; err != nil {
			return err
		}
		if admission.State != MediaReleased {
			return ErrMediaAdmissionUnavailable
		}
		if admission.BillingState == "refunded" {
			return nil
		}
		if admission.BillingState != "refund_pending" {
			return ErrMediaAdmissionUnavailable
		}
		var persisted Task
		if err := tx.Where("id = ? AND task_id = ? AND user_id = ?", task.ID, task.TaskID, task.UserId).First(&persisted).Error; err != nil {
			return err
		}
		if persisted.Status != TaskStatusUnknown && persisted.Status != TaskStatusFailure {
			return ErrMediaAdmissionUnavailable
		}
		if err := tx.Model(&Task{}).Where("id = ?", persisted.ID).Updates(map[string]any{"status": TaskStatusFailure, "progress": "100%", "finish_time": time.Now().Unix(), "quota": 0}).Error; err != nil {
			return err
		}
		return tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Updates(map[string]any{"billing_state": "refunded", "updated_at": time.Now().Unix()}).Error
	})
}

func MediaConfirmedFailureState(ctx context.Context, task *Task) (string, bool, error) {
	if task == nil || task.PrivateData.MediaAdmissionID == "" || task.ID <= 0 {
		return "", false, nil
	}
	var snapshot struct {
		Task             `gorm:"embedded"`
		AdmissionID      string
		AdmissionTask    int64
		AdmissionChannel int
		AdmissionState   string
		BillingState     string
		SendingAt        int64
		ProviderTerminal bool
		AdmissionKind    string
	}
	taskTable := DB.NamingStrategy.TableName("Task")
	admissionTable := DB.NamingStrategy.TableName("MediaAdmission")
	poolTable := DB.NamingStrategy.TableName("MediaPool")
	err := DB.WithContext(ctx).Model(&Task{}).Table(taskTable+" AS media_receipt_task").
		Select("media_receipt_task.*, media_receipt_admission.id AS admission_id, media_receipt_admission.task_db_id AS admission_task, media_receipt_admission.channel_id AS admission_channel, media_receipt_admission.state AS admission_state, media_receipt_admission.billing_state AS billing_state, media_receipt_admission.sending_at AS sending_at, media_receipt_admission.provider_terminal AS provider_terminal, media_receipt_pool.media_kind AS admission_kind").
		Joins("JOIN "+admissionTable+" AS media_receipt_admission ON media_receipt_admission.task_db_id = media_receipt_task.id").
		Joins("JOIN "+poolTable+" AS media_receipt_pool ON media_receipt_pool.id = media_receipt_admission.pool_id").
		Where("media_receipt_task.id = ? AND media_receipt_task.task_id = ? AND media_receipt_task.user_id = ?", task.ID, task.TaskID, task.UserId).Take(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if snapshot.PrivateData.MediaAdmissionID != snapshot.AdmissionID || snapshot.AdmissionTask != snapshot.ID || snapshot.AdmissionChannel != snapshot.ChannelId || snapshot.AdmissionState != MediaReleased || (snapshot.Status != TaskStatusFailure && snapshot.Status != TaskStatusSuccess) || snapshot.PrivateData.MediaKind != snapshot.AdmissionKind || (snapshot.AdmissionKind != MediaImage && snapshot.AdmissionKind != MediaVideo) {
		return "", false, nil
	}
	if snapshot.SendingAt > 0 && snapshot.ProviderTerminal {
		return "", true, nil
	}
	if snapshot.AdmissionID != snapshot.TaskID || snapshot.Status != TaskStatusFailure || snapshot.Quota != 0 {
		return "", false, nil
	}
	if snapshot.SendingAt != 0 || snapshot.PrivateData.UpstreamTaskID != "" || (snapshot.BillingState != "not_charged" && snapshot.BillingState != "refunded") {
		return "", false, nil
	}
	if snapshot.BillingState == "not_charged" && (snapshot.PrivateData.BillingSource == "subscription" || snapshot.PrivateData.SubscriptionId > 0) {
		return "", false, nil
	}
	return snapshot.BillingState, false, nil
}

func SaveMediaReceipt(ctx context.Context, task *Task) error {
	if task == nil || task.PrivateData.MediaAdmissionID == "" {
		return ErrMediaAdmissionUnavailable
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ? AND channel_id = ?", task.PrivateData.MediaAdmissionID, task.ID, task.ChannelId).First(&admission).Error; err != nil {
			return err
		}
		if admission.State == MediaReleased {
			return ErrMediaAdmissionUnavailable
		}
		result := tx.Model(&Task{}).Where("id = ? AND task_id = ? AND user_id = ? AND status NOT IN ?", task.ID, task.TaskID, task.UserId, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Updates(map[string]any{"private_data": task.PrivateData, "data": task.Data, "submit_time": task.SubmitTime, "quota": task.Quota})
		if result.Error != nil {
			return result.Error
		}
		// A same-value write can have RowsAffected=0 on MySQL.
		return nil
	})
}

func HoldMediaUnknown(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ?", task.PrivateData.MediaAdmissionID, task.ID).First(&admission).Error; err != nil {
			return err
		}
		if admission.State == MediaReleased || admission.SendingAt == 0 {
			return ErrMediaAdmissionUnavailable
		}
		if err := tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Updates(map[string]any{"state": MediaUnknown, "updated_at": time.Now().Unix()}).Error; err != nil {
			return err
		}
		return tx.Model(&Task{}).Where("id = ? AND status NOT IN ?", task.ID, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Updates(map[string]any{"status": TaskStatusUnknown, "finish_time": 0, "progress": "0%", "private_data": task.PrivateData, "data": task.Data, "submit_time": task.SubmitTime, "quota": task.Quota}).Error
	})
}

func releaseMediaAdmission(tx *gorm.DB, admission *MediaAdmission) error {
	result := tx.Model(&MediaAdmission{}).Where("id = ? AND state <> ?", admission.ID, MediaReleased).Updates(map[string]any{"state": MediaReleased, "updated_at": time.Now().Unix()})
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	result = tx.Model(&MediaPool{}).Where("id = ? AND occupied > 0", admission.PoolID).Updates(map[string]any{"occupied": gorm.Expr("occupied - 1"), "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrMediaAdmissionUnavailable
	}
	return nil
}

func ReleaseMediaUnsent(ctx context.Context, task *Task) error {
	return ReleaseMediaUnsentWithRefund(ctx, task, false)
}

func ReleaseMediaUnsentWithRefund(ctx context.Context, task *Task, refundRequired bool) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockMediaPolicy(tx); err != nil {
			return err
		}
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ? AND channel_id = ?", task.PrivateData.MediaAdmissionID, task.ID, task.ChannelId).First(&admission).Error; err != nil {
			return err
		}
		if admission.State == MediaReleased && admission.SendingAt == 0 && admission.BillingState == "not_charged" && task.PrivateData.UpstreamTaskID == "" {
			var persisted Task
			if err := tx.Where("id = ? AND task_id = ? AND user_id = ?", task.ID, task.TaskID, task.UserId).First(&persisted).Error; err != nil {
				return err
			}
			if persisted.PrivateData.MediaAdmissionID == admission.ID && persisted.PrivateData.UpstreamTaskID == "" && persisted.Status == TaskStatusFailure && persisted.Quota == 0 {
				return nil
			}
			return ErrMediaAdmissionUnavailable
		}
		if admission.State != MediaReserved || admission.SendingAt != 0 || task.PrivateData.UpstreamTaskID != "" || admission.BillingState == "preparing" || admission.BillingState == "settlement_uncertain" {
			return ErrMediaAdmissionUnavailable
		}
		var persisted Task
		if err := tx.Where("id = ? AND task_id = ? AND user_id = ?", task.ID, task.TaskID, task.UserId).First(&persisted).Error; err != nil {
			return err
		}
		if persisted.PrivateData.UpstreamTaskID != "" || persisted.PrivateData.MediaAdmissionID != admission.ID || persisted.Status != TaskStatusUnknown || persisted.Quota != task.Quota {
			return ErrMediaAdmissionUnavailable
		}
		billingState, status, progress, finish := "not_charged", TaskStatus(TaskStatusFailure), "100%", time.Now().Unix()
		if task.Quota > 0 || refundRequired || task.PrivateData.BillingSource == "subscription" || task.PrivateData.SubscriptionId > 0 {
			billingState, status, progress, finish = "refund_pending", TaskStatusUnknown, "0%", 0
		}
		if err := tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Update("billing_state", billingState).Error; err != nil {
			return err
		}
		if err := tx.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{"status": status, "progress": progress, "finish_time": finish, "fail_reason": "Submission was not sent"}).Error; err != nil {
			return err
		}
		return releaseMediaAdmission(tx, &admission)
	})
}

func CompleteMediaSubmission(ctx context.Context, task *Task, terminal bool) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockMediaPolicy(tx); err != nil {
			return err
		}
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ? AND channel_id = ?", task.PrivateData.MediaAdmissionID, task.ID, task.ChannelId).First(&admission).Error; err != nil {
			return err
		}
		if admission.State != MediaSending && admission.State != MediaUnknown {
			return ErrMediaAdmissionUnavailable
		}
		result := tx.Model(&Task{}).Where("id = ? AND status NOT IN ?", task.ID, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Select("*").Updates(task)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrMediaAdmissionUnavailable
		}
		if terminal {
			if task.Status != TaskStatusSuccess && task.Status != TaskStatusFailure {
				return ErrMediaAdmissionUnavailable
			}
			if err := tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Updates(map[string]any{"billing_state": "reserved", "provider_terminal": true}).Error; err != nil {
				return err
			}
			return releaseMediaAdmission(tx, &admission)
		}
		if task.Status == TaskStatusSuccess || task.Status == TaskStatusFailure {
			return ErrMediaAdmissionUnavailable
		}
		return tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Updates(map[string]any{"state": MediaActive, "billing_state": "reserved", "updated_at": time.Now().Unix()}).Error
	})
}

func UpdateMediaTaskWithStatus(ctx context.Context, task *Task, from TaskStatus, terminalConfirmed bool) (bool, error) {
	var won bool
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockMediaPolicy(tx); err != nil {
			return err
		}
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ?", task.PrivateData.MediaAdmissionID, task.ID).First(&admission).Error; err != nil {
			return err
		}
		terminal := task.Status == TaskStatusSuccess || task.Status == TaskStatusFailure
		if terminal && !terminalConfirmed {
			return ErrMediaAdmissionUnavailable
		}
		if admission.State == MediaReleased {
			return nil
		}
		result := tx.Model(task).Where("status = ?", from).Select("*").Updates(task)
		if result.Error != nil {
			return result.Error
		}
		won = result.RowsAffected > 0
		if won && terminal {
			if err := tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Update("provider_terminal", true).Error; err != nil {
				return err
			}
			return releaseMediaAdmission(tx, &admission)
		}
		return nil
	})
	return won, err
}

func MediaTaskPollable(ctx context.Context, id string) (bool, error) {
	var admission MediaAdmission
	if err := DB.WithContext(ctx).Where("id = ?", id).First(&admission).Error; err != nil {
		return false, err
	}
	return admission.State == MediaActive || admission.State == MediaUnknown, nil
}

// RefreshMediaConcurrencyPools is an explicit administrative registration and
// legacy-reconciliation operation, not a provider probe or a GET side effect.
func RefreshMediaConcurrencyPools(ctx context.Context) error {
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := mediaPolicyForUpdate(tx); err != nil {
			return err
		}
		var channels []Channel
		if err := tx.Where("status = ?", common.ChannelStatusEnabled).Limit(2001).Find(&channels).Error; err != nil {
			return err
		}
		if len(channels) > 2000 {
			return ErrMediaAdmissionUnavailable
		}
		generation := jsplugin.DefaultRegistry.Generation()
		for _, channel := range channels {
			for name := range strings.SplitSeq(channel.Models, ",") {
				modelName := strings.TrimSpace(name)
				for _, plugin := range generation.PluginsByModel(modelName) {
					if plugin.Meta.Key == "hailuo" || !mediaChannelUsesPlugin(&channel, plugin.Meta) {
						continue
					}
					kind := TaskMediaKind(&Task{Platform: constant.TaskPlatform(plugin.Meta.Key), Properties: Properties{OriginModelName: modelName}})
					if kind == "" {
						continue
					}
					for _, key := range channel.GetKeys() {
						if !mediaChannelHasKey(&channel, key, true) {
							continue
						}
						if _, err := ensureMediaPool(tx, key, kind); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	})
	if err == nil {
		InvalidatePricingCache()
	}
	return err
}

func mediaChannelUsesPlugin(channel *Channel, meta jsplugin.Meta) bool {
	if slices.Contains(meta.ChannelTypes, channel.Type) {
		return true
	}
	setting := channel.GetSetting()
	if channel.Type == constant.ChannelTypeTaskPlugin {
		return setting.TaskPluginKey == meta.Key
	}
	return channel.Type == constant.ChannelTypeNewAPI && (setting.TaskPluginKey == meta.Key || slices.Contains(setting.TaskExtendPluginKeys, meta.Key))
}

func mediaChannelSupportsKind(channel *Channel, kind string) bool {
	generation := jsplugin.DefaultRegistry.Generation()
	for name := range strings.SplitSeq(channel.Models, ",") {
		modelName := strings.TrimSpace(name)
		for _, plugin := range generation.PluginsByModel(modelName) {
			if mediaChannelUsesPlugin(channel, plugin.Meta) && TaskMediaKind(&Task{Platform: constant.TaskPlatform(plugin.Meta.Key), Properties: Properties{OriginModelName: modelName}}) == kind {
				return true
			}
		}
	}
	return false
}

func GetMediaConcurrencySnapshot(ctx context.Context) (MediaConcurrencySnapshot, error) {
	policy, err := GetMediaConcurrencyPolicy(ctx)
	if err != nil {
		return MediaConcurrencySnapshot{}, err
	}
	snapshot := MediaConcurrencySnapshot{Policy: policy, Pools: []MediaPoolSnapshot{}, ObservedAt: time.Now().Unix()}
	var channels []Channel
	if err := DB.WithContext(ctx).Limit(2001).Find(&channels).Error; err != nil || len(channels) > 2000 {
		return snapshot, ErrMediaAdmissionUnavailable
	}
	var pools []MediaPool
	if err := DB.WithContext(ctx).Order("id").Limit(2001).Find(&pools).Error; err != nil || len(pools) > 2000 {
		return snapshot, ErrMediaAdmissionUnavailable
	}
	var accounts []H3Account
	if err := DB.WithContext(ctx).Limit(101).Find(&accounts).Error; err != nil || len(accounts) > 100 {
		return snapshot, ErrMediaAdmissionUnavailable
	}
	snapshot.Policy = canonicalMediaPolicy(policy, channels, pools, accounts, false)
	linkedRows := make(map[string]MediaPoolSnapshot)
	for _, pool := range pools {
		row := MediaPoolSnapshot{PoolID: pool.ID, Label: "Media account " + pool.ID, MediaKind: pool.MediaKind, ChannelIDs: []int{}, Occupied: pool.Occupied, Reconciled: pool.Reconciled}
		row.ConfiguredLimit = mediaEffectiveLimit(policy, pool.ID, pool.MediaKind, 0)
		row.EffectiveLimit = mediaEffectiveLimit(policy, pool.ID, pool.MediaKind, pool.KnownLimit)
		if pool.KnownLimit > 0 {
			known := pool.KnownLimit
			row.KnownUpstreamLimit = &known
		}
		linkedAccountID := ""
		for _, channel := range channels {
			for _, key := range channel.GetKeys() {
				if mediaIdentityDigest(key, pool.MediaKind) == pool.IdentityDigest {
					row.ChannelIDs = append(row.ChannelIDs, channel.Id)
					row.Enabled = row.Enabled || (mediaChannelHasKey(&channel, key, true) && mediaChannelSupportsKind(&channel, pool.MediaKind))
					if pool.MediaKind == MediaVideo {
						for _, account := range accounts {
							if account.CredentialDigest == h3CredentialDigest(key) {
								linkedAccountID = account.ID
							}
						}
					}
					break
				}
			}
		}
		var unknown int64
		if err := DB.WithContext(ctx).Model(&MediaAdmission{}).Where("pool_id = ? AND state IN ?", pool.ID, []string{MediaUnknown, MediaSending}).Count(&unknown).Error; err != nil {
			return snapshot, err
		}
		row.Unknown = int(unknown)
		if linkedAccountID != "" {
			linkedRows[linkedAccountID] = row
			continue
		}
		if row.Enabled && row.Reconciled {
			row.AvailableSlots = max(0, row.EffectiveLimit-row.Occupied)
		}
		snapshot.Pools = append(snapshot.Pools, row)
	}
	for _, account := range accounts {
		known := H3AccountLimit
		row := MediaPoolSnapshot{PoolID: "h3:" + account.ID, Label: "H3 account " + account.ID, MediaKind: MediaVideo, ChannelIDs: []int{account.ChannelID}, KnownUpstreamLimit: &known, Occupied: account.Occupied, Enabled: account.Enabled, Reconciled: account.Reconciled}
		row.ConfiguredLimit = mediaEffectiveLimit(policy, row.PoolID, MediaVideo, 0)
		row.EffectiveLimit = mediaEffectiveLimit(policy, row.PoolID, MediaVideo, known)
		validChannel := false
		for _, channel := range channels {
			if channel.Id == account.ChannelID && channel.Status == common.ChannelStatusEnabled && !channel.ChannelInfo.IsMultiKey && h3CredentialDigest(channel.Key) == account.CredentialDigest && h3ConfigurationDigest(&channel) == account.ConfigurationDigest {
				validChannel = true
			}
		}
		row.Enabled = row.Enabled && validChannel
		var unknown int64
		if err := DB.WithContext(ctx).Model(&H3Admission{}).Where("account_id = ? AND state IN ?", account.ID, []string{H3Unknown, H3Sending}).Count(&unknown).Error; err != nil {
			return snapshot, err
		}
		row.Unknown = int(unknown)
		if linked, ok := linkedRows[account.ID]; ok {
			row.Occupied += linked.Occupied
			row.Unknown += linked.Unknown
			row.Reconciled = row.Reconciled && linked.Reconciled
			row.ConfiguredLimit = min(row.ConfiguredLimit, linked.ConfiguredLimit)
			row.EffectiveLimit = min(row.EffectiveLimit, linked.EffectiveLimit)
			if linked.KnownUpstreamLimit != nil {
				known = min(known, *linked.KnownUpstreamLimit)
			}
			for _, channelID := range linked.ChannelIDs {
				if !slices.Contains(row.ChannelIDs, channelID) {
					row.ChannelIDs = append(row.ChannelIDs, channelID)
				}
			}
			slices.Sort(row.ChannelIDs)
		}
		if row.Enabled && row.Reconciled {
			row.AvailableSlots = max(0, row.EffectiveLimit-row.Occupied)
		}
		snapshot.Pools = append(snapshot.Pools, row)
	}
	return snapshot, nil
}

func GetMediaClientConcurrencyHint(pluginKey, modelName, mediaKind string) *int {
	if DB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	policy, err := GetMediaConcurrencyPolicy(ctx)
	if err != nil {
		return nil
	}
	plugin, ok := jsplugin.DefaultRegistry.Generation().Get(pluginKey)
	if !ok {
		return nil
	}
	var channels []Channel
	if err := DB.WithContext(ctx).Where("status = ?", common.ChannelStatusEnabled).Limit(2001).Find(&channels).Error; err != nil || len(channels) > 2000 {
		return nil
	}
	seen := make(map[string]bool)
	total := 0
	for _, channel := range channels {
		if !mediaChannelUsesPlugin(&channel, plugin.Meta) {
			continue
		}
		var abilities int64
		if err := DB.WithContext(ctx).Model(&Ability{}).Where("channel_id = ? AND model = ? AND enabled = ?", channel.Id, modelName, true).Count(&abilities).Error; err != nil {
			return nil
		}
		if abilities == 0 {
			continue
		}
		for _, key := range channel.GetKeys() {
			if !mediaChannelHasKey(&channel, key, true) {
				continue
			}
			digest := mediaIdentityDigest(key, mediaKind)
			if seen[digest] {
				continue
			}
			seen[digest] = true
			var pool MediaPool
			err := DB.WithContext(ctx).Where("identity_digest = ?", digest).First(&pool).Error
			if err != nil {
				// No declaration before the durable ledger has reconciled this key.
				return nil
			}
			if !pool.Reconciled {
				return nil
			}
			limit, _, err := mediaSharedVideoLimit(DB.WithContext(ctx), policy, &pool, key)
			if err != nil {
				return nil
			}
			total += limit
		}
	}
	if total == 0 {
		return nil
	}
	limit := 16
	if mediaKind == MediaImage {
		limit = 64
	}
	total = min(total, limit)
	return &total
}

// MediaTaskRouteDigest excludes the selected key: a key rotation must not move
// an accepted task to a different origin or change how that key authenticates.
func MediaTaskRouteDigest(channel *Channel, platform constant.TaskPlatform) string {
	if channel == nil {
		return ""
	}
	organization := ""
	if channel.OpenAIOrganization != nil {
		organization = *channel.OpenAIOrganization
	}
	return MediaTaskRelayRouteDigest(&relaycommon.ChannelMeta{ChannelType: channel.Type, ChannelBaseUrl: channel.GetBaseURL(), ChannelSetting: channel.GetSetting(), ChannelOtherSettings: channel.GetOtherSettings(), HeadersOverride: channel.GetHeaderOverride(), ParamOverride: channel.GetParamOverride(), Organization: organization}, platform)
}

func MediaTaskRelayRouteDigest(meta *relaycommon.ChannelMeta, platform constant.TaskPlatform) string {
	plugin, known := mediaTaskPlugin(platform)
	if !known {
		return ""
	}
	return MediaTaskPluginRouteDigest(meta, plugin.Meta)
}

// The request/adaptor passes its pinned plugin metadata, so a concurrent
// registry replacement cannot stamp a different auth contract onto this route.
func MediaTaskPluginRouteDigest(meta *relaycommon.ChannelMeta, plugin jsplugin.Meta) string {
	if meta == nil || plugin.Key == "" {
		return ""
	}
	baseURL := meta.ChannelBaseUrl
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(meta.ChannelType)
	}
	if baseURL == "" {
		return ""
	}
	headers := meta.HeadersOverride
	if len(headers) == 0 {
		headers = nil
	}
	value := struct {
		Type                         int
		BaseURL, Proxy, Organization string
		Bindings                     []string
		TLSInsecureSkipVerify        bool
		Headers                      map[string]any
		Plugin                       string
		Auth                         jsplugin.AuthMeta
		AllowedHosts                 []string
	}{meta.ChannelType, baseURL, meta.ChannelSetting.Proxy, meta.Organization, meta.ChannelSetting.TaskPluginBindings(), meta.ChannelSetting.TLSInsecureSkipVerify, headers, plugin.Key, plugin.Auth, plugin.AllowedHosts}
	data, err := common.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func MediaTaskRouteMatches(task *Task, channel *Channel) bool {
	return task != nil && strings.TrimSpace(task.PrivateData.Key) != "" && task.PrivateData.RouteDigest != "" && task.PrivateData.RouteDigest == MediaTaskRouteDigest(channel, task.Platform)
}
