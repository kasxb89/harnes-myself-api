package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
)

var ErrH3Capacity = errors.New("H3 account capacity exhausted")
var ErrH3AdmissionUnavailable = errors.New("H3 admission is unavailable")

const H3AccountLimit = 4
const H3Reserved = "reserved"
const H3Sending = "sending"
const H3Active = "active"
const H3Unknown = "unknown"
const H3Released = "released"

// H3Account is an explicit administrator assertion of one upstream account.
// Different credentials alone do not establish independent provider accounts.
// Digests are private equality checks, never public identities or API output.
type H3Account struct {
	ConfigurationDigest string `gorm:"type:varchar(64)" json:"-"`
	ID                  string `gorm:"primaryKey;type:varchar(64)" json:"account_id"`
	ChannelID           int    `gorm:"uniqueIndex" json:"channel_id"`
	CredentialDigest    string `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	Limit               int    `json:"limit"`
	Occupied            int    `json:"occupied"`
	Enabled             bool   `json:"enabled"`
	Reconciled          bool   `json:"reconciled"`
	UpdatedAt           int64  `json:"updated_at"`
}

// H3Admission is a durable send fence, not a time-expiring lease. No timer may
// release sending/unknown work: the provider may still be rendering it.
type H3Admission struct {
	SendingAt    int64  `json:"-"`
	ID           string `gorm:"primaryKey;type:varchar(191)" json:"-"`
	TaskDBID     int64  `gorm:"uniqueIndex" json:"-"`
	AccountID    string `gorm:"type:varchar(64);index" json:"-"`
	ChannelID    int    `gorm:"index" json:"-"`
	State        string `gorm:"type:varchar(20);index" json:"-"`
	BillingState string `gorm:"type:varchar(24)" json:"-"`
	CreatedAt    int64  `json:"-"`
	UpdatedAt    int64  `json:"-"`
}

func MigrateH3Admission(db *gorm.DB) error {
	return db.AutoMigrate(&H3Account{}, &H3Admission{})
}

func h3CredentialDigest(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

// ProvisionH3Account is callable by a private deployment operator while the
// old H3 admission route is blocked. It never enables traffic. It is idempotent
// only for the same channel, credential and explicit account identity.
func ProvisionH3Account(ctx context.Context, accountID string, channelID int) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(accountID) || channelID <= 0 {
		return ErrH3AdmissionUnavailable
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := tx.First(&channel, channelID).Error; err != nil {
			return err
		}
		if channel.ChannelInfo.IsMultiKey || len(channel.GetKeys()) != 1 || strings.TrimSpace(channel.Key) == "" {
			return ErrH3AdmissionUnavailable
		}
		parsed, err := url.Parse(channel.GetBaseURL())
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return ErrH3AdmissionUnavailable
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "metaso.cn" && host != "www.metaso.cn" && host != "api.metaso.cn" {
			return ErrH3AdmissionUnavailable
		}
		if channel.Type != constant.ChannelTypeMiniMax && !(channel.Type == constant.ChannelTypeTaskPlugin && channel.GetSetting().TaskPluginKey == "hailuo") {
			return ErrH3AdmissionUnavailable
		}
		desired := H3Account{ID: accountID, ChannelID: channelID, CredentialDigest: h3CredentialDigest(channel.Key), ConfigurationDigest: h3ConfigurationDigest(&channel), Limit: H3AccountLimit, UpdatedAt: time.Now().Unix()}
		var existing H3Account
		err = tx.Where("id = ? OR channel_id = ? OR credential_digest = ?", accountID, channelID, desired.CredentialDigest).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&desired).Error
		}
		if err != nil {
			return err
		}
		if existing.ID != accountID || existing.ChannelID != channelID || existing.CredentialDigest != desired.CredentialDigest || existing.ConfigurationDigest != desired.ConfigurationDigest || existing.Limit != H3AccountLimit {
			return ErrH3AdmissionUnavailable
		}
		return nil
	})
}

// ReconcileH3Account imports all unresolved persisted rows, including UNKNOWN
// and inconsistent progress=100 rows. It never clips historical occupancy to
// four and never sends, charges, refunds, or clears an uncertain reservation.
// The caller must quiesce old hosts before this operation.
func ReconcileH3Account(ctx context.Context, accountID string) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account H3Account
		if err := lockForUpdate(tx).Where("id = ?", accountID).First(&account).Error; err != nil {
			return err
		}
		if account.Enabled {
			return ErrH3AdmissionUnavailable
		}
		// SQLite obtains a write lock before observing the task/admission set.
		if err := tx.Model(&H3Account{}).Where("id = ? AND enabled = ?", account.ID, false).Update("updated_at", time.Now().Unix()).Error; err != nil {
			return err
		}
		var tasks []Task
		if err := tx.Where("channel_id = ? AND status <> ?", account.ChannelID, TaskStatusSuccess).Limit(10001).Find(&tasks).Error; err != nil {
			return err
		}
		if len(tasks) > 10000 {
			return ErrH3AdmissionUnavailable
		}
		var channel Channel
		if err := tx.First(&channel, account.ChannelID).Error; err != nil {
			return err
		}
		if channel.ChannelInfo.IsMultiKey || h3CredentialDigest(channel.Key) != account.CredentialDigest || h3ConfigurationDigest(&channel) != account.ConfigurationDigest {
			return ErrH3AdmissionUnavailable
		}
		for _, task := range tasks {
			if task.Status == TaskStatusFailure && !H3LocalFailureUncertain(&task) {
				continue
			}
			var existing H3Admission
			err := tx.Where("task_db_id = ?", task.ID).First(&existing).Error
			if err == nil {
				if existing.AccountID != account.ID {
					return ErrH3AdmissionUnavailable
				}
				if existing.State == H3Sending && task.PrivateData.UpstreamTaskID != "" {
					if err := tx.Model(&H3Admission{}).Where("id = ? AND state = ?", existing.ID, H3Sending).Updates(map[string]any{"state": H3Unknown, "updated_at": time.Now().Unix()}).Error; err != nil {
						return err
					}
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// The dedicated channel is asserted to be an H3 account by provisioning.
			id := task.TaskID
			if id == "" {
				id = fmt.Sprintf("legacy-h3-%d", task.ID)
			}
			admission := H3Admission{ID: id, TaskDBID: task.ID, AccountID: account.ID, ChannelID: account.ChannelID, State: H3Active, BillingState: "legacy", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}
			if task.PrivateData.UpstreamTaskID == "" && strings.HasPrefix(task.TaskID, "task_") {
				admission.State = H3Unknown
			}
			if task.Status == TaskStatusFailure {
				admission.State = H3Unknown
				admission.BillingState = "legacy_uncertain"
				task.Status = TaskStatusUnknown
				task.Progress = "0%"
				task.FinishTime = 0
			}
			if err := tx.Create(&admission).Error; err != nil {
				return err
			}
			task.PrivateData.H3AdmissionID = id
			if task.PrivateData.Key == "" {
				task.PrivateData.Key = channel.Key
			}
			if err := tx.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{"private_data": task.PrivateData, "status": task.Status, "progress": task.Progress, "finish_time": task.FinishTime}).Error; err != nil {
				return err
			}
		}
		var occupied int64
		if err := tx.Model(&H3Admission{}).Where("account_id = ? AND state <> ?", account.ID, H3Released).Count(&occupied).Error; err != nil {
			return err
		}
		return tx.Model(&H3Account{}).Where("id = ?", account.ID).Updates(map[string]any{"occupied": occupied, "reconciled": true, "updated_at": time.Now().Unix()}).Error
	})
}

func SetH3AccountEnabled(ctx context.Context, accountID string, enabled bool) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the same stable row to serialize operational enable operations.
		var first H3Account
		if err := lockForUpdate(tx).Order("id").First(&first).Error; err != nil {
			return err
		}
		if err := tx.Model(&H3Account{}).Where("id = ?", first.ID).Update("updated_at", time.Now().Unix()).Error; err != nil {
			return err
		}
		var account H3Account
		if err := tx.Where("id = ?", accountID).First(&account).Error; err != nil {
			return err
		}
		if enabled {
			if !account.Reconciled || account.Limit != H3AccountLimit {
				return ErrH3AdmissionUnavailable
			}
			var count int64
			if err := tx.Model(&H3Account{}).Where("enabled = ? AND id <> ?", true, accountID).Count(&count).Error; err != nil {
				return err
			}
			if count >= 4 {
				return ErrH3AdmissionUnavailable
			}
			var channel Channel
			if err := tx.First(&channel, account.ChannelID).Error; err != nil {
				return err
			}
			if h3CredentialDigest(channel.Key) != account.CredentialDigest || h3ConfigurationDigest(&channel) != account.ConfigurationDigest {
				return ErrH3AdmissionUnavailable
			}
		}
		return tx.Model(&H3Account{}).Where("id = ?", accountID).Update("enabled", enabled).Error
	})
}

// ExportH3AccountChecks deliberately excludes credentials, digests, URLs and
// task payloads. It is suitable for a deployment receipt, not proof of health.
func ExportH3AccountChecks(ctx context.Context) ([]H3Account, error) {
	var rows []H3Account
	err := DB.WithContext(ctx).Select("id", "channel_id", "limit", "occupied", "enabled", "reconciled", "updated_at").Order("id").Limit(100).Find(&rows).Error
	return rows, err
}

// AcquireH3Admission uses the primary DB as the one authority across processes.
// The conditional increment and unique admission+task INSERT commit together.
func AcquireH3Admission(ctx context.Context, task *Task, selectedKey string) error {
	return acquireH3Admission(DB.WithContext(ctx), task, selectedKey)
}
func acquireH3Admission(db *gorm.DB, task *Task, selectedKey string) error {
	if task == nil || task.TaskID == "" || task.ChannelId <= 0 {
		return ErrH3AdmissionUnavailable
	}
	var account H3Account
	if err := db.Where("channel_id = ?", task.ChannelId).First(&account).Error; err != nil {
		return ErrH3AdmissionUnavailable
	}
	if !account.Enabled || !account.Reconciled || account.Limit != H3AccountLimit || account.CredentialDigest != h3CredentialDigest(selectedKey) {
		return ErrH3AdmissionUnavailable
	}
	return db.Transaction(func(tx *gorm.DB) error {
		// First transactional statement is the write. SQLite must not upgrade an
		// earlier shared read snapshot while another process already holds a write.
		result := tx.Model(&H3Account{}).Where("id = ? AND enabled = ? AND reconciled = ? AND occupied < ?", account.ID, true, true, H3AccountLimit).Updates(map[string]any{"occupied": gorm.Expr("occupied + 1"), "updated_at": time.Now().Unix()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrH3Capacity
		}
		var current H3Account
		if err := tx.Where("id = ?", account.ID).First(&current).Error; err != nil {
			return err
		}
		if current.CredentialDigest != account.CredentialDigest || current.ConfigurationDigest != account.ConfigurationDigest {
			return ErrH3AdmissionUnavailable
		}
		var channel Channel
		if err := tx.First(&channel, account.ChannelID).Error; err != nil {
			return err
		}
		if channel.Status != common.ChannelStatusEnabled || channel.ChannelInfo.IsMultiKey || account.ConfigurationDigest != h3ConfigurationDigest(&channel) || account.CredentialDigest != h3CredentialDigest(channel.Key) {
			return ErrH3AdmissionUnavailable
		}
		task.Status = TaskStatusUnknown
		task.SubmitTime = 0
		task.Progress = "0%"
		task.PrivateData.Key = selectedKey
		task.PrivateData.H3AdmissionID = task.TaskID
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		reservation := H3Admission{ID: task.TaskID, TaskDBID: task.ID, AccountID: account.ID, ChannelID: task.ChannelId, State: H3Reserved, BillingState: "not_started", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}
		return tx.Create(&reservation).Error
	})
}

func SaveH3BillingReservation(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		update := tx.Model(&H3Admission{}).Where("id = ? AND state = ?", task.PrivateData.H3AdmissionID, H3Reserved).Updates(map[string]any{"billing_state": "reserved", "updated_at": time.Now().Unix()})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrH3AdmissionUnavailable
		}
		return tx.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{"private_data": task.PrivateData, "quota": task.Quota}).Error
	})
}

func MarkH3Sending(ctx context.Context, id string) error {
	result := DB.WithContext(ctx).Model(&H3Admission{}).Where("id = ? AND state = ? AND billing_state = ?", id, H3Reserved, "reserved").Updates(map[string]any{"state": H3Sending, "sending_at": time.Now().Unix(), "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrH3AdmissionUnavailable
	}
	return nil
}

func HoldH3Unknown(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&H3Admission{}).Where("id = ? AND state IN ?", task.PrivateData.H3AdmissionID, []string{H3Sending, H3Active, H3Unknown}).Updates(map[string]any{"state": H3Unknown, "updated_at": time.Now().Unix()}).Error; err != nil {
			return err
		}
		return tx.Model(&Task{}).Where("id = ? AND status NOT IN ?", task.ID, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Updates(map[string]any{"status": TaskStatusUnknown, "finish_time": 0, "progress": "0%", "private_data": task.PrivateData, "data": task.Data, "submit_time": task.SubmitTime}).Error
	})
}

// CompleteH3Submission replaces the pre-created row. A confirmed immediate
// terminal releases in the same transaction; uncertain results never do.
func CompleteH3Submission(ctx context.Context, task *Task, terminal bool) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admission H3Admission
		if err := lockForUpdate(tx).Where("id = ?", task.PrivateData.H3AdmissionID).First(&admission).Error; err != nil {
			return err
		}
		if admission.State != H3Sending && admission.State != H3Unknown {
			return ErrH3AdmissionUnavailable
		}
		update := tx.Model(&Task{}).Where("id = ? AND status NOT IN ?", task.ID, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Select("*").Updates(task)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrH3AdmissionUnavailable
		}
		if terminal {
			if err := tx.Model(&H3Admission{}).Where("id = ?", admission.ID).Update("billing_state", "reserved").Error; err != nil {
				return err
			}
			return releaseH3Admission(tx, &admission)
		}
		return tx.Model(&H3Admission{}).Where("id = ?", admission.ID).Updates(map[string]any{"state": H3Active, "billing_state": "reserved", "updated_at": time.Now().Unix()}).Error
	})
}

func UpdateH3TaskWithStatus(ctx context.Context, task *Task, from TaskStatus, terminalConfirmed bool) (bool, error) {
	var won bool
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(task).Where("status = ?", from).Select("*").Updates(task)
		if result.Error != nil {
			return result.Error
		}
		won = result.RowsAffected > 0
		if !won || !terminalConfirmed {
			return nil
		}
		var admission H3Admission
		if err := lockForUpdate(tx).Where("id = ?", task.PrivateData.H3AdmissionID).First(&admission).Error; err != nil {
			return err
		}
		return releaseH3Admission(tx, &admission)
	})
	return won, err
}

func ReleaseH3BeforeSend(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admission H3Admission
		if err := lockForUpdate(tx).Where("id = ?", task.PrivateData.H3AdmissionID).First(&admission).Error; err != nil {
			return err
		}
		if admission.State != H3Reserved {
			return ErrH3AdmissionUnavailable
		}
		billingState := "not_charged"
		taskStatus := TaskStatusFailure
		finishTime := time.Now().Unix()
		progress := "100%"
		if task.Quota > 0 {
			billingState = "refund_pending"
			taskStatus = TaskStatusUnknown
			finishTime = 0
			progress = "0%"
		}
		if err := tx.Model(&H3Admission{}).Where("id = ?", admission.ID).Update("billing_state", billingState).Error; err != nil {
			return err
		}
		if err := tx.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{"status": taskStatus, "finish_time": finishTime, "progress": progress, "fail_reason": "Submission was not sent"}).Error; err != nil {
			return err
		}
		return releaseH3Admission(tx, &admission)
	})
}

func releaseH3Admission(tx *gorm.DB, admission *H3Admission) error {
	result := tx.Model(&H3Admission{}).Where("id = ? AND state <> ?", admission.ID, H3Released).Updates(map[string]any{"state": H3Released, "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	counter := tx.Model(&H3Account{}).Where("id = ? AND occupied > 0", admission.AccountID).Updates(map[string]any{"occupied": gorm.Expr("occupied - 1"), "updated_at": time.Now().Unix()})
	if counter.Error != nil {
		return counter.Error
	}
	if counter.RowsAffected != 1 {
		return ErrH3AdmissionUnavailable
	}
	return nil
}

// GuardH3ChannelChange fails closed for an enabled account or unresolved work.
// Operators disable, reconcile/drain, then reprovision a new identity instead
// of mutating the account behind tasks that must poll the original provider.
func GuardH3ChannelChange(ctx context.Context, channelID int) error {
	var channel Channel
	if err := DB.WithContext(ctx).Select("id", "type", "setting").Where("id = ?", channelID).First(&channel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if channel.Type != constant.ChannelTypeMiniMax && !(channel.Type == constant.ChannelTypeTaskPlugin && channel.GetSetting().TaskPluginKey == "hailuo") {
		return nil
	}
	var count int64
	if err := DB.WithContext(ctx).Model(&H3Account{}).Where("channel_id = ? AND (enabled = ? OR occupied > 0)", channelID, true).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("H3 account must be disabled and have no unresolved tasks before changing or deleting its channel")
	}
	return nil
}

func SetH3BillingState(ctx context.Context, id, state string) error {
	result := DB.WithContext(ctx).Model(&H3Admission{}).Where("id = ? AND state = ?", id, H3Reserved).Updates(map[string]any{"billing_state": state, "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrH3AdmissionUnavailable
	}
	return nil
}

// SaveH3Receipt preserves a known provider identity before any adjusted-price,
// client-cancellation, or presentation failure can interrupt the controller.
func SaveH3Receipt(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Model(&Task{}).Where("id = ? AND status NOT IN ?", task.ID, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Updates(map[string]any{"private_data": task.PrivateData, "data": task.Data, "submit_time": task.SubmitTime}).Error
}

func H3TaskPollable(ctx context.Context, id string) (bool, error) {
	var row H3Admission
	if err := DB.WithContext(ctx).Select("state").Where("id = ?", id).First(&row).Error; err != nil {
		return false, err
	}
	return row.State == H3Active || row.State == H3Unknown, nil
}

// H3LocalFailureUncertain treats every legacy FAILURE as uncertain unless
// its persisted vendor envelope proves the matching original task terminated.
// Error messages, HTTP mappings and a local finish timestamp are not proof.
func H3LocalFailureUncertain(task *Task) bool {
	if task == nil || len(task.Data) == 0 {
		return true
	}
	var envelope struct {
		Error any `json:"error"`
		Task  *struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
	}
	if err := common.Unmarshal(task.Data, &envelope); err != nil || envelope.Error != nil || envelope.Task == nil {
		return true
	}
	return envelope.Task.ID == "" || envelope.Task.ID != task.GetUpstreamTaskID() || (envelope.Task.Status != "failed" && envelope.Task.Status != "cancelled")
}

// GetH3ClientConcurrencyHint returns the configured operating ceiling only.
// It is never a reservation, health assertion, or currently available slots.
func GetH3ClientConcurrencyHint() *int {
	if DB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var accounts []H3Account
	if err := DB.WithContext(ctx).Where("enabled = ? AND reconciled = ?", true, true).Limit(5).Find(&accounts).Error; err != nil || len(accounts) == 0 || len(accounts) > 4 {
		return nil
	}
	total := 0
	for _, account := range accounts {
		if account.Limit != H3AccountLimit {
			return nil
		}
		var channel Channel
		if err := DB.WithContext(ctx).Where("id = ? AND status = ?", account.ChannelID, common.ChannelStatusEnabled).First(&channel).Error; err != nil {
			return nil
		}
		if channel.ChannelInfo.IsMultiKey || h3CredentialDigest(channel.Key) != account.CredentialDigest || h3ConfigurationDigest(&channel) != account.ConfigurationDigest {
			return nil
		}
		exact := false
		for name := range strings.SplitSeq(channel.Models, ",") {
			if strings.TrimSpace(name) == "MiniMax-H3" {
				exact = true
			}
		}
		if !exact {
			return nil
		}
		var abilities int64
		if err := DB.WithContext(ctx).Model(&Ability{}).Where("channel_id = ? AND model = ? AND enabled = ?", account.ChannelID, "MiniMax-H3", true).Count(&abilities).Error; err != nil || abilities == 0 {
			return nil
		}
		total += H3AccountLimit
	}
	return &total
}

// H3AdmissionStore permits distinct processes/connections to share the same
// primary-store transaction contract. It never holds an in-memory slot count.
type H3AdmissionStore struct{ DB *gorm.DB }

func (store H3AdmissionStore) Acquire(ctx context.Context, task *Task, key string) error {
	if store.DB == nil {
		return ErrH3AdmissionUnavailable
	}
	return acquireH3Admission(store.DB.WithContext(ctx), task, key)
}

func H3BillingNeedsReview(ctx context.Context, id string) (bool, error) {
	var row H3Admission
	if err := DB.WithContext(ctx).Select("billing_state").Where("id = ?", id).First(&row).Error; err != nil {
		return true, err
	}
	return row.BillingState == "preparing" || row.BillingState == "legacy_uncertain" || row.BillingState == "settlement_uncertain", nil
}
func MarkH3BillingUncertain(ctx context.Context, id string) error {
	result := DB.WithContext(ctx).Model(&H3Admission{}).Where("id = ?", id).Update("billing_state", "settlement_uncertain")
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrH3AdmissionUnavailable
	}
	return nil
}

type H3AdmissionAudit struct {
	PublicTaskID string `json:"task_id"`
	AccountID    string `json:"account_id"`
	ChannelID    int    `json:"channel_id"`
	State        string `json:"state"`
	BillingState string `json:"billing_state"`
	SendingAt    int64  `json:"sending_at"`
}

func ExportH3AdmissionAudit(ctx context.Context) ([]H3AdmissionAudit, error) {
	var rows []H3AdmissionAudit
	err := DB.WithContext(ctx).Model(&H3Admission{}).Select("id as public_task_id", "account_id", "channel_id", "state", "billing_state", "sending_at").Order("created_at desc").Limit(1000).Scan(&rows).Error
	return rows, err
}

func h3ConfigurationDigest(channel *Channel) string {
	value := struct {
		Type    int
		BaseURL string
		Mapping string
		Models  string
		Setting any
	}{channel.Type, channel.GetBaseURL(), channel.GetModelMapping(), channel.Models, channel.GetSetting()}
	data, err := common.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func IsH3Task(task *Task) bool {
	if task == nil {
		return false
	}
	name := task.Properties.UpstreamModelName
	if name == "" {
		name = task.Properties.OriginModelName
	}
	return strings.EqualFold(name, "MiniMax-H3") && (task.Platform == "hailuo" || task.Platform == "35")
}

// RejectH3Submission is used only for an evidence-checked authentication
// rejection of the create POST. Query authentication failures never call it.
func RejectH3Submission(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admission H3Admission
		if err := lockForUpdate(tx).Where("id = ?", task.PrivateData.H3AdmissionID).First(&admission).Error; err != nil {
			return err
		}
		if admission.State != H3Sending {
			return ErrH3AdmissionUnavailable
		}
		var persisted Task
		if err := tx.First(&persisted, task.ID).Error; err != nil {
			return err
		}
		if task.PrivateData.UpstreamTaskID != "" || persisted.PrivateData.UpstreamTaskID != "" || persisted.Status != TaskStatusUnknown {
			return ErrH3AdmissionUnavailable
		}
		// Keep the task UNKNOWN until the original refund is confirmed. A crash
		// after capacity release must not make GET claim a financial outcome.
		if err := tx.Model(&H3Admission{}).Where("id = ?", admission.ID).Update("billing_state", "refund_pending").Error; err != nil {
			return err
		}
		return releaseH3Admission(tx, &admission)
	})
}

// ConfirmH3Refund only records an already completed refund. It never credits
// money and is not a retry mechanism for the non-idempotent funding operation.
func ConfirmH3Refund(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admission H3Admission
		if err := lockForUpdate(tx).Where("id = ?", task.PrivateData.H3AdmissionID).First(&admission).Error; err != nil {
			return err
		}
		if admission.State != H3Released {
			return ErrH3AdmissionUnavailable
		}
		if admission.BillingState == "refunded" {
			return nil
		}
		if admission.BillingState != "refund_pending" {
			return ErrH3AdmissionUnavailable
		}
		var persisted Task
		if err := tx.First(&persisted, admission.TaskDBID).Error; err != nil {
			return err
		}
		if persisted.Status != TaskStatusUnknown && persisted.Status != TaskStatusFailure {
			return ErrH3AdmissionUnavailable
		}
		updates := map[string]any{"quota": 0}
		if persisted.Status == TaskStatusUnknown {
			updates["status"] = TaskStatusFailure
			updates["finish_time"] = time.Now().Unix()
			updates["progress"] = "100%"
			reason := persisted.FailReason
			if reason == "" {
				reason = "Upstream authentication rejected the video submission"
			}
			updates["fail_reason"] = reason
		}
		update := tx.Model(&Task{}).Where("id = ? AND status = ?", admission.TaskDBID, persisted.Status).Updates(updates)
		if update.Error != nil {
			return update.Error
		}
		if err := tx.Model(&H3Admission{}).Where("id = ?", admission.ID).Update("billing_state", "refunded").Error; err != nil {
			return err
		}
		task.Quota = 0
		return nil
	})
}
