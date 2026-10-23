package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrClientWorkloadConflict     = errors.New("client workload identity or sequence conflict")
	ErrClientWorkloadRuntimeEnded = errors.New("client workload runtime has ended")
)

// These are current observations and immutable identities, not an execution
// queue. None of these rows authorizes a provider call or changes a task.
type ClientWorkloadRuntime struct {
	Key              string `gorm:"type:char(64);primaryKey" json:"-"`
	UserID           int    `gorm:"not null;index" json:"-"`
	RuntimeSessionID string `gorm:"type:varchar(36);not null" json:"-"`
	PresenceID       int    `gorm:"not null;index" json:"-"`
	LoginSID         string `gorm:"type:varchar(64);not null;index" json:"-"`
	AcceptedSequence int64  `gorm:"not null" json:"-"`
	PayloadDigest    string `gorm:"type:varchar(64);not null" json:"-"`
	AcceptedAt       int64  `gorm:"not null;index" json:"-"`
	ChatActivity     string `gorm:"type:varchar(16);not null" json:"-"`
	ProjectRun       string `gorm:"type:varchar(16);not null" json:"-"`
	StoryboardTask   string `gorm:"type:varchar(16);not null" json:"-"`
	LocalOperation   string `gorm:"type:varchar(16);not null" json:"-"`
	Shots            string `gorm:"type:varchar(16);not null" json:"-"`
}

type ClientWorkloadRecord struct {
	Key              string `gorm:"type:char(64);primaryKey" json:"-"`
	UserID           int    `gorm:"not null;index" json:"-"`
	ActivityID       string `gorm:"type:varchar(128);not null" json:"-"`
	Kind             string `gorm:"type:varchar(24);not null" json:"-"`
	ChatSessionID    string `gorm:"type:varchar(128);not null" json:"-"`
	ProjectID        string `gorm:"type:varchar(128);not null" json:"-"`
	ParentActivityID string `gorm:"type:varchar(128);not null" json:"-"`
	AttemptID        string `gorm:"type:varchar(36);not null" json:"-"`
	APITaskID        string `gorm:"type:varchar(256);not null" json:"-"`
	APITaskRowID     int64  `gorm:"not null" json:"-"`
	EndedAt          int64  `gorm:"not null" json:"-"`
	Outcome          string `gorm:"type:varchar(16);not null" json:"-"`
}

type ClientWorkloadObservation struct {
	Key              string   `gorm:"type:char(64);primaryKey" json:"-"`
	RuntimeKey       string   `gorm:"type:char(64);not null;index:idx_workload_runtime_sequence,priority:1" json:"-"`
	ActivityKey      string   `gorm:"type:char(64);not null;index" json:"-"`
	IncludedSequence int64    `gorm:"not null;index:idx_workload_runtime_sequence,priority:2" json:"-"`
	WorkState        string   `gorm:"type:varchar(16);not null" json:"-"`
	Phase            string   `gorm:"type:varchar(16);not null" json:"-"`
	LocalJobID       string   `gorm:"type:varchar(128);not null" json:"-"`
	ShotIDs          LongText `gorm:"not null" json:"-"`
	ObservedAt       int64    `gorm:"not null" json:"-"`
}

type ClientWorkloadShot struct {
	Key              string `gorm:"type:char(64);primaryKey" json:"-"`
	RuntimeKey       string `gorm:"type:char(64);not null;index:idx_workload_shot_runtime_sequence,priority:1" json:"-"`
	CanonicalKey     string `gorm:"type:char(64);not null;index" json:"-"`
	ProjectID        string `gorm:"type:varchar(128);not null" json:"-"`
	ShotID           string `gorm:"type:varchar(128);not null" json:"-"`
	IncludedSequence int64  `gorm:"not null;index:idx_workload_shot_runtime_sequence,priority:2" json:"-"`
}

type ClientWorkloadAck struct {
	SchemaVersion            int    `json:"schema_version"`
	Recorded                 bool   `json:"recorded"`
	Disposition              string `json:"disposition"`
	AcceptedSequence         int64  `json:"accepted_sequence"`
	ServerTime               int64  `json:"server_time"`
	HeartbeatIntervalSeconds int64  `json:"heartbeat_interval_seconds"`
	WorkloadTimeoutSeconds   int64  `json:"workload_timeout_seconds"`
}

