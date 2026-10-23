package model

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const (
	MaxClientWorkloadBodyBytes = 262144
	maxClientWorkloadRows      = 256
	maxClientWorkloadSequence  = int64(9007199254740991)
)

var (
	ErrClientWorkloadInvalid = errors.New("invalid client workload snapshot")
	clientWorkloadUUID       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// ClientWorkloadSnapshot contains content-free observations only. The caller
// must separately verify presence, current login, tenant ownership and links.
type ClientWorkloadSnapshot struct {
	SchemaVersion    int                           `json:"schema_version"`
	RuntimeSessionID string                        `json:"runtime_session_id"`
	Sequence         int64                         `json:"sequence"`
	Complete         bool                          `json:"complete"`
	Collections      ClientWorkloadCollections     `json:"collections"`
	Activities       []ClientWorkloadActivityInput `json:"activities"`
	Shots            []ClientWorkloadShotInput     `json:"shots"`
}

type ClientWorkloadCollections struct {
	ChatActivity   string `json:"chat_activity"`
	ProjectRun     string `json:"project_run"`
	StoryboardTask string `json:"storyboard_task"`
	LocalOperation string `json:"local_operation"`
	Shots          string `json:"shots"`
}

type ClientWorkloadActivityInput struct {
	ActivityID       string   `json:"activity_id"`
	Kind             string   `json:"kind"`
	WorkState        string   `json:"work_state"`
	Phase            string   `json:"phase"`
	ChatSessionID    string   `json:"chat_session_id,omitempty"`
	ProjectID        string   `json:"project_id,omitempty"`
	ParentActivityID string   `json:"parent_activity_id,omitempty"`
	ShotIDs          []string `json:"shot_ids,omitempty"`
	AttemptID        string   `json:"attempt_id,omitempty"`
	APITaskID        string   `json:"api_task_id,omitempty"`
	LocalJobID       string   `json:"local_job_id,omitempty"`
	Outcome          string   `json:"outcome,omitempty"`
}

type ClientWorkloadShotInput struct {
	ProjectID string `json:"project_id"`
	ShotID    string `json:"shot_id"`
}

// A struct field receives every occurrence, including escaped duplicate keys.
// Map decoding alone would silently retain the last value.
type clientWorkloadField struct {
	seen bool
	raw  common.RawMessage
}

func (field *clientWorkloadField) UnmarshalJSON(data []byte) error {
	if field.seen || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrClientWorkloadInvalid
	}
	field.seen = true
	field.raw = slices.Clone(data)
	return nil
}

// decodeClientWorkloadObject combines exact case-sensitive names with duplicate
// detection through field wrappers, while using the shared JSON codec throughout.
func decodeClientWorkloadObject(data []byte, wire any, allowed []string) error {
	var object map[string]common.RawMessage
	if common.Unmarshal(data, &object) != nil || object == nil {
		return ErrClientWorkloadInvalid
	}
	for key := range object {
		if !slices.Contains(allowed, key) {
			return ErrClientWorkloadInvalid
		}
	}
	if common.Unmarshal(data, wire) != nil {
		return ErrClientWorkloadInvalid
	}
	return nil
}

