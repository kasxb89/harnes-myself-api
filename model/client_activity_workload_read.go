package model

import (
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ClientWorkloadSummary struct {
	SchemaVersion int                       `json:"schema_version"`
	ServerTime    int64                     `json:"server_time"`
	Scope         string                    `json:"scope"`
	Known         ClientWorkloadKnown       `json:"known"`
	Uncertainty   ClientWorkloadUncertainty `json:"uncertainty"`
	Coverage      ClientWorkloadCoverage    `json:"coverage"`
}

type ClientWorkloadKnown struct {
	GeneratingChatSessions int64 `json:"generating_chat_sessions"`
	Projects               int64 `json:"projects"`
	PlannedShots           int64 `json:"planned_shots"`
	TaskAttempts           int64 `json:"task_attempts"`
	AcceptedTasks          int64 `json:"accepted_tasks"`
	QueuedTasks            int64 `json:"queued_tasks"`
	GeneratingTasks        int64 `json:"generating_tasks"`
	SavingTasks            int64 `json:"saving_tasks"`
	EditingProjects        int64 `json:"editing_projects"`
	LocalOperations        int64 `json:"local_operations"`
}

type ClientWorkloadUncertainty struct {
	UnknownTaskAttempts       int64 `json:"unknown_task_attempts"`
	ConflictingTaskAttempts   int64 `json:"conflicting_task_attempts"`
	UnknownChatSessions       int64 `json:"unknown_chat_sessions"`
	UnknownProjects           int64 `json:"unknown_projects"`
	ExpiredRuntimeSessions    int64 `json:"expired_runtime_sessions"`
	UnreportedRuntimeSessions int64 `json:"unreported_runtime_sessions"`
}

type ClientWorkloadCoverage struct {
	OnlineRuntimeSessions    int64                                       `json:"online_runtime_sessions"`
	ReportingRuntimeSessions int64                                       `json:"reporting_runtime_sessions"`
	Collections              map[string]ClientWorkloadCollectionCoverage `json:"collections"`
}

type ClientWorkloadCollectionCoverage struct {
	CollectedRuntimeSessions int64  `json:"collected_runtime_sessions"`
	Coverage                 string `json:"coverage"`
}

type ClientWorkloadDetail struct {
	SchemaVersion     int                          `json:"schema_version"`
	ObservationStatus string                       `json:"observation_status"`
	AcceptedAt        int64                        `json:"accepted_at"`
	AcceptedSequence  int64                        `json:"accepted_sequence"`
	Collections       ClientWorkloadCollections    `json:"collections"`
	Activities        []ClientWorkloadActivityView `json:"activities"`
	Shots             []ClientWorkloadShotView     `json:"shots"`
	Truncated         bool                         `json:"truncated"`
}

type ClientWorkloadActivityView struct {
	ClientWorkloadActivityInput
	ObservationStatus string `json:"observation_status"`
}

type ClientWorkloadShotView struct {
	ClientWorkloadShotInput
	ObservationStatus string `json:"observation_status"`
}

// These projections intentionally contain no refresh credential, private
// fingerprint, client content or server-side payload digest.
type clientWorkloadLoginView struct {
	SID             string `gorm:"column:sid"`
	UserID          int
	Version         int64
	UserAuthVersion int64
	Status          string
	ExpiresAt       int64
	RevokedAt       int64
}

type clientWorkloadObservedRecord struct {
	RuntimeKey       string
	ActivityKey      string
	IncludedSequence int64
	WorkState        string
	Phase            string
	LocalJobID       string
	ShotIDs          string
	ObservedAt       int64
	UserID           int
	ActivityID       string
	Kind             string
	ChatSessionID    string
	ProjectID        string
	ParentActivityID string
	AttemptID        string
	APITaskID        string
	EndedAt          int64
	Outcome          string
}

type clientWorkloadIdentity struct {
	UserID int
	ID     string
}

type clientWorkloadResolvedAttempt struct {
	UserID           int
	WorkState        string
	Phase            string
	Outcome          string
	ChatSessionID    string
	ProjectID        string
	ParentActivityID string
	APITaskID        string
	Conflict         bool
	ObservedChats    map[string]bool
}

// clientWorkloadValidLogins checks the current durable session control plane.
// Renewing tokens under the same SID is allowed; revoked/expired sessions,
// disabled/deleted accounts and stale user auth versions are not live reporters.
func clientWorkloadValidLogins(db *gorm.DB, runtimes []ClientWorkloadRuntime, now int64) (map[string]bool, error) {
	valid := make(map[string]bool, len(runtimes))
	if len(runtimes) == 0 {
		return valid, nil
	}
	loginIDs := make([]string, 0, len(runtimes))
	userIDs := make([]int, 0, len(runtimes))
	seenLogins, seenUsers := map[string]bool{}, map[int]bool{}
	for _, runtime := range runtimes {
		if !seenLogins[runtime.LoginSID] {
			loginIDs = append(loginIDs, runtime.LoginSID)
			seenLogins[runtime.LoginSID] = true
		}
		if !seenUsers[runtime.UserID] {
			userIDs = append(userIDs, runtime.UserID)
			seenUsers[runtime.UserID] = true
		}
	}
	logins := make(map[string]clientWorkloadLoginView, len(loginIDs))
	for batch := range slices.Chunk(loginIDs, 400) {
		var rows []clientWorkloadLoginView
		if err := db.Model(&UserSession{}).Select("sid", "user_id", "version", "user_auth_version", "status", "expires_at", "revoked_at").Where("sid IN ?", batch).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			logins[row.SID] = row
		}
	}
	users := make(map[int]User, len(userIDs))
	for batch := range slices.Chunk(userIDs, 400) {
		var rows []User
		if err := db.Select("id", "status", "auth_version").Where("id IN ?", batch).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			users[row.Id] = row
		}
	}
	for _, runtime := range runtimes {
		login, loginFound := logins[runtime.LoginSID]
		user, userFound := users[runtime.UserID]
		valid[runtime.Key] = loginFound && userFound && runtime.LoginSID != "" &&
			login.UserID == runtime.UserID && login.Status == UserSessionStatusActive && login.RevokedAt == 0 &&
			login.ExpiresAt > now && login.Version > 0 && login.UserAuthVersion > 0 &&
			login.UserAuthVersion == user.AuthVersion && user.Status == common.UserStatusEnabled
	}
	return valid, nil
}

