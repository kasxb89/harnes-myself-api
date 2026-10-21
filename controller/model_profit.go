package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetModelProfits(c *gin.Context) {
	filter := model.ModelProfitFilter{ModelName: c.Query("model_name")}
	for key, target := range map[string]*int64{"start_timestamp": &filter.Start, "end_timestamp": &filter.End} {
		value, err := strconv.ParseInt(c.Query(key), 10, 64)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
			return
		}
		*target = value
	}
	report, err := model.GetModelProfits(c.Request.Context(), filter)
	if err != nil {
		if errors.Is(err, model.ErrModelProfitParameters) || errors.Is(err, model.ErrModelProfitTooLarge) {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to query statistics"))
		return
	}
	common.ApiSuccess(c, report)
}

func GetModelProfitCostRules(c *gin.Context) {
	config, err := model.GetModelProfitCostRules(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to query statistics"))
		return
	}
	common.ApiSuccess(c, config)
}

func SaveModelProfitCostRules(c *gin.Context) {
	if c.ContentType() != "application/json" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, model.MaxModelProfitCostRulesBytes+1))
	if err != nil || len(body) > model.MaxModelProfitCostRulesBytes {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	config, err := model.ParseModelProfitCostRules(body)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	config, err = model.SaveModelProfitCostRules(c.Request.Context(), config)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, model.ErrModelProfitParameters) || errors.Is(err, model.ErrModelProfitHistory) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, model.ErrModelProfitConflict) {
			status = http.StatusConflict
		}
		if status == http.StatusInternalServerError {
			common.ApiErrorStatus(c, status, common.NewMessage("Failed to save profit cost rules"))
			return
		}
		common.ApiErrorStatus(c, status, common.NewMessage(err.Error()))
		return
	}
	common.ApiSuccess(c, config)
}
