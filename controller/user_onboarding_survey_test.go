package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type surveyTestResponse struct {
	Success bool                       `json:"success"`
	Code    string                     `json:"code"`
	Data    model.OnboardingSurveyView `json:"data"`
}

func surveyTestRouter() *gin.Engine {
	router := gin.New()
	self := router.Group("/api/user", middleware.DisableCache(), middleware.UserAuth())
	self.GET("/onboarding-survey", GetUserOnboardingSurvey)
	self.POST("/onboarding-survey", SubmitUserOnboardingSurvey)
	self.POST("/onboarding-survey/defer", DeferUserOnboardingSurvey)
	router.GET("/api/user/onboarding-surveys", middleware.DisableCache(), middleware.AdminAuth(), middleware.RequirePermission(authz.UserSurveysRead), ListUserOnboardingSurveys)
	return router
}

func surveyHTTPRequest(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func surveyReadResponse(t *testing.T, response *httptest.ResponseRecorder) surveyTestResponse {
	t.Helper()
	var result surveyTestResponse
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	return result
}

func surveyFixture(t *testing.T, name string, response *httptest.ResponseRecorder) {
	t.Helper()
	if dir := os.Getenv("SURVEY_FIXTURE_OUTPUT_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		// Export response bodies only. Authentication tokens, cookies and OTPs
		// stay inside the isolated test process and never enter the fixture pack.
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".json"), response.Body.Bytes(), 0600))
	}
}

func surveySignIn(t *testing.T, phone string) phoneAuthTestResponse {
	t.Helper()
	challenge, code := phoneTestChallenge(t, phone)
	response, result := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, ChallengeID: challenge, Code: code, IdempotencyKey: uuid.NewString()})
	require.True(t, result.Success, response.Body.String())
	require.NotEmpty(t, result.Data.AccessToken)
	return result
}

