package controller

import (
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/creationcontent"
	"github.com/gin-gonic/gin"
)

func GetCustomerService(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, model.CurrentCustomerServiceConfig())
}

func SaveCustomerService(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.ContentType() != "application/json" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(model.ErrInvalidCustomerService.Error()))
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, model.MaxCustomerServiceBodyBytes+1))
	if len(data) > model.MaxCustomerServiceBodyBytes {
		common.ApiErrorStatus(c, http.StatusRequestEntityTooLarge, common.NewMessage("Customer service configuration is too large"))
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(model.ErrInvalidCustomerService.Error()))
		return
	}
	config, err := model.ParseCustomerServiceConfig(data)
	if err == nil {
		err = model.SaveCustomerServiceConfig(config)
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrCustomerServiceSave) {
			status = http.StatusInternalServerError
		}
		common.ApiErrorStatus(c, status, common.NewMessage(err.Error()))
		return
	}
	common.ApiSuccess(c, config)
}

func UploadCustomerServiceAsset(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, creationcontent.MaxImageSize+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		creationContentError(c, model.ErrCustomerServiceImage)
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		creationContentError(c, model.ErrCustomerServiceImage)
		return
	}
	defer file.Close()
	asset, err := model.UploadCustomerServiceImage(file)
	if err != nil {
		creationContentError(c, err)
		return
	}
	common.ApiSuccess(c, asset)
}
