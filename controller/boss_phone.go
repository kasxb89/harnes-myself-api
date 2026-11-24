package controller

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	bossContractVersion      = 1
	bossPhoneContractVersion = 2
)

var bossPhoneCodeSender = common.SendSmsCode
var bossPhoneSchedule = gopool.Go
var bossPhoneLoginSealer = service.SealBossPhoneLoginResult

type bossPhoneVerifyRequest struct {
	FlowToken       string `json:"flow_token"`
	Method          string `json:"method"`
	Code            string `json:"code"`
	IdempotencyKey  string `json:"idempotency_key"`
	ContractVersion int    `json:"contract_version"`
}

type bossAuthResponseWriter struct {
	gin.ResponseWriter
	status int
	body   bytes.Buffer
}

func (writer *bossAuthResponseWriter) WriteHeader(status int) {
	if status >= http.StatusBadRequest {
		writer.status = status
		return
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *bossAuthResponseWriter) WriteHeaderNow() {
	if writer.status == 0 {
		writer.ResponseWriter.WriteHeaderNow()
	}
}

func (writer *bossAuthResponseWriter) Write(data []byte) (int, error) {
	if writer.status != 0 {
		return writer.body.Write(data)
	}
	return writer.ResponseWriter.Write(data)
}

func (writer *bossAuthResponseWriter) WriteString(data string) (int, error) {
	return writer.Write([]byte(data))
}

func (writer *bossAuthResponseWriter) Status() int {
	if writer.status != 0 {
		return writer.status
	}
	return writer.ResponseWriter.Status()
}

// BossAuthEnvelope keeps shared IP/body-limit failures in the same contract as
// controller failures. Successes stream directly; error bodies alone are held.
func BossAuthEnvelope() gin.HandlerFunc {
	return func(c *gin.Context) {
		writer := &bossAuthResponseWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()
		c.Writer = writer.ResponseWriter
		if writer.status == 0 {
			return
		}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if common.Unmarshal(writer.body.Bytes(), &envelope) == nil && envelope.Error.Code != "" && envelope.Error.Message != "" {
			c.Writer.WriteHeader(writer.status)
			_, _ = c.Writer.Write(writer.body.Bytes())
			return
		}
		code, message := "BOSS_AUTH_UNAVAILABLE", "Authentication could not be completed"
		switch writer.status {
		case http.StatusTooManyRequests:
			code, message = "BOSS_RATE_LIMITED", "Too many requests"
		case http.StatusRequestEntityTooLarge:
			code, message = "BOSS_INVALID_PARAMETERS", "Invalid parameters"
		}
		retry, _ := strconv.Atoi(c.Writer.Header().Get("Retry-After"))
		c.Writer.Header().Del("Content-Length")
		bossPhoneError(c, writer.status, code, message, max(0, retry))
	}
}

func BossCapabilities(c *gin.Context) {
	setAuthNoStore(c)
	if !bossPhoneAvailable(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"contract_version": bossContractVersion, "phone_contract_version": bossPhoneContractVersion, "required_group": model.BossPhoneGroup, "registration_enabled": false}})
}

func bossPhoneAvailable(c *gin.Context) bool {
	if !common.PhoneLoginEnabled || common.PhoneLoginDefaultGroup == model.BossPhoneGroup {
		bossPhoneError(c, http.StatusServiceUnavailable, "BOSS_AUTH_UNAVAILABLE", "Boss authentication is unavailable", 0)
		return false
	}
	return true
}

func bossPhoneError(c *gin.Context, status int, code, message string, retry int) {
	setAuthNoStore(c)
	detail := gin.H{"code": code, "message": message}
	if retry > 0 {
		detail["retry_after_seconds"] = retry
		c.Header("Retry-After", strconv.Itoa(retry))
	}
	c.JSON(status, gin.H{"error": detail})
}

func bossPhoneLoginError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrPhoneIdempotencyConflict):
		bossPhoneError(c, http.StatusConflict, "BOSS_IDEMPOTENCY_CONFLICT", "The request key is already bound to another request", 0)
	case errors.Is(err, model.ErrPhoneChallengeConsumed), errors.Is(err, model.ErrAuthFlowConsumed):
		bossPhoneError(c, http.StatusConflict, "BOSS_CHALLENGE_CONSUMED", "This authentication challenge is no longer usable", 0)
	case errors.Is(err, service.ErrProofMethod), errors.Is(err, service.ErrVerificationUnavailable):
		bossPhoneError(c, http.StatusForbidden, "BOSS_VERIFICATION_UNAVAILABLE", "The required verification method is unavailable", 0)
	case errors.Is(err, model.ErrBossPhoneUnqualified), errors.Is(err, model.ErrPhoneCodeInvalid), errors.Is(err, model.ErrPhoneLoginFactorInvalid), errors.Is(err, model.ErrAuthFlowInvalid), errors.Is(err, model.ErrAuthFlowExpired), errors.Is(err, model.ErrUserSessionInactive), errors.Is(err, service.ErrLoginSessionRevoked), errors.Is(err, gorm.ErrRecordNotFound):
		bossPhoneError(c, http.StatusUnauthorized, "BOSS_AUTH_FAILED", "Authentication could not be completed", 0)
	default:
		bossPhoneError(c, http.StatusServiceUnavailable, "BOSS_AUTH_UNAVAILABLE", "Authentication could not be completed", 1)
	}
}

