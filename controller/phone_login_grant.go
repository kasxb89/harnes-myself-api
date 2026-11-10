package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ListPhoneLoginGrants(c *gin.Context) {
	setAuthNoStore(c)
	page := common.GetPageQuery(c)
	query := model.DB.Model(&model.PhoneLoginGrant{})
	if phone := c.Query("phone_e164"); phone != "" {
		query = query.Where("phone_e164 = ?", phone)
	}
	var total int64
	var grants []model.PhoneLoginGrant
	if err := query.Count(&total).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later."))
		return
	}
	if err := query.Order("issued_at DESC, phone_e164").Offset(page.GetStartIdx()).Limit(page.GetPageSize()).Find(&grants).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later."))
		return
	}
	page.SetTotal(int(total))
	page.SetItems(grants)
	common.ApiSuccess(c, page)
}

func ManagePhoneLoginGrant(c *gin.Context) {
	setAuthNoStore(c)
	var request service.PhoneLoginGrantContext
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if common.DecodeJson(c.Request.Body, &request) != nil || !common.IsManualLoginPhone(request.PhoneE164) || (request.Action != "issue" && request.Action != "revoke") {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminPhoneGrant, request)
	if authorization == nil {
		return
	}
	if request.Action == "revoke" {
		if err := model.RevokePhoneLoginGrant(request.PhoneE164, authorization.AuthSessionIdentity); err != nil {
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later."))
			return
		}
		recordManageAudit(c, "phone_login_grant.revoke", map[string]any{"phone_e164": request.PhoneE164})
		common.ApiSuccess(c, nil)
		return
	}
	grant, code, err := model.IssuePhoneLoginGrant(request.PhoneE164, authorization.AuthSessionIdentity)
	if errors.Is(err, model.ErrPhoneGrantNotAllowed) {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Manual phone login is restricted to ordinary international accounts"))
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later."))
		return
	}
	// Deliberately returned only by this root-only, step-up protected issuance.
	// Generic admin audit stores the route/action, never the response body.
	recordManageAudit(c, "phone_login_grant.issue", map[string]any{"phone_e164": request.PhoneE164, "expires_at": grant.ExpiresAt})
	common.ApiSuccess(c, gin.H{"grant": grant, "code": code})
}
