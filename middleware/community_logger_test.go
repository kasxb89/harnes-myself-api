package middleware

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"net/http/httptest"
	"testing"
)

func TestCommunitySignedMediaQueryNeverEntersAccessLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var log bytes.Buffer
	before := gin.DefaultWriter
	gin.DefaultWriter = &log
	defer func() { gin.DefaultWriter = before }()
	for _, path := range []string{"/api/product/community-demands/attachments/id/content", "/api/community-demand-management/attachments/id/content", "/api/product/forum/attachments/id/content", "/api/product/demand-service-media/profile/asset/content"} {
		engine := gin.New()
		SetUpLogger(engine)
		engine.GET(path, func(c *gin.Context) { assert.Equal(t, "synthetic-short-lived-secret", c.Query("token")); c.Status(200) })
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest("GET", path+"?token=synthetic-short-lived-secret", nil))
		assert.Equal(t, 200, w.Code)
	}
	assert.NotContains(t, log.String(), "synthetic-short-lived-secret")
	assert.NotContains(t, log.String(), "?token=")
}