func TestOnboardingSurveyEnrollmentAndAuthenticatedLifecycle(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}, &model.UserAccessToken{}))
	require.NoError(t, authz.Init(db))
	router := surveyTestRouter()
	login := surveySignIn(t, "+8613800000101")
	token := login.Data.AccessToken
	path := "/api/user/onboarding-survey"
	pending := surveyHTTPRequest(router, http.MethodGet, path, token, "")
	require.Equal(t, http.StatusOK, pending.Code)
	view := surveyReadResponse(t, pending).Data
	assert.True(t, view.Eligible)
	assert.Equal(t, model.SurveyPending, view.Status)
	assert.Len(t, view.Options, 9)
	assert.Equal(t, 9, view.MaxSelections)
	assert.Equal(t, 1000, view.MaxNotesLength)
	assert.Empty(t, view.SelectedOptionIDs)
	assert.Positive(t, view.RegisteredAt)
	assert.Zero(t, view.SubmittedAt)
	assert.Contains(t, pending.Header().Get("Cache-Control"), "no-store")
	assert.NotContains(t, pending.Body.String(), "+8613800000101")
	surveyFixture(t, "pending", pending)
	deferBody := `{"schema_version":1,"survey_id":"product-use-v1"}`
	deferred := surveyHTTPRequest(router, http.MethodPost, path+"/defer", token, deferBody)
	require.Equal(t, http.StatusOK, deferred.Code)
	deferredView := surveyReadResponse(t, deferred).Data
	assert.Equal(t, model.SurveyDeferred, deferredView.Status)
	assert.Positive(t, deferredView.DeferredAt)
	surveyFixture(t, "deferred", deferred)
	again := surveyHTTPRequest(router, http.MethodPost, path+"/defer", token, deferBody)
	assert.Equal(t, deferredView, surveyReadResponse(t, again).Data)
	answer := `{"schema_version":1,"survey_id":"product-use-v1","selected_option_ids":["coding","daily_qa"],"notes":"  测试资料与办公\r\n第二行  "}`
	submitted := surveyHTTPRequest(router, http.MethodPost, path, token, answer)
	require.Equal(t, http.StatusOK, submitted.Code)
	submittedView := surveyReadResponse(t, submitted).Data
	assert.Equal(t, []string{"daily_qa", "coding"}, submittedView.SelectedOptionIDs)
	assert.Equal(t, "测试资料与办公\n第二行", submittedView.Notes)
	assert.Equal(t, model.SurveySubmitted, submittedView.Status)
	assert.Positive(t, submittedView.SubmittedAt)
	assert.Equal(t, deferredView.DeferredAt, submittedView.DeferredAt)
	surveyFixture(t, "submitted", submitted)
	canonical := `{"schema_version":1,"survey_id":"product-use-v1","selected_option_ids":["daily_qa","coding"],"notes":"测试资料与办公\n第二行"}`
	replayed := surveyHTTPRequest(router, http.MethodPost, path, token, canonical)
	assert.Equal(t, submittedView, surveyReadResponse(t, replayed).Data)
	surveyFixture(t, "same-answer-replay", replayed)
	conflict := surveyHTTPRequest(router, http.MethodPost, path, token, `{"schema_version":1,"survey_id":"product-use-v1","selected_option_ids":["other"]}`)
	assert.Equal(t, http.StatusConflict, conflict.Code)
	assert.Equal(t, "SURVEY_ALREADY_SUBMITTED", surveyReadResponse(t, conflict).Code)
	surveyFixture(t, "different-answer-conflict", conflict)
	assert.Equal(t, submittedView, surveyReadResponse(t, surveyHTTPRequest(router, http.MethodPost, path+"/defer", token, deferBody)).Data)
	// A fresh device/login uses a new session, while the authoritative answer
	// and its original timestamps remain identical.
	newSession, err := service.CreateLoginSession(login.Data.User.ID, "phone", "127.0.0.1", "another-test-device")
	require.NoError(t, err)
	assert.Equal(t, submittedView, surveyReadResponse(t, surveyHTTPRequest(router, http.MethodGet, path, newSession.AccessToken, "")).Data)
	var originalUser model.User
	require.NoError(t, db.First(&originalUser, login.Data.User.ID).Error)
	assert.Equal(t, 77+quotaFromYuan(10), originalUser.Quota)
	assert.Equal(t, originalUser.CreatedAt, submittedView.RegisteredAt)
	for _, old := range []struct {
		name, phone string
		adopt       bool
	}{
		{"existing", "+8613800000102", false}, {"adopted", "+8613800000103", true},
	} {
		t.Run(old.name, func(t *testing.T) {
			user := model.User{Username: phoneUsername(old.phone), DisplayName: "legacy", AffCode: old.name, AuthVersion: 1, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			if !old.adopt {
				user.PhoneE164 = &old.phone
			}
			require.NoError(t, db.Create(&user).Error)
			oldLogin := surveySignIn(t, old.phone)
			require.Equal(t, user.Id, oldLogin.Data.User.ID)
			response := surveyHTTPRequest(router, http.MethodGet, path, oldLogin.Data.AccessToken, "")
			oldView := surveyReadResponse(t, response).Data
			assert.False(t, oldView.Eligible)
			assert.Equal(t, "not_eligible", oldView.Status)
			assert.Zero(t, oldView.RegisteredAt)
			if !old.adopt {
				surveyFixture(t, "not-eligible", response)
			}
			denied := surveyHTTPRequest(router, http.MethodPost, path, oldLogin.Data.AccessToken, canonical)
			assert.Equal(t, http.StatusForbidden, denied.Code)
			assert.Equal(t, "SURVEY_NOT_ELIGIBLE", surveyReadResponse(t, denied).Code)
			surveyFixture(t, "not-eligible-submit", denied)
		})
	}
	unauthorized := surveyHTTPRequest(router, http.MethodGet, path, "", "")
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	surveyFixture(t, "unauthenticated", unauthorized)
	cookieRequest := httptest.NewRequest(http.MethodPost, path, strings.NewReader(answer))
	cookieRequest.Header.Set("Content-Type", "application/json")
	cookieRequest.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: "cookie-only-is-not-authentication"})
	cookieResponse := httptest.NewRecorder()
	router.ServeHTTP(cookieResponse, cookieRequest)
	assert.Equal(t, http.StatusUnauthorized, cookieResponse.Code)
	// New records cannot be read via the administrator list by ordinary users.
	adminPath := "/api/user/onboarding-surveys"
	assert.Equal(t, http.StatusForbidden, surveyHTTPRequest(router, http.MethodGet, adminPath, token, "").Code)
	admin := model.User{Username: "survey-admin", AffCode: "survey-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, authz.SetUserPermissions(admin.Id, authz.PermissionsMap{"user_surveys": {"read": true}}))
	adminSession, err := service.CreateLoginSession(admin.Id, "password", "127.0.0.1", "test-admin")
	require.NoError(t, err)
	listing := surveyHTTPRequest(router, http.MethodGet, adminPath+"?status=submitted&option_id=coding&phone=00000101", adminSession.AccessToken, "")
	require.Equal(t, http.StatusOK, listing.Code)
	var listed struct {
		Data struct {
			Items []model.OnboardingSurveyRegistration `json:"items"`
			Total int64                                `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(listing.Body.Bytes(), &listed))
	require.Len(t, listed.Data.Items, 1)
	assert.EqualValues(t, 1, listed.Data.Total)
	assert.Equal(t, "+8613800000101", listed.Data.Items[0].PhoneE164)
	assert.Equal(t, submittedView.Notes, listed.Data.Items[0].Notes)
	surveyFixture(t, "admin-registrations", listing)
	surveySignIn(t, "+8613800000104")
	deferredLogin := surveySignIn(t, "+8613800000105")
	require.Equal(t, http.StatusOK, surveyHTTPRequest(router, http.MethodPost, path+"/defer", deferredLogin.Data.AccessToken, deferBody).Code)
	fullListing := surveyHTTPRequest(router, http.MethodGet, adminPath, adminSession.AccessToken, "")
	require.Equal(t, http.StatusOK, fullListing.Code)
	surveyFixture(t, "admin-all-registrations", fullListing)

	require.NoError(t, authz.SetUserPermissions(admin.Id, authz.PermissionsMap{"user_surveys": {"read": false}}))
	assert.Equal(t, http.StatusForbidden, surveyHTTPRequest(router, http.MethodGet, adminPath, adminSession.AccessToken, "").Code)
	_, err = model.RevokeUserSession(login.Data.User.ID, login.Data.Session.SID, "survey-test-revoked")
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, surveyHTTPRequest(router, http.MethodGet, path, token, "").Code)
	assert.Equal(t, submittedView, surveyReadResponse(t, surveyHTTPRequest(router, http.MethodGet, path, newSession.AccessToken, "")).Data)
}

func TestOnboardingSurveyValidationAndOwnership(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	router := surveyTestRouter()
	alice := surveySignIn(t, "+8613800000111")
	bob := surveySignIn(t, "+8613800000112")
	base := `{"schema_version":1,"survey_id":"product-use-v1","selected_option_ids":["other"]}`
	for name, body := range map[string]string{
		"empty": `{}`, "empty choices": strings.Replace(base, `["other"]`, `[]`, 1),
		"unknown choice":   strings.Replace(base, "other", "unknown", 1),
		"duplicate":        strings.Replace(base, `["other"]`, `["other","other"]`, 1),
		"wrong schema":     strings.Replace(base, `:1,`, `:2,`, 1),
		"wrong survey":     strings.Replace(base, "product-use-v1", "product-use-v2", 1),
		"foreign user":     strings.TrimSuffix(base, "}") + fmt.Sprintf(`,"user_id":%d}`, bob.Data.User.ID),
		"foreign phone":    strings.TrimSuffix(base, "}") + `,"phone_e164":"+8613800000112"}`,
		"null":             strings.TrimSuffix(base, "}") + `,"notes":null}`,
		"overlong Unicode": strings.TrimSuffix(base, "}") + `,"notes":"` + strings.Repeat("文", 1001) + `"}`,
		"control":          strings.TrimSuffix(base, "}") + `,"notes":"\u0000"}`,
		"trailing":         base + ` {}`, "array": `[]`, "wrong type": strings.Replace(base, `["other"]`, `"other"`, 1),
		"oversized": strings.TrimSuffix(base, "}") + `,"notes":"` + strings.Repeat("x", 8200) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := surveyHTTPRequest(router, http.MethodPost, "/api/user/onboarding-survey", alice.Data.AccessToken, body)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Equal(t, "SURVEY_INVALID_REQUEST", surveyReadResponse(t, response).Code)
			if name == "foreign user" {
				surveyFixture(t, "invalid-foreign-user", response)
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/api/user/onboarding-survey", strings.NewReader(base))
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Authorization", "Bearer "+alice.Data.AccessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	valid := strings.TrimSuffix(base, "}") + `,"notes":"` + strings.Repeat("😀", 1000) + `"}`
	assert.Equal(t, http.StatusOK, surveyHTTPRequest(router, http.MethodPost, "/api/user/onboarding-survey", alice.Data.AccessToken, valid).Code)
	escaped := strings.TrimSuffix(base, "}") + `,"notes":"` + strings.Repeat(`\ud83d\ude00`, 1000) + `"}`
	assert.Equal(t, http.StatusOK, surveyHTTPRequest(router, http.MethodPost, "/api/user/onboarding-survey", alice.Data.AccessToken, escaped).Code, "the Unicode limit also permits legitimate JSON surrogate escapes")
	bobView, err := model.GetOnboardingSurvey(bob.Data.User.ID)
	require.NoError(t, err)
	assert.Equal(t, model.SurveyPending, bobView.Status)
	assert.Empty(t, bobView.Notes)
	for _, query := range []string{"page_size=101", "p=-1", "status=unknown", "option_id=unknown", "phone=%25"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/api/user/onboarding-surveys?"+query, nil)
		ListUserOnboardingSurveys(c)
		assert.Equal(t, http.StatusBadRequest, c.Writer.Status())
	}
	var count int64
	require.NoError(t, db.Model(&model.UserOnboardingSurvey{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

func TestOnboardingSurveyConcurrentSubmission(t *testing.T) {
	for _, mode := range []string{"same answer", "different answer", "defer versus submit"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := setupPhoneAuthRecovery(t)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(8)
			login := surveySignIn(t, "+8613800000121")
			router := surveyTestRouter()
			base := `{"schema_version":1,"survey_id":"product-use-v1","selected_option_ids":["daily_qa","coding"]}`
			bodies := [2]string{base, strings.Replace(base, `["daily_qa","coding"]`, `["coding","daily_qa"]`, 1)}
			paths := [2]string{"/api/user/onboarding-survey", "/api/user/onboarding-survey"}
			if mode == "different answer" {
				bodies[1] = strings.Replace(base, `["daily_qa","coding"]`, `["other"]`, 1)
			}
			if mode == "defer versus submit" {
				bodies[1] = `{"schema_version":1,"survey_id":"product-use-v1"}`
				paths[1] += "/defer"
			}
			var responses [2]*httptest.ResponseRecorder
			var ready sync.WaitGroup
			ready.Add(2)
			require.NoError(t, db.Callback().Update().Before("gorm:begin_transaction").Register("survey:overlap", func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "UserOnboardingSurvey" {
					ready.Done()
					ready.Wait()
				}
			}))
			var workers sync.WaitGroup
			for i := range 2 {
				workers.Go(func() {
					responses[i] = surveyHTTPRequest(router, http.MethodPost, paths[i], login.Data.AccessToken, bodies[i])
				})
			}
			workers.Wait()
			require.NoError(t, db.Callback().Update().Remove("survey:overlap"))
			codes := []int{responses[0].Code, responses[1].Code}
			if mode == "different answer" {
				assert.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, codes)
			} else {
				assert.Equal(t, []int{http.StatusOK, http.StatusOK}, codes)
			}
			final, err := model.GetOnboardingSurvey(login.Data.User.ID)
			require.NoError(t, err)
			assert.Equal(t, model.SurveySubmitted, final.Status)
			if mode == "same answer" {
				assert.Equal(t, surveyReadResponse(t, responses[0]).Data, surveyReadResponse(t, responses[1]).Data)
			}
		})
	}
}