func ParseClientWorkloadSnapshot(data []byte) (ClientWorkloadSnapshot, error) {
	invalid := ClientWorkloadSnapshot{}
	if len(data) == 0 || len(data) > MaxClientWorkloadBodyBytes || !utf8.Valid(data) {
		return invalid, ErrClientWorkloadInvalid
	}
	var wire struct {
		SchemaVersion    clientWorkloadField `json:"schema_version"`
		RuntimeSessionID clientWorkloadField `json:"runtime_session_id"`
		Sequence         clientWorkloadField `json:"sequence"`
		Complete         clientWorkloadField `json:"complete"`
		Collections      clientWorkloadField `json:"collections"`
		Activities       clientWorkloadField `json:"activities"`
		Shots            clientWorkloadField `json:"shots"`
	}
	if decodeClientWorkloadObject(data, &wire, []string{"schema_version", "runtime_session_id", "sequence", "complete", "collections", "activities", "shots"}) != nil ||
		!wire.SchemaVersion.seen || !wire.RuntimeSessionID.seen || !wire.Sequence.seen || !wire.Complete.seen ||
		!wire.Collections.seen || !wire.Activities.seen || !wire.Shots.seen {
		return invalid, ErrClientWorkloadInvalid
	}
	var snapshot ClientWorkloadSnapshot
	if common.Unmarshal(data, &snapshot) != nil || snapshot.SchemaVersion != 1 || !snapshot.Complete ||
		!clientWorkloadUUID.MatchString(snapshot.RuntimeSessionID) || snapshot.Sequence < 1 || snapshot.Sequence > maxClientWorkloadSequence ||
		snapshot.Activities == nil || snapshot.Shots == nil || len(snapshot.Activities)+len(snapshot.Shots) > maxClientWorkloadRows {
		return invalid, ErrClientWorkloadInvalid
	}
	if err := validateClientWorkloadCollections(wire.Collections.raw); err != nil {
		return invalid, err
	}
	var activities []common.RawMessage
	if common.Unmarshal(wire.Activities.raw, &activities) != nil {
		return invalid, ErrClientWorkloadInvalid
	}
	seenActivities := make(map[string]struct{}, len(activities))
	for i, raw := range activities {
		activity, err := parseClientWorkloadActivity(raw)
		if err != nil {
			return invalid, err
		}
		if _, duplicate := seenActivities[activity.ActivityID]; duplicate || !snapshot.Collections.Collected(activity.Kind) {
			return invalid, ErrClientWorkloadInvalid
		}
		seenActivities[activity.ActivityID] = struct{}{}
		snapshot.Activities[i] = activity
	}
	var shots []common.RawMessage
	if common.Unmarshal(wire.Shots.raw, &shots) != nil || (len(shots) != 0 && snapshot.Collections.Shots != "collected") {
		return invalid, ErrClientWorkloadInvalid
	}
	seenShots := make(map[ClientWorkloadShotInput]struct{}, len(shots))
	for i, raw := range shots {
		var shotWire struct {
			ProjectID clientWorkloadField `json:"project_id"`
			ShotID    clientWorkloadField `json:"shot_id"`
		}
		var shot ClientWorkloadShotInput
		if decodeClientWorkloadObject(raw, &shotWire, []string{"project_id", "shot_id"}) != nil ||
			!shotWire.ProjectID.seen || !shotWire.ShotID.seen || common.Unmarshal(raw, &shot) != nil ||
			!validClientWorkloadOpaqueID(shot.ProjectID, 128) || !validClientWorkloadOpaqueID(shot.ShotID, 128) {
			return invalid, ErrClientWorkloadInvalid
		}
		if _, duplicate := seenShots[shot]; duplicate {
			return invalid, ErrClientWorkloadInvalid
		}
		seenShots[shot] = struct{}{}
		snapshot.Shots[i] = shot
	}
	return snapshot, nil
}

// Collected reports only the explicit collection flag, never inferred presence.
func (collections ClientWorkloadCollections) Collected(kind string) bool {
	switch kind {
	case "chat_activity":
		return collections.ChatActivity == "collected"
	case "project_run":
		return collections.ProjectRun == "collected"
	case "storyboard_task":
		return collections.StoryboardTask == "collected"
	case "local_operation":
		return collections.LocalOperation == "collected"
	case "shots":
		return collections.Shots == "collected"
	default:
		return false
	}
}

func validateClientWorkloadCollections(data []byte) error {
	var wire struct {
		ChatActivity   clientWorkloadField `json:"chat_activity"`
		ProjectRun     clientWorkloadField `json:"project_run"`
		StoryboardTask clientWorkloadField `json:"storyboard_task"`
		LocalOperation clientWorkloadField `json:"local_operation"`
		Shots          clientWorkloadField `json:"shots"`
	}
	if decodeClientWorkloadObject(data, &wire, []string{"chat_activity", "project_run", "storyboard_task", "local_operation", "shots"}) != nil {
		return ErrClientWorkloadInvalid
	}
	for _, field := range []clientWorkloadField{wire.ChatActivity, wire.ProjectRun, wire.StoryboardTask, wire.LocalOperation, wire.Shots} {
		var value string
		if !field.seen || common.Unmarshal(field.raw, &value) != nil || (value != "collected" && value != "not_collected") {
			return ErrClientWorkloadInvalid
		}
	}
	return nil
}