func decodeBossPhoneRequest(c *gin.Context, target any, fields ...string) bool {
	encoded, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10))
	var values map[string]common.RawMessage
	if err != nil || common.Unmarshal(encoded, &values) != nil || len(values) != len(fields) {
		return false
	}
	for _, field := range fields {
		value, ok := values[field]
		if !ok || strings.TrimSpace(string(value)) == "null" {
			return false
		}
	}
	return common.Unmarshal(encoded, target) == nil
}

func BossPhoneChallenge(c *gin.Context) {
	setAuthNoStore(c)
	if !bossPhoneAvailable(c) {
		return
	}
	var request phoneChallengeRequest
	if !decodeBossPhoneRequest(c, &request, "phone_e164", "idempotency_key", "contract_version") || request.ContractVersion != bossPhoneContractVersion || !phoneE164Pattern.MatchString(request.PhoneE164) || !phoneIdempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		bossPhoneError(c, http.StatusBadRequest, "BOSS_INVALID_PARAMETERS", "Invalid parameters", 0)
		return
	}
	code, err := common.GenerateNumericVerificationCode(6)
	if err != nil {
		bossPhoneLoginError(c, err)
		return
	}
	challenge, dispatch, err := model.ReserveBossPhoneChallenge(request.PhoneE164, request.IdempotencyKey, code, time.Now().Add(time.Duration(common.VerificationValidMinutes)*time.Minute))
	if err != nil {
		bossPhoneLoginError(c, err)
		return
	}
	// No delivery status, eligibility, provider error, or development OTP is
	// exposed. Unknown/unbound/disabled/wrong-group/limited numbers look alike.
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"challenge_id": challenge.ID, "expires_at": challenge.ExpiresAt, "resend_after_seconds": int(model.PhoneSendCooldown.Seconds())}})
	if dispatch {
		phone, id, sender := request.PhoneE164, challenge.ID, bossPhoneCodeSender
		// Dispatch asynchronously so provider timing cannot enumerate accounts.
		// A lost worker leaves a durable sending reservation, never a resend.
		bossPhoneSchedule(func() {
			if err := model.DispatchBossPhoneChallenge(phone, id, func() (string, int) {
				err := sender(phone, code)
				if err == nil {
					return "accepted", 0
				}
				if errors.Is(err, common.ErrSmsNotConfigured) || errors.Is(err, common.ErrSmsRequest) || errors.Is(err, common.ErrSmsRejected) || errors.Is(err, common.ErrSmsThrottled) || errors.Is(err, common.ErrSmsUnavailable) {
					return "not_sent", 0
				}
				return "unknown", 0
			}); err != nil {
				common.SysError("Boss phone dispatch did not complete; its reservation remains non-retryable")
			}
		})
	}
}

func BossPhoneLogin(c *gin.Context) {
	setAuthNoStore(c)
	if !bossPhoneAvailable(c) {
		return
	}
	var request phoneLoginRequest
	if !decodeBossPhoneRequest(c, &request, "phone_e164", "code", "challenge_id", "idempotency_key", "contract_version") || request.ContractVersion != bossPhoneContractVersion || !phoneE164Pattern.MatchString(request.PhoneE164) || !phoneOTPPattern.MatchString(request.Code) || len(request.ChallengeID) != 64 || !phoneIdempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		bossPhoneError(c, http.StatusBadRequest, "BOSS_INVALID_PARAMETERS", "Invalid parameters", 0)
		return
	}
	var result *service.PhoneLoginResult
	_, err := model.ExchangeBossPhoneChallenge(request.PhoneE164, request.ChallengeID, request.IdempotencyKey, request.Code,
		func(tx *gorm.DB, user *model.User, binding *model.BossPhoneBinding) (string, int, string, error) {
			var err error
			result, err = service.PrepareBossPhoneLoginWithTx(tx, user, binding, c.ClientIP(), c.Request.UserAgent())
			if err != nil {
				return "", 0, "", err
			}
			sealed, err := bossPhoneLoginSealer(result, request.IdempotencyKey)
			if err != nil {
				return "", 0, "", err
			}
			sid := ""
			if result.Bundle != nil {
				sid = result.Bundle.Session.SID
			}
			return sealed, user.Id, sid, nil
		}, func(tx *gorm.DB, flow *model.AuthFlow, sealed string) error {
			var err error
			result, err = service.ReplayBossPhoneLoginResultWithTx(tx, flow, sealed, request.IdempotencyKey)
			return err
		})
	if err != nil {
		bossPhoneLoginError(c, err)
		return
	}
	writeBossPhoneLoginResult(c, result)
}

