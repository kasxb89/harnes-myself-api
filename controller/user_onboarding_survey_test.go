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
	"sync/atomic"
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

func surveyTestRouter(extra ...gin.HandlerFunc) *gin.Engine {
	router := gin.New()
	router.Use(extra...)
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
			firstAtGuard, releaseFirst, secondAtHTTP := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("survey:overlap", func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "OnboardingSurveyConfigState" {
					once.Do(func() { close(firstAtGuard); <-releaseFirst })
				}
			}))
			var entered atomic.Int32
			router = surveyTestRouter(func(c *gin.Context) {
				if entered.Add(1) == 2 {
					close(secondAtHTTP)
				}
				c.Next()
			})
			var workers sync.WaitGroup
			workers.Go(func() {
				responses[0] = surveyHTTPRequest(router, http.MethodPost, paths[0], login.Data.AccessToken, bodies[0])
			})
			<-firstAtGuard
			workers.Go(func() {
				responses[1] = surveyHTTPRequest(router, http.MethodPost, paths[1], login.Data.AccessToken, bodies[1])
			})
			<-secondAtHTTP
			close(releaseFirst)
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

func TestConfigurableSurveyRevisionAndHistoricalMeaning(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	current, err := model.GetSurveyConfiguration()
	require.NoError(t, err)
	cfg, err := model.SaveSurveyConfiguration(current.Revision, model.DefaultSurveyConfiguration().Options)
	require.NoError(t, err)
	root := model.User{Username: "config-admin", AffCode: "config-admin", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&root).Error)
	admin, err := service.CreateLoginSession(root.Id, "password", "127.0.0.1", "config-fixture")
	require.NoError(t, err)
	router := surveyTestRouter()
	router.GET("/api/user/onboarding-survey-config", middleware.AdminAuth(), middleware.RequirePermission(authz.UserSurveysRead), GetOnboardingSurveyConfiguration)
	router.PUT("/api/user/onboarding-survey-config", middleware.AdminAuth(), middleware.RequirePermission(authz.UserSurveysWrite), SaveOnboardingSurveyConfiguration)
	login := surveySignIn(t, "+8613800000201")
	path := "/api/user/onboarding-survey?schema_version=2"
	pending := surveyHTTPRequest(router, "GET", path, login.Data.AccessToken, "")
	require.Equal(t, 200, pending.Code)
	surveyFixture(t, "pending", pending)
	require.Len(t, surveyReadResponse(t, pending).Data.Options, 26)
	assert.Equal(t, 404, surveyHTTPRequest(router, "GET", "/api/user/onboarding-survey", login.Data.AccessToken, "").Code, "legacy client cannot bypass a disabled scenario")
	configResponse := surveyHTTPRequest(router, "GET", "/api/user/onboarding-survey-config", admin.AccessToken, "")
	require.Equal(t, 200, configResponse.Code)
	surveyFixture(t, "configuration", configResponse)
	// A previously submitted six-e record has no new snapshot columns. Its
	// original meanings must remain readable without rewriting that row.
	legacy := surveySignIn(t, "+8613800000202")
	require.NoError(t, db.Model(&model.UserOnboardingSurvey{}).Where("user_id = ?", legacy.Data.User.ID).Updates(map[string]any{"status": model.SurveySubmitted, "selected_options_json": `["coding"]`, "notes": "legacy answer", "submitted_at": 1600000000}).Error)
	legacyBefore := surveyHTTPRequest(router, "GET", path, legacy.Data.AccessToken, "")
	require.Equal(t, 200, legacyBefore.Code)
	surveyFixture(t, "legacy-submitted", legacyBefore)
	oldBody, err := common.Marshal(map[string]any{"schema_version": 2, "survey_id": model.OnboardingSurveyID, "config_revision": cfg.Revision, "selected_option_ids": []string{"daily_qa", "code_debugging"}, "notes": "保留我的备注"})
	require.NoError(t, err)
	options := append([]model.SurveyConfigOption{}, cfg.Options...)
	for i := range options {
		if options[i].ID == "daily_qa" {
			options[i].Label = "日常交流与问答"
			options[i].LabelEN = "Everyday conversation and questions"
		}
		if options[i].ID == "code_debugging" {
			options[i].Enabled = false
		}
	}
	options = append(options, model.SurveyConfigOption{ID: "custom_research", Label: "专属资料整理", LabelEN: "Custom research organization", Enabled: true})
	editBody, err := common.Marshal(map[string]any{"schema_version": 2, "expected_config_revision": cfg.Revision, "options": options})
	require.NoError(t, err)
	changed := surveyHTTPRequest(router, "PUT", "/api/user/onboarding-survey-config", admin.AccessToken, string(editBody))
	require.Equal(t, 200, changed.Code, changed.Body.String())
	surveyFixture(t, "configuration-changed", changed)
	stale := surveyHTTPRequest(router, "POST", "/api/user/onboarding-survey", login.Data.AccessToken, string(oldBody))
	require.Equal(t, 409, stale.Code, stale.Body.String())
	assert.Equal(t, "SURVEY_CONFIG_CHANGED", surveyReadResponse(t, stale).Code)
	surveyFixture(t, "stale-config", stale)
	currentPending := surveyHTTPRequest(router, "GET", path, login.Data.AccessToken, "")
	require.Equal(t, 200, currentPending.Code)
	surveyFixture(t, "config-changed-pending", currentPending)
	view := surveyReadResponse(t, currentPending).Data
	assert.NotEqual(t, cfg.Revision, view.ConfigRevision)
	assert.Empty(t, view.SelectedOptionIDs)
	assert.Empty(t, view.Notes)
	answer, err := common.Marshal(map[string]any{"schema_version": 2, "survey_id": model.OnboardingSurveyID, "config_revision": view.ConfigRevision, "selected_option_ids": []string{"daily_qa", "custom_research"}, "notes": "保留我的备注"})
	require.NoError(t, err)
	submitted := surveyHTTPRequest(router, "POST", "/api/user/onboarding-survey", login.Data.AccessToken, string(answer))
	require.Equal(t, 200, submitted.Code, submitted.Body.String())
	surveyFixture(t, "submitted", submitted)
	snapshot := surveyReadResponse(t, submitted).Data
	// Rename, reorder and disable selected IDs after submission; old answer,
	// original labels, ordering and revision still describe the original meaning.
	latest, err := model.GetSurveyConfiguration()
	require.NoError(t, err)
	for i := range latest.Options {
		if latest.Options[i].ID == "daily_qa" {
			latest.Options[i].Label = "后来改的名字"
			latest.Options[i].LabelEN = "Later renamed meaning"
			latest.Options[i].Enabled = false
		}
	}
	latest.Options[0], latest.Options[1] = latest.Options[1], latest.Options[0]
	_, err = model.SaveSurveyConfiguration(latest.Revision, latest.Options)
	require.NoError(t, err)
	historical := surveyHTTPRequest(router, "GET", path, login.Data.AccessToken, "")
	require.Equal(t, 200, historical.Code)
	assert.Equal(t, snapshot, surveyReadResponse(t, historical).Data)
	surveyFixture(t, "config-changed-submitted", historical)
	replay := surveyHTTPRequest(router, "POST", "/api/user/onboarding-survey", login.Data.AccessToken, string(answer))
	require.Equal(t, 200, replay.Code)
	assert.Equal(t, snapshot, surveyReadResponse(t, replay).Data)
	assert.Equal(t, surveyReadResponse(t, legacyBefore).Data, surveyReadResponse(t, surveyHTTPRequest(router, "GET", path, legacy.Data.AccessToken, "")).Data)
	deferredLogin := surveySignIn(t, "+8613800000203")
	deferred := surveyHTTPRequest(router, "POST", "/api/user/onboarding-survey/defer", deferredLogin.Data.AccessToken, `{"schema_version":2,"survey_id":"product-use-v1"}`)
	require.Equal(t, 200, deferred.Code)
	surveyFixture(t, "deferred", deferred)
	oldPhone := "+8613800000204"
	old := model.User{Username: phoneUsername(oldPhone), AffCode: "old-config-member", PhoneE164: &oldPhone, Role: 1, Status: 1, AuthVersion: 1}
	require.NoError(t, db.Create(&old).Error)
	oldLogin := surveySignIn(t, oldPhone)
	ineligible := surveyHTTPRequest(router, "GET", path, oldLogin.Data.AccessToken, "")
	require.Equal(t, 200, ineligible.Code)
	surveyFixture(t, "not-eligible", ineligible)
	listing := surveyHTTPRequest(router, "GET", "/api/user/onboarding-surveys", admin.AccessToken, "")
	require.Equal(t, 200, listing.Code)
	surveyFixture(t, "admin-registrations", listing)
	// Startup preserves the administrator's current revision and immutable old
	// answer, rather than reseeding defaults over edited configuration.
	before, err := model.GetSurveyConfiguration()
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, db.AutoMigrate(model.SurveyConfigurationModels()...))
		require.NoError(t, model.InitializeSurveyConfiguration())
	}
	after, err := model.GetSurveyConfiguration()
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, snapshot, surveyReadResponse(t, surveyHTTPRequest(router, "GET", path, login.Data.AccessToken, "")).Data)
}

