package model

import (
	"context"
	"errors"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	ErrVideoUsageQueryTooBroad = errors.New("video_usage_query_too_broad")
	ErrVideoUsageQueryFailed   = errors.New("video_usage_query_failed")
	videoUsagePublicID         = regexp.MustCompile(`^task_[A-Za-z0-9]{32}$`)
)

type VideoUsageFilter struct {
	Start, End              int64
	Model                   string
	UserID                  int
	ClientSource, TaskScope string
	Page, PageSize          int
}
type VideoUsageCurrent struct {
	NotStart     int `json:"not_start"`
	Submitted    int `json:"submitted"`
	Queued       int `json:"queued"`
	InProgress   int `json:"in_progress"`
	Unknown      int `json:"unknown"`
	Inflight     int `json:"inflight"`
	TotalRecords int `json:"total_records"`
}
type VideoUsageDuration struct {
	MeanSeconds    *float64 `json:"mean_seconds"`
	P50Seconds     *float64 `json:"p50_seconds"`
	P95Seconds     *float64 `json:"p95_seconds"`
	ValidSamples   int      `json:"valid_samples"`
	MissingSamples int      `json:"missing_samples"`
	InvalidSamples int      `json:"invalid_samples"`
	ZeroSamples    int      `json:"zero_samples"`
}
type VideoUsagePeak struct {
	Value             *int `json:"value"`
	IntervalSamples   int  `json:"interval_samples"`
	ExcludedIntervals int  `json:"excluded_intervals"`
	UnknownState      int  `json:"unknown_state"`
	InvalidTimestamps int  `json:"invalid_timestamps"`
	MissingFinish     int  `json:"missing_finish"`
	ZeroLength        int  `json:"zero_length"`
}
type VideoUsageHistory struct {
	TaskCount   int                `json:"task_count"`
	Success     int                `json:"success"`
	Failure     int                `json:"failure"`
	Unresolved  int                `json:"unresolved"`
	Users       int                `json:"users"`
	Turnaround  VideoUsageDuration `json:"turnaround"`
	RecordedRun VideoUsageDuration `json:"recorded_run"`
}
type VideoUsageUser struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}
type VideoUsageTask struct {
	TimingQuality               string          `json:"timing_quality"`
	ID                          int64           `json:"id"`
	TaskID                      *string         `json:"task_id"`
	Model                       string          `json:"model"`
	User                        *VideoUsageUser `json:"user"`
	ClientSource                string          `json:"client_source"`
	Status                      string          `json:"status"`
	SubmittedAt                 *int64          `json:"submitted_at"`
	StartedAt                   *int64          `json:"started_at"`
	FinishedAt                  *int64          `json:"finished_at"`
	TurnaroundSeconds           *float64        `json:"turnaround_seconds"`
	RecordedRunningSeconds      *float64        `json:"recorded_running_seconds"`
	AgeSeconds                  *float64        `json:"age_seconds"`
	WaitingSeconds              *float64        `json:"waiting_seconds"`
	ReportedVideoSeconds        *float64        `json:"reported_video_seconds"`
	ReportedVideoDurationSource string          `json:"reported_video_duration_source"`
}
type VideoUsageModel struct {
	Model   string            `json:"model"`
	Current VideoUsageCurrent `json:"current"`
	History VideoUsageHistory `json:"history"`
	Peak    VideoUsagePeak    `json:"peak"`
}
type VideoUsageResponse struct {
	SchemaVersion       int      `json:"schema_version"`
	AsOf                int64    `json:"as_of"`
	StartTimestamp      int64    `json:"start_timestamp"`
	EndTimestamp        int64    `json:"end_timestamp"`
	ObservationComplete bool     `json:"observation_complete"`
	Limitations         []string `json:"limitations"`
	Summary             struct {
		Current VideoUsageCurrent `json:"current"`
		History VideoUsageHistory `json:"history"`
		Peak    VideoUsagePeak    `json:"peak"`
	} `json:"summary"`
	SourceCoverage struct {
		Total   int `json:"total"`
		Desktop int `json:"desktop"`
		Unknown int `json:"unknown"`
	} `json:"source_coverage"`
	ClientSessionDuration struct {
		Status  string   `json:"status"`
		Seconds *float64 `json:"seconds"`
	} `json:"client_session_duration"`
	UpstreamCapacity struct {
		Status          string `json:"status"`
		ConfiguredLimit *int   `json:"configured_limit"`
	} `json:"upstream_capacity"`
	Models        []VideoUsageModel `json:"models"`
	FilterOptions struct {
		Models []string         `json:"models"`
		Users  []VideoUsageUser `json:"users"`
	} `json:"filter_options"`
	Tasks struct {
		Scope    string           `json:"scope"`
		Page     int              `json:"page"`
		PageSize int              `json:"page_size"`
		Total    int              `json:"total"`
		Items    []VideoUsageTask `json:"items"`
	} `json:"tasks"`
}

