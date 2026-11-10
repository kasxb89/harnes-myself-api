package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func modelConfigurationError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, model.ErrModelConfigurationConflict) {
		status = http.StatusConflict
	}
	common.ApiErrorStatus(c, status, common.NewMessage(err.Error()))
}

func respondModelConfigurationUnsent(c *gin.Context, status int, code, message string) bool {
	if !c.GetBool("model_configuration_unsent") || c.GetBool("model_configuration_billing_started") {
		return false
	}
	c.Header("X-New-Api-Task-Id", "")
	c.JSON(status, gin.H{"error": gin.H{"code": code, "type": "invalid_request_error", "message": message}, "accepted": false, "submission_state": "not_submitted", "retryable": false, "resubmittable": false, "charged": false, "reserved": false})
	return true
}

func GetModelConfiguration(c *gin.Context) {
	snapshot, err := model.GetModelConfigurationSnapshot(c.Query("model"))
	if err != nil {
		modelConfigurationError(c, err)
		return
	}
	populateOfficialReferenceSpecifications(&snapshot)
	c.Header("Cache-Control", "private, no-store")
	common.ApiSuccess(c, snapshot)
}

func SaveModelConfigurationDraft(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, model.MaxModelConfigurationBytes)
	var request struct {
		Revision    string                   `json:"revision"`
		BaseVersion string                   `json:"base_version"`
		Definition  model.ModelConfiguration `json:"definition"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		modelConfigurationError(c, errors.New("Invalid unified model configuration"))
		return
	}
	if err := model.SaveModelConfigurationDraft(request.Revision, request.BaseVersion, request.Definition); err != nil {
		modelConfigurationError(c, err)
		return
	}
	recordManageAudit(c, "model.configuration.draft", map[string]any{"model": request.Definition.ModelName})
	GetModelConfiguration(c)
}

func PublishModelConfiguration(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request struct {
		Revision        string   `json:"revision"`
		Models          []string `json:"models"`
		RollbackVersion string   `json:"rollback_version"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		modelConfigurationError(c, errors.New("Invalid unified model publication"))
		return
	}
	if err := model.PublishModelConfigurations(request.Revision, request.Models, request.RollbackVersion); err != nil {
		modelConfigurationError(c, err)
		return
	}
	recordManageAudit(c, "model.configuration.publish", map[string]any{"models": request.Models, "rollback_version": request.RollbackVersion})
	GetModelConfiguration(c)
}