func TestConfigurableSurveyRejectsStaleAndInvalidAdministratorWrites(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	cfg, err := model.GetSurveyConfiguration()
	require.NoError(t, err)
	for _, change := range []string{"remove saved ID", "duplicate ID", "invalid ID", "empty English label", "no active option", "too many", "control character"} {
		t.Run(change, func(t *testing.T) {
			options := append([]model.SurveyConfigOption{}, cfg.Options...)
			switch change {
			case "remove saved ID":
				options = options[1:]
			case "duplicate ID":
				options = append(options, options[0])
			case "invalid ID":
				options = append(options, model.SurveyConfigOption{ID: "UPPER", Label: "新", LabelEN: "New", Enabled: true})
			case "empty English label":
				options[0].LabelEN = ""
			case "no active option":
				for i := range options {
					options[i].Enabled = false
				}
			case "too many":
				for i := range 65 {
					options = append(options, model.SurveyConfigOption{ID: fmt.Sprintf("extra_%d", i), Label: "新", LabelEN: "New", Enabled: true})
				}
			case "control character":
				options[0].Label = "bad\x01label"
			}
			_, err := model.SaveSurveyConfiguration(cfg.Revision, options)
			assert.ErrorIs(t, err, model.ErrSurveyInvalid)
			actual, err := model.GetSurveyConfiguration()
			require.NoError(t, err)
			assert.Equal(t, cfg, actual)
		})
	}
	// Two real concurrent edits with the same expected revision cannot silently
	// overwrite each other. The loser must reload the winning configuration.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	var failures [2]error
	for i := range 2 {
		workers.Go(func() {
			<-start
			options := append([]model.SurveyConfigOption{}, cfg.Options...)
			options[0].Label = fmt.Sprintf("管理员版本%d", i)
			_, failures[i] = model.SaveSurveyConfiguration(cfg.Revision, options)
		})
	}
	close(start)
	workers.Wait()
	assert.True(t, (failures[0] == nil && errors.Is(failures[1], model.ErrSurveyConfigChanged)) || (failures[1] == nil && errors.Is(failures[0], model.ErrSurveyConfigChanged)), "one editor wins; one receives a revision conflict")
}