func clientWorkloadObservationQuery(db *gorm.DB, includeBindings bool) *gorm.DB {
	// Quote the reserved key with GORM's active dialect, including isolated test
	// databases whose production initCol() has not run.
	keyColumn := db.Statement.Quote(clause.Column{Table: "client_workload_records", Name: "key"})
	// Summary needs compact identities and states only. Avoid loading potentially
	// large association payloads from every live runtime into the global aggregate.
	columns := "client_workload_observations.runtime_key, client_workload_observations.activity_key, client_workload_observations.included_sequence, client_workload_observations.work_state, client_workload_observations.phase, client_workload_observations.local_job_id, client_workload_observations.shot_ids, client_workload_observations.observed_at, client_workload_records.user_id, client_workload_records.activity_id, client_workload_records.kind, client_workload_records.chat_session_id, client_workload_records.project_id, client_workload_records.parent_activity_id, client_workload_records.attempt_id, client_workload_records.api_task_id, client_workload_records.ended_at, client_workload_records.outcome"
	if !includeBindings {
		columns = strings.Replace(columns, "client_workload_observations.shot_ids, ", "", 1)
	}
	return db.Model(&ClientWorkloadObservation{}).Select(columns).
		Joins("JOIN client_workload_records ON " + keyColumn + " = client_workload_observations.activity_key")
}

// A canonical terminal activity cannot become active again because another
// runtime still retains an older active observation of that same execution.
func (observation clientWorkloadObservedRecord) effectiveState() (string, string) {
	if observation.EndedAt != 0 {
		return "ended", "ended"
	}
	return observation.WorkState, observation.Phase
}

