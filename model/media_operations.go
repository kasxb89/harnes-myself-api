package model

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"slices"
)

var (
	ErrMediaOperationsQueryTooBroad = errors.New("media_operations_query_too_broad")
	ErrMediaOperationsQueryFailed   = errors.New("media_operations_query_failed")
)

const mediaOperationsScanLimit = 10000
const MediaOperationsUnknownModel = "[unknown]"

type MediaOperationsCatalog struct {
	Models      map[string]string
	Platforms   map[string]string
	SubmitPaths map[string]string
}

type MediaOperationsFilter struct {
	Start, End       int64
	Model, MediaKind string
}
type MediaOperationsCapacity struct {
	Status          string `json:"status"`
	ConfiguredLimit *int   `json:"configured_limit"`
	AvailableSlots  *int   `json:"available_slots"`
}
type MediaOperationsTaskCounts struct {
	NotStart   int `json:"not_start"`
	Submitted  int `json:"submitted"`
	Queued     int `json:"queued"`
	InProgress int `json:"in_progress"`
	Unknown    int `json:"unknown"`
	Success    int `json:"success"`
	Failure    int `json:"failure"`
}
type MediaOperationsCurrentCounts struct {
	NotStart   int `json:"not_start"`
	Submitted  int `json:"submitted"`
	Queued     int `json:"queued"`
	InProgress int `json:"in_progress"`
	Unknown    int `json:"unknown"`
}
type MediaOperationsHistory struct {
	AcceptedTasks             int `json:"accepted_tasks"`
	ErrorEvents               int `json:"error_events"`
	ErrorRequests             int `json:"error_requests"`
	SubmitErrorRequests       int `json:"submit_error_requests"`
	TerminalErrorRequests     int `json:"terminal_error_requests"`
	UnknownStageErrorRequests int `json:"unknown_stage_error_requests"`
	ObservedPolicyAttempts    int `json:"observed_policy_attempts"`
	ClientReportEvents        int `json:"client_report_events"`
	CorrelatedClientRequests  int `json:"correlated_client_requests"`
}
type MediaOperationsCount struct {
	Category string `json:"category"`
	Events   int    `json:"events"`
	Requests int    `json:"requests"`
}
type MediaOperationsStage struct {
	Source   string `json:"source"`
	Stage    string `json:"stage"`
	Events   int    `json:"events"`
	Requests int    `json:"requests"`
}
type MediaOperationsHistorySummary struct {
	MediaOperationsHistory
	Categories []MediaOperationsCount `json:"categories"`
	Stages     []MediaOperationsStage `json:"stages"`
}
type MediaOperationsPoll struct {
	TasksWithFailures      int `json:"tasks_with_failures"`
	MaxConsecutiveFailures int `json:"max_consecutive_failures"`
}
type MediaOperationsModel struct {
	Model     string                    `json:"model"`
	MediaKind string                    `json:"media_kind"`
	Capacity  MediaOperationsCapacity   `json:"capacity"`
	Tasks     MediaOperationsTaskCounts `json:"tasks"`
	History   MediaOperationsHistory    `json:"history"`
	Poll      MediaOperationsPoll       `json:"poll"`
}
type MediaOperationsResponse struct {
	SchemaVersion       int                           `json:"schema_version"`
	AsOf                int64                         `json:"as_of"`
	HistoryStart        int64                         `json:"history_start"`
	HistoryEnd          int64                         `json:"history_end"`
	Source              string                        `json:"source"`
	ObservationComplete bool                          `json:"observation_complete"`
	Limitations         []string                      `json:"limitations"`
	Summary             MediaOperationsCurrentCounts  `json:"summary"`
	Models              []MediaOperationsModel        `json:"models"`
	HistorySummary      MediaOperationsHistorySummary `json:"history_summary"`
}

