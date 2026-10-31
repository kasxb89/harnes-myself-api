package controller

import (
	"io"
	"net/http"
	"os"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func GetYcapiCatalog(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	data, err := ycapi.Snapshot()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("YCAPI catalog is unavailable"))
		return
	}
	common.ApiSuccess(c, data)
}

// Configuration generation is read-only. Administrators still use the normal
// channel, versioned pricing and capacity APIs to apply any reviewed changes.
func GetYcapiConfiguration(c *gin.Context) {
	models, err := ycapi.Models()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	rate := decimal.NewFromFloat(operation_setting.USDExchangeRate)
	entries := make([]map[string]any, 0, len(models))
	for _, m := range models {
		expression, err := ycapi.BillingExpression(m.ID, rate)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		entry := map[string]any{"model": m.ID, "kind": m.Kind, "name": m.Name, "billing_expr": expression}
		if m.Kind == "text" {
			entry["upstream_model"] = m.UpstreamID
			entry["lane_id"] = m.Prices[0].LaneID
			entry["context_window"] = m.ContextWindow
			entry["max_output_tokens"] = m.MaxOutputTokens
		}
		entries = append(entries, entry)
	}
	c.Header("Cache-Control", "private, no-store")
	common.ApiSuccess(c, map[string]any{"status": "NOT_APPLIED", "usd_exchange_rate": rate.String(), "models": entries})
}
func PreviewYcapiQuote(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if os.Getenv("YCAPI_PREVIEW_ENABLED") != "1" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if c.ContentType() != "application/json" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid YCAPI preview request"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 128*1024+1))
	if err != nil || len(data) > 128*1024 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid YCAPI preview request"))
		return
	}
	var req ycapi.PreviewRequest
	if common.Unmarshal(data, &req) != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid YCAPI preview request"))
		return
	}
	// Reject route/price/asset overrides, including nested fields. The preview
	// accepts exactly this DTO; no user JSON is passed through to the provider.
	var fields map[string]common.RawMessage
	if common.Unmarshal(data, &fields) != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid YCAPI preview request"))
		return
	}
	for key := range fields {
		switch key {
		case "modelId", "specId", "prompt", "inputTokens", "outputTokens", "maxOutputTokens", "voice", "responseFormat", "speed":
		default:
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid YCAPI preview request"))
			return
		}
	}
	result, err := ycapi.BuildPreview(req)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid YCAPI preview request"))
		return
	}
	common.ApiSuccess(c, result)
}
