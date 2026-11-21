package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type demandDirectoryCursor struct {
	Key    string `json:"key"`
	Filter string `json:"filter"`
}

// Immutable profile codes (real engineer IDs) preserve order when administrators edit names.
func GetDemandDirectory(c *gin.Context) {
	kind := c.GetString("demand_directory_kind")
	admin := c.GetBool("demand_admin")
	legacy := kind == "engineers" && c.Query("limit") == "" && c.Query("after_id") == ""
	defaultLimit, maxLimit := 30, 100
	if legacy {
		defaultLimit, maxLimit = 200, 200
	}
	limit, err := demandLimit(c, defaultLimit, maxLimit)
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	search, category := strings.TrimSpace(c.Query("q")), c.Query("category_id")
	if utf8.RuneCountInString(search) > 100 || len(category) > 64 {
		demandRespond(c, nil, &service.DemandError{Status: 400, Code: "DEMAND_INVALID_INPUT", Message: "Invalid directory filter"})
		return
	}
	var q *gorm.DB
	key := "code"
	switch kind {
	case "service-profiles":
		q = model.DB.Model(&model.DemandServiceProfile{})
		if !admin {
			q = q.Where("published = ? AND enabled = ?", true, true)
		}
	case "customer-profiles":
		q = model.DB.Model(&model.DemandCustomerProfile{})
	default:
		q = model.DB.Model(&model.DemandEngineer{})
		key = "id"
		if !admin {
			q = q.Where("published = ? AND enabled = ?", true, true).Where("user_id IN (?)", model.DB.Model(&model.User{}).Select("id").Where("status = ?", common.UserStatusEnabled))
		}
	}
	escape := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	if search != "" {
		pattern := "%" + escape.Replace(strings.ToLower(search)) + "%"
		fields := "LOWER(display_name) LIKE ? ESCAPE '!'"
		args := []any{pattern}
		if kind != "engineers" {
			fields += " OR LOWER(code) LIKE ? ESCAPE '!'"
			args = append(args, pattern)
		}
		if kind == "service-profiles" {
			fields += " OR LOWER(service_direction) LIKE ? ESCAPE '!'"
			args = append(args, pattern)
		}
		if kind == "customer-profiles" {
			fields += " OR LOWER(organization) LIKE ? ESCAPE '!'"
			args = append(args, pattern)
		}
		q = q.Where("("+fields+")", args...)
	}
	if category != "" && kind != "customer-profiles" {
		raw, _ := common.Marshal(category)
		q = q.Where("category_json LIKE ? ESCAPE '!'", "%"+escape.Replace(string(raw))+"%")
	}
	var total int64
	if err = q.Count(&total).Error; err != nil {
		demandRespond(c, nil, err)
		return
	}
	filterBytes, _ := common.Marshal([]any{kind, admin, search, category, legacy})
	sum := sha256.Sum256(filterBytes)
	fingerprint := hex.EncodeToString(sum[:])
	if raw := c.Query("after_id"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		var cur demandDirectoryCursor
		if len(raw) > 1024 || e != nil || common.Unmarshal(b, &cur) != nil || cur.Key == "" || len(cur.Key) > 80 || cur.Filter != fingerprint {
			demandRespond(c, nil, &service.DemandError{Status: 400, Code: "DEMAND_CURSOR_INVALID", Message: "Invalid directory cursor"})
			return
		}
		q = q.Where(key+" > ?", cur.Key)
	}
	q = q.Order(key + " ASC").Limit(limit + 1)
	// Legacy readers keep their original sort order, while bounded responses cannot exceed old decoders.
	if legacy {
		q = q.Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "sort"}, Reorder: true}, {Column: clause.Column{Name: "id"}}}})
	}
	views := []any{}
	nextKey := ""
	more := false
	switch kind {
	case "service-profiles":
		items := []model.DemandServiceProfile{}
		err = q.Find(&items).Error
		more = len(items) > limit
		if more {
			items = items[:limit]
		}
		galleries := map[string]service.DemandServiceGalleryView{}
		if c.Query("include_media") == "1" {
			galleries, err = service.GetDemandServiceGalleryBatch(model.DB.WithContext(c.Request.Context()), demandActor(c), items)
			if err != nil {
				demandRespond(c, nil, err)
				return
			}
		}
		for _, p := range items {
			view := service.ProjectDemandServiceProfile(p)
			if gallery, ok := galleries[p.ID]; ok {
				view.Gallery = &gallery
			}
			views = append(views, view)
			nextKey = p.Code
		}
	case "customer-profiles":
		items := []model.DemandCustomerProfile{}
		err = q.Find(&items).Error
		more = len(items) > limit
		if more {
			items = items[:limit]
		}
		for _, p := range items {
			views = append(views, service.ProjectDemandCustomerProfile(p))
			nextKey = p.Code
		}
	default:
		items := []model.DemandEngineer{}
		err = q.Find(&items).Error
		more = len(items) > limit
		if more {
			items = items[:limit]
		}
		for _, p := range items {
			views = append(views, service.DemandEngineerPublic(p, admin))
			nextKey = p.ID
		}
	}
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	next := ""
	if more && !legacy {
		b, _ := common.Marshal(demandDirectoryCursor{nextKey, fingerprint})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	demandRespond(c, gin.H{"items": views, "next_after_id": next, "has_more": more, "total": total, "server_time": time.Now().Unix()}, nil)
}
func SaveDemandServiceProfile(c *gin.Context) {
	var in service.DemandServiceProfileView
	if !demandBind(c, &in) {
		return
	}
	v, err := service.SaveDemandServiceProfile(model.DB, c.Param("profile_id"), in)
	demandRespond(c, v, err)
}
func SaveDemandCustomerProfile(c *gin.Context) {
	var in service.DemandCustomerProfileView
	if !demandBind(c, &in) {
		return
	}
	v, err := service.SaveDemandCustomerProfile(model.DB, c.Param("profile_id"), in)
	demandRespond(c, v, err)
}
func ImportDemandProfiles(c *gin.Context) {
	var in service.DemandProfileImportInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	if common.DecodeJson(c.Request.Body, &in) != nil {
		demandRespond(c, nil, &service.DemandError{Status: 400, Code: "DEMAND_INVALID_INPUT", Message: "Invalid profile import"})
		return
	}
	v, err := service.ImportDemandProfiles(model.DB, in)
	demandRespond(c, v, err)
}

func ImportDemandPresetProfiles(c *gin.Context) {
	var in struct {
		BatchCode string `json:"batch_code"`
	}
	if !demandBind(c, &in) {
		return
	}
	v, err := service.ImportDemandPresetProfiles(model.DB, demandActor(c), in.BatchCode)
	demandRespond(c, v, err)
}
