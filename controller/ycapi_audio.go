package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
)

func QuoteYcapiSpeech(c *gin.Context) {
	var request service.YcapiSpeechRequest
	if c.ContentType() != "application/json" {
		c.AbortWithStatus(http.StatusUnsupportedMediaType)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var fields map[string]common.RawMessage
	if common.UnmarshalBodyReusable(c, &fields) != nil || common.UnmarshalBodyReusable(c, &request) != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	for key := range fields {
		switch key {
		case "model", "input", "voice", "response_format", "speed":
		default:
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
	}
	quote, err := service.QuoteYcapiSpeech(c, request, time.Now().Unix())
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"error": gin.H{"type": "audio_quote_error", "code": "quote_unavailable", "message": "Speech quote is unavailable for this request"}})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, quote)
}
