package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetShowcaseConfig(c *gin.Context) {
	demandRespond(c, gin.H{"capabilities": gin.H{"showcases_v1": true, "pagination": true, "public_ready_media": true}, "categories": service.ShowcaseCategories}, nil)
}
func GetShowcaseCatalog(c *gin.Context) {
	in := service.ShowcaseListInput{Cursor: c.Query("cursor"), Category: c.Query("category"), Query: c.Query("query"), Published: c.Query("published"), SeedBatch: c.Query("seed_batch"), Admin: c.GetBool("demand_admin")}
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 100 {
			demandRespond(c, nil, &service.DemandError{Status: 400, Code: "SHOWCASE_INVALID_INPUT", Message: "Invalid showcase filter"})
			return
		}
		in.Limit = v
	}
	v, err := service.ListShowcases(model.DB, in)
	demandRespond(c, v, err)
}
func GetShowcaseDetail(c *gin.Context) {
	v, err := service.GetShowcase(model.DB, c.Param("showcase_id"), c.GetBool("demand_admin"))
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	if c.GetBool("demand_admin") {
		demandRespond(c, gin.H{"item": v}, nil)
	} else {
		demandRespond(c, gin.H{"item": v.ShowcaseView}, nil)
	}
}
func CreateShowcase(c *gin.Context) {
	var in service.ShowcaseCreate
	if !demandBind(c, &in) {
		return
	}
	v, err := service.CreateShowcase(model.DB, demandActor(c), in)
	demandRespond(c, v, err)
}
func UpdateShowcase(c *gin.Context) {
	var in service.ShowcaseEdit
	if !demandBind(c, &in) {
		return
	}
	v, err := service.EditShowcase(model.DB, demandActor(c), c.Param("showcase_id"), in)
	demandRespond(c, v, err)
}
func CreateShowcaseUpload(c *gin.Context) {
	var in service.DemandUploadCreate
	if !demandBind(c, &in) {
		return
	}
	v, err := service.CreateShowcaseUpload(model.DB, demandActor(c), in)
	demandRespond(c, v, err)
}
func ShowcaseUploadScope(c *gin.Context) {
	if err := service.CheckShowcaseUpload(model.DB, demandActor(c), c.Param("upload_id")); err != nil {
		demandRespond(c, nil, err)
		c.Abort()
		return
	}
	c.Next()
}
func ServeShowcaseAsset(c *gin.Context) {
	v, file, disposition, err := service.OpenShowcaseAsset(model.DB, c.Param("showcase_id"), c.Param("attachment_id"))
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	defer file.Close()
	c.Header("Content-Type", v.MimeType)
	c.Header("Content-Disposition", disposition)
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	http.ServeContent(c.Writer, c.Request, "showcase-media", time.Time{}, file)
}
