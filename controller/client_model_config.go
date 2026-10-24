package controller

import (
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetClientModelConfig(c *gin.Context) {
	common.ApiSuccess(c, model.CurrentClientModelConfig())
}

func GetClientModelCandidates(c *gin.Context) {
	service.RefreshBlackrxCatalog(c.Request.Context())
	models, err := model.ListClientModelCandidates()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to load client model catalog"))
		return
	}
	common.ApiSuccess(c, gin.H{"models": models})
}

func SaveClientModelConfig(c *gin.Context) {
	if c.ContentType() != "application/json" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid client model configuration"))
		return
	}
	const maxBodyBytes = model.MaxClientModelConfigBodyBytes
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes+1))
	if len(data) > maxBodyBytes {
		common.ApiErrorStatus(c, http.StatusRequestEntityTooLarge, common.NewMessage("Client model configuration is too large"))
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid client model configuration"))
		return
	}
	config, err := model.ParseClientModelConfig(data)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid client model configuration"))
		return
	}
	if err := model.SaveClientModelConfig(config); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrClientModelCatalog) || errors.Is(err, model.ErrClientModelSave) {
			status = http.StatusInternalServerError
		}
		common.ApiErrorStatus(c, status, common.NewMessage(err.Error()))
		return
	}
	// Return this committed object even if another administrator saves afterward.
	common.ApiSuccess(c, config)
}

func GetClientModelPresentation(c *gin.Context) {
	common.ApiSuccess(c, model.CurrentClientModelPresentation())
}

func SaveClientModelPresentation(c *gin.Context) {
	if c.ContentType() != "application/json" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid client model presentation"))
		return
	}
	const maxBodyBytes = model.MaxClientModelPresentationBodyBytes
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes+1))
	if len(data) > maxBodyBytes {
		common.ApiErrorStatus(c, http.StatusRequestEntityTooLarge, common.NewMessage("Client model presentation is too large"))
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid client model presentation"))
		return
	}
	config, err := model.ParseClientModelPresentation(data)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid client model presentation"))
		return
	}
	if err := model.SaveClientModelPresentation(config); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrClientModelPresentationSave) {
			status = http.StatusInternalServerError
		}
		common.ApiErrorStatus(c, status, common.NewMessage(err.Error()))
		return
	}
	common.ApiSuccess(c, config)
}
