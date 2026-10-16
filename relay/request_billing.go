package relay

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// resolveRequestPrice computes one attempt's token quantities and installs the
// pricing snapshot on the RelayInfo (`PriceData` / `TieredBillingSnapshot`).
// Settle reads that snapshot, so every model the relay actually serves has to
// pass through here before the attempt runs.
func resolveRequestPrice(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	meta := &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer}
	if info.Request != nil && (needSensitiveCheck || constant.CountToken) {
		meta = info.Request.GetTokenCountMeta()
	} else {
		// Avoid building CombineText when only the pricing quantities are needed.
		switch request := info.Request.(type) {
		case *dto.GeneralOpenAIRequest:
			meta.MaxTokens = int(max(lo.FromPtr(request.MaxTokens), lo.FromPtr(request.MaxCompletionTokens)))
		case *dto.OpenAIResponsesRequest:
			meta.MaxTokens = int(lo.FromPtr(request.MaxOutputTokens))
		case *dto.ClaudeRequest:
			meta.MaxTokens = int(lo.FromPtr(request.MaxTokens))
		case *dto.ImageRequest:
			meta = request.GetTokenCountMeta()
		}
	}

	if needSensitiveCheck && meta != nil {
		if contains, words := service.CheckSensitiveText(meta.CombineText); contains {
			service.RequestPolicy(c).AddEvent(service.PolicyEvent{ErrorCode: string(types.ErrorCodeSensitiveWordsDetected), ErrorSource: "local", Decision: service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "global"}, Health: "unchanged"})
			message := fmt.Sprintf("user sensitive words detected: %s", strings.Join(words, ", "))
			logger.LogWarn(c, message)
			return types.NewError(errors.New(message), types.ErrorCodeSensitiveWordsDetected)
		}
	}

	tokens, err := service.EstimateRequestToken(c, meta, info)
	if err != nil {
		return types.NewError(err, types.ErrorCodeCountTokenFailed)
	}
	info.SetEstimatePromptTokens(tokens)

	if _, err := helper.ModelPriceHelper(c, info, tokens, meta); err != nil {
		return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
	}
	return nil
}

// PrepareRequestBilling estimates and reserves one request's charge. Transports
// provide the current request body through BodyStorage or BillingRequestInput;
// channel retries retain the resulting billing session and pricing snapshot.
func PrepareRequestBilling(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if apiErr := resolveRequestPrice(c, info); apiErr != nil {
		return apiErr
	}
	if info.PriceData.FreeModel {
		logger.LogInfo(c, common.LogText("model %s is free, skipping pre-consume", info.OriginModelName))
		return nil
	}
	return service.PreConsumeBilling(c, info.PriceData.QuotaToPreConsume, info)
}

// ReprepareFallbackBilling moves one request onto a fallback model and redoes the
// pricing work PrepareRequestBilling did for the requested model. Without it the
// fallback would be charged at the requested model's price, because settle reads
// the snapshot this installs rather than re-deriving a price from the model.
//
// The reservation only grows: a pricier fallback reserves the difference (and a
// final failure still refunds the whole session), a cheaper one leaves the
// reservation alone and settle returns the difference.
// @param c - request context.
// @param info - the request's relay info, whose billing session may be nil.
// @param model - the model this request is switching to.
// @returns nil when the request may proceed on the new model.
func ReprepareFallbackBilling(c *gin.Context, info *relaycommon.RelayInfo, model string) *types.NewAPIError {
	info.OriginModelName = model
	// The requested model's pricing identity must not survive into this attempt's
	// price resolution or the consume log.
	info.BillingModelName = ""
	info.TieredBillingSnapshot = nil
	if apiErr := resolveRequestPrice(c, info); apiErr != nil {
		return apiErr
	}
	if info.PriceData.FreeModel || info.Billing == nil {
		return nil
	}
	if err := info.Billing.Reserve(info.PriceData.QuotaToPreConsume); err != nil {
		return types.NewError(err, types.ErrorCodeModelPriceError)
	}
	return nil
}

// RefundFailedRequestBilling applies the common final-failure policy after all
// eligible attempts have ended. A settled BillingSession never refunds again.
func RefundFailedRequestBilling(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) *types.NewAPIError {
	if apiErr == nil {
		return nil
	}
	apiErr = service.NormalizeViolationFeeError(apiErr)
	if info.Billing != nil {
		info.Billing.Refund(c)
	}
	service.ChargeViolationFeeIfNeeded(c, info, apiErr)
	return apiErr
}
