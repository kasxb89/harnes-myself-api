package model

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const VideoIngressRetentionSeconds int64 = 3600
const videoIngressCounterLimit int64 = 1_000_000_000

type VideoIngressPoint struct {
	StartTimestamp               int64    `json:"start_timestamp"`
	EndTimestamp                 int64    `json:"end_timestamp"`
	Seconds                      int64    `json:"seconds"`
	CoveredSeconds               int64    `json:"covered_seconds"`
	Coverage                     string   `json:"coverage"`
	SubmitRequests               *int64   `json:"submit_requests"`
	StatusRequests               *int64   `json:"status_requests"`
	SubmitQPS                    *float64 `json:"submit_qps"`
	StatusQPS                    *float64 `json:"status_qps"`
	SubmitPending                *int64   `json:"submit_pending"`
	SubmitAccepted               *int64   `json:"submit_accepted"`
	SubmitEndedWithoutAcceptance *int64   `json:"submit_ended_without_acceptance"`
}
type VideoIngressSnapshot struct {
	Status             string              `json:"status"`
	Scope              string              `json:"scope"`
	FiltersApplied     []string            `json:"filters_applied"`
	HistoryPersistence string              `json:"history_persistence"`
	RetentionSeconds   int64               `json:"retention_seconds"`
	ProcessStartedAt   *int64              `json:"process_started_at"`
	CoverageStart      *int64              `json:"coverage_start"`
	CoverageEnd        *int64              `json:"coverage_end"`
	Limitations        []string            `json:"limitations"`
	Points             []VideoIngressPoint `json:"points"`
}
type videoIngressBucket struct {
	at                                       int64
	submit, status, pending, accepted, ended int64
	lost                                     bool
}

// VideoIngressRecorder holds a fixed 3601-slot ring: one current partial
// second plus the preceding 3600 complete seconds. It retains no identities.
type VideoIngressRecorder struct {
	mu            sync.Mutex
	started       int64
	coverageStart int64
	newest        int64
	buckets       [3601]videoIngressBucket
}
type VideoIngressObservation struct {
	recorder       *VideoIngressRecorder
	second         int64
	submit         bool
	tracked        bool
	classification atomic.Int32
}

var processVideoIngress atomic.Pointer[VideoIngressRecorder]

func NewVideoIngressRecorder(start time.Time) *VideoIngressRecorder {
	return &VideoIngressRecorder{started: start.Unix(), coverageStart: start.Unix() + 1, newest: start.Unix()}
}
func ProcessVideoIngressRecorder() *VideoIngressRecorder {
	if current := processVideoIngress.Load(); current != nil {
		return current
	}
	candidate := NewVideoIngressRecorder(time.Now())
	if processVideoIngress.CompareAndSwap(nil, candidate) {
		return candidate
	}
	return processVideoIngress.Load()
}
func (r *VideoIngressRecorder) Begin(operation string, at time.Time) *VideoIngressObservation {
	observation := &VideoIngressObservation{recorder: r, second: at.Unix(), submit: operation == "submit"}
	if r == nil || operation != "submit" && operation != "status" || observation.second < 0 {
		return observation
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if observation.second < r.newest-VideoIngressRetentionSeconds {
		return observation
	}
	r.newest = max(r.newest, observation.second)
	bucket := &r.buckets[observation.second%int64(len(r.buckets))]
	if bucket.at != observation.second {
		*bucket = videoIngressBucket{at: observation.second}
	}
	if bucket.submit >= videoIngressCounterLimit || bucket.status >= videoIngressCounterLimit {
		bucket.lost = true
		return observation
	}
	if observation.submit {
		bucket.submit++
		bucket.pending++
	} else {
		bucket.status++
	}
	observation.tracked = true
	return observation
}

type videoIngressContextKey struct{}

func (o *VideoIngressObservation) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, videoIngressContextKey{}, o)
}
func VideoIngressRequestObserved(ctx context.Context) bool {
	_, ok := ctx.Value(videoIngressContextKey{}).(*VideoIngressObservation)
	return ok
}
func MarkVideoIngressAccepted(ctx context.Context) {
	if observation, ok := ctx.Value(videoIngressContextKey{}).(*VideoIngressObservation); ok {
		observation.classify(true)
	}
}
func (o *VideoIngressObservation) classify(accepted bool) {
	if o == nil || o.recorder == nil || !o.tracked || !o.submit {
		return
	}
	state := int32(2)
	if accepted {
		state = 1
	}
	if !o.classification.CompareAndSwap(0, state) {
		return
	}
	r := o.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	bucket := &r.buckets[o.second%int64(len(r.buckets))]
	if bucket.at != o.second {
		return
	}
	if bucket.pending <= 0 {
		bucket.lost = true
		return
	}
	bucket.pending--
	if accepted {
		bucket.accepted++
	} else {
		bucket.ended++
	}
}
func (o *VideoIngressObservation) Finish(accepted bool) { o.classify(accepted) }