func TestOnboardingSurveyUpgradePreservesExistingUsersAndSessions(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	// Remove only the new, isolated fixture table to represent pre-feature
	// schema. Existing phone users and a live session predate its migration.
	require.NoError(t, db.Migrator().DropTable(&model.UserOnboardingSurvey{}))
	phone := "+8613800000131"
	legacy := model.User{Username: phoneUsername(phone), AffCode: "legacy-upgrade", PhoneE164: &phone, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 7, Quota: 123456, CreatedAt: 1600000000}
	require.NoError(t, db.Create(&legacy).Error)
	login := surveySignIn(t, phone)
	var before model.User
	require.NoError(t, db.First(&before, legacy.Id).Error)
	var sessionBefore model.UserSession
	require.NoError(t, db.Where("sid = ?", login.Data.Session.SID).First(&sessionBefore).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.UserOnboardingSurvey{}))
	}
	view, err := model.GetOnboardingSurvey(legacy.Id)
	require.NoError(t, err)
	assert.False(t, view.Eligible)
	assert.Equal(t, "not_eligible", view.Status)
	assert.True(t, db.Migrator().HasIndex(&model.UserOnboardingSurvey{}, "idx_survey_status_registered"))
	var after model.User
	require.NoError(t, db.First(&after, legacy.Id).Error)
	assert.Equal(t, before, after)
	var sessionAfter model.UserSession
	require.NoError(t, db.Where("sid = ?", login.Data.Session.SID).First(&sessionAfter).Error)
	assert.Equal(t, sessionBefore, sessionAfter)
	assert.Equal(t, http.StatusOK, surveyHTTPRequest(surveyTestRouter(), http.MethodGet, "/api/user/onboarding-survey", login.Data.AccessToken, "").Code)
	newLogin := surveySignIn(t, "+8613800000132")
	_, err = model.SubmitOnboardingSurvey(newLogin.Data.User.ID, []string{"other"}, "retained after migration")
	require.NoError(t, err)
	var original model.UserOnboardingSurvey
	require.NoError(t, db.First(&original, "user_id = ?", newLogin.Data.User.ID).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.UserOnboardingSurvey{}))
	}
	var preserved model.UserOnboardingSurvey
	require.NoError(t, db.First(&preserved, "user_id = ?", newLogin.Data.User.ID).Error)
	assert.Equal(t, original, preserved)
	duplicate := original
	assert.Error(t, db.Create(&duplicate).Error, "primary key must prevent duplicate enrollment on every engine")
}

