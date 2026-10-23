package controller

import (
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetMediaConcurrency(c *gin.Context) {
	snapshot, err := model.GetMediaConcurrencySnapshot(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to load media concurrency configuration"))
		return
	}
	writeMediaConcurrencySnapshot(c, snapshot)
}

func SaveMediaConcurrency(c *gin.Context) {
	fields, ok := readMediaConcurrencyRequest(c)
	if !ok {
		return
	}
	for key := range fields {
		if key != "schema_version" && key != "expected_revision" && key != "defaults" && key != "pool_overrides" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
			return
		}
	}
	var request struct {
		SchemaVersion    int                            `json:"schema_version"`
		ExpectedRevision string                         `json:"expected_revision"`
		Defaults         model.MediaConcurrencyDefaults `json:"defaults"`
		PoolOverrides    []model.MediaPoolOverride      `json:"pool_overrides"`
	}
	encoded, err := common.Marshal(fields)
	if err != nil || common.Unmarshal(encoded, &request) != nil || request.SchemaVersion != 1 || request.ExpectedRevision == "" || len(request.ExpectedRevision) > 128 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	var defaults map[string]common.RawMessage
	if common.Unmarshal(fields["defaults"], &defaults) != nil || len(defaults) != 2 || defaults["video"] == nil || defaults["image"] == nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	var overrides []map[string]common.RawMessage
	if value, present := fields["pool_overrides"]; present {
		if common.Unmarshal(value, &overrides) != nil || overrides == nil || len(overrides) > 128 {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
			return
		}
		for _, override := range overrides {
			if len(override) != 2 || override["pool_id"] == nil || override["limit"] == nil {
				common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
				return
			}
		}
	}
	policy := model.MediaConcurrencyPolicy{SchemaVersion: request.SchemaVersion, Defaults: request.Defaults, PoolOverrides: request.PoolOverrides}
	_, err = model.SaveMediaConcurrencyPolicy(c.Request.Context(), request.ExpectedRevision, policy)
	if errors.Is(err, model.ErrMediaPolicyConflict) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Configuration changed; refresh and try again"), gin.H{"code": "MEDIA_CONCURRENCY_POLICY_CONFLICT"})
		return
	}
	if errors.Is(err, model.ErrInvalidMediaConcurrencyPolicy) {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to save media concurrency configuration"))
		return
	}
	GetMediaConcurrency(c)
}

// RefreshMediaConcurrency registers current configured credentials and
// reconciles local ledgers. It never probes the supplier or generates media.
func RefreshMediaConcurrency(c *gin.Context) {
	fields, ok := readMediaConcurrencyRequest(c)
	if !ok {
		return
	}
	var version int
	if len(fields) != 1 || common.Unmarshal(fields["schema_version"], &version) != nil || version != 1 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	if err := model.RefreshMediaConcurrencyPools(c.Request.Context()); err != nil {
		if errors.Is(err, model.ErrMediaLegacyIdentity) {
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to reconcile media account pools"), gin.H{"code": "MEDIA_LEGACY_IDENTITY_UNCONFIRMED"})
			return
		}
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to reconcile media account pools"))
		return
	}
	GetMediaConcurrency(c)
}

func readMediaConcurrencyRequest(c *gin.Context) (map[string]common.RawMessage, bool) {
	if c.ContentType() != "application/json" {
		common.ApiErrorStatus(c, http.StatusUnsupportedMediaType, common.NewMessage("Invalid parameters"))
		return nil, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusRequestEntityTooLarge, common.NewMessage("Invalid parameters"))
		return nil, false
	}
	var fields map[string]common.RawMessage
	if common.Unmarshal(body, &fields) != nil || fields == nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return nil, false
	}
	return fields, true
}

func writeMediaConcurrencySnapshot(c *gin.Context, snapshot model.MediaConcurrencySnapshot) {
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{
		"schema_version": snapshot.Policy.SchemaVersion,
		"revision":       snapshot.Policy.Revision,
		"defaults":       snapshot.Policy.Defaults,
		"pool_overrides": snapshot.Policy.PoolOverrides,
		"pools":          snapshot.Pools,
	})
}