func (attempt *clientWorkloadResolvedAttempt) merge(observation clientWorkloadObservedRecord) {
	if attempt.ObservedChats == nil {
		attempt.ObservedChats = map[string]bool{}
	}
	if observation.ChatSessionID != "" {
		attempt.ObservedChats[observation.ChatSessionID] = true
	}
	state, phase := observation.effectiveState()
	if attempt.WorkState == "" {
		attempt.UserID, attempt.WorkState, attempt.Phase = observation.UserID, state, phase
		attempt.Outcome = observation.Outcome
	} else if attempt.WorkState != state || attempt.Phase != phase || (state == "ended" && attempt.Outcome != observation.Outcome) {
		attempt.Conflict = true
	}
	for _, link := range []struct {
		current  *string
		incoming string
	}{
		{&attempt.ChatSessionID, observation.ChatSessionID},
		{&attempt.ProjectID, observation.ProjectID},
		{&attempt.ParentActivityID, observation.ParentActivityID},
		{&attempt.APITaskID, observation.APITaskID},
	} {
		if link.incoming == "" {
			continue
		}
		if *link.current != "" && *link.current != link.incoming {
			attempt.Conflict = true
			continue
		}
		*link.current = link.incoming
	}
}

// GetClientWorkloadSummary reads only current live presence and the matching
// accepted snapshot rows. Retained historical observations and gateway call logs
// cannot establish current activity or silently turn missing coverage into zero.
func GetClientWorkloadSummary(now int64) (summary *ClientWorkloadSummary, err error) {
	// Repeatable read is explicit: PostgreSQL's default READ COMMITTED would
	// otherwise mix accepted_sequence from one commit with overwritten rows from
	// the next commit. SQLite uses its consistent read transaction snapshot.
	err = clientActivityReadTransaction(func(tx *gorm.DB) error {
		summary, err = getClientWorkloadSummary(tx, now)
		return err
	})
	return summary, err
}

