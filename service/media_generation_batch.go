package service

import (
	"errors"
	"math"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/gin-gonic/gin"
)

const MediaGenerationBatchClientVersionHeader = "X-Yunshu-Media-Batch-Version"

var ErrMediaBatchClientUnsupported = errors.New("media_batch_client_unsupported")

// This explicit decoder capability is an SDK compatibility declaration, not
// authentication. Accepted original receipts are resolved before this gate.
func MediaGenerationBatchClientSupported(c *gin.Context) bool {
	return c != nil && c.GetHeader(MediaGenerationBatchClientVersionHeader) == "1"
}

// MediaGenerationBatch is frozen with the original quote. It is additive: old
// accepted operations have no batch and retain their original recovery rules.
type MediaGenerationBatch struct {
	SchemaVersion           int     `json:"schema_version"`
	RequestedOutputCount    int     `json:"requested_output_count"`
	ExpectedMainOutputCount int     `json:"expected_main_output_count"`
	OutputDurationSeconds   float64 `json:"output_duration_seconds"`
	TotalOutputSeconds      float64 `json:"total_output_seconds"`
	Meter                   string  `json:"meter"`
	AdmissionUnit           string  `json:"admission_unit"`
	AdmissionWeight         int     `json:"admission_weight"`
	PartialPolicy           string  `json:"partial_policy"`
	PolicyRevision          string  `json:"policy_revision"`
}

func mediaGenerationBatchMaximum(def model.ModelConfiguration, contract ycapi.MediaContract, expression string) int {
	if contract.Kind != "video" || def.MediaExecution == nil || def.MediaExecution.BatchPolicy == nil {
		return 1
	}
	p := def.MediaExecution.BatchPolicy
	if p.Policy != "qualified_native_batch" || model.ValidateModelMediaExecutionConfiguration(def) != nil || p.ExpressionSHA256 != billingexpr.ExprHashString(expression) || p.ContractSHA256 != MediaGenerationDigest(contract) {
		return 1
	}
	return p.MaxCount
}

// New batch admission rejects unreviewed modes before materials/quote I/O.
func validateMediaGenerationBatchRequest(def model.ModelConfiguration, contract ycapi.MediaContract, params map[string]any, expression string) error {
	maximum := mediaGenerationBatchMaximum(def, contract, expression)
	keys := billingexpr.UsedUsageKeys(expression)
	if contract.Kind == "video" && (keys["output_count"] || keys["total_output_seconds"]) && maximum <= 1 {
		return errors.New("model_price_unavailable")
	}
	if err := ycapi.ValidateMediaRuntimeQuantity(contract, params, maximum); err != nil {
		return err
	}
	if contract.Kind != "video" || maximum <= 1 {
		return nil
	}
	mode, present := params["mode"]
	if !present {
		return nil
	} // effective administrator/native defaults are checked after normalization before upload
	modeText, valid := mode.(string)
	if !valid || !slices.Contains(def.MediaExecution.BatchPolicy.Modes, modeText) {
		return errors.New("invalid_parameters")
	}
	return nil
}

func mediaGenerationBatchPriceConfigured(def model.ModelConfiguration, contract ycapi.MediaContract, expression string) bool {
	keys := billingexpr.UsedUsageKeys(expression)
	return contract.Kind != "video" || !keys["output_count"] && !keys["total_output_seconds"] || mediaGenerationBatchMaximum(def, contract, expression) > 1
}

func mediaGenerationQuantity(def model.ModelConfiguration, contract ycapi.MediaContract, expression string) map[string]any {
	maximum := mediaGenerationBatchMaximum(def, contract, expression)
	if maximum <= 1 {
		return nil
	}
	p := def.MediaExecution.BatchPolicy
	return map[string]any{"schema_version": 1, "parameter": "count", "minimum": 1, "maximum": maximum, "default": 1, "semantics": "native_operation_main_outputs", "meter": p.Meter, "admission_unit": "native_operation", "admission_weight": 1, "partial_policy": "retain_reservation_until_complete", "policy_revision": MediaGenerationDigest(p), "modes": slices.Clone(p.Modes)}
}

// The legacy seconds fact continues to mean one video's prior meter. Only an
// explicitly qualified price may opt into the two aggregate output facts.
func mediaGenerationBatchUsage(def model.ModelConfiguration, contract ycapi.MediaContract, params, facts map[string]any, expression string) (*MediaGenerationBatch, error) {
	if contract.Kind != "video" {
		return nil, nil
	}
	maximum := mediaGenerationBatchMaximum(def, contract, expression)
	if err := ycapi.ValidateMediaRuntimeQuantity(contract, params, maximum); err != nil {
		return nil, err
	}
	n := 1
	if raw, ok := params["count"].(float64); ok {
		n = int(raw)
	} // bounded integral1..50 above
	seconds, _ := facts["seconds"].(float64)
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > 3600 {
		return nil, errors.New("invalid_parameters")
	}
	facts["output_count"], facts["total_output_seconds"] = float64(n), float64(0)
	if maximum <= 1 {
		return nil, nil
	}
	p := def.MediaExecution.BatchPolicy
	mode, _ := params["mode"].(string)
	if !slices.Contains(p.Modes, mode) {
		return nil, errors.New("invalid_parameters")
	}
	requested := float64(0)
	if text, ok := params["duration"].(string); ok {
		requested, _ = strconv.ParseFloat(text, 64)
	}
	if value, ok := params["duration"].(float64); ok {
		requested = value
	}
	outputSeconds := float64(0)
	if p.DurationBasis == "requested_uniform" && requested > 0 && requested == seconds && !math.IsNaN(requested) && !math.IsInf(requested, 0) {
		outputSeconds = requested
	}
	total := outputSeconds * float64(n)
	if total > 3600 || p.Meter == "total_output_seconds" && total <= 0 {
		return nil, errors.New("invalid_parameters")
	}
	facts["total_output_seconds"] = total
	return &MediaGenerationBatch{SchemaVersion: 1, RequestedOutputCount: n, ExpectedMainOutputCount: n, OutputDurationSeconds: outputSeconds, TotalOutputSeconds: total, Meter: p.Meter, AdmissionUnit: "native_operation", AdmissionWeight: 1, PartialPolicy: "retain_reservation_until_complete", PolicyRevision: MediaGenerationDigest(p)}, nil
}

