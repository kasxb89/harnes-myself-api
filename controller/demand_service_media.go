package controller

import (
	"mime"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetDemandServiceGallery(c *gin.Context) {
	v, err := service.GetDemandServiceGallery(model.DB.WithContext(c.Request.Context()), demandActor(c), c.Param("profile_id"))
	demandRespond(c, v, err)
}
func PutDemandServiceGallery(c *gin.Context) {
	var in service.DemandServiceGalleryInput
	if !demandBind(c, &in) {
		return
	}
	v, replayed, err := service.SaveDemandServiceGallery(model.DB.WithContext(c.Request.Context()), demandActor(c), c.Param("profile_id"), in)
	demandRespond(c, gin.H{"gallery": v, "replayed": replayed}, err)
}
func CreateDemandServiceMediaUpload(c *gin.Context) {
	var in service.DemandUploadCreate
	if !demandBind(c, &in) {
		return
	}
	v, err := service.CreateDemandServiceMediaUpload(model.DB.WithContext(c.Request.Context()), demandActor(c), in)
	demandRespond(c, v, err)
}
func GetDemandServiceMediaUpload(c *gin.Context) {
	v, err := service.GetDemandServiceMediaUpload(model.DB.WithContext(c.Request.Context()), demandActor(c), c.Param("upload_id"))
	demandRespond(c, v, err)
}
func PutDemandServiceMediaChunk(c *gin.Context) {
	if _, err := service.GetDemandServiceMediaUpload(model.DB.WithContext(c.Request.Context()), demandActor(c), c.Param("upload_id")); err != nil {
		demandRespond(c, nil, err)
		return
	}
	PutDemandUploadChunk(c)
}
func CompleteDemandServiceMedia(c *gin.Context) {
	if _, err := service.GetDemandServiceMediaUpload(model.DB.WithContext(c.Request.Context()), demandActor(c), c.Param("upload_id")); err != nil {
		demandRespond(c, nil, err)
		return
	}
	CompleteDemandUpload(c)
}
func GetDemandServiceMediaLink(c *gin.Context) {
	var in struct {
		Disposition string `json:"disposition"`
	}
	if !demandBind(c, &in) {
		return
	}
	v, err := service.SignDemandServiceMediaLink(model.DB.WithContext(c.Request.Context()), demandActor(c), c.Param("profile_id"), c.Param("attachment_id"), in.Disposition)
	demandRespond(c, v, err)
}
func ServeDemandServiceMedia(c *gin.Context) {
	v, f, disposition, err := service.OpenDemandServiceMedia(model.DB.WithContext(c.Request.Context()), c.Param("profile_id"), c.Param("attachment_id"), c.Query("token"))
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	c.Header("Content-Type", v.MimeType)
	c.Header("Content-Disposition", disposition)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("ETag", `"`+v.SHA256+`"`)
	_, params, _ := mime.ParseMediaType(disposition)
	name := params["filename"]
	if name == "" || strings.ContainsAny(name, "/\\\x00") {
		name = "service-media"
	}
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), f)
}