func TestConfigurableSurveyEditAndAnswerAreSerialized(t *testing.T) {
	for _, editFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(editFirst), func(t *testing.T) {
			db, _ := setupPhoneAuthRecovery(t)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(8)
			login := surveySignIn(t, "+8613800000221")
			cfg, err := model.GetSurveyConfiguration()
			require.NoError(t, err)
			changed := append([]model.SurveyConfigOption{}, cfg.Options...)
			changed[0].Label = "更改后的名称"
			firstGuard, release, secondEntering := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			require.NoError(t, db.Callback().Update().After("gorm:update").Register("survey:edit-answer", func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "OnboardingSurveyConfigState" {
					once.Do(func() { close(firstGuard); <-release })
				}
			}))
			var editErr, answerErr error
			var answer model.OnboardingSurveyView
			edit := func() { _, editErr = model.SaveSurveyConfiguration(cfg.Revision, changed) }
			submit := func() {
				answer, answerErr = model.SubmitOnboardingSurveyVersion(login.Data.User.ID, []string{"daily_qa"}, "held draft", 2, cfg.Revision)
			}
			var workers sync.WaitGroup
			workers.Go(func() {
				if editFirst {
					edit()
				} else {
					submit()
				}
			})
			<-firstGuard
			workers.Go(func() {
				close(secondEntering)
				if editFirst {
					submit()
				} else {
					edit()
				}
			})
			<-secondEntering
			close(release)
			workers.Wait()
			require.NoError(t, db.Callback().Update().Remove("survey:edit-answer"))
			require.NoError(t, editErr)
			if editFirst {
				assert.ErrorIs(t, answerErr, model.ErrSurveyConfigChanged)
				view, err := model.GetOnboardingSurvey(login.Data.User.ID)
				require.NoError(t, err)
				assert.Equal(t, model.SurveyPending, view.Status)
			} else {
				require.NoError(t, answerErr)
				assert.Equal(t, cfg.Revision, answer.ConfigRevision)
				assert.Equal(t, cfg.ActiveOptions(), answer.Options)
				persisted, err := model.GetOnboardingSurvey(login.Data.User.ID)
				require.NoError(t, err)
				assert.Equal(t, answer, persisted)
			}
		})
	}
}