type videoUsageRow struct {
	UpstreamModel                     string
	ID                                int64
	TaskID                            string
	UserID                            int
	Platform                          string
	Action                            string
	Status                            TaskStatus
	SubmitTime, StartTime, FinishTime int64
	OriginModel                       string
	ClientSource                      string
	ReportedSeconds, ReportedType     string
}
type videoUsageAccumulator struct {
	current         VideoUsageCurrent
	history         VideoUsageHistory
	peak            VideoUsagePeak
	users           map[int]struct{}
	turnaround, run []float64
	events          []videoUsageIntervalEvent
}
type videoUsageIntervalEvent struct {
	at    int64
	delta int
}

func (a *videoUsageAccumulator) observe(row videoUsageRow, filter VideoUsageFilter, asOf int64) {
	terminal := row.Status == TaskStatusSuccess || row.Status == TaskStatusFailure
	if !terminal {
		a.current.TotalRecords++
		switch row.Status {
		case TaskStatusNotStart:
			a.current.NotStart++
			a.current.Inflight++
		case TaskStatusSubmitted:
			a.current.Submitted++
			a.current.Inflight++
		case TaskStatusQueued:
			a.current.Queued++
			a.current.Inflight++
		case TaskStatusInProgress:
			a.current.InProgress++
			a.current.Inflight++
		default:
			a.current.Unknown++
		}
	}
	if row.SubmitTime >= filter.Start && row.SubmitTime < filter.End {
		a.history.TaskCount++
		if row.UserID > 0 {
			if a.users == nil {
				a.users = make(map[int]struct{})
			}
			a.users[row.UserID] = struct{}{}
		}
		switch row.Status {
		case TaskStatusSuccess:
			a.history.Success++
		case TaskStatusFailure:
			a.history.Failure++
		default:
			a.history.Unresolved++
		}
		if terminal {
			value, quality := videoUsageElapsed(row.SubmitTime, row.FinishTime, asOf)
			if videoUsageTimingQuality(row, asOf) == "invalid" {
				quality = "invalid"
			}
			switch quality {
			case "valid":
				a.turnaround = append(a.turnaround, *value)
				if *value == 0 {
					a.history.Turnaround.ZeroSamples++
				}
			case "missing":
				a.history.Turnaround.MissingSamples++
			default:
				a.history.Turnaround.InvalidSamples++
			}
			run, quality := videoUsageElapsed(row.StartTime, row.FinishTime, asOf)
			if videoUsageTimingQuality(row, asOf) == "invalid" {
				quality = "invalid"
			} else if row.SubmitTime == 0 {
				quality = "missing"
			}
			switch quality {
			case "valid":
				a.run = append(a.run, *run)
				if *run == 0 {
					a.history.RecordedRun.ZeroSamples++
				}
			case "missing":
				a.history.RecordedRun.MissingSamples++
			default:
				a.history.RecordedRun.InvalidSamples++
			}
		}
	}

	// Invalid records must remain visible quality gaps before any overlap
	// shortcut; future submissions cannot masquerade as an observed zero peak.
	if videoUsageTimingQuality(row, asOf) == "invalid" || row.SubmitTime == 0 {
		a.peak.InvalidTimestamps++
		a.peak.ExcludedIntervals++
		return
	}
	if row.SubmitTime >= filter.End {
		return
	}
	if terminal && row.FinishTime > 0 && row.FinishTime <= filter.Start {
		return
	}
	switch row.Status {
	case TaskStatusNotStart, TaskStatusSubmitted, TaskStatusQueued, TaskStatusInProgress, TaskStatusSuccess, TaskStatusFailure:
	default:
		a.peak.UnknownState++
		a.peak.ExcludedIntervals++
		return
	}

	end := asOf
	if terminal {
		if row.FinishTime == 0 {
			a.peak.MissingFinish++
			a.peak.ExcludedIntervals++
			return
		}
		if row.FinishTime == row.SubmitTime {
			a.peak.ZeroLength++
			a.peak.ExcludedIntervals++
			return
		}
		end = row.FinishTime
	}
	start := max(row.SubmitTime, filter.Start)
	end = min(end, filter.End, asOf)
	if end <= start {
		return
	}
	a.peak.IntervalSamples++
	a.events = append(a.events, videoUsageIntervalEvent{start, 1}, videoUsageIntervalEvent{end, -1})
}