func VideoIngressForProcess(points []VideoUsageTrendPoint, asOf int64) VideoIngressSnapshot {
	return processVideoIngress.Load().Snapshot(points, asOf)
}
func (r *VideoIngressRecorder) Snapshot(points []VideoUsageTrendPoint, asOf int64) VideoIngressSnapshot {
	result := VideoIngressSnapshot{Status: "not_started", Scope: "all_video_ingress_current_process", FiltersApplied: []string{"start_timestamp", "end_timestamp"}, HistoryPersistence: "not_collected", RetentionSeconds: VideoIngressRetentionSeconds, Limitations: []string{"date_only_independent_of_task_filters", "current_process_not_all_instances", "restart_and_expired_history_unknown", "incomplete_buckets_are_not_zero", "http_handler_entry_not_proxy_or_network_arrival", "proxy_tls_http_parser_and_gin_redirects_not_observed", "unclassified_dynamic_media_routes_excluded", "generic_task_id_reads_unclassified_before_auth", "saturated_counter_buckets_unknown", "ended_without_acceptance_does_not_prove_not_submitted"}, Points: []VideoIngressPoint{}}
	if r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		result.Status = "live_process_only"
		start := max(r.coverageStart, asOf-VideoIngressRetentionSeconds, r.newest-VideoIngressRetentionSeconds)
		end := asOf
		result.ProcessStartedAt = &r.started
		result.CoverageStart = &start
		result.CoverageEnd = &end
	}
	for _, point := range points {
		item := VideoIngressPoint{StartTimestamp: point.StartTimestamp, EndTimestamp: point.EndTimestamp, Seconds: point.Seconds, Coverage: "unavailable"}
		if r != nil && result.CoverageStart != nil {
			item.CoveredSeconds = max(int64(0), min(point.EndTimestamp, *result.CoverageEnd)-max(point.StartTimestamp, *result.CoverageStart))
			if item.CoveredSeconds > 0 {
				item.Coverage = "partial"
			}
			if item.Seconds > 0 && item.CoveredSeconds == item.Seconds {
				var submit, status, pending, accepted, ended int64
				complete := true
				for second := point.StartTimestamp; second < point.EndTimestamp; second++ {
					bucket := r.buckets[second%int64(len(r.buckets))]
					if bucket.at != second {
						continue
					}
					if bucket.lost {
						complete = false
						break
					}
					submit += bucket.submit
					status += bucket.status
					pending += bucket.pending
					accepted += bucket.accepted
					ended += bucket.ended
				}
				if complete {
					item.Coverage = "complete"
					item.SubmitRequests = &submit
					item.StatusRequests = &status
					item.SubmitPending = &pending
					item.SubmitAccepted = &accepted
					item.SubmitEndedWithoutAcceptance = &ended
					submitQPS := float64(submit) / float64(item.Seconds)
					statusQPS := float64(status) / float64(item.Seconds)
					item.SubmitQPS = &submitQPS
					item.StatusQPS = &statusQPS
				}
			}
		}
		result.Points = append(result.Points, item)
	}
	return result
}
