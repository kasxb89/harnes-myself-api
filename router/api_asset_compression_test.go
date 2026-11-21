package router

import (
	"bytes"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAPIMediaCompressionPreservesOriginalRangeAndHead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(apiResponseCompression())
	paths := []string{"/api/creation/assets/local/content", "/api/product/demands/attachments/local/content", "/api/product/community-demands/attachments/local/content", "/api/product/forum/attachments/local/content", "/api/product/demand-showcases/assets/case/local/content", "/api/product/demand-service-media/profile/local/content"}
	body := bytes.Repeat([]byte("local original binary media"), 100)
	for _, path := range paths {
		engine.GET(path, func(c *gin.Context) {
			c.Header("Content-Type", "video/mp4")
			http.ServeContent(c.Writer, c.Request, "local.mp4", time.Time{}, bytes.NewReader(body))
		})
		engine.HEAD(path, func(c *gin.Context) {
			c.Header("Content-Type", "video/mp4")
			http.ServeContent(c.Writer, c.Request, "local.mp4", time.Time{}, bytes.NewReader(body))
		})
	}
	for _, path := range paths {
		for _, method := range []string{"GET", "HEAD"} {
			request := httptest.NewRequest(method, path, nil)
			request.Header.Set("Accept-Encoding", "gzip")
			request.Header.Set("Range", "bytes=0-31")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, 206, response.Code, path)
			require.Empty(t, response.Header().Get("Content-Encoding"), path)
			require.Equal(t, fmt.Sprintf("bytes 0-31/%d", len(body)), response.Header().Get("Content-Range"), path)
			if method == "HEAD" {
				require.Empty(t, response.Body.Bytes())
			} else {
				require.Equal(t, body[:32], response.Body.Bytes())
			}
		}
	}
	engine.GET("/api/local-metadata", func(c *gin.Context) { c.JSON(200, gin.H{"synthetic": "compressible public metadata only"}) })
	request := httptest.NewRequest("GET", "/api/local-metadata", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, "gzip", response.Header().Get("Content-Encoding"))
}