func videoUsageTimingQuality(row videoUsageRow, asOf int64) string {
	for _, value := range []int64{row.SubmitTime, row.StartTime, row.FinishTime} {
		if value < 0 || value > asOf {
			return "invalid"
		}
	}
	if row.StartTime > 0 && row.SubmitTime > 0 && row.StartTime < row.SubmitTime || row.FinishTime > 0 && row.SubmitTime > 0 && row.FinishTime < row.SubmitTime || row.StartTime > 0 && row.FinishTime > 0 && row.FinishTime < row.StartTime {
		return "invalid"
	}
	terminal := row.Status == TaskStatusSuccess || row.Status == TaskStatusFailure
	if row.SubmitTime == 0 || terminal && (row.StartTime == 0 || row.FinishTime == 0) || row.Status == TaskStatusInProgress && row.StartTime == 0 {
		return "missing"
	}
	return "valid"
}

// Timestamps are gateway records, not a measurement of upstream render time.
func videoUsageElapsed(start, end, asOf int64) (*float64, string) {
	if start < 0 || end < 0 || start > asOf || end > asOf || start > 0 && end > 0 && end < start {
		return nil, "invalid"
	}
	if start == 0 || end == 0 {
		return nil, "missing"
	}
	value := float64(end - start)
	return &value, "valid"
}
func (a *videoUsageAccumulator) finish() {
	a.history.Users = len(a.users)
	for _, entry := range []struct {
		values []float64
		output *VideoUsageDuration
	}{{a.turnaround, &a.history.Turnaround}, {a.run, &a.history.RecordedRun}} {
		if len(entry.values) == 0 {
			continue
		}
		sort.Float64s(entry.values)
		sum := 0.0
		for _, value := range entry.values {
			sum += value
		}
		mean := sum / float64(len(entry.values))
		p50 := entry.values[int(math.Ceil(.5*float64(len(entry.values))))-1]
		p95 := entry.values[int(math.Ceil(.95*float64(len(entry.values))))-1]
		entry.output.MeanSeconds = &mean
		entry.output.P50Seconds = &p50
		entry.output.P95Seconds = &p95
		entry.output.ValidSamples = len(entry.values)
	}
	if len(a.events) == 0 {
		if a.peak.ExcludedIntervals == 0 {
			value := 0
			a.peak.Value = &value
		}
		return
	}
	slices.SortFunc(a.events, func(a, b videoUsageIntervalEvent) int {
		if a.at < b.at {
			return -1
		}
		if a.at > b.at {
			return 1
		}
		return a.delta - b.delta
	})
	active, peak := 0, 0
	for _, event := range a.events {
		active += event.delta
		peak = max(peak, active)
	}
	a.peak.Value = &peak
}

