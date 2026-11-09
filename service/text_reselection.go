package service

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const textReselectionKey = "text_reselection_before_submit"

// SingleTextAttempt opts an individual managed chat request out of hidden
// server retries and cross-model fallback. Its client owns reselection only
// after receiving an explicit before-submit receipt.
func SingleTextAttempt(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Method == http.MethodPost &&
		c.Request.URL.Path == "/v1/chat/completions" && c.GetHeader("X-Yunshu-Text-Reselection") == "1"
}

// TextReselectionReceipt proves only the original local rejection before any
// upstream attempt or billing reservation. It does not authorize retrying a
// timeout, a provider rejection, or an already-started response.
type TextReselectionReceipt struct {
	SchemaVersion   int    `json:"schema_version"`
	Model           string `json:"model"`
	RequestID       string `json:"request_id"`
	SubmissionState string `json:"submission_state"`
	UpstreamSent    bool   `json:"upstream_submitted"`
	BillingReserved bool   `json:"billing_reserved"`
	Reason          string `json:"reason"`
}

// MarkTextModelUnavailableBeforeSubmit may only be called at the initial
// distributor or capacity rejection, before PrepareRequestBilling and Relay's
// attempt loop. Incoming request fields and provider errors cannot set it.
func MarkTextModelUnavailableBeforeSubmit(c *gin.Context, model string) {
	if !SingleTextAttempt(c) || c.Request.Context().Err() != nil ||
		c.Writer.Written() || model == "" || c.GetString(common.RequestIdKey) == "" ||
		c.GetString("expected_task_plugin_key") != "" {
		return
	}
	if _, pinned, _ := GetChannelConstraints(c).ResolvedPin(); pinned {
		return
	}
	if RequestPolicy(c).SessionMode == "strict" {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Set(textReselectionKey, &TextReselectionReceipt{
		SchemaVersion: 1, Model: model, RequestID: c.GetString(common.RequestIdKey),
		SubmissionState: "not_submitted", Reason: "model_unavailable",
	})
}

// TextModelReselection reads the host-owned receipt for this HTTP request only.
func TextModelReselection(c *gin.Context) *TextReselectionReceipt {
	value, _ := c.Get(textReselectionKey)
	receipt, _ := value.(*TextReselectionReceipt)
	return receipt
}