func TestConfigurableSurveyMaximumPayloadAndUnicode(t *testing.T) {
	_, _ = setupPhoneAuthRecovery(t)
	cfg, err := model.GetSurveyConfiguration()
	require.NoError(t, err)
	for i := range cfg.Options {
		cfg.Options[i].Label = strings.Repeat("<", 80)
		cfg.Options[i].LabelEN = strings.Repeat("<", 160)
	}
	for len(cfg.Options) < 64 {
		i := len(cfg.Options)
		cfg.Options = append(cfg.Options, model.SurveyConfigOption{ID: fmt.Sprintf("new_scene_%d", i), Label: strings.Repeat("<", 80), LabelEN: strings.Repeat("<", 160), Enabled: true})
	}
	cfg, err = model.SaveSurveyConfiguration(cfg.Revision, cfg.Options)
	require.NoError(t, err)
	login := surveySignIn(t, "+8613800000222")
	ids := make([]string, 0, 64)
	for _, o := range cfg.Options {
		ids = append(ids, o.ID)
	}
	body, err := common.Marshal(map[string]any{"schema_version": 2, "survey_id": "product-use-v1", "config_revision": cfg.Revision, "selected_option_ids": ids, "notes": strings.Repeat("😀", 1000)})
	require.NoError(t, err)
	router := surveyTestRouter()
	submitted := surveyHTTPRequest(router, "POST", "/api/user/onboarding-survey", login.Data.AccessToken, string(body))
	require.Equal(t, 200, submitted.Code, submitted.Body.String())
	assert.Less(t, submitted.Body.Len(), 128*1024, "Host's response bound accommodates every allowed field")
	view := surveyReadResponse(t, submitted).Data
	assert.Len(t, view.Options, 64)
	assert.Len(t, view.SelectedOptionIDs, 64)
	assert.Equal(t, 64, view.MaxSelections)
}

func TestConfigurableSurveyFilterMatchesExactOptionID(t *testing.T) {
	_, _ = setupPhoneAuthRecovery(t)
	cfg, err := model.GetSurveyConfiguration()
	require.NoError(t, err)
	cfg.Options = append(cfg.Options, model.SurveyConfigOption{ID: "dailyxqa", Label: "另一场景", LabelEN: "Another scenario", Enabled: true})
	cfg, err = model.SaveSurveyConfiguration(cfg.Revision, cfg.Options)
	require.NoError(t, err)
	for i, id := range []string{"daily_qa", "dailyxqa"} {
		login := surveySignIn(t, fmt.Sprintf("+861380000023%d", i))
		_, err := model.SubmitOnboardingSurveyVersion(login.Data.User.ID, []string{id}, "", 2, cfg.Revision)
		require.NoError(t, err)
	}
	items, total, err := model.ListOnboardingSurveys(model.OnboardingSurveyFilter{OptionID: "daily_qa"}, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "underscore in a saved option ID is a literal character")
	require.Len(t, items, 1)
	assert.Equal(t, []string{"daily_qa"}, items[0].SelectedOptionIDs)
}
