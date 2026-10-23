package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ClientActivityHeartbeatSeconds = int64(30)
	ClientActivityTimeoutSeconds   = int64(90)
)

var ErrClientActivitySessionLimit = errors.New("client activity session limit reached")

// ClientActivitySession records only an authenticated desktop process's presence.
// SessionID is a fresh random UUID, never a device fingerprint or login credential.
type ClientActivitySession struct {
	ID                int    `json:"id" gorm:"primaryKey"`
	UserID            int    `json:"user_id" gorm:"not null;uniqueIndex:idx_client_activity_user_session,priority:1;index"`
	SessionID         string `json:"session_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_client_activity_user_session,priority:2"`
	Platform          string `json:"platform" gorm:"type:varchar(16);not null"`
	AppVersion        string `json:"app_version" gorm:"type:varchar(80);not null"`
	StartedAt         int64  `json:"started_at" gorm:"not null;index"`
	LastSeenAt        int64  `json:"last_seen_at" gorm:"not null;index"`
	EndedAt           int64  `json:"ended_at" gorm:"not null"`
	OnlineSeconds     int64  `json:"online_seconds" gorm:"not null"`
	ForegroundSeconds int64  `json:"foreground_seconds" gorm:"not null"`
	Foreground        bool   `json:"foreground" gorm:"not null"`
}