func TestOnboardingSurveyUnavailableDoesNotBreakExistingLogin(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	old := surveySignIn(t, "+8613800000141")
	require.NoError(t, db.Migrator().DropTable(&model.UserOnboardingSurvey{}))
	response := surveyHTTPRequest(surveyTestRouter(), http.MethodGet, "/api/user/onboarding-survey", old.Data.AccessToken, "")
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Equal(t, "SURVEY_UNAVAILABLE", surveyReadResponse(t, response).Code)
	assert.NotContains(t, response.Body.String(), "table")
	surveyFixture(t, "unavailable", response)
	// The old login branch never consults enrollment. Also prove the unchanged
	// auth layer accepts its pre-existing session while the survey is unavailable.
	user, created, err := findOrCreatePhoneUser("+8613800000141")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, old.Data.User.ID, user.Id)
	_, err = service.CreateLoginSession(user.Id, "phone", "127.0.0.1", "existing-user")
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.UserOnboardingSurvey{}))
	challenge, code := phoneTestChallenge(t, "+8613800000142")
	phoneLoginSealer = func(*service.PhoneLoginResult, string) (string, error) {
		return "", errors.New("fixture receipt failure")
	}
	failed, result := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: "+8613800000142", ChallengeID: challenge, Code: code, IdempotencyKey: uuid.NewString()})
	assert.Equal(t, http.StatusServiceUnavailable, failed.Code)
	assert.Equal(t, "PHONE_LOGIN_UNAVAILABLE", result.Code)
	var count int64
	require.NoError(t, db.Model(&model.UserOnboardingSurvey{}).Count(&count).Error)
	assert.Zero(t, count, "failed registration cannot leave enrollment")
	var failedUser model.User
	assert.ErrorIs(t, db.Where("phone_e164 = ?", "+8613800000142").First(&failedUser).Error, gorm.ErrRecordNotFound)
}