func clientActivityReadTransaction(read func(*gorm.DB) error) error {
	return DB.Transaction(read, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

func getClientWorkloadSummary(db *gorm.DB, now int64) (*ClientWorkloadSummary, error) {
	summary := &ClientWorkloadSummary{SchemaVersion: 1, ServerTime: now, Scope: "global_live",
		Coverage: ClientWorkloadCoverage{Collections: map[string]ClientWorkloadCollectionCoverage{}}}
	kinds := []string{"chat_activity", "project_run", "storyboard_task", "local_operation", "shots"}
	for _, kind := range kinds {
		summary.Coverage.Collections[kind] = ClientWorkloadCollectionCoverage{Coverage: "not_collected"}
	}
	var presences []ClientActivitySession
	if err := db.Select("id", "user_id", "session_id").Where("ended_at = 0 AND last_seen_at > ? AND last_seen_at <= ?", now-ClientActivityTimeoutSeconds, now).Find(&presences).Error; err != nil {
		return nil, err
	}
	summary.Coverage.OnlineRuntimeSessions = int64(len(presences))
	if len(presences) == 0 {
		return summary, nil
	}
	presenceByID := make(map[int]ClientActivitySession, len(presences))
	presenceIDs := make([]int, 0, len(presences))
	for _, presence := range presences {
		presenceByID[presence.ID] = presence
		presenceIDs = append(presenceIDs, presence.ID)
	}
	var runtimes []ClientWorkloadRuntime
	for batch := range slices.Chunk(presenceIDs, 400) {
		var rows []ClientWorkloadRuntime
		if err := db.Where("presence_id IN ? AND accepted_sequence > 0", batch).Find(&rows).Error; err != nil {
			return nil, err
		}
		runtimes = append(runtimes, rows...)
	}
	validLogins, err := clientWorkloadValidLogins(db, runtimes, now)
	if err != nil {
		return nil, err
	}
	reported := map[int]bool{}
	fresh := make([]ClientWorkloadRuntime, 0, len(runtimes))
	freshByKey := make(map[string]ClientWorkloadRuntime, len(runtimes))
	for _, runtime := range runtimes {
		presence := presenceByID[runtime.PresenceID]
		if presence.ID == 0 || presence.UserID != runtime.UserID || presence.SessionID != runtime.RuntimeSessionID {
			continue
		}
		reported[presence.ID] = true
		if runtime.AcceptedAt <= now && runtime.AcceptedAt > now-ClientActivityTimeoutSeconds && validLogins[runtime.Key] {
			fresh = append(fresh, runtime)
			freshByKey[runtime.Key] = runtime
			for _, kind := range kinds {
				if runtime.collections().Collected(kind) {
					coverage := summary.Coverage.Collections[kind]
					coverage.CollectedRuntimeSessions++
					summary.Coverage.Collections[kind] = coverage
				}
			}
		} else {
			summary.Uncertainty.ExpiredRuntimeSessions++
		}
	}
	summary.Uncertainty.UnreportedRuntimeSessions = int64(len(presences) - len(reported))
	summary.Coverage.ReportingRuntimeSessions = int64(len(fresh))
	for _, kind := range kinds {
		coverage := summary.Coverage.Collections[kind]
		if coverage.CollectedRuntimeSessions == summary.Coverage.OnlineRuntimeSessions {
			coverage.Coverage = "complete"
		} else if coverage.CollectedRuntimeSessions > 0 {
			coverage.Coverage = "partial"
		}
		summary.Coverage.Collections[kind] = coverage
	}
	chats, projects, edits, operations := map[clientWorkloadIdentity]bool{}, map[clientWorkloadIdentity]bool{}, map[clientWorkloadIdentity]bool{}, map[clientWorkloadIdentity]bool{}
	unknownChats, unknownProjects := map[clientWorkloadIdentity]bool{}, map[clientWorkloadIdentity]bool{}
	attempts := map[clientWorkloadIdentity]*clientWorkloadResolvedAttempt{}
	omittedAttempts := map[clientWorkloadIdentity][]clientWorkloadObservedRecord{}
	plannedShots := map[string]bool{}
	for batch := range slices.Chunk(fresh, 400) {
		conditions := make([]clause.Expression, 0, len(batch))
		for _, runtime := range batch {
			conditions = append(conditions, clause.Eq{Column: clause.Column{Table: "client_workload_observations", Name: "runtime_key"}, Value: runtime.Key})
		}
		var observations []clientWorkloadObservedRecord
		if err := clientWorkloadObservationQuery(db, false).Where(clause.Or(conditions...)).Scan(&observations).Error; err != nil {
			return nil, err
		}
		for _, observation := range observations {
			runtime := freshByKey[observation.RuntimeKey]
			if observation.UserID != runtime.UserID {
				continue
			}
			state, phase := observation.effectiveState()
			chat := clientWorkloadIdentity{observation.UserID, observation.ChatSessionID}
			project := clientWorkloadIdentity{observation.UserID, observation.ProjectID}
			if observation.IncludedSequence != runtime.AcceptedSequence || !runtime.collections().Collected(observation.Kind) {
				// Omission is not an explicit end. Retain compact identity uncertainty;
				// an explicit canonical end suppresses stale observations globally.
				if state == "ended" {
					continue
				}
				if observation.Kind == "storyboard_task" && observation.AttemptID != "" {
					key := clientWorkloadIdentity{observation.UserID, observation.AttemptID}
					omittedAttempts[key] = append(omittedAttempts[key], observation)
				}
				if chat.ID != "" {
					unknownChats[chat] = true
				}
				if project.ID != "" {
					unknownProjects[project] = true
				}
				continue
			}
			if observation.Kind == "storyboard_task" {
				key := clientWorkloadIdentity{observation.UserID, observation.AttemptID}
				if key.ID == "" {
					continue
				}
				if attempts[key] == nil {
					attempts[key] = &clientWorkloadResolvedAttempt{}
				}
				attempts[key].merge(observation)
				continue
			}
			if state == "unknown" {
				if chat.ID != "" {
					unknownChats[chat] = true
				}
				if observation.Kind == "project_run" && project.ID != "" {
					unknownProjects[project] = true
				}
				continue
			}
			if state != "active" {
				continue
			}
			if chat.ID != "" && (observation.Kind != "chat_activity" || phase == "busy") {
				chats[chat] = true
			}
			switch observation.Kind {
			case "project_run":
				if project.ID != "" {
					projects[project] = true
					if phase == "saving" || phase == "editing" {
						edits[project] = true
					}
				}
			case "local_operation":
				operations[clientWorkloadIdentity{observation.UserID, observation.ActivityID}] = true
			}
		}
		shotConditions := make([]clause.Expression, 0, len(batch))
		for _, runtime := range batch {
			if runtime.Shots == "collected" {
				shotConditions = append(shotConditions, clause.And(clause.Eq{Column: "runtime_key", Value: runtime.Key}, clause.Eq{Column: "included_sequence", Value: runtime.AcceptedSequence}))
			}
		}
		if len(shotConditions) > 0 {
			var shots []ClientWorkloadShot
			if err := db.Select("canonical_key").Where(clause.Or(shotConditions...)).Find(&shots).Error; err != nil {
				return nil, err
			}
			for _, shot := range shots {
				plannedShots[shot.CanonicalKey] = true
			}
		}
	}
	for key, observations := range omittedAttempts {
		// A current observation of the same attempt takes precedence over an old
		// omission in another runtime, without fabricating a state conflict.
		if attempts[key] != nil {
			continue
		}
		summary.Uncertainty.UnknownTaskAttempts++
		for _, observation := range observations {
			if observation.ChatSessionID != "" {
				unknownChats[clientWorkloadIdentity{observation.UserID, observation.ChatSessionID}] = true
			}
		}
	}
	for _, attempt := range attempts {
		chat := clientWorkloadIdentity{attempt.UserID, attempt.ChatSessionID}
		if attempt.Conflict {
			summary.Uncertainty.ConflictingTaskAttempts++
			for chatID := range attempt.ObservedChats {
				unknownChats[clientWorkloadIdentity{attempt.UserID, chatID}] = true
			}
			continue
		}
		if attempt.WorkState == "unknown" {
			summary.Uncertainty.UnknownTaskAttempts++
			if chat.ID != "" {
				unknownChats[chat] = true
			}
			continue
		}
		if attempt.WorkState != "active" {
			continue
		}
		summary.Known.TaskAttempts++
		if chat.ID != "" {
			chats[chat] = true
		}
		switch attempt.Phase {
		case "accepted":
			summary.Known.AcceptedTasks++
		case "queued":
			summary.Known.QueuedTasks++
		case "generating":
			summary.Known.GeneratingTasks++
		case "saving":
			summary.Known.SavingTasks++
		}
	}
	for chat := range chats {
		delete(unknownChats, chat)
	}
	for project := range projects {
		delete(unknownProjects, project)
	}
	summary.Known.GeneratingChatSessions = int64(len(chats))
	summary.Known.Projects = int64(len(projects))
	summary.Known.PlannedShots = int64(len(plannedShots))
	summary.Known.EditingProjects = int64(len(edits))
	summary.Known.LocalOperations = int64(len(operations))
	summary.Uncertainty.UnknownChatSessions = int64(len(unknownChats))
	summary.Uncertainty.UnknownProjects = int64(len(unknownProjects))
	return summary, nil
}

// GetClientWorkloadDetail returns a bounded safe observation history, not login
// control-plane rows. Missing collection support remains explicitly uncollected.
func GetClientWorkloadDetail(userID int, runtimeSessionID string, now int64) (detail *ClientWorkloadDetail, err error) {
	err = clientActivityReadTransaction(func(tx *gorm.DB) error {
		detail, err = getClientWorkloadDetail(tx, userID, runtimeSessionID, now)
		return err
	})
	return detail, err
}

func getClientWorkloadDetail(db *gorm.DB, userID int, runtimeSessionID string, now int64) (*ClientWorkloadDetail, error) {
	detail := &ClientWorkloadDetail{SchemaVersion: 1, ObservationStatus: "not_collected",
		Collections: ClientWorkloadCollections{ChatActivity: "not_collected", ProjectRun: "not_collected", StoryboardTask: "not_collected", LocalOperation: "not_collected", Shots: "not_collected"},
		Activities:  []ClientWorkloadActivityView{}, Shots: []ClientWorkloadShotView{}}
	var runtime ClientWorkloadRuntime
	err := db.Where(clause.Eq{Column: "key", Value: clientWorkloadKey(strconv.Itoa(userID), runtimeSessionID)}).Take(&runtime).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return detail, nil
	}
	if err != nil {
		return nil, err
	}
	if runtime.UserID != userID || runtime.RuntimeSessionID != runtimeSessionID || runtime.AcceptedSequence == 0 {
		return detail, nil
	}
	var presence ClientActivitySession
	if err := db.Where("id = ? AND user_id = ? AND session_id = ?", runtime.PresenceID, userID, runtimeSessionID).Take(&presence).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	detail.AcceptedAt, detail.AcceptedSequence, detail.Collections = runtime.AcceptedAt, runtime.AcceptedSequence, runtime.collections()
	detail.ObservationStatus = "expired"
	validLogins, err := clientWorkloadValidLogins(db, []ClientWorkloadRuntime{runtime}, now)
	if err != nil {
		return nil, err
	}
	if presence.EndedAt != 0 {
		detail.ObservationStatus = "ended"
	} else if presence.ID != 0 && presence.UserID == userID && presence.SessionID == runtimeSessionID &&
		presence.LastSeenAt > now-ClientActivityTimeoutSeconds && presence.LastSeenAt <= now &&
		runtime.AcceptedAt > now-ClientActivityTimeoutSeconds && runtime.AcceptedAt <= now && validLogins[runtime.Key] {
		detail.ObservationStatus = "live"
	}
	var observations []clientWorkloadObservedRecord
	if err := clientWorkloadObservationQuery(db, true).Where("client_workload_observations.runtime_key = ? AND client_workload_records.user_id = ?", runtime.Key, userID).
		Order("client_workload_observations.included_sequence DESC").Order("client_workload_observations.observed_at DESC").Order("client_workload_records.activity_id ASC").Limit(maxClientWorkloadRows + 1).Scan(&observations).Error; err != nil {
		return nil, err
	}
	var shots []ClientWorkloadShot
	if err := db.Where("runtime_key = ?", runtime.Key).Order("included_sequence DESC").Order("project_id ASC").Order("shot_id ASC").Limit(maxClientWorkloadRows + 1).Find(&shots).Error; err != nil {
		return nil, err
	}
	type entry struct {
		sequence int64
		observed int64
		activity *clientWorkloadObservedRecord
		shot     *ClientWorkloadShot
	}
	entries := make([]entry, 0, len(observations)+len(shots))
	for i := range observations {
		entries = append(entries, entry{sequence: observations[i].IncludedSequence, observed: observations[i].ObservedAt, activity: &observations[i]})
	}
	for i := range shots {
		entries = append(entries, entry{sequence: shots[i].IncludedSequence, shot: &shots[i]})
	}
	slices.SortStableFunc(entries, func(a, b entry) int {
		if a.sequence != b.sequence {
			if a.sequence > b.sequence {
				return -1
			}
			return 1
		}
		if a.observed > b.observed {
			return -1
		}
		if a.observed < b.observed {
			return 1
		}
		return 0
	})
	detail.Truncated = len(entries) > maxClientWorkloadRows
	for _, item := range entries[:min(len(entries), maxClientWorkloadRows)] {
		if item.activity != nil {
			observation := item.activity
			state, phase := observation.effectiveState()
			activity := ClientWorkloadActivityView{ClientWorkloadActivityInput: ClientWorkloadActivityInput{
				ActivityID: observation.ActivityID, Kind: observation.Kind, WorkState: state, Phase: phase,
				ChatSessionID: observation.ChatSessionID, ProjectID: observation.ProjectID, ParentActivityID: observation.ParentActivityID,
				AttemptID: observation.AttemptID, APITaskID: observation.APITaskID, LocalJobID: observation.LocalJobID}, ObservationStatus: "live"}
			if common.UnmarshalJsonStr(observation.ShotIDs, &activity.ShotIDs) != nil {
				return nil, ErrClientWorkloadInvalid
			}
			if state == "ended" {
				activity.Outcome, activity.ObservationStatus = observation.Outcome, "ended"
			} else if observation.IncludedSequence != runtime.AcceptedSequence || !runtime.collections().Collected(observation.Kind) {
				activity.ObservationStatus = "not_reported"
			} else if detail.ObservationStatus != "live" {
				activity.ObservationStatus = "expired"
			} else if state == "unknown" {
				activity.ObservationStatus = "unknown"
			}
			detail.Activities = append(detail.Activities, activity)
			continue
		}
		shot := item.shot
		status := "live"
		if shot.IncludedSequence != runtime.AcceptedSequence || runtime.Shots != "collected" {
			status = "not_reported"
		} else if detail.ObservationStatus != "live" {
			status = "expired"
		}
		detail.Shots = append(detail.Shots, ClientWorkloadShotView{ClientWorkloadShotInput: ClientWorkloadShotInput{ProjectID: shot.ProjectID, ShotID: shot.ShotID}, ObservationStatus: status})
	}
	return detail, nil
}