// JSON members are projected independently: task payloads, credentials, input,
// prices and entire log metadata objects never enter this observation query.
func mediaOperationsJSON(db *gorm.DB, column string, path ...string) string {
	switch db.Dialector.Name() {
	case "postgres":
		expression := "NULLIF(CAST(" + column + " AS text), '')::json"
		for i, key := range path {
			if i == len(path)-1 {
				expression += "->>'" + key + "'"
			} else {
				expression += "->'" + key + "'"
			}
		}
		return "COALESCE(" + expression + ", '')"
	case "mysql":
		return "COALESCE(JSON_UNQUOTE(JSON_EXTRACT(IF(JSON_VALID(" + column + "), " + column + ", '{}'), '$." + strings.Join(path, ".") + "')), '')"
	case "clickhouse":
		args := make([]string, len(path))
		for i, key := range path {
			args[i] = "'" + key + "'"
		}
		return "JSONExtractRaw(" + column + ", " + strings.Join(args, ", ") + ")"
	default:
		return "COALESCE(json_extract(CASE WHEN json_valid(" + column + ") THEN " + column + " ELSE '{}' END, '$." + strings.Join(path, ".") + "'), '')"
	}
}

// ClickHouse JSONExtractRaw quotes strings; other dialects return plain scalars.
func mediaOperationsScalar(value string, clickhouse bool) string {
	if clickhouse && strings.HasPrefix(value, "\"") {
		var decoded string
		if common.UnmarshalJsonStr(value, &decoded) == nil {
			return decoded
		}
	}
	return value
}

type mediaOperationsTaskRow struct {
	Platform      string
	Action        string
	Status        TaskStatus
	SubmitTime    int64
	OriginalModel string
	PollFailures  string
}
type mediaOperationsLogRow struct {
	ErrorContent string
	ClientStage  string
	UserID       int
	RequestID    string
	ModelName    string
	Type         int
	ClientCode   string
	Stage        string
	Code         string
	ProviderCode string
	RequestPath  string
	Source       string
	Tool         string
	Correlation  string
	Policy       string
}
type mediaOperationsPolicyEvent struct {
	Attempt   int `json:"attempt"`
	ChannelID int `json:"channel_id"`
}
type mediaOperationsRequestKey struct {
	User    int
	Request string
}
type mediaOperationsAttemptKey struct {
	mediaOperationsRequestKey
	Attempt, Channel int
}
type mediaOperationsAccumulator struct {
	history                                       MediaOperationsHistory
	errors, submit, terminal, unknown, correlated map[mediaOperationsRequestKey]struct{}
	attempts                                      map[mediaOperationsAttemptKey]struct{}
}

func newMediaOperationsAccumulator() *mediaOperationsAccumulator {
	return &mediaOperationsAccumulator{errors: map[mediaOperationsRequestKey]struct{}{}, submit: map[mediaOperationsRequestKey]struct{}{}, terminal: map[mediaOperationsRequestKey]struct{}{}, unknown: map[mediaOperationsRequestKey]struct{}{}, correlated: map[mediaOperationsRequestKey]struct{}{}, attempts: map[mediaOperationsAttemptKey]struct{}{}}
}
func (a *mediaOperationsAccumulator) result() MediaOperationsHistory {
	result := a.history
	result.ErrorRequests = len(a.errors)
	result.SubmitErrorRequests = len(a.submit)
	result.TerminalErrorRequests = len(a.terminal)
	result.UnknownStageErrorRequests = len(a.unknown)
	result.CorrelatedClientRequests = len(a.correlated)
	result.ObservedPolicyAttempts = len(a.attempts)
	return result
}
func mediaOperationsCategory(code string) string {
	switch strings.ToLower(code) {
	case "content_policy_violation", "content_filter", "content_policy", "safety_violation":
		return "content_policy"
	case "resource_exhausted":
		return "generation_capacity"
	case "model_not_found", "model_not_exist", "model_not_available", "model_not_supported":
		return "model_unavailable"
	case "invalid_request_error", "invalid_argument", "invalid_parameters", "duration_not_supported", "audio_not_supported", "shot_duration_unsupported":
		return "invalid_request"
	case "rate_limit_exceeded", "rate_limit_error":
		return "rate_limit"
	case "concurrency_limit_exceeded", "concurrency_limit":
		return "concurrency_limit"
	case "transport_timeout", "task_timeout", "queue_wait_timeout", "http_request_timeout", "timeout":
		return "timeout"
	case "do_request_failed", "transport_error":
		return "transport"
	case "insufficient_quota", "local_save_failed":
		return "other"
	default:
		return "unknown"
	}
}