// Framing makes opaque identity tuples unambiguous. Fixed lowercase hex unique
// keys preserve exact case even with MySQL's default case-insensitive collation.
func clientWorkloadKey(parts ...string) string {
	framed := ""
	for _, part := range parts {
		framed += strconv.Itoa(len(part)) + ":" + part
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(framed)))
}

func (r ClientWorkloadRuntime) collections() ClientWorkloadCollections {
	return ClientWorkloadCollections{ChatActivity: r.ChatActivity, ProjectRun: r.ProjectRun, StoryboardTask: r.StoryboardTask, LocalOperation: r.LocalOperation, Shots: r.Shots}
}

func clientWorkloadCollection(c ClientWorkloadCollections, kind string) string {
	switch kind {
	case "chat_activity":
		return c.ChatActivity
	case "project_run":
		return c.ProjectRun
	case "storyboard_task":
		return c.StoryboardTask
	case "local_operation":
		return c.LocalOperation
	case "shots":
		return c.Shots
	}
	return "not_collected"
}

// RecordClientWorkload locks only telemetry guard rows for publication. Existing
// presence and interactive-session rows are read/locked, never renewed here.
func RecordClientWorkload(identity AuthSessionIdentity, snapshot ClientWorkloadSnapshot, now int64) (ClientWorkloadAck, error) {
	ack := ClientWorkloadAck{SchemaVersion: 1, ServerTime: now, HeartbeatIntervalSeconds: ClientActivityHeartbeatSeconds, WorkloadTimeoutSeconds: ClientActivityTimeoutSeconds}
	digest, err := snapshot.Digest()
	if err != nil {
		return ack, ErrClientWorkloadInvalid
	}
	userKey := strconv.Itoa(identity.UserID)
	runtimeKey := clientWorkloadKey(userKey, snapshot.RuntimeSessionID)
	err = DB.Transaction(func(tx *gorm.DB) error {
		budgets, err := lockClientWorkloadBudgets(tx, identity.UserID)
		if err != nil {
			return err
		}
		delta := int64(0)
		candidate := ClientWorkloadRuntime{Key: runtimeKey, UserID: identity.UserID, RuntimeSessionID: snapshot.RuntimeSessionID, LoginSID: identity.SessionID}
		var existingRuntime int64
		if err := tx.Model(&ClientWorkloadRuntime{}).Where(clause.Eq{Column: "key", Value: runtimeKey}).Count(&existingRuntime).Error; err != nil {
			return err
		}
		insertRuntime := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate)
		if insertRuntime.Error != nil {
			return insertRuntime.Error
		}
		if existingRuntime == 0 {
			delta += clientWorkloadRuntimeReservedBytes
		}
		// Obtain the SQLite write reservation before reading a snapshot; other
		// dialects acquire the same row lock, without touching presence timestamps.
		if err := tx.Model(&ClientWorkloadRuntime{}).Where(clause.Eq{Column: "key", Value: runtimeKey}).UpdateColumn("accepted_sequence", gorm.Expr("accepted_sequence")).Error; err != nil {
			return err
		}
		var runtime ClientWorkloadRuntime
		if err := lockForUpdate(tx).Where(clause.Eq{Column: "key", Value: runtimeKey}).Take(&runtime).Error; err != nil {
			return err
		}
		if runtime.UserID != identity.UserID || runtime.RuntimeSessionID != snapshot.RuntimeSessionID || runtime.LoginSID != identity.SessionID {
			return ErrClientWorkloadInvalid
		}
		var presence ClientActivitySession
		if err := lockForUpdate(tx).Where("user_id = ? AND session_id = ?", identity.UserID, snapshot.RuntimeSessionID).Take(&presence).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrClientWorkloadInvalid
			}
			return err
		}
		if presence.EndedAt != 0 {
			return ErrClientWorkloadRuntimeEnded
		}
		var session UserSession
		if err := lockForUpdate(tx).Where("sid = ? AND user_id = ?", identity.SessionID, identity.UserID).Take(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserSessionInactive
			}
			return err
		}
		var user User
		if err := tx.Select("id", "status", "auth_version").Where("id = ?", identity.UserID).Take(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrClientWorkloadInvalid
			}
			return err
		}
		if session.SID != identity.SessionID || session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now || session.Version != identity.SessionVersion || session.UserAuthVersion != identity.UserAuthVersion || user.AuthVersion != identity.UserAuthVersion || user.Status != common.UserStatusEnabled {
			return ErrUserSessionInactive
		}
		ack.AcceptedSequence = runtime.AcceptedSequence
		if snapshot.Sequence < runtime.AcceptedSequence {
			ack.Disposition = "out_of_order"
			return nil
		}
		if snapshot.Sequence == runtime.AcceptedSequence {
			if digest != runtime.PayloadDigest {
				return ErrClientWorkloadConflict
			}
			ack.Disposition = "duplicate"
			return nil
		}
		inputs := slices.Clone(snapshot.Activities)
		slices.SortFunc(inputs, func(a, b ClientWorkloadActivityInput) int {
			return strings.Compare(clientWorkloadKey(userKey, a.ActivityID), clientWorkloadKey(userKey, b.ActivityID))
		})
		records := make(map[string]ClientWorkloadRecord, len(inputs))
		for _, input := range inputs {
			key := clientWorkloadKey(userKey, input.ActivityID)
			row := ClientWorkloadRecord{Key: key, UserID: identity.UserID, ActivityID: input.ActivityID, Kind: input.Kind}
			var existingRecord int64
			if err := tx.Model(&ClientWorkloadRecord{}).Where(clause.Eq{Column: "key", Value: key}).Count(&existingRecord).Error; err != nil {
				return err
			}
			insertRecord := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if insertRecord.Error != nil {
				return insertRecord.Error
			}
			if existingRecord == 0 {
				delta += clientWorkloadRecordReservedBytes
			}
			if err := lockForUpdate(tx).Where(clause.Eq{Column: "key", Value: key}).Take(&row).Error; err != nil {
				return err
			}
			if row.UserID != identity.UserID || row.ActivityID != input.ActivityID || row.Kind != input.Kind {
				return ErrClientWorkloadConflict
			}
			if row.EndedAt != 0 && (input.WorkState != "ended" || input.Outcome != row.Outcome) {
				return ErrClientWorkloadConflict
			}
			links := []struct {
				current  *string
				incoming string
			}{{&row.ChatSessionID, input.ChatSessionID}, {&row.ProjectID, input.ProjectID}, {&row.ParentActivityID, input.ParentActivityID}, {&row.AttemptID, input.AttemptID}, {&row.APITaskID, input.APITaskID}}
			for _, link := range links {
				if link.incoming == "" {
					continue
				}
				if *link.current != "" && *link.current != link.incoming {
					return ErrClientWorkloadConflict
				}
				*link.current = link.incoming
			}
			if input.APITaskID != "" {
				var tasks []Task
				if err := tx.Select("id", "task_id", "user_id").Where("user_id = ? AND task_id = ?", identity.UserID, input.APITaskID).Find(&tasks).Error; err != nil {
					return err
				}
				exact := int64(0)
				for _, task := range tasks {
					if task.UserId == identity.UserID && task.TaskID == input.APITaskID {
						if exact != 0 {
							return ErrClientWorkloadInvalid
						}
						exact = task.ID
					}
				}
				if exact == 0 {
					return ErrClientWorkloadInvalid
				}
				if row.APITaskRowID != 0 && row.APITaskRowID != exact {
					return ErrClientWorkloadConflict
				}
				row.APITaskRowID = exact
			}
			if input.WorkState == "ended" && row.EndedAt == 0 {
				row.EndedAt = now
				row.Outcome = input.Outcome
			}
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			records[input.ActivityID] = row
		}
		for _, input := range inputs {
			row := records[input.ActivityID]
			if row.ParentActivityID != "" {
				parent, ok := records[row.ParentActivityID]
				if !ok {
					err := tx.Where(clause.Eq{Column: "key", Value: clientWorkloadKey(userKey, row.ParentActivityID)}).Take(&parent).Error
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return ErrClientWorkloadInvalid
					}
					if err != nil {
						return err
					}
				}
				if parent.UserID != identity.UserID || parent.ActivityID != row.ParentActivityID || parent.Kind != "project_run" || parent.ActivityID == row.ActivityID || (row.ProjectID != "" && parent.ProjectID != row.ProjectID) || (row.ChatSessionID != "" && parent.ChatSessionID != "" && parent.ChatSessionID != row.ChatSessionID) {
					return ErrClientWorkloadInvalid
				}
			}
			encoded, err := common.Marshal(input.ShotIDs)
			if err != nil {
				return err
			}
			observationKey := clientWorkloadKey(runtimeKey, row.Key)
			var prior ClientWorkloadObservation
			err = tx.Select("key", "shot_ids").Where(clause.Eq{Column: "key", Value: observationKey}).Take(&prior).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				delta += clientWorkloadObservationReservedBytes + int64(len(encoded))
			} else if err != nil {
				return err
			} else {
				delta += int64(len(encoded) - len(prior.ShotIDs))
			}
			observed := ClientWorkloadObservation{Key: observationKey, RuntimeKey: runtimeKey, ActivityKey: row.Key, IncludedSequence: snapshot.Sequence, WorkState: input.WorkState, Phase: input.Phase, LocalJobID: input.LocalJobID, ShotIDs: LongText(encoded), ObservedAt: now}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&observed).Error; err != nil {
				return err
			}
		}
		for _, input := range snapshot.Shots {
			canonical := clientWorkloadKey(userKey, input.ProjectID, input.ShotID)
			shot := ClientWorkloadShot{Key: clientWorkloadKey(runtimeKey, canonical), RuntimeKey: runtimeKey, CanonicalKey: canonical, ProjectID: input.ProjectID, ShotID: input.ShotID, IncludedSequence: snapshot.Sequence}
			var priorShot ClientWorkloadShot
			err = tx.Where(clause.Eq{Column: "key", Value: shot.Key}).Take(&priorShot).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				delta += clientWorkloadShotReservedBytes
			} else if err != nil {
				return err
			} else if priorShot.ProjectID != shot.ProjectID || priorShot.ShotID != shot.ShotID {
				return ErrClientWorkloadConflict
			}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&shot).Error; err != nil {
				return err
			}
		}

		// Retain compact identity/state proof, but reclaim bulky associations only
		// after a newer complete snapshot no longer includes that observation.
		var stale []ClientWorkloadObservation
		if err := tx.Select("key", "shot_ids").Where("runtime_key = ? AND included_sequence <> ? AND shot_ids <> ? AND shot_ids <> ?", runtimeKey, snapshot.Sequence, "[]", "null").Find(&stale).Error; err != nil {
			return err
		}
		for _, old := range stale {
			if len(old.ShotIDs) <= 2 {
				continue
			}
			if err := tx.Model(&ClientWorkloadObservation{}).Where(clause.Eq{Column: "key", Value: old.Key}).Update("shot_ids", LongText("[]")).Error; err != nil {
				return err
			}
			delta -= int64(len(old.ShotIDs) - 2)
		}
		if err := applyClientWorkloadBudget(tx, budgets, delta, now); err != nil {
			return err
		}
		runtime.PresenceID = presence.ID
		runtime.AcceptedSequence = snapshot.Sequence
		runtime.PayloadDigest = digest
		runtime.AcceptedAt = now
		runtime.ChatActivity = snapshot.Collections.ChatActivity
		runtime.ProjectRun = snapshot.Collections.ProjectRun
		runtime.StoryboardTask = snapshot.Collections.StoryboardTask
		runtime.LocalOperation = snapshot.Collections.LocalOperation
		runtime.Shots = snapshot.Collections.Shots
		if err := tx.Save(&runtime).Error; err != nil {
			return err
		}
		ack.Recorded = true
		ack.Disposition = "accepted"
		ack.AcceptedSequence = snapshot.Sequence
		return nil
	})
	return ack, err
}
