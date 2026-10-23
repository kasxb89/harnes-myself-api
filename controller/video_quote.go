package controller

import (
	"io"
	"mime"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// QuoteVideo only evaluates the caller's proposed workload. It never enters
// generation admission, channel credential selection or billing pre-consume.
func QuoteVideo(c *gin.Context) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeVideoQuoteError(c, http.StatusUnsupportedMediaType, "invalid_request")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeVideoQuoteError(c, http.StatusRequestEntityTooLarge, "invalid_request")
		return
	}
	var envelope map[string]common.RawMessage
	if err := common.Unmarshal(body, &envelope); err != nil || envelope == nil {
		writeVideoQuoteError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	for key := range envelope {
		if key != "model" && key != "works_count" && key != "segments" {
			writeVideoQuoteError(c, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	var segments []map[string]common.RawMessage
	if err := common.Unmarshal(envelope["segments"], &segments); err != nil {
		writeVideoQuoteError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	for _, segment := range segments {
		for key := range segment {
			if key != "id" && key != "parameters" && key != "reference_counts" {
				writeVideoQuoteError(c, http.StatusBadRequest, "invalid_request")
				return
			}
		}
		if counts, present := segment["reference_counts"]; present {
			var fields map[string]common.RawMessage
			if err := common.Unmarshal(counts, &fields); err != nil || fields == nil {
				writeVideoQuoteError(c, http.StatusBadRequest, "invalid_request")
				return
			}
			for field := range fields {
				if field != "images" && field != "videos" && field != "audios" {
					writeVideoQuoteError(c, http.StatusBadRequest, "invalid_request")
					return
				}
			}
		}
	}
	var request types.VideoQuoteRequest
	if err := common.Unmarshal(body, &request); err != nil {
		writeVideoQuoteError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		value, _ := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		limit, _ := value.(map[string]bool)
		if !middleware.TokenModelLimitAllows(limit, request.Model) {
			writeVideoQuoteError(c, http.StatusForbidden, "access_denied")
			return
		}
	}
	quote, quoteErr := service.QuoteVideo(c, &request)
	if quoteErr != nil {
		writeVideoQuoteError(c, quoteErr.StatusCode, quoteErr.Code)
		return
	}
	c.JSON(http.StatusOK, quote)
}

func writeVideoQuoteError(c *gin.Context, status int, code string) {
	key := i18n.MsgVideoQuoteUnavailable
	switch code {
	case "invalid_request", "invalid_quote_parameters":
		key = i18n.MsgVideoQuoteInvalidRequest
	case "access_denied":
		key = i18n.MsgVideoQuoteAccessDenied
	}
	c.JSON(status, gin.H{"error": gin.H{"type": "video_quote_error", "code": code, "message": i18n.T(c, key)}})
}
