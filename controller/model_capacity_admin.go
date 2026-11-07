package controller

import (
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetModelCapacityCatalog(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	catalog, err := model.ReadModelCapacityCatalog()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, catalog)
}

func PatchModelCapacity(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20))
	var patch model.ModelCapacityPatch
	if err != nil || common.Unmarshal(data, &patch) != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid model capacity changes"))
		return
	}
	catalog, err := model.PatchModelCapacity(patch)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrModelCapacityConflict) {
			status = http.StatusConflict
		}
		common.ApiErrorStatus(c, status, common.NewMessage(err.Error()))
		return
	}
	recordManageAudit(c, "model.capacity.update", map[string]any{"route_count": len(patch.Routes)})
	common.ApiSuccess(c, catalog)
}
