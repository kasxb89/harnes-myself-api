package controller

import (
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/creationcontent"
	"github.com/gin-gonic/gin"
)

func creationContentError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, creationcontent.ErrConflict) {
		status = http.StatusConflict
	}
	if errors.Is(err, creationcontent.ErrStorage) {
		status = http.StatusInternalServerError
	}
	common.ApiErrorStatus(c, status, common.NewMessage(err.Error()))
}
func GetCreationContent(c *gin.Context) {
	content, err := creationcontent.Default().Load(true)
	if err != nil {
		creationContentError(c, err)
		return
	}
	c.Header("Cache-Control", "no-cache")
	common.ApiSuccess(c, content)
}
func GetAdminCreationContent(c *gin.Context) {
	content, err := creationcontent.Default().Load(false)
	if err != nil {
		creationContentError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, content)
}
func SaveCreationContent(c *gin.Context) {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20))
	if err != nil {
		creationContentError(c, creationcontent.ErrInvalid)
		return
	}
	var content creationcontent.Content
	if common.Unmarshal(data, &content) != nil {
		creationContentError(c, creationcontent.ErrInvalid)
		return
	}
	saved, err := creationcontent.Default().Save(content)
	if err != nil {
		creationContentError(c, err)
		return
	}
	common.ApiSuccess(c, saved)
}
func UploadCreationAsset(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, creationcontent.MaxVideoSize+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		creationContentError(c, creationcontent.ErrAsset)
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		creationContentError(c, creationcontent.ErrAsset)
		return
	}
	defer file.Close()
	asset, err := creationcontent.Default().Upload(file)
	if err != nil {
		creationContentError(c, err)
		return
	}
	common.ApiSuccess(c, asset)
}
func GetCreationAsset(c *gin.Context) {
	creationcontent.Default().ServeAsset(c.Writer, c.Request, c.Param("id"))
}