func validMediaGenerationBatch(batch *MediaGenerationBatch) bool {
	return batch != nil && batch.SchemaVersion == 1 && batch.RequestedOutputCount >= 1 && batch.RequestedOutputCount <= 50 && batch.ExpectedMainOutputCount == batch.RequestedOutputCount && slices.Contains([]string{"output_count", "total_output_seconds"}, batch.Meter) && batch.OutputDurationSeconds >= 0 && !math.IsNaN(batch.OutputDurationSeconds) && !math.IsInf(batch.OutputDurationSeconds, 0) && batch.TotalOutputSeconds == batch.OutputDurationSeconds*float64(batch.RequestedOutputCount) && batch.TotalOutputSeconds <= 3600 && (batch.Meter != "total_output_seconds" || batch.TotalOutputSeconds > 0) && batch.AdmissionUnit == "native_operation" && batch.AdmissionWeight == 1 && batch.PartialPolicy == "retain_reservation_until_complete" && len(batch.PolicyRevision) == 64
}

// MediaGenerationBatchTaskProjection only returns safe frozen counters and
// logical artifact grouping, never supplier URLs, private IDs or credentials.
func MediaGenerationBatchTaskProjection(t *model.Task, quote *model.MediaGenerationQuote) (map[string]any, map[string]map[string]any, error) {
	var frozen MediaGenerationFrozen
	if quote == nil || common.UnmarshalJsonStr(string(quote.UsageFactsJSON), &frozen) != nil {
		return nil, nil, errors.New("quote unavailable")
	}
	if frozen.Batch == nil {
		return nil, nil, nil
	}
	if !validMediaGenerationBatch(frozen.Batch) {
		return nil, nil, errors.New("invalid retained batch")
	}
	raw, err := common.Marshal(frozen.Batch)
	if err != nil {
		return nil, nil, err
	}
	public := map[string]any{}
	if err = common.Unmarshal(raw, &public); err != nil {
		return nil, nil, err
	}
	public["received_main_output_count"], public["completeness"] = 0, "pending"
	artifacts := map[string]map[string]any{}
	var state struct {
		Batch    *MediaGenerationBatch `json:"batch"`
		Snapshot struct {
			Batch struct {
				Received     int    `json:"received_main_output_count"`
				Completeness string `json:"completeness"`
				Failure      string `json:"failure_reason"`
			} `json:"batch"`
			Artifacts []struct {
				Key         string `json:"key"`
				Role        string `json:"role"`
				OutputID    string `json:"output_id"`
				OutputIndex *int   `json:"output_index"`
			} `json:"artifacts"`
		} `json:"snapshot"`
	}
	if len(t.PrivateData.PluginState) == 0 {
		return public, artifacts, nil
	}
	if common.Unmarshal(t.PrivateData.PluginState, &state) != nil || state.Batch == nil || MediaGenerationDigest(state.Batch) != MediaGenerationDigest(frozen.Batch) {
		return nil, nil, errors.New("invalid retained batch")
	}
	counters := state.Snapshot.Batch
	if counters.Received < 0 || counters.Received > 64 || !slices.Contains([]string{"pending", "complete", "partial", "mismatch"}, counters.Completeness) {
		return nil, nil, errors.New("invalid retained batch outputs")
	}
	if counters.Completeness == "complete" && counters.Received != frozen.Batch.ExpectedMainOutputCount {
		return nil, nil, errors.New("invalid retained batch outputs")
	}
	public["received_main_output_count"], public["completeness"] = counters.Received, counters.Completeness
	if counters.Failure != "" {
		if !slices.Contains([]string{"main_outputs_missing", "main_outputs_mismatch"}, counters.Failure) {
			return nil, nil, errors.New("invalid retained batch outputs")
		}
		public["failure_reason"] = counters.Failure
	}
	for _, a := range state.Snapshot.Artifacts {
		if a.Key == "" || !slices.Contains([]string{"main", "preview", "auxiliary", "duplicate"}, a.Role) {
			return nil, nil, errors.New("invalid retained batch outputs")
		}
		meta := map[string]any{"role": a.Role}
		if a.OutputIndex != nil {
			if *a.OutputIndex < 0 || *a.OutputIndex >= 64 || a.OutputID != "main-"+strconv.Itoa(*a.OutputIndex+1) {
				return nil, nil, errors.New("invalid retained batch outputs")
			}
			meta["output_id"], meta["output_index"] = a.OutputID, *a.OutputIndex
		}
		artifacts[a.Key] = meta
	}
	return public, artifacts, nil
}