// GetVideoUsage reads only a bounded credential-free task projection. The
// independently configured log database is neither joined nor required.
func GetVideoUsage(ctx context.Context, filter VideoUsageFilter, catalog MediaOperationsCatalog) (*VideoUsageResponse, error) {
	if DB == nil {
		return nil, ErrVideoUsageQueryFailed
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	db := DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
	asOf := common.GetTimestamp()
	response := &VideoUsageResponse{SchemaVersion: 1, AsOf: asOf, StartTimestamp: filter.Start, EndTimestamp: filter.End, ObservationComplete: false, Limitations: []string{"gateway_recorded_times_not_render_duration", "tasks_recorded_after_upstream_receipt", "legacy_timestamps_may_be_provider_reported", "unknown_states_excluded_from_peak", "peak_is_not_capacity", "client_source_is_a_declaration", "legacy_client_source_unknown", "client_online_duration_not_collected", "only_h3_output_seconds_reported"}, Models: []VideoUsageModel{}}
	response.ClientSessionDuration.Status = "not_collected"
	response.UpstreamCapacity.Status = "unknown"
	response.FilterOptions.Models = []string{}
	response.FilterOptions.Users = []VideoUsageUser{}
	response.Tasks.Scope = filter.TaskScope
	response.Tasks.Page = filter.Page
	response.Tasks.PageSize = filter.PageSize
	response.Tasks.Items = []VideoUsageTask{}
	videoModels := []string{}
	for name, kind := range catalog.Models {
		if kind == "video" {
			videoModels = append(videoModels, name)
		}
	}
	platforms := []string{}
	for platform, kind := range catalog.Platforms {
		if kind == "video" {
			platforms = append(platforms, platform)
		}
	}
	videoActions := []string{constant.TaskActionTextToVideo, constant.TaskActionImageToVideo, constant.TaskActionFirstTailToVideo, constant.TaskActionReferenceToVideo}
	modelExpression := mediaOperationsJSON(db, "properties", "origin_model_name")
	reportedExpression := mediaOperationsJSON(db, "data", "task", "usage", "output_seconds")
	reportedType := "json_type(CASE WHEN json_valid(data) THEN data ELSE '{}' END, '$.task.usage.output_seconds')"
	switch db.Dialector.Name() {
	case "postgres":
		reportedType = "json_typeof(NULLIF(CAST(data AS text), '')::json->'task'->'usage'->'output_seconds')"
	case "mysql":
		reportedType = "JSON_TYPE(JSON_EXTRACT(IF(JSON_VALID(data), data, '{}'), '$.task.usage.output_seconds'))"
	}
	selectFields := "id, task_id, user_id, platform, action, status, submit_time, start_time, finish_time, SUBSTRING(" + modelExpression + ", 1, 256) AS origin_model, SUBSTRING(" + mediaOperationsJSON(db, "properties", "upstream_model_name") + ", 1, 256) AS upstream_model, SUBSTRING(" + mediaOperationsJSON(db, "private_data", "execution", "client_source") + ", 1, 33) AS client_source, SUBSTRING(" + reportedExpression + ", 1, 65) AS reported_seconds, COALESCE(" + reportedType + ", '') AS reported_type"
	query := db.Model(&Task{}).Select(selectFields).Where("platform IN ? OR "+modelExpression+" IN ? OR action IN ?", platforms, videoModels, videoActions).
		Where("status NOT IN ? OR (submit_time >= ? AND submit_time < ?) OR (submit_time < ? AND (finish_time > ? OR finish_time <= 0 OR finish_time < submit_time))", []TaskStatus{TaskStatusSuccess, TaskStatusFailure}, filter.Start, filter.End, filter.End, filter.Start)
	if filter.Model != "" && filter.Model != MediaOperationsUnknownModel {
		query = query.Where(modelExpression+" = ?", filter.Model)
	}
	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	rows, err := query.Limit(10001).Rows()
	if err != nil {
		return nil, ErrVideoUsageQueryFailed
	}
	defer rows.Close()
	candidates := []videoUsageRow{}
	scanned := 0
	for rows.Next() {
		scanned++
		if scanned > 10000 {
			return nil, ErrVideoUsageQueryTooBroad
		}
		var row videoUsageRow
		if err := db.ScanRows(rows, &row); err != nil {
			return nil, ErrVideoUsageQueryFailed
		}
		if len(row.OriginModel) > 255 || len(row.UpstreamModel) > 255 || len(row.ClientSource) > 32 || len(row.ReportedSeconds) > 64 || len(row.Status) > 32 || len(row.TaskID) > 255 {
			return nil, ErrVideoUsageQueryTooBroad
		}
		if row.OriginModel == "" {
			row.OriginModel = MediaOperationsUnknownModel
		}
		if filter.Model != "" && row.OriginModel != filter.Model {
			continue
		}
		if row.ClientSource != "desktop" {
			row.ClientSource = "unknown"
		}
		candidates = append(candidates, row)
	}
	if rows.Err() != nil {
		return nil, ErrVideoUsageQueryFailed
	}
	// Close the cursor before the next primary query; single-connection fixtures
	// and SQLite otherwise cannot make progress on metadata lookups.
	if err := rows.Close(); err != nil {
		return nil, ErrVideoUsageQueryFailed
	}
	var configured []struct{ Model string }
	if err := db.Model(&Ability{}).Select("abilities.model AS model").Joins("INNER JOIN channels ON channels.id = abilities.channel_id").Where("abilities.enabled = ? AND channels.status = ? AND abilities.model IN ?", true, common.ChannelStatusEnabled, videoModels).Distinct().Limit(5001).Scan(&configured).Error; err != nil {
		return nil, ErrVideoUsageQueryFailed
	}
	if len(configured) > 5000 {
		return nil, ErrVideoUsageQueryTooBroad
	}
	names := map[string]struct{}{}
	for _, item := range configured {
		names[item.Model] = struct{}{}
	}
	userIDs := map[int]struct{}{}
	for _, row := range candidates {
		names[row.OriginModel] = struct{}{}
		if row.UserID > 0 {
			userIDs[row.UserID] = struct{}{}
		}
	}
	if len(names) > 5000 {
		return nil, ErrVideoUsageQueryTooBroad
	}
	for name := range names {
		response.FilterOptions.Models = append(response.FilterOptions.Models, name)
	}
	sort.Strings(response.FilterOptions.Models)
	ids := make([]int, 0, len(userIDs))
	for id := range userIDs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	users := map[int]VideoUsageUser{}
	if len(ids) > 0 {
		var records []VideoUsageUser
		if err := db.Model(&User{}).Select("id, username, display_name").Where("id IN ?", ids).Limit(10001).Scan(&records).Error; err != nil {
			return nil, ErrVideoUsageQueryFailed
		}
		if len(records) > 10000 {
			return nil, ErrVideoUsageQueryTooBroad
		}
		for _, user := range records {
			if len(user.Username) > 64 || len(user.DisplayName) > 255 {
				return nil, ErrVideoUsageQueryTooBroad
			}
			users[user.ID] = user
		}
		for _, id := range ids {
			user, exists := users[id]
			if !exists {
				user = VideoUsageUser{ID: id}
				users[id] = user
			}
			response.FilterOptions.Users = append(response.FilterOptions.Users, user)
		}
	}
	all := &videoUsageAccumulator{}
	models := map[string]*videoUsageAccumulator{}
	for _, name := range response.FilterOptions.Models {
		if filter.Model == "" || name == filter.Model {
			models[name] = &videoUsageAccumulator{}
		}
	}
	selected := []videoUsageRow{}
	for _, row := range candidates {
		cohort := row.SubmitTime >= filter.Start && row.SubmitTime < filter.End
		if cohort {
			response.SourceCoverage.Total++
			if row.ClientSource == "desktop" {
				response.SourceCoverage.Desktop++
			} else {
				response.SourceCoverage.Unknown++
			}
		}
		if filter.ClientSource != "all" && filter.ClientSource != "" && row.ClientSource != filter.ClientSource {
			continue
		}
		all.observe(row, filter, asOf)
		if models[row.OriginModel] == nil {
			models[row.OriginModel] = &videoUsageAccumulator{}
		}
		models[row.OriginModel].observe(row, filter, asOf)
		terminal := row.Status == TaskStatusSuccess || row.Status == TaskStatusFailure
		if filter.TaskScope == "current" && !terminal || filter.TaskScope == "history" && cohort {
			selected = append(selected, row)
		}
	}
	all.finish()
	response.Summary.Current = all.current
	response.Summary.History = all.history
	response.Summary.Peak = all.peak
	modelNames := make([]string, 0, len(models))
	for name := range models {
		modelNames = append(modelNames, name)
	}
	sort.Strings(modelNames)
	for _, name := range modelNames {
		a := models[name]
		a.finish()
		response.Models = append(response.Models, VideoUsageModel{Model: name, Current: a.current, History: a.history, Peak: a.peak})
	}
	slices.SortFunc(selected, func(a, b videoUsageRow) int {
		if a.SubmitTime > b.SubmitTime {
			return -1
		}
		if a.SubmitTime < b.SubmitTime {
			return 1
		}
		if a.ID > b.ID {
			return -1
		}
		if a.ID < b.ID {
			return 1
		}
		return 0
	})
	response.Tasks.Total = len(selected)
	offset := (filter.Page - 1) * filter.PageSize
	if offset < len(selected) {
		for _, row := range selected[offset:min(offset+filter.PageSize, len(selected))] {
			item := VideoUsageTask{ID: row.ID, Model: row.OriginModel, ClientSource: row.ClientSource, Status: string(row.Status), ReportedVideoDurationSource: "unknown", TimingQuality: videoUsageTimingQuality(row, asOf)}
			if videoUsagePublicID.MatchString(row.TaskID) {
				id := row.TaskID
				item.TaskID = &id
			}
			if row.UserID > 0 {
				user := users[row.UserID]
				item.User = &user
			}
			if row.SubmitTime > 0 && row.SubmitTime <= asOf {
				value := row.SubmitTime
				item.SubmittedAt = &value
			}
			if row.StartTime > 0 && row.StartTime <= asOf {
				value := row.StartTime
				item.StartedAt = &value
			}
			if row.FinishTime > 0 && row.FinishTime <= asOf {
				value := row.FinishTime
				item.FinishedAt = &value
			}
			if item.TimingQuality != "invalid" {
				terminal := row.Status == TaskStatusSuccess || row.Status == TaskStatusFailure
				if terminal {
					item.TurnaroundSeconds, _ = videoUsageElapsed(row.SubmitTime, row.FinishTime, asOf)
					if row.StartTime >= row.SubmitTime && row.SubmitTime > 0 {
						item.RecordedRunningSeconds, _ = videoUsageElapsed(row.StartTime, row.FinishTime, asOf)
					}
				} else if item.SubmittedAt != nil {
					value := float64(asOf - row.SubmitTime)
					item.AgeSeconds = &value
				}
				if item.SubmittedAt != nil && item.StartedAt != nil {
					value := float64(row.StartTime - row.SubmitTime)
					item.WaitingSeconds = &value
				} else if item.SubmittedAt != nil && (row.Status == TaskStatusNotStart || row.Status == TaskStatusSubmitted || row.Status == TaskStatusQueued) && row.StartTime == 0 {
					value := float64(asOf - row.SubmitTime)
					item.WaitingSeconds = &value
				}
			}
			effectiveModel := row.UpstreamModel
			if effectiveModel == "" {
				effectiveModel = row.OriginModel
			}
			if effectiveModel == "MiniMax-H3" && row.Platform == "hailuo" && row.Status == TaskStatusSuccess && slices.Contains([]string{"integer", "real", "number", "INTEGER", "DOUBLE", "DECIMAL"}, row.ReportedType) {
				if value, err := strconv.ParseFloat(row.ReportedSeconds, 64); err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 4 && value <= 15 {
					item.ReportedVideoSeconds = &value
					item.ReportedVideoDurationSource = "h3_usage"
				}
			}
			response.Tasks.Items = append(response.Tasks.Items, item)
		}
	}
	if ctx.Err() != nil {
		return nil, ErrVideoUsageQueryFailed
	}
	return response, nil
}
