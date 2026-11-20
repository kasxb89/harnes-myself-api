package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func SetDemandEmailRoutes(api *gin.RouterGroup) {
	admin := api.Group("/demand-management/email-notifications", middleware.DisableCache(), middleware.AdminAuth(), controller.DemandSessionOnly, middleware.RequirePermission(authz.DemandsRead), middleware.RequirePermission(authz.CommunityDemandsRead))
	admin.GET("/config", controller.GetDemandEmailConfig)
	admin.GET("/deliveries", controller.ListDemandEmailDeliveries)
	admin.GET("/test-receipts/:client_action_id", controller.GetDemandEmailTestReceipt)
	write := admin.Group("", middleware.RequirePermission(authz.DemandsWrite), middleware.RequirePermission(authz.CommunityDemandsWrite), controller.DemandOriginGuard)
	write.PUT("/config", controller.SaveDemandEmailConfig)
	write.POST("/test", middleware.UserCriticalRateLimit("demand-email-test"), controller.CreateDemandEmailTest)
}
