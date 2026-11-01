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
	ConfigRevision    string   `json:"config_revision"`
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
		allowed = append(allowed, "selected_option_ids", "notes", "config_revision")
	}
	for key, value := range fields {
		if !slices.Contains(allowed, key) || strings.TrimSpace(string(value)) == "null" {
			return nil, model.ErrSurveyInvalid
		}
	}
	var request onboardingSurveyRequest
	if err := common.Unmarshal(body, &request); err != nil || (request.SchemaVersion != 1 && request.SchemaVersion != 2) || request.SurveyID != model.OnboardingSurveyID {
		return nil, model.ErrSurveyInvalid
	}
	if !deferOnly && (request.SchemaVersion == 2 && !model.ValidSurveyConfigRevision(request.ConfigRevision) || request.SchemaVersion == 1 && request.ConfigRevision != "") {
		return nil, model.ErrSurveyInvalid
	}
	return &request, nil
}

func onboardingSurveyError(c *gin.Context, err error) {
	status, code, message := http.StatusServiceUnavailable, "SURVEY_UNAVAILABLE", "Questionnaire is temporarily unavailable"
	switch {
	case errors.Is(err, model.ErrSurveyUnsupported):
		status, code, message = http.StatusNotFound, "SURVEY_UNSUPPORTED", "This questionnaire requires a newer client"
	case errors.Is(err, model.ErrSurveyConfigChanged):
		view, e := model.GetOnboardingSurvey(c.GetInt("id"))
		if e == nil {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "SURVEY_CONFIG_CHANGED", "message": "Questionnaire options have changed. Review your choices before submitting.", "data": view})
			return
		}
		status, code, message = http.StatusServiceUnavailable, "SURVEY_UNAVAILABLE", "Questionnaire is temporarily unavailable"
	case errors.Is(err, model.ErrSurveyInvalid):
		status, code, message = http.StatusBadRequest, "SURVEY_INVALID_REQUEST", "Invalid questionnaire request"
	case errors.Is(err, model.ErrSurveyNotEligible):
		status, code, message = http.StatusForbidden, "SURVEY_NOT_ELIGIBLE", "This account is not eligible for the questionnaire"
	case errors.Is(err, model.ErrSurveyAlreadySubmitted):
		status, code, message = http.StatusConflict, "SURVEY_ALREADY_SUBMITTED", "Questionnaire has already been submitted"
	}
	c.JSON(status, gin.H{"success": false, "code": code, "message": message})
}

func writeOnboardingSurveyView(c *gin.Context, view model.OnboardingSurveyView, schema int) {
	if schema == 1 {
		var err error
		view, err = model.LegacySurveyView(view)
		if err != nil {
			onboardingSurveyError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}
func GetUserOnboardingSurvey(c *gin.Context) {
	schema := 1
	if c.Query("schema_version") == "2" {
		schema = 2
	} else if c.Query("schema_version") != "" && c.Query("schema_version") != "1" {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	view, err := model.GetOnboardingSurvey(c.GetInt("id"))
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	writeOnboardingSurveyView(c, view, schema)
}

func SubmitUserOnboardingSurvey(c *gin.Context) {
	request, err := decodeOnboardingSurveyRequest(c, false)
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	view, err := model.SubmitOnboardingSurveyVersion(c.GetInt("id"), request.SelectedOptionIDs, request.Notes, request.SchemaVersion, request.ConfigRevision)
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	writeOnboardingSurveyView(c, view, request.SchemaVersion)
}

func DeferUserOnboardingSurvey(c *gin.Context) {
	request, err := decodeOnboardingSurveyRequest(c, true)
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	view, err := model.DeferOnboardingSurvey(c.GetInt("id"))
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	writeOnboardingSurveyView(c, view, request.SchemaVersion)
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
	cfg, err := model.GetSurveyConfiguration()
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	if filter.OptionID != "" && !slices.ContainsFunc(cfg.Options, func(option model.SurveyConfigOption) bool { return option.ID == filter.OptionID }) {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	items, total, err := model.ListOnboardingSurveys(filter, (page-1)*size, size)
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"schema_version": 2, "survey_id": model.OnboardingSurveyID, "config_revision": cfg.Revision,
		"options": cfg.Options, "items": items, "total": total, "p": page, "page_size": size,
	}})
}

func GetOnboardingSurveyConfiguration(c *gin.Context) {
	cfg, err := model.GetSurveyConfiguration()
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": cfg})
}
func SaveOnboardingSurveyConfiguration(c *gin.Context) {
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024))
	if err != nil || !utf8.Valid(body) {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	var raw map[string]common.RawMessage
	if common.Unmarshal(body, &raw) != nil || len(raw) != 3 || raw["schema_version"] == nil || raw["expected_config_revision"] == nil || raw["options"] == nil {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	var request struct {
		SchemaVersion int                        `json:"schema_version"`
		Expected      string                     `json:"expected_config_revision"`
		Options       []model.SurveyConfigOption `json:"options"`
	}
	var rows []map[string]common.RawMessage
	if common.Unmarshal(raw["options"], &rows) != nil {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	for _, row := range rows {
		if len(row) != 4 || common.GetJsonType(row["id"]) != "string" || common.GetJsonType(row["label"]) != "string" || common.GetJsonType(row["label_en"]) != "string" || common.GetJsonType(row["enabled"]) != "boolean" {
			onboardingSurveyError(c, model.ErrSurveyInvalid)
			return
		}
	}
	if common.Unmarshal(body, &request) != nil || request.SchemaVersion != 2 || !model.ValidSurveyConfigRevision(request.Expected) {
		onboardingSurveyError(c, model.ErrSurveyInvalid)
		return
	}
	cfg, err := model.SaveSurveyConfiguration(request.Expected, request.Options)
	if errors.Is(err, model.ErrSurveyConfigChanged) {
		current, e := model.GetSurveyConfiguration()
		if e != nil {
			onboardingSurveyError(c, e)
			return
		}
		c.JSON(409, gin.H{"success": false, "code": "SURVEY_CONFIG_CHANGED", "message": "Questionnaire options have changed. Review your choices before submitting.", "data": current})
		return
	}
	if err != nil {
		onboardingSurveyError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": cfg})
}
