package controller

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type onboardingSurveyRequest struct {
	SchemaVersion     int      `json:"schema_version"`
	SurveyID          string   `json:"survey_id"`
	SelectedOptionIDs []string `json:"selected_option_ids"`
	Notes             string   `json:"notes"`
}

// This API accepts explicit dashboard Authorization credentials, never cookies.
// Require JSON as well; HTML form posts cannot submit questionnaire answers.
func decodeOnboardingSurveyRequest(c *gin.Context, deferOnly bool) (*onboardingSurveyRequest, error) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, model.ErrSurveyInvalid
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024))
	if err != nil || !utf8.Valid(body) {
		return nil, model.ErrSurveyInvalid
	}
	var fields map[string]common.RawMessage
	if err := common.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, model.ErrSurveyInvalid
	}
	allowed := []string{"schema_version", "survey_id"}
	if !deferOnly {
		allowed = append(allowed, "selected_option_ids", "notes")
	}
	for key, value := range fields {
		if !slices.Contains(allowed, key) || strings.TrimSpace(string(value)) == "null" {
			return nil, model.ErrSurveyInvalid
		}
	}
	var request onboardingSurveyRequest
	if err := common.Unmarshal(body, &request); err != nil || request.SchemaVersion != model.OnboardingSurveySchemaVersion || request.SurveyID != model.OnboardingSurveyID {
		return nil, model.ErrSurveyInvalid
	}
	return &request, nil
}

func onboardingSurveyError(c *gin.Context, err error) {
	status, code, message := http.StatusServiceUnavailable, "SURVEY_UNAVAILABLE", "Questionnaire is temporarily unavailable"
	switch {
	case errors.Is(err, model.ErrSurveyInvalid):
		status, code, message = http.StatusBadRequest, "SURVEY_INVALID_REQUEST", "Invalid questionnaire request"
	case errors.Is(err, model.ErrSurveyNotEligible):
		status, code, message = http.StatusForbidden, "SURVEY_NOT_ELIGIBLE", "This account is not eligible for the questionnaire"
	case errors.Is(err, model.ErrSurveyAlreadySubmitted):
		status, code, message = http.StatusConflict, "SURVEY_ALREADY_SUBMITTED", "Questionnaire has already been submitted"
	}
	c.JSON(status, gin.H{"success": false, "code": code, "message": message})
}

func GetUserOnboardingSurvey(c *gin.Context) {
	view, err := model.GetOnboardingSurvey(c.GetInt("id"))
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

func SubmitUserOnboardingSurvey(c *gin.Context) {
	request, err := decodeOnboardingSurveyRequest(c, false)
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	view, err := model.SubmitOnboardingSurvey(c.GetInt("id"), request.SelectedOptionIDs, request.Notes)
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

func DeferUserOnboardingSurvey(c *gin.Context) {
	if _, err := decodeOnboardingSurveyRequest(c, true); err != nil {
		onboardingSurveyError(c, err)
		return
	}
	view, err := model.DeferOnboardingSurvey(c.GetInt("id"))
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

func ListUserOnboardingSurveys(c *gin.Context) {
	page, size := 1, 20
	var err error
	if raw := c.Query("p"); raw != "" {
		page, err = strconv.Atoi(raw)
	}
	if err != nil || page < 1 || page > 1000000 {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	if raw := c.Query("page_size"); raw != "" {
		size, err = strconv.Atoi(raw)
	}
	if err != nil || size < 1 || size > 100 {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	filter := model.OnboardingSurveyFilter{Phone: strings.TrimSpace(c.Query("phone")), Status: c.Query("status"), OptionID: c.Query("option_id")}
	if len(filter.Phone) > 32 || (filter.Status != "" && !slices.Contains([]string{model.SurveyPending, model.SurveyDeferred, model.SurveySubmitted}, filter.Status)) {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	for _, ch := range filter.Phone {
		if ch != '+' && (ch < '0' || ch > '9') {
			onboardingSurveyError(c, model.ErrSurveyInvalid)
			return
		}
	}
	if filter.OptionID != "" && !slices.ContainsFunc(model.OnboardingSurveyOptions(), func(option model.SurveyOption) bool { return option.ID == filter.OptionID }) {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	items, total, err := model.ListOnboardingSurveys(filter, (page-1)*size, size)
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"schema_version": model.OnboardingSurveySchemaVersion, "survey_id": model.OnboardingSurveyID,
		"options": model.OnboardingSurveyOptions(), "items": items, "total": total, "p": page, "page_size": size,
	}})
}
