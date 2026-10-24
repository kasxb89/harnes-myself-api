package router

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SetVideoQueueRouter adds an opt-in surface. Existing /v1/videos behavior and
// response envelopes stay unchanged for installed clients.
func SetVideoQueueRouter(router *gin.Engine) {
	read := router.Group("/v1/video/queue", middleware.RouteTag("relay"), middleware.TokenAuthReadOnly(), middleware.FreshVideoQueueReadIdentity())
	read.GET("", controller.ListVideoQueue)
	read.GET("/:queue_id", controller.GetVideoQueue)
	read.POST("/:queue_id/cancel", middleware.VideoQueueCancelIPRateLimit(), middleware.VideoQueueCancelRateLimit(), controller.CancelVideoQueue)
	write := router.Group("/v1/video/queue", middleware.RouteTag("relay"), middleware.VideoQueueEnqueueIPRateLimit(), middleware.TokenAuth())
	write.POST("", middleware.VideoQueueEnqueueRateLimit(), middleware.SystemPerformanceCheck(), middleware.FreshVideoQueueIdentity(), controller.EnqueueVideo)

	// This is an in-process router, not a loopback request. The same public
	// protocol handlers run, with fresh original identity and host-only context.
	dispatchRouter := gin.New()
	_ = dispatchRouter.SetTrustedProxies(nil)
	dispatchRouter.Use(gin.CustomRecovery(func(c *gin.Context, _ any) { c.AbortWithStatus(http.StatusInternalServerError) }))
	dispatchRouter.Use(middleware.BodyStorageCleanup())
	dispatchRouter.Use(middleware.VideoIngressObserver(nil))
	handlers, err := taskPluginProtocolHandlers("openai_video", "create")
	if err != nil {
		panic(err)
	}
	// TokenAuth remains first. Fresh identity then overrides cached permission
	// fields before rate limiting, plugin selection, channel selection or money.
	handlers = append(handlers[:2:2], append([]gin.HandlerFunc{middleware.FreshVideoQueueIdentity()}, handlers[2:]...)...)
	// A queue must never fall back to a legacy adaptor without the durable
	// admission/send ledger. The plugin is pinned by normal distribution.
	handlers = append(handlers[:len(handlers)-1:len(handlers)-1], requireVideoQueueDurableRoute, handlers[len(handlers)-1])
	dispatchRouter.POST("/v1/videos", handlers...)
	service.SetVideoQueueDispatcher(func(ctx context.Context, dispatch *service.VideoQueueDispatch) (int, error) {
		token, err := model.GetTokenByIds(dispatch.Item.TokenID, dispatch.Item.UserID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			dispatch.PreflightCode = "video_queue_authorization_revoked"
			return http.StatusUnauthorized, nil
		}
		if err != nil {
			return http.StatusServiceUnavailable, nil
		}
		fingerprint := sha256.Sum256([]byte(token.Key))
		if subtle.ConstantTimeCompare([]byte(dispatch.Item.TokenFingerprint), []byte(hex.EncodeToString(fingerprint[:]))) != 1 {
			dispatch.PreflightCode = "video_queue_authorization_revoked"
			return http.StatusUnauthorized, nil
		}
		if _, err := model.GetUserById(dispatch.Item.UserID, false); errors.Is(err, gorm.ErrRecordNotFound) {
			return http.StatusForbidden, nil
		} else if err != nil {
			return http.StatusServiceUnavailable, nil
		}
		if token.Key == "" || net.ParseIP(dispatch.Item.ClientIP) == nil {
			return http.StatusUnauthorized, nil
		}
		request, err := http.NewRequestWithContext(service.VideoQueueRequestContext(ctx, dispatch), http.MethodPost, "/v1/videos", strings.NewReader(dispatch.Item.RequestBody))
		if err != nil {
			return 0, errors.New("failed to construct video dispatch")
		}
		request.RemoteAddr = net.JoinHostPort(dispatch.Item.ClientIP, "0")
		request.Header.Set("Content-Type", "application/json")
		// The existing token is read only for this dispatch and is never written
		// to the queue, response, logs, new files or another HTTP connection.
		request.Header.Set("Authorization", "Bearer "+token.Key)
		if dispatch.Item.ClientSource == "desktop" {
			request.Header.Set("X-Yunshu-Client", "desktop")
		}
		writer := &videoQueueResponse{header: make(http.Header)}
		dispatchRouter.ServeHTTP(writer, request)
		request.Header.Del("Authorization")
		if writer.status == 0 {
			writer.status = http.StatusOK
		}
		return writer.status, nil
	})
}

func requireVideoQueueDurableRoute(c *gin.Context) {
	value, _ := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, _ := value.(jsplugin.PinnedPlugin)
	endpointValue, _ := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	endpoint, endpointOK := endpointValue.(jsplugin.PinnedEndpoint)
	if pinned.Plugin == nil || pinned.Generation == nil || !endpointOK || endpoint.Protocol != "openai_video" || endpoint.Generation != pinned.Generation {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	common.SetContextKey(c, common.RequestIdKey, service.VideoQueueTaskID(c))
	c.Next()
}

// The dispatcher only needs the HTTP outcome and its private proof context.
// Responses are discarded without buffering provider data or credentials.
type videoQueueResponse struct {
	header http.Header
	status int
}

func (writer *videoQueueResponse) Header() http.Header { return writer.header }
func (writer *videoQueueResponse) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
}
func (writer *videoQueueResponse) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return len(body), nil
}
func (writer *videoQueueResponse) ReadFrom(reader io.Reader) (int64, error) {
	return io.Copy(io.Discard, reader)
}
