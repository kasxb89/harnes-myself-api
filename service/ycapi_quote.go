package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type YcapiSpeechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	ResponseFormat string   `json:"response_format"`
	Speed          *float64 `json:"speed,omitempty"`
	QuoteID        string   `json:"quote_id,omitempty"`
}

// QuoteYcapiSpeech never reserves funds or calls an upstream. Audio output uses
// an estimate of 8192 tokens, replaced only by supplier-reported usage.
func QuoteYcapiSpeech(c *gin.Context, req YcapiSpeechRequest, issued int64) (map[string]any, error) {
	m, ok := ycapi.Lookup(req.Model)
	if !ok || m.Kind != "audio" || c.GetInt("id") <= 0 {
		return nil, errors.New("speech quote unavailable")
	}
	groups := CapacityAccountGroups(c)
	if !slices.Contains(FilterUnavailableCatalogModels(GetGroupsEnabledModels(groups), groups), req.Model) {
		return nil, errors.New("speech model unavailable")
	}
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		value, _ := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		allowed, _ := value.(map[string]bool)
		if !allowed[req.Model] {
			return nil, errors.New("speech model unavailable")
		}
	}
	// Struct order matches the client's exact canonical fingerprint order.
	request := struct {
		Model          string   `json:"model"`
		Input          string   `json:"input"`
		Voice          string   `json:"voice"`
		ResponseFormat string   `json:"response_format"`
		Speed          *float64 `json:"speed,omitempty"`
	}{req.Model, req.Input, req.Voice, req.ResponseFormat, req.Speed}
	bytes, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	input, output, maxOutput := len([]byte(req.Input)), 8192, 8192
	preview, err := ycapi.BuildPreview(ycapi.PreviewRequest{ModelID: req.Model, SpecID: "tokens", Prompt: req.Input, InputTokens: &input, OutputTokens: &output, MaxOutputTokens: &maxOutput, Voice: req.Voice, ResponseFormat: req.ResponseFormat, Speed: req.Speed})
	if err != nil {
		return nil, err
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "auto" || group == "" {
		return nil, errors.New("speech quote requires an exact group")
	}
	rate := decimal.NewFromFloat(operation_setting.USDExchangeRate)
	expected, err := ycapi.BillingExpression(req.Model, rate)
	actual, configured := billing_setting.GetBillingExpr(req.Model)
	if err != nil || !configured || expected != actual || billing_setting.GetBillingMode(req.Model) != billing_setting.BillingModeTieredExpr {
		return nil, errors.New("speech pricing changed")
	}
	if preview.Retail == "" || !rate.IsPositive() {
		return nil, errors.New("speech quote unavailable")
	}
	ratio := decimal.NewFromFloat(GetUserGroupRatio(common.GetContextKeyString(c, constant.ContextKeyUserGroup), group))
	snapshot := &billingexpr.BillingSnapshot{ExprString: actual, ExprHash: billingexpr.ExprHashString(actual), GroupRatio: ratio.InexactFloat64(), QuotaPerUnit: common.QuotaPerUnit, TaskUsageBilling: true, BillingTime: issued}
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snapshot, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: map[string]any{"input_tokens": float64(input), "output_tokens": float64(output)}})
	if err != nil || result.Clamp != nil || result.ActualQuotaAfterGroup < 0 {
		return nil, errors.New("speech estimate exceeds request limit")
	}
	quota := result.ActualQuotaAfterGroup
	digest := sha256.Sum256(bytes)
	fingerprint := hex.EncodeToString(digest[:])
	account := common.GenerateHMAC(fmt.Sprintf("ycapi-audio-account:%d:%d:%s", c.GetInt("id"), c.GetInt("token_id"), group))
	revision := common.GenerateHMAC("ycapi-audio-price:" + expected + ":" + rate.String() + ":" + ratio.String() + ":" + fingerprint + ":" + account)
	quoteID := fmt.Sprintf("%d.%s", issued, common.GenerateHMAC(fmt.Sprintf("ycapi-audio-quote:%s:%d", revision, issued)))
	return map[string]any{"object": "audio.quote", "schema_version": 1, "quote_id": quoteID, "account_binding": account, "model": req.Model, "request_fingerprint": fingerprint, "total_quota": quota,
		"estimated_total": decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(rate).String(), "currency": "CNY", "pricing_revision": revision,
		"created_at": issued, "expires_at": issued + 60, "estimate_only": true, "charged": false, "submitted": false, "reserved": false}, nil
}

func ValidateYcapiSpeechQuote(c *gin.Context, req YcapiSpeechRequest) error {
	stamp, _, ok := strings.Cut(req.QuoteID, ".")
	issued, err := strconv.ParseInt(stamp, 10, 64)
	now := time.Now().Unix()
	if !ok || err != nil || issued > now || issued+60 <= now {
		return errors.New("speech quote expired")
	}
	quote, err := QuoteYcapiSpeech(c, req, issued)
	if err != nil || quote["quote_id"] != req.QuoteID {
		return errors.New("speech quote mismatch")
	}
	return nil
}
