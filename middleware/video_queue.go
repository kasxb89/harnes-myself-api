package middleware

import (
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
)

// FreshVideoQueueIdentity follows ordinary TokenAuth and narrows it with fresh
// database state before queued work is accepted or dispatched. The original
// client IP, original token ownership and current restrictions remain binding.
func FreshVideoQueueIdentity() gin.HandlerFunc {
	return freshVideoQueueIdentity(false)
}

func VideoQueueEnqueueRateLimit() gin.HandlerFunc {
	return userRateLimitFactory(120, 60, "video-queue-enqueue")
}
func VideoQueueEnqueueIPRateLimit() gin.HandlerFunc {
	return rateLimitFactory(240, 60, "video-queue-enqueue-ip")
}
func VideoQueueCancelRateLimit() gin.HandlerFunc {
	return userRateLimitFactory(120, 60, "video-queue-cancel")
}
func VideoQueueCancelIPRateLimit() gin.HandlerFunc {
	return rateLimitFactory(240, 60, "video-queue-cancel-ip")
}

// FreshVideoQueueReadIdentity permits an exhausted/expired original key to
// observe or cancel unstarted work, never to enqueue or dispatch new work.
func FreshVideoQueueReadIdentity() gin.HandlerFunc {
	return freshVideoQueueIdentity(true)
}

func freshVideoQueueIdentity(readOnly bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := model.GetTokenByIds(c.GetInt("token_id"), c.GetInt("id"))
		if err != nil || token.Status == common.TokenStatusDisabled || !readOnly && (token.Status != common.TokenStatusEnabled || token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix() || !token.UnlimitedQuota && token.RemainQuota <= 0) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "video_queue_identity_invalid", "message": i18n.T(c, i18n.MsgTokenInvalid)}})
			return
		}
		if dispatch := service.VideoQueueDispatchFromContext(c); dispatch != nil && (token.Id != dispatch.Item.TokenID || token.UserId != dispatch.Item.UserID || c.ClientIP() != dispatch.Item.ClientIP) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimPrefix(parts[1], "sk-") != token.Key {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if ips := token.GetIpLimits(); len(ips) > 0 {
			ip := net.ParseIP(c.ClientIP())
			if ip == nil || !common.IsIpInCIDRList(ip, ips) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
		}
		user, err := model.GetUserById(token.UserId, false)
		if err != nil || user.Status != common.UserStatusEnabled {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if readOnly {
			c.Next()
			return
		}
		group := user.Group
		if token.Group != "" {
			_, usable := service.GetUserUsableGroups(user.Group)[token.Group]
			if !usable || token.Group != "auto" && !ratio_setting.ContainsGroupRatio(token.Group) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			group = token.Group
		}
		user.ToBaseUser().WriteContext(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		// TokenAuth may have populated these optional permissions from a warm
		// cache. Explicitly replace them even when the fresh token omits them.
		common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, service.GetUserAutoGroup(user.Group))
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{})
		c.Set("token_quota", token.RemainQuota)
		if SetupContextForToken(c, token) != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