func parseClientWorkloadActivity(data []byte) (ClientWorkloadActivityInput, error) {
	invalid := ClientWorkloadActivityInput{}
	var wire struct {
		ActivityID       clientWorkloadField `json:"activity_id"`
		Kind             clientWorkloadField `json:"kind"`
		WorkState        clientWorkloadField `json:"work_state"`
		Phase            clientWorkloadField `json:"phase"`
		ChatSessionID    clientWorkloadField `json:"chat_session_id"`
		ProjectID        clientWorkloadField `json:"project_id"`
		ParentActivityID clientWorkloadField `json:"parent_activity_id"`
		ShotIDs          clientWorkloadField `json:"shot_ids"`
		AttemptID        clientWorkloadField `json:"attempt_id"`
		APITaskID        clientWorkloadField `json:"api_task_id"`
		LocalJobID       clientWorkloadField `json:"local_job_id"`
		Outcome          clientWorkloadField `json:"outcome"`
	}
	if decodeClientWorkloadObject(data, &wire, []string{"activity_id", "kind", "work_state", "phase", "chat_session_id", "project_id", "parent_activity_id", "shot_ids", "attempt_id", "api_task_id", "local_job_id", "outcome"}) != nil ||
		!wire.ActivityID.seen || !wire.Kind.seen || !wire.WorkState.seen || !wire.Phase.seen {
		return invalid, ErrClientWorkloadInvalid
	}
	var activity ClientWorkloadActivityInput
	if common.Unmarshal(data, &activity) != nil || !validClientWorkloadOpaqueID(activity.ActivityID, 128) {
		return invalid, ErrClientWorkloadInvalid
	}
	for _, field := range []struct {
		wire  clientWorkloadField
		value string
		max   int
	}{
		{wire.ChatSessionID, activity.ChatSessionID, 128},
		{wire.ProjectID, activity.ProjectID, 128},
		{wire.ParentActivityID, activity.ParentActivityID, 128},
		{wire.APITaskID, activity.APITaskID, 256},
		{wire.LocalJobID, activity.LocalJobID, 128},
	} {
		if field.wire.seen && !validClientWorkloadOpaqueID(field.value, field.max) {
			return invalid, ErrClientWorkloadInvalid
		}
	}
	if wire.AttemptID.seen && !clientWorkloadUUID.MatchString(activity.AttemptID) {
		return invalid, ErrClientWorkloadInvalid
	}
	seenShots := make(map[string]struct{}, len(activity.ShotIDs))
	for _, shotID := range activity.ShotIDs {
		if !validClientWorkloadOpaqueID(shotID, 128) {
			return invalid, ErrClientWorkloadInvalid
		}
		if _, duplicate := seenShots[shotID]; duplicate {
			return invalid, ErrClientWorkloadInvalid
		}
		seenShots[shotID] = struct{}{}
	}
	var phases []string
	switch activity.Kind {
	case "chat_activity":
		if !wire.ChatSessionID.seen || wire.ProjectID.seen || wire.ParentActivityID.seen || wire.ShotIDs.seen || wire.AttemptID.seen || wire.APITaskID.seen || wire.LocalJobID.seen {
			return invalid, ErrClientWorkloadInvalid
		}
		phases = []string{"busy"}
	case "project_run":
		if !wire.ProjectID.seen || wire.ParentActivityID.seen || wire.ShotIDs.seen || wire.AttemptID.seen || wire.APITaskID.seen {
			return invalid, ErrClientWorkloadInvalid
		}
		phases = []string{"waiting", "preparing", "accepted", "queued", "generating", "saving", "editing"}
	case "storyboard_task":
		if !wire.AttemptID.seen {
			return invalid, ErrClientWorkloadInvalid
		}
		phases = []string{"waiting", "preparing", "submitting", "accepted", "queued", "generating", "saving"}
	case "local_operation":
		if wire.ShotIDs.seen || wire.AttemptID.seen || wire.APITaskID.seen {
			return invalid, ErrClientWorkloadInvalid
		}
		phases = []string{"downloading", "saving", "composing", "publishing"}
	default:
		return invalid, ErrClientWorkloadInvalid
	}
	switch activity.WorkState {
	case "active":
		if !slices.Contains(phases, activity.Phase) || wire.Outcome.seen {
			return invalid, ErrClientWorkloadInvalid
		}
	case "unknown":
		if activity.Phase != "unknown" || wire.Outcome.seen {
			return invalid, ErrClientWorkloadInvalid
		}
	case "ended":
		if activity.Phase != "ended" || !wire.Outcome.seen || !slices.Contains([]string{"succeeded", "failed", "cancelled", "interrupted"}, activity.Outcome) {
			return invalid, ErrClientWorkloadInvalid
		}
	default:
		return invalid, ErrClientWorkloadInvalid
	}
	return activity, nil
}

func validClientWorkloadOpaqueID(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || value == "." || value == ".." {
		return false
	}
	for i := range len(value) {
		character := value[i]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '.' || character == '_' || character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// Digest binds a sequence to its complete payload, independent of object key or
// set-like row ordering. It never mutates the caller's arrays or accepts an
// invalid directly constructed DTO. No time or server-derived fields are added.
func (snapshot ClientWorkloadSnapshot) Digest() (string, error) {
	raw, err := common.Marshal(snapshot)
	if err != nil {
		return "", ErrClientWorkloadInvalid
	}
	canonical, err := ParseClientWorkloadSnapshot(raw)
	if err != nil {
		return "", err
	}
	slices.SortFunc(canonical.Activities, func(a, b ClientWorkloadActivityInput) int {
		return strings.Compare(a.ActivityID, b.ActivityID)
	})
	for i := range canonical.Activities {
		slices.Sort(canonical.Activities[i].ShotIDs)
	}
	slices.SortFunc(canonical.Shots, func(a, b ClientWorkloadShotInput) int {
		if a.ProjectID != b.ProjectID {
			return strings.Compare(a.ProjectID, b.ProjectID)
		}
		return strings.Compare(a.ShotID, b.ShotID)
	})
	raw, err = common.Marshal(canonical)
	if err != nil {
		return "", ErrClientWorkloadInvalid
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