var mediaOperationsLegacyPatterns = []struct {
	category string
	pattern  *regexp.Regexp
}{
	{"content_policy", regexp.MustCompile(`(?i)content[ _-]*policy[ _-]*(?:violation|reject)|被内容策略拒绝|内容.{0,32}(?:未通过安全审核|审核不通过)`)},
	{"generation_capacity", regexp.MustCompile(`(?i)generation resources (?:are )?unavailable|no available generation resources|没有可用的生成资源`)},
	{"model_unavailable", regexp.MustCompile(`(?i)model(?:[ _-]|[^\r\n]{0,96})?(?:not[ _-]*found|not[ _-]*exist|unavailable|does not exist)|模型.{0,48}(?:不存在|不可用|不支持)`)},
}

func mediaOperationsLegacyCategory(content string) string {
	for _, rule := range mediaOperationsLegacyPatterns {
		if rule.pattern.MatchString(content) {
			return rule.category
		}
	}
	return "unknown"
}

// GetMediaOperations is a bounded read-only gateway observation, not an
// upstream capacity probe. Catalog kinds must come from actual plugin claims.
func GetMediaOperations(ctx context.Context, filter MediaOperationsFilter, catalog MediaOperationsCatalog) (*MediaOperationsResponse, error) {
	if DB == nil || LOG_DB == nil {
		return nil, ErrMediaOperationsQueryFailed
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	db := DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
	logDB := LOG_DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
	response := &MediaOperationsResponse{SchemaVersion: 1, AsOf: common.GetTimestamp(), HistoryStart: filter.Start, HistoryEnd: filter.End, Source: "persisted_gateway_observation", Limitations: []string{"capacity_not_configured", "tasks_recorded_after_submission", "historical_errors_may_lack_stage", "historical_errors_may_lack_request_id", "policy_attempts_are_not_upstream_posts", "poll_failures_are_consecutive", "client_reports_are_not_gateway_observations", "records_without_media_evidence_excluded", "cross_database_snapshot_not_atomic"}, Models: []MediaOperationsModel{}, HistorySummary: MediaOperationsHistorySummary{Categories: []MediaOperationsCount{}, Stages: []MediaOperationsStage{}}}
	kinds := make(map[string]string, len(catalog.Models))
	for name, kind := range catalog.Models {
		kinds[name] = kind
	}
	addEvidence := func(name, kind string) {
		if kind != "image" && kind != "video" {
			return
		}
		if old, exists := kinds[name]; !exists {
			kinds[name] = kind
		} else if old != kind {
			kinds[name] = "unknown"
		}
	}
	models := map[string]*MediaOperationsModel{}
	accumulators := map[string]*mediaOperationsAccumulator{}
	all := newMediaOperationsAccumulator()
	kindFor := func(name string) string {
		if kind := kinds[name]; kind == "image" || kind == "video" {
			return kind
		}
		return "unknown"
	}
	include := func(name string) bool {
		return (filter.Model == "" || filter.Model == name) && (filter.MediaKind == "all" || filter.MediaKind == "" || filter.MediaKind == kindFor(name))
	}
	ensure := func(name string) *MediaOperationsModel {
		if item := models[name]; item != nil {
			return item
		}
		item := &MediaOperationsModel{Model: name, MediaKind: kindFor(name), Capacity: MediaOperationsCapacity{Status: "unknown"}}
		models[name] = item
		accumulators[name] = newMediaOperationsAccumulator()
		return item
	}
	var abilities []struct{ Model string }
	if err := db.Model(&Ability{}).Select("abilities.model AS model").Joins("INNER JOIN channels ON channels.id = abilities.channel_id").Where("abilities.enabled = ? AND channels.status = ?", true, common.ChannelStatusEnabled).Distinct().Limit(mediaOperationsScanLimit + 1).Scan(&abilities).Error; err != nil {
		return nil, ErrMediaOperationsQueryFailed
	}
	if len(abilities) > mediaOperationsScanLimit {
		return nil, ErrMediaOperationsQueryTooBroad
	}
	for _, ability := range abilities {
		if kindFor(ability.Model) != "unknown" && include(ability.Model) {
			ensure(ability.Model)
		}
	}
	modelExpression := mediaOperationsJSON(db, "properties", "origin_model_name")
	taskQuery := db.Model(&Task{}).Select("platform, action, status, submit_time, "+"SUBSTRING("+modelExpression+", 1, 256) AS original_model, "+mediaOperationsJSON(db, "private_data", "poll_failures")+" AS poll_failures").Where("status NOT IN ? OR (submit_time >= ? AND submit_time <= ?)", []string{TaskStatusSuccess, TaskStatusFailure}, filter.Start, filter.End)
	mediaModels := make([]string, 0, len(catalog.Models))
	for name, kind := range catalog.Models {
		if kind == "image" || kind == "video" {
			mediaModels = append(mediaModels, name)
		}
	}
	mediaPlatforms := []string{string(constant.TaskPlatformMidjourney)}
	for platform := range catalog.Platforms {
		mediaPlatforms = append(mediaPlatforms, platform)
	}
	videoActions := []string{constant.TaskActionImageToVideo, constant.TaskActionTextToVideo, constant.TaskActionFirstTailToVideo, constant.TaskActionReferenceToVideo}
	taskQuery = taskQuery.Where("platform IN ? OR "+modelExpression+" IN ? OR action IN ?", mediaPlatforms, mediaModels, videoActions)
	if filter.Model != "" && filter.Model != MediaOperationsUnknownModel {
		taskQuery = taskQuery.Where(modelExpression+" = ?", filter.Model)
	}
	var tasks []mediaOperationsTaskRow
	if err := taskQuery.Limit(mediaOperationsScanLimit + 1).Scan(&tasks).Error; err != nil {
		return nil, ErrMediaOperationsQueryFailed
	}
	if len(tasks) > mediaOperationsScanLimit {
		return nil, ErrMediaOperationsQueryTooBroad
	}
	for _, task := range tasks {
		if len(task.OriginalModel) > 255 {
			return nil, ErrMediaOperationsQueryTooBroad
		}
	}

	fields := []string{"user_id", "request_id", "model_name", "type", "CASE WHEN type = 8 THEN SUBSTRING(content, 1, 257) ELSE '' END AS client_code", "CASE WHEN type = 5 THEN SUBSTRING(content, 1, 8192) ELSE '' END AS error_content"}
	for _, field := range []struct {
		alias string
		path  []string
	}{
		{"stage", []string{"error_stage"}}, {"code", []string{"error_code"}}, {"provider_code", []string{"provider_error_code"}},
		{"request_path", []string{"request_path"}}, {"source", []string{"source"}}, {"tool", []string{"tool"}}, {"correlation", []string{"request_id"}},
		{"policy", []string{"admin_info", "request_policy"}},
	} {
		expression := mediaOperationsJSON(logDB, "other", field.path...)
		limit := 2049
		if field.alias == "policy" {
			limit = 131073
		}
		fields = append(fields, "SUBSTRING("+expression+", 1, "+strconv.Itoa(limit)+") AS "+field.alias)
	}
	// Client stage is a separate field from gateway error_stage.
	fields = append(fields, mediaOperationsJSON(logDB, "other", "stage")+" AS client_stage")
	var logs []mediaOperationsLogRow
	logQuery := logDB.Model(&Log{}).Select(strings.Join(fields, ", ")).Where("created_at >= ? AND created_at <= ? AND type IN ?", filter.Start, filter.End, []int{LogTypeConsume, LogTypeError, LogTypeClientError})
	for _, task := range tasks {
		name := task.OriginalModel
		if name == "" {
			name = MediaOperationsUnknownModel
		}
		if kind, exists := catalog.Platforms[task.Platform]; exists {
			addEvidence(name, kind)
		}
		if task.Platform == string(constant.TaskPlatformMidjourney) {
			addEvidence(name, "image")
		}
		if slices.Contains(videoActions, task.Action) {
			addEvidence(name, "video")
		}
	}
	observedModels := append([]string{}, mediaModels...)
	for _, task := range tasks {
		if task.OriginalModel != "" {
			observedModels = append(observedModels, task.OriginalModel)
		}
	}
	for name := range models {
		observedModels = append(observedModels, name)
	}
	mediaPaths := make([]string, 0, len(catalog.SubmitPaths)*2)
	for path := range catalog.SubmitPaths {
		mediaPaths = append(mediaPaths, path, "/harness"+path)
	}
	pathExpression := mediaOperationsJSON(logDB, "other", "request_path")
	if logDB.Dialector.Name() == "clickhouse" {
		pathExpression = "JSONExtractString(other, 'request_path')"
	}
	logQuery = logQuery.Where("model_name IN ? OR "+pathExpression+" IN ? OR type = ?", observedModels, mediaPaths, LogTypeClientError)
	if filter.Model != "" && filter.Model != MediaOperationsUnknownModel {
		logQuery = logQuery.Where("model_name = ?", filter.Model)
	}
	rows, err := logQuery.Limit(mediaOperationsScanLimit + 1).Rows()
	if err != nil {
		return nil, ErrMediaOperationsQueryFailed
	}
	defer rows.Close()
	metadataBytes := 0
	for rows.Next() {
		if len(logs) == mediaOperationsScanLimit {
			return nil, ErrMediaOperationsQueryTooBroad
		}
		var row mediaOperationsLogRow
		if err := logDB.ScanRows(rows, &row); err != nil {
			return nil, ErrMediaOperationsQueryFailed
		}
		metadataBytes += len(row.Policy) + len(row.ErrorContent) + len(row.RequestPath) + len(row.Code) + len(row.ProviderCode) + len(row.Tool) + len(row.Correlation) + len(row.Stage) + len(row.ClientStage) + len(row.Source) + len(row.ClientCode)
		if metadataBytes > 8*1024*1024 || len(row.Policy) > 131072 || len(row.RequestPath) > 2048 || len(row.Code) > 2048 || len(row.ProviderCode) > 2048 || len(row.Tool) > 2048 || len(row.Correlation) > 2048 || len(row.ClientCode) > 256 {
			return nil, ErrMediaOperationsQueryTooBroad
		}
		logs = append(logs, row)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrMediaOperationsQueryFailed
	}
	clickhouse := logDB.Dialector.Name() == "clickhouse"
	for _, row := range logs {
		name := row.ModelName
		if name == "" {
			name = MediaOperationsUnknownModel
		}
		path := strings.TrimPrefix(mediaOperationsScalar(row.RequestPath, clickhouse), "/harness")
		addEvidence(name, catalog.SubmitPaths[path])

		// Client tool names describe intent, not an authoritative model capability.
		// They never assign or overwrite the server-observed media kind.

	}
	// Resolve observed media evidence before applying the kind filter. A model
	// supported by conflicting media evidence remains explicitly unknown.
	for name, item := range models {
		item.MediaKind = kindFor(name)
		if !include(name) {
			delete(models, name)
			delete(accumulators, name)
		}
	}
	for _, task := range tasks {
		name := task.OriginalModel
		if name == "" {
			name = MediaOperationsUnknownModel
		}
		_, mediaPlatform := catalog.Platforms[task.Platform]
		if kindFor(name) == "unknown" && !mediaPlatform && task.Platform != string(constant.TaskPlatformMidjourney) && !slices.Contains(videoActions, task.Action) {
			continue
		}
		if !include(name) {
			continue
		}
		item := ensure(name)
		history := task.SubmitTime >= filter.Start && task.SubmitTime <= filter.End
		if history {
			accumulators[name].history.AcceptedTasks++
			all.history.AcceptedTasks++
		}
		switch task.Status {
		case TaskStatusSuccess:
			if history {
				item.Tasks.Success++
			}
		case TaskStatusFailure:
			if history {
				item.Tasks.Failure++
			}
		case TaskStatusNotStart:
			item.Tasks.NotStart++
			response.Summary.NotStart++
		case TaskStatusSubmitted:
			item.Tasks.Submitted++
			response.Summary.Submitted++
		case TaskStatusQueued:
			item.Tasks.Queued++
			response.Summary.Queued++
		case TaskStatusInProgress:
			item.Tasks.InProgress++
			response.Summary.InProgress++
		default:
			item.Tasks.Unknown++
			response.Summary.Unknown++
		}
		if task.Status != TaskStatusSuccess && task.Status != TaskStatusFailure {
			failures, _ := strconv.Atoi(task.PollFailures)
			if failures > 0 {
				item.Poll.TasksWithFailures++
				item.Poll.MaxConsecutiveFailures = max(item.Poll.MaxConsecutiveFailures, failures)
			}
		}
	}
	categories := map[string]*MediaOperationsCount{}
	categoryRequests := map[string]map[mediaOperationsRequestKey]struct{}{}
	stages := map[string]*MediaOperationsStage{}
	stageRequests := map[string]map[mediaOperationsRequestKey]struct{}{}

	for _, row := range logs {
		name := row.ModelName
		if name == "" {
			name = MediaOperationsUnknownModel
		}
		if !include(name) {
			continue
		}
		// A media submit path is evidence of the route used, even when its old
		// model no longer appears in the current catalog. Other logs are excluded.
		path := strings.TrimPrefix(mediaOperationsScalar(row.RequestPath, clickhouse), "/harness")
		_, mediaPath := catalog.SubmitPaths[path]
		if kindFor(name) == "unknown" && models[name] == nil && row.Type != LogTypeClientError && !mediaPath {
			continue
		}
		if row.Type == LogTypeClientError {
			tool := mediaOperationsScalar(row.Tool, clickhouse)
			if mediaOperationsScalar(row.Source, clickhouse) != "client" {
				continue
			}
			if tool != "generate_image" && tool != "generate_image_project" && tool != "generate_video" && tool != "generate_video_project" {
				continue
			}
		}
		ensure(name)
		a := accumulators[name]
		key := mediaOperationsRequestKey{User: row.UserID, Request: row.RequestID}
		// Missing historical RIDs cannot be truthfully deduplicated into requests.
		hasRequest := key.Request != ""
		source, stage := "gateway", "unknown"
		code := mediaOperationsScalar(row.Code, clickhouse)
		if provider := mediaOperationsScalar(row.ProviderCode, clickhouse); provider != "" {
			code = provider
		}
		if row.Type == LogTypeClientError {
			source = "client"
			stage = mediaOperationsScalar(row.ClientStage, clickhouse)
			code = row.ClientCode
			switch stage {
			case "preflight", "submit", "query", "local-save":
			default:
				stage = "unknown"
			}
			a.history.ClientReportEvents++
			all.history.ClientReportEvents++
			correlation := mediaOperationsScalar(row.Correlation, clickhouse)
			key.Request = correlation
			hasRequest = correlation != ""
			if hasRequest {
				a.correlated[key] = struct{}{}
				all.correlated[key] = struct{}{}
			}
		} else if row.Type == LogTypeError {
			switch observed := mediaOperationsScalar(row.Stage, clickhouse); observed {
			case "submit", "task_poll", "task_terminal", "task_timeout":
				stage = observed
			default:
				if mediaPath {
					stage = "submit"
				}
			}
			a.history.ErrorEvents++
			all.history.ErrorEvents++
			if hasRequest {
				a.errors[key] = struct{}{}
				all.errors[key] = struct{}{}
				switch stage {
				case "submit":
					a.submit[key] = struct{}{}
					all.submit[key] = struct{}{}
				case "task_poll", "task_terminal", "task_timeout":
					a.terminal[key] = struct{}{}
					all.terminal[key] = struct{}{}
				default:
					a.unknown[key] = struct{}{}
					all.unknown[key] = struct{}{}
				}
			}
		}
		policy := mediaOperationsScalar(row.Policy, clickhouse)
		if len(policy) > 131072 {
			return nil, ErrMediaOperationsQueryTooBroad
		}
		if policy != "" && policy != "null" && hasRequest && row.Type != LogTypeClientError {
			var events []mediaOperationsPolicyEvent
			if err := common.UnmarshalJsonStr(policy, &events); err != nil {
				return nil, ErrMediaOperationsQueryFailed
			}
			if len(events) > 512 {
				return nil, ErrMediaOperationsQueryTooBroad
			}
			for _, event := range events {
				if event.Attempt > 0 && event.ChannelID > 0 {
					attempt := mediaOperationsAttemptKey{mediaOperationsRequestKey: key, Attempt: event.Attempt, Channel: event.ChannelID}
					a.attempts[attempt] = struct{}{}
					all.attempts[attempt] = struct{}{}
				}
			}
		}
		if row.Type == LogTypeConsume {
			continue
		}
		category := mediaOperationsCategory(code)
		if row.Type == LogTypeError && mediaOperationsScalar(row.ProviderCode, clickhouse) == "" {
			// Legacy logs predate provider_error_code. Match only explicit semantic
			// messages internally; never return the raw error text or infer HTTP source.
			if legacy := mediaOperationsLegacyCategory(row.ErrorContent); legacy != "unknown" {
				category = legacy
			}
		}
		if categories[category] == nil {
			categories[category] = &MediaOperationsCount{Category: category}
			categoryRequests[category] = map[mediaOperationsRequestKey]struct{}{}
		}
		categories[category].Events++
		// Client and gateway errors stay separate in stage rows; categories count
		// observed events from both sources without treating diagnostics as relays.
		if hasRequest {
			categoryRequests[category][key] = struct{}{}
		}
		stageKey := source + ":" + stage
		if stages[stageKey] == nil {
			stages[stageKey] = &MediaOperationsStage{Source: source, Stage: stage}
			stageRequests[stageKey] = map[mediaOperationsRequestKey]struct{}{}
		}
		stages[stageKey].Events++
		if hasRequest {
			stageRequests[stageKey][key] = struct{}{}
		}
	}
	if len(models) > 5000 {
		return nil, ErrMediaOperationsQueryTooBroad
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		models[name].History = accumulators[name].result()
		response.Models = append(response.Models, *models[name])
	}
	response.HistorySummary.MediaOperationsHistory = all.result()
	categoryNames := make([]string, 0, len(categories))
	for name := range categories {
		categoryNames = append(categoryNames, name)
	}
	sort.Strings(categoryNames)
	for _, name := range categoryNames {
		categories[name].Requests = len(categoryRequests[name])
		response.HistorySummary.Categories = append(response.HistorySummary.Categories, *categories[name])
	}
	stageNames := make([]string, 0, len(stages))
	for name := range stages {
		stageNames = append(stageNames, name)
	}
	sort.Strings(stageNames)
	for _, name := range stageNames {
		stages[name].Requests = len(stageRequests[name])
		response.HistorySummary.Stages = append(response.HistorySummary.Stages, *stages[name])
	}
	if err := ctx.Err(); err != nil {
		return nil, ErrMediaOperationsQueryFailed
	}
	return response, nil
}
