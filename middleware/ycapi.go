package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

// Non-YCAPI speech providers keep their existing request and billing path.
func PrepareYcapiSpeech() gin.HandlerFunc {
	return func(c *gin.Context) {
		var request service.YcapiSpeechRequest
		if common.UnmarshalBodyReusable(c, &request) != nil || !strings.HasPrefix(request.Model, "ycapi-") {
			c.Next()
			return
		}
		m, ok := ycapi.Lookup(request.Model)
		if !ok || m.Kind != "audio" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_request", "message": "Speech model is not included"}})
			return
		}
		if service.ValidateYcapiSpeechQuote(c, request) != nil {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": gin.H{"code": "quote_changed", "message": "Speech quote expired or does not match this request"}})
			return
		}
		c.Params = append(c.Params, gin.Param{Key: "key", Value: "ycapi"})
		PrepareTaskPluginSubmit()(c)
	}
}
