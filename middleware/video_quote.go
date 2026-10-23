package middleware

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// VideoQuoteAuth validates relay credentials without marking a token expired or
// exhausted. A caller with no remaining funds may still inspect an estimate.
func VideoQuoteAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Header("Pragma", "no-cache")
		c.Header("X-Content-Type-Options", "nosniff")
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			abortVideoQuoteAuth(c, http.StatusUnauthorized, "invalid_api_key")
			return
		}
		key := strings.TrimPrefix(parts[1], "sk-")
		// A channel-pinned key changes routing. Quotes deliberately reject that
		// optional admin suffix instead of estimating a different routing set.
		if key == "" || len(key) > 128 || strings.Contains(key, "-") {
			abortVideoQuoteAuth(c, http.StatusUnauthorized, "invalid_api_key")
			return
		}
		token, err := model.GetTokenByKey(key, true)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				abortVideoQuoteAuth(c, http.StatusUnauthorized, "invalid_api_key")
			} else {
				abortVideoQuoteAuth(c, http.StatusServiceUnavailable, "quote_unavailable")
			}
			return
		}
		if (token.Status != common.TokenStatusEnabled && token.Status != common.TokenStatusExhausted) ||
			(token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix()) {
			abortVideoQuoteAuth(c, http.StatusUnauthorized, "invalid_api_key")
			return
		}
		if allowIPs := token.GetIpLimits(); len(allowIPs) > 0 {
			ip := net.ParseIP(c.ClientIP())
			if ip == nil || !common.IsIpInCIDRList(ip, allowIPs) {
				abortVideoQuoteAuth(c, http.StatusForbidden, "access_denied")
				return
			}
		}
		user, err := model.GetUserCache(token.UserId)
		if err != nil {
			abortVideoQuoteAuth(c, http.StatusServiceUnavailable, "quote_unavailable")
			return
		}
		if user.Status != common.UserStatusEnabled {
			abortVideoQuoteAuth(c, http.StatusForbidden, "access_denied")
			return
		}
		group := user.Group
		if token.Group != "" {
			_, usable := service.GetUserUsableGroups(user.Group)[token.Group]
			if !usable || (token.Group != "auto" && !ratio_setting.ContainsGroupRatio(token.Group)) {
				abortVideoQuoteAuth(c, http.StatusForbidden, "access_denied")
				return
			}
			group = token.Group
		}
		user.WriteContext(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		if err := SetupContextForToken(c, token); err != nil {
			abortVideoQuoteAuth(c, http.StatusForbidden, "access_denied")
			return
		}
		c.Next()
	}
}

func VideoQuoteRateLimit() gin.HandlerFunc {
	return userRateLimitFactory(60, 60, "video-quote")
}

func VideoQuoteIPRateLimit() gin.HandlerFunc {
	limit := rateLimitFactory(120, 60, "video-quote-ip")
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Header("Pragma", "no-cache")
		c.Header("X-Content-Type-Options", "nosniff")
		limit(c)
	}
}

func abortVideoQuoteAuth(c *gin.Context, status int, code string) {
	key := i18n.MsgVideoQuoteUnavailable
	switch code {
	case "invalid_api_key":
		key = i18n.MsgVideoQuoteInvalidAPIKey
	case "access_denied":
		key = i18n.MsgVideoQuoteAccessDenied
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": "video_quote_error", "code": code, "message": i18n.T(c, key)}})
}