type ClientActivityHeartbeat struct {
	SessionID  string `json:"session_id"`
	Event      string `json:"event"`
	Foreground bool   `json:"foreground"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
}

type ClientActivityView struct {
	ClientActivitySession
	Username          string `json:"username"`
	Online            bool   `json:"online"`
	Status            string `json:"status"`
	ModelCalls        int64  `json:"model_calls"`
	ModelErrors       int64  `json:"model_errors"`
	LastModelAt       int64  `json:"last_model_at"`
	ObservationDetail string `json:"observation_detail"`
}

type ClientActivityModelUsage struct {
	ModelName    string `json:"model_name"`
	Calls        int64  `json:"calls"`
	Errors       int64  `json:"errors"`
	LastCalledAt int64  `json:"last_called_at"`
}

type ClientActivityUsage struct {
	Calls  int64                      `json:"calls"`
	Errors int64                      `json:"errors"`
	Models []ClientActivityModelUsage `json:"models"`
}

type ClientActivityFilter struct {
	Scope     string
	TimeBasis string
	Username  string
	Status    string
	Start     int64
	End       int64
}

type ClientActivitySummary struct {
	ServerTime               int64                  `json:"server_time"`
	HeartbeatIntervalSeconds int64                  `json:"heartbeat_interval_seconds"`
	OnlineTimeoutSeconds     int64                  `json:"online_timeout_seconds"`
	OnlineUsers              int64                  `json:"online_users"`
	RuntimeSessions          int64                  `json:"runtime_sessions"`
	Workload                 *ClientWorkloadSummary `json:"workload,omitempty"`
	ForegroundUsers          int64                  `json:"foreground_users"`
	StartedSessions          int64                  `json:"started_sessions"`
	ActiveUsers              int64                  `json:"active_users"`
	OnlineSeconds            int64                  `json:"online_seconds"`
	ForegroundSeconds        int64                  `json:"foreground_seconds"`
	ModelCalls               int64                  `json:"model_calls"`
	ModelErrors              int64                  `json:"model_errors"`
}

// RecordClientActivity uses server time and the previous foreground state. Gaps
// longer than the timeout (sleep/network loss) never become observed usage time.
func RecordClientActivity(userID int, heartbeat ClientActivityHeartbeat, now int64) (*ClientActivitySession, error) {
	var saved ClientActivitySession
	err := DB.Transaction(func(tx *gorm.DB) error {
		candidate := ClientActivitySession{UserID: userID, SessionID: heartbeat.SessionID,
			Platform: heartbeat.Platform, AppVersion: heartbeat.AppVersion,
			StartedAt: now, LastSeenAt: now, Foreground: heartbeat.Foreground}
		if heartbeat.Event == "stop" {
			candidate.EndedAt = now
			candidate.Foreground = false
		}
		insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected > 0 {
			var recentSessions int64
			if err := tx.Model(&ClientActivitySession{}).Where("user_id = ? AND started_at >= ?", userID, now-24*60*60).Count(&recentSessions).Error; err != nil {
				return err
			}
			if recentSessions > 100 {
				return ErrClientActivitySessionLimit
			}
		}
		if err := lockForUpdate(tx).Where("user_id = ? AND session_id = ?", userID, heartbeat.SessionID).First(&saved).Error; err != nil {
			return err
		}
		if saved.EndedAt != 0 || now < saved.LastSeenAt {
			return nil
		}
		elapsed := now - saved.LastSeenAt
		if elapsed <= ClientActivityTimeoutSeconds {
			saved.OnlineSeconds += elapsed
			if saved.Foreground {
				saved.ForegroundSeconds += elapsed
			}
		}
		saved.LastSeenAt = now
		saved.Foreground = heartbeat.Foreground
		if heartbeat.Event == "stop" {
			saved.EndedAt, saved.Foreground = now, false
		}
		return tx.Model(&saved).Select("last_seen_at", "ended_at", "online_seconds", "foreground_seconds", "foreground").Updates(&saved).Error
	})
	return &saved, err
}

func clientActivityQuery(filter ClientActivityFilter, now int64) *gorm.DB {
	return clientActivityQueryDB(DB, filter, now)
}

func clientActivityQueryDB(db *gorm.DB, filter ClientActivityFilter, now int64) *gorm.DB {
	query := db.Model(&ClientActivitySession{})
	if filter.Scope == "live" {
		query = query.Where("ended_at = 0 AND last_seen_at > ? AND last_seen_at <= ?", now-ClientActivityTimeoutSeconds, now)
	} else if filter.Scope == "history" && filter.TimeBasis == "last_seen" {
		query = query.Where("last_seen_at >= ? AND last_seen_at <= ?", filter.Start, filter.End)
	} else {
		query = query.Where("started_at >= ? AND started_at <= ?", filter.Start, filter.End)
	}
	if filter.Username != "" {
		query = query.Where("user_id IN (?)", db.Model(&User{}).Select("id").Where("username = ?", filter.Username))
	}
	switch filter.Status {
	case "online":
		query = query.Where("ended_at = 0 AND last_seen_at > ? AND last_seen_at <= ?", now-ClientActivityTimeoutSeconds, now)
	case "foreground":
		query = query.Where("ended_at = 0 AND last_seen_at > ? AND last_seen_at <= ? AND foreground = ?", now-ClientActivityTimeoutSeconds, now, true)
	case "background":
		query = query.Where("ended_at = 0 AND last_seen_at > ? AND last_seen_at <= ? AND foreground = ?", now-ClientActivityTimeoutSeconds, now, false)
	case "offline":
		query = query.Where("ended_at <> 0 OR last_seen_at <= ? OR last_seen_at > ?", now-ClientActivityTimeoutSeconds, now)
	}
	return query
}

func clientActivityView(session ClientActivitySession, username string, now int64) ClientActivityView {
	view := ClientActivityView{ClientActivitySession: session, Username: username, Status: "offline"}
	view.Online = session.EndedAt == 0 && session.LastSeenAt > now-ClientActivityTimeoutSeconds && session.LastSeenAt <= now
	view.ObservationDetail = "expired"
	if session.EndedAt != 0 {
		view.ObservationDetail = "ended"
	} else if view.Online && session.LastSeenAt <= now {
		view.ObservationDetail = "live"
	}
	if view.Online {
		view.Status = "background"
		if session.Foreground {
			view.Status = "foreground"
		}
	}
	return view
}

// ClientActivityUsageForUser reads existing gateway logs; a client heartbeat is
// never a model call. Overlapping devices for the same user cannot be attributed
// to a particular process, so this is explicitly user usage in the session window.
func ClientActivityUsageForUser(userID int, start, end int64) (ClientActivityUsage, error) {
	usage := ClientActivityUsage{Models: []ClientActivityModelUsage{}}
	query := LOG_DB.Model(&Log{}).Where("created_at >= ? AND created_at <= ? AND type IN ?", start, end, []int{LogTypeConsume, LogTypeError})
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	err := query.Select("model_name, SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS calls, SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS errors, MAX(created_at) AS last_called_at", LogTypeConsume, LogTypeError).
		Group("model_name").Order("last_called_at DESC").Scan(&usage.Models).Error
	for _, item := range usage.Models {
		usage.Calls += item.Calls
		usage.Errors += item.Errors
	}
	return usage, err
}

func ListClientActivitySessions(filter ClientActivityFilter, offset, limit int, now int64) ([]ClientActivityView, int64, error) {
	query := clientActivityQuery(filter, now)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var sessions []ClientActivitySession
	if err := query.Order("last_seen_at DESC").Order("id DESC").Offset(offset).Limit(limit).Find(&sessions).Error; err != nil {
		return nil, 0, err
	}
	views := make([]ClientActivityView, 0, len(sessions))
	for _, session := range sessions {
		view, _, err := GetClientActivitySession(session.ID, now)
		if err != nil {
			return nil, 0, err
		}
		views = append(views, *view)
	}
	return views, total, nil
}

func GetClientActivitySession(id int, now int64) (*ClientActivityView, ClientActivityUsage, error) {
	view, err := getClientActivitySessionDB(DB, id, now)
	if err != nil {
		return nil, ClientActivityUsage{}, err
	}
	usage, err := populateClientActivityUsage(view)
	return view, usage, err
}

func getClientActivitySessionDB(db *gorm.DB, id int, now int64) (*ClientActivityView, error) {
	var session ClientActivitySession
	if err := db.First(&session, id).Error; err != nil {
		return nil, err
	}
	var username string
	if err := db.Model(&User{}).Where("id = ?", session.UserID).Select("username").Scan(&username).Error; err != nil {
		return nil, err
	}
	view := clientActivityView(session, username, now)
	return &view, nil
}

func populateClientActivityUsage(view *ClientActivityView) (ClientActivityUsage, error) {
	end := view.LastSeenAt
	if view.EndedAt > 0 {
		end = view.EndedAt
	}
	usage, err := ClientActivityUsageForUser(view.UserID, view.StartedAt, end)
	if err != nil {
		return usage, err
	}
	view.ModelCalls, view.ModelErrors = usage.Calls, usage.Errors
	for _, item := range usage.Models {
		view.LastModelAt = max(view.LastModelAt, item.LastCalledAt)
	}
	return usage, nil
}

// GetClientActivitySessionWithWorkload composes presence and telemetry from one
// main-database snapshot. Gateway log usage is a separate window projection and
// deliberately runs after that transaction, including when LOG_DB == DB.
func GetClientActivitySessionWithWorkload(id int, now int64) (view *ClientActivityView, usage ClientActivityUsage, workload *ClientWorkloadDetail, err error) {
	err = clientActivityReadTransaction(func(tx *gorm.DB) error {
		view, err = getClientActivitySessionDB(tx, id, now)
		if err != nil {
			return err
		}
		workload, err = getClientWorkloadDetail(tx, view.UserID, view.SessionID, now)
		return err
	})
	if err != nil {
		return nil, usage, nil, err
	}
	usage, err = populateClientActivityUsage(view)
	return view, usage, workload, err
}

func GetClientActivitySummary(filter ClientActivityFilter, now int64) (*ClientActivitySummary, error) {
	summary, err := getClientActivitySummaryDB(DB, filter, now)
	if err != nil {
		return nil, err
	}
	err = populateClientActivitySummaryUsage(summary, filter)
	return summary, err
}

// GetClientActivitySummaryWithWorkload preserves one main-database version for
// all presence counters and workload coverage. Logs retain their independent
// aggregate-period semantics rather than pretending to share this transaction.
func GetClientActivitySummaryWithWorkload(filter ClientActivityFilter, now int64) (summary *ClientActivitySummary, err error) {
	err = clientActivityReadTransaction(func(tx *gorm.DB) error {
		summary, err = getClientActivitySummaryDB(tx, filter, now)
		if err != nil {
			return err
		}
		summary.Workload, err = getClientWorkloadSummary(tx, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	err = populateClientActivitySummaryUsage(summary, filter)
	return summary, err
}

func populateClientActivitySummaryUsage(summary *ClientActivitySummary, filter ClientActivityFilter) error {
	usage, err := ClientActivityUsageForUser(0, filter.Start, filter.End)
	if err != nil {
		return err
	}
	summary.ModelCalls, summary.ModelErrors = usage.Calls, usage.Errors
	return nil
}

func getClientActivitySummaryDB(db *gorm.DB, filter ClientActivityFilter, now int64) (*ClientActivitySummary, error) {
	summary := ClientActivitySummary{ServerTime: now, HeartbeatIntervalSeconds: ClientActivityHeartbeatSeconds, OnlineTimeoutSeconds: ClientActivityTimeoutSeconds}
	periodFilter := filter
	periodFilter.Scope, periodFilter.TimeBasis = "history", "started"
	query := clientActivityQueryDB(db, periodFilter, now)
	if err := query.Count(&summary.StartedSessions).Error; err != nil {
		return nil, err
	}
	if err := query.Distinct("user_id").Count(&summary.ActiveUsers).Error; err != nil {
		return nil, err
	}
	var durations struct{ OnlineSeconds, ForegroundSeconds int64 }
	if err := clientActivityQueryDB(db, periodFilter, now).Select("COALESCE(SUM(online_seconds), 0) AS online_seconds, COALESCE(SUM(foreground_seconds), 0) AS foreground_seconds").Scan(&durations).Error; err != nil {
		return nil, err
	}
	summary.OnlineSeconds, summary.ForegroundSeconds = durations.OnlineSeconds, durations.ForegroundSeconds
	online := db.Model(&ClientActivitySession{}).Where("ended_at = 0 AND last_seen_at > ? AND last_seen_at <= ?", now-ClientActivityTimeoutSeconds, now)
	if err := db.Model(&ClientActivitySession{}).Where("ended_at = 0 AND last_seen_at > ? AND last_seen_at <= ?", now-ClientActivityTimeoutSeconds, now).Count(&summary.RuntimeSessions).Error; err != nil {
		return nil, err
	}
	if err := online.Distinct("user_id").Count(&summary.OnlineUsers).Error; err != nil {
		return nil, err
	}
	if err := online.Where("foreground = ?", true).Distinct("user_id").Count(&summary.ForegroundUsers).Error; err != nil {
		return nil, err
	}
	return &summary, nil
}

// ValidateClientActivityRange bounds administrator queries without depending on
// a client clock. Session durations remain cumulative for the selected sessions.
func ValidateClientActivityRange(start, end, now int64) (int64, int64, error) {
	if end == 0 {
		end = now
	}
	end = min(end, now)
	if start == 0 {
		start = end - int64(7*24*time.Hour/time.Second)
	}
	if start < 0 || end < start || end-start > int64(31*24*time.Hour/time.Second) {
		return 0, 0, errors.New("invalid client activity time range")
	}
	return start, end, nil
}