func BossPhoneVerify(c *gin.Context) {
	setAuthNoStore(c)
	if !bossPhoneAvailable(c) {
		return
	}
	var request bossPhoneVerifyRequest
	if !decodeBossPhoneRequest(c, &request, "flow_token", "method", "code", "idempotency_key", "contract_version") || request.ContractVersion != bossPhoneContractVersion || len(request.FlowToken) != 43 || !phoneIdempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		bossPhoneError(c, http.StatusBadRequest, "BOSS_INVALID_PARAMETERS", "Invalid parameters", 0)
		return
	}
	if request.Method != "totp" {
		bossPhoneError(c, http.StatusForbidden, "BOSS_VERIFICATION_UNAVAILABLE", "The required verification method is unavailable", 0)
		return
	}
	if !phoneOTPPattern.MatchString(request.Code) {
		bossPhoneError(c, http.StatusBadRequest, "BOSS_INVALID_PARAMETERS", "Invalid parameters", 0)
		return
	}
	var result *service.PhoneLoginResult
	_, err := model.ExchangeBossPhoneVerification(request.FlowToken, request.IdempotencyKey, request.Code,
		func(tx *gorm.DB, flow *model.AuthFlow, user *model.User, binding *model.BossPhoneBinding) (string, int, string, error) {
			var err error
			result, err = service.CompleteBossPhoneVerificationWithTx(tx, flow, user, binding, request.Code, c.ClientIP(), c.Request.UserAgent())
			if err != nil {
				return "", 0, "", err
			}
			sealed, err := bossPhoneLoginSealer(result, request.IdempotencyKey)
			if err != nil {
				return "", 0, "", err
			}
			return sealed, user.Id, result.Bundle.Session.SID, nil
		}, func(tx *gorm.DB, flow *model.AuthFlow, sealed string) error {
			var err error
			result, err = service.ReplayBossPhoneLoginResultWithTx(tx, flow, sealed, request.IdempotencyKey)
			return err
		})
	if err != nil {
		bossPhoneLoginError(c, err)
		return
	}
	c.Set("login_verification_method", "totp")
	writeBossPhoneLoginResult(c, result)
}

func writeBossPhoneLoginResult(c *gin.Context, result *service.PhoneLoginResult) {
	if result.Verification != nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": result.Verification})
		return
	}
	user, _, err := service.BossPhoneAuthState(result.Bundle.AccessToken)
	if err != nil {
		bossPhoneLoginError(c, model.ErrBossPhoneUnqualified)
		return
	}
	service.WriteRefreshCookie(c, result.RefreshToken)
	c.Set("login_method", model.BossPhoneLoginMethod)
	model.UpdateUserLastLoginAt(user.Id)
	recordLoginAudit(user, c)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"access_token": result.Bundle.AccessToken, "token_type": result.Bundle.TokenType, "access_expires_at": result.Bundle.AccessExpiresAt, "session": result.Bundle.Session, "user": bossPhoneUserData(user), "relay_key": result.RelayKey, "relay_key_id": result.RelayKeyID}})
}

func BossAuthState(c *gin.Context) {
	setAuthNoStore(c)
	if !bossPhoneAvailable(c) {
		return
	}
	values := c.Request.Header.Values("Authorization")
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(values) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		bossPhoneError(c, http.StatusUnauthorized, "BOSS_SESSION_INVALID", "Authentication is required", 0)
		return
	}
	user, session, err := service.BossPhoneAuthState(parts[1])
	if err != nil {
		bossPhoneError(c, http.StatusUnauthorized, "BOSS_SESSION_INVALID", "Authentication is required", 0)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"user": bossPhoneUserData(user), "session": session}})
}

// A credential-free projection for the server broker, not a second wallet.
func bossPhoneUserData(user *model.User) gin.H {
	return gin.H{"id": user.Id, "username": user.Username, "display_name": user.DisplayName, "phone_e164": user.PhoneE164, "group": user.Group, "status": user.Status, "quota": user.Quota, "used_quota": user.UsedQuota}
}
