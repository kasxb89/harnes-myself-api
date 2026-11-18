package controller

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func DemandSessionOnly(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		demandRespond(c, nil, &service.DemandError{Status: 403, Code: "DEMAND_AUTH_REQUIRED", Message: "A login session is required"})
		c.Abort()
		return
	}
	c.Next()
}
func DemandAdminScope(c *gin.Context) { c.Set("demand_admin", true); c.Next() }

// Session Bearer credentials are attached by the desktop Host or same-origin
// dashboard. Cookie-only requests retain the existing CSRF Origin guard.
func DemandOriginGuard(c *gin.Context) {
	if strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
		c.Next()
		return
	}
	middleware.SessionCookieOriginGuard()(c)
}
func DemandEngineerScope(c *gin.Context) {
	if _, err := service.DemandEngineerForUser(model.DB, c.GetInt("id")); err != nil {
		demandRespond(c, nil, err)
		c.Abort()
		return
	}
	c.Set("demand_engineer", true)
	c.Next()
}
func demandActor(c *gin.Context) service.DemandActor {
	return service.DemandActor{UserID: c.GetInt("id"), Admin: c.GetBool("demand_admin")}
}
func demandRespond(c *gin.Context, data any, err error) {
	if err == nil {
		common.ApiSuccess(c, data)
		return
	}
	var problem *service.DemandError
	if !errors.As(err, &problem) {
		problem = &service.DemandError{Status: 503, Code: "DEMAND_STORAGE_UNAVAILABLE", Message: "Demand service is temporarily unavailable"}
	}
	c.JSON(problem.Status, gin.H{"success": false, "code": problem.Code, "message": i18n.T(c, problem.Message), "request_id": c.GetString(common.RequestIdKey)})
}
func demandBind(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	if err := common.DecodeJson(c.Request.Body, v); err != nil {
		demandRespond(c, nil, &service.DemandError{Status: 400, Code: "DEMAND_INVALID_INPUT", Message: "Invalid demand input"})
		return false
	}
	return true
}
func GetDemandConfiguration(c *gin.Context) {
	v, err := service.LoadDemandConfig(model.DB)
	if c.GetBool("demand_admin") {
		demandRespond(c, v, err)
		return
	}
	demandRespond(c, gin.H{"config": v.Config, "server_time": time.Now().Unix(), "capabilities": gin.H{"resumable_upload": true, "polling": true}}, err)
}
func SaveDemandConfiguration(c *gin.Context) {
	var in service.DemandConfigVersion
	if !demandBind(c, &in) {
		return
	}
	v, err := service.SaveDemandConfig(model.DB, in)
	demandRespond(c, v, err)
}
func GetDemandEngineers(c *gin.Context) {
	items := []model.DemandEngineer{}
	q := model.DB.Model(&model.DemandEngineer{})
	if !c.GetBool("demand_admin") {
		q = q.Where("published = ? AND enabled = ?", true, true)
		q = q.Where("user_id IN (?)", model.DB.Model(&model.User{}).Select("id").Where("status = ?", common.UserStatusEnabled))
	}
	if err := q.Order("sort ASC, id ASC").Find(&items).Error; err != nil {
		demandRespond(c, nil, err)
		return
	}
	views := []service.DemandEngineerView{}
	for _, e := range items {
		v := service.DemandEngineerPublic(e, c.GetBool("demand_admin"))
		if cat := c.Query("category_id"); cat != "" {
			found := false
			for _, id := range v.CategoryIDs {
				if id == cat {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		views = append(views, v)
	}
	demandRespond(c, gin.H{"items": views, "server_time": time.Now().Unix()}, nil)
}
func SaveDemandEngineer(c *gin.Context) {
	var in service.DemandEngineerView
	if !demandBind(c, &in) {
		return
	}
	v, err := service.SaveDemandEngineer(model.DB, c.Param("engineer_id"), in)
	demandRespond(c, v, err)
}
func DemandEngineerHeartbeat(c *gin.Context) {
	var in struct {
		Accepting *bool `json:"accepting"`
	}
	if !demandBind(c, &in) {
		return
	}
	e, err := service.DemandEngineerForUser(model.DB, c.GetInt("id"))
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	now := time.Now().Unix()
	updates := map[string]any{"last_heartbeat": now}
	if in.Accepting != nil {
		updates["accepting"] = *in.Accepting
		updates["revision"] = gorm.Expr("revision + 1")
		e.Accepting = *in.Accepting
	}
	err = model.DB.Model(&model.DemandEngineer{}).Where("id = ? AND enabled = ?", e.ID, true).Updates(updates).Error
	e.LastHeartbeat = now
	demandRespond(c, gin.H{"engineer": service.DemandEngineerPublic(e, false), "presence": service.DemandEngineerPublic(e, false).Presence, "server_time": now, "expires_at": now + 90}, err)
}
func CreateUserDemand(c *gin.Context) {
	var in service.DemandCreate
	if !demandBind(c, &in) {
		return
	}
	out, err := service.CreateDemand(model.DB, demandActor(c), in)
	if err == nil && !out.Replayed {
		c.JSON(http.StatusCreated, gin.H{"success": true, "data": out})
		return
	}
	demandRespond(c, out, err)
}
func GetDemand(c *gin.Context) {
	a := demandActor(c)
	d, err := service.DemandAccess(model.DB, a, c.Param("id"))
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	v, err := service.ProjectDemand(model.DB, a, d)
	demandRespond(c, gin.H{"demand": v}, err)
}
func SendDemandMessage(c *gin.Context) {
	var in service.DemandSend
	if !demandBind(c, &in) {
		return
	}
	v, err := service.SendDemandMessage(model.DB, demandActor(c), c.Param("id"), in)
	demandRespond(c, v, err)
}
func PostDemandAction(c *gin.Context) {
	var in service.DemandAction
	if !demandBind(c, &in) {
		return
	}
	v, err := service.ActOnDemand(model.DB, demandActor(c), c.Param("id"), in)
	demandRespond(c, v, err)
}
func PostDemandRead(c *gin.Context) {
	var in struct {
		LastReadSeq int64 `json:"last_read_seq"`
	}
	if !demandBind(c, &in) {
		return
	}
	v, err := service.MarkDemandRead(model.DB, demandActor(c), c.Param("id"), in.LastReadSeq)
	demandRespond(c, v, err)
}
func demandLimit(c *gin.Context, defaultLimit, maxLimit int) (int, error) {
	raw := c.Query("limit")
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxLimit {
		return 0, &service.DemandError{Status: 400, Code: "DEMAND_CURSOR_INVALID", Message: "Invalid demand cursor"}
	}
	return n, nil
}
func GetDemandMessages(c *gin.Context) {
	a := demandActor(c)
	d, err := service.DemandAccess(model.DB, a, c.Param("id"))
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	after, err := strconv.ParseInt(c.DefaultQuery("after_seq", "0"), 10, 64)
	if err != nil || after < 0 || after > d.LastSeq {
		demandRespond(c, nil, &service.DemandError{Status: 400, Code: "DEMAND_CURSOR_INVALID", Message: "Invalid demand cursor"})
		return
	}
	limit, err := demandLimit(c, 100, 200)
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	items := []model.DemandMessage{}
	err = model.DB.Where("demand_id = ? AND seq > ? AND seq <= ?", d.ID, after, d.LastSeq).Order("seq ASC").Limit(limit + 1).Find(&items).Error
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	views, peerRead, err := service.ProjectDemandMessages(model.DB, a, d, items)
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	next := after
	if len(items) > 0 {
		next = items[len(items)-1].Seq
	}
	view, err := service.ProjectDemand(model.DB, a, d)
	demandRespond(c, gin.H{"items": views, "next_after_seq": next, "last_seq": d.LastSeq, "peer_read_seq": peerRead, "has_more": more, "demand": view}, err)
}

type demandListCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

func demandListQuery(c *gin.Context) (*gorm.DB, error) {
	a := demandActor(c)
	q := model.DB.Model(&model.Demand{})
	if !a.Admin {
		if c.GetBool("demand_engineer") {
			e, err := service.DemandEngineerForUser(model.DB, a.UserID)
			if err != nil {
				return nil, err
			}
			q = q.Where("engineer_id = ?", e.ID)
		} else {
			q = q.Where("user_id = ?", a.UserID)
		}
	}
	if status := c.Query("status"); status != "" {
		valid := false
		for _, s := range []string{"pending", "communicating", "in_progress", "awaiting_acceptance", "completed", "cancelled", "closed"} {
			if status == s {
				valid = true
			}
		}
		if !valid {
			return nil, &service.DemandError{Status: 400, Code: "DEMAND_INVALID_INPUT", Message: "Invalid demand status"}
		}
		q = q.Where("status = ?", status)
	}
	if a.Admin {
		for _, field := range []string{"user_id", "engineer_id"} {
			if v := c.Query(field); v != "" {
				q = q.Where(field+" = ?", v)
			}
		}
		for _, pair := range [][2]string{{"created_after", "created_at >= ?"}, {"created_before", "created_at <= ?"}} {
			if s := c.Query(pair[0]); s != "" {
				at, err := strconv.ParseInt(s, 10, 64)
				if err != nil || at < 0 {
					return nil, &service.DemandError{Status: 400, Code: "DEMAND_CURSOR_INVALID", Message: "Invalid demand date filter"}
				}
				q = q.Where(pair[1], at)
			}
		}
	}
	return q, nil
}
func ListDemands(c *gin.Context) {
	q, err := demandListQuery(c)
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	limit, err := demandLimit(c, 30, 100)
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	if raw := c.Query("before_id"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		var cursor demandListCursor
		if e != nil || len(raw) > 200 || common.Unmarshal(b, &cursor) != nil || cursor.At < 0 || len(cursor.ID) > 40 {
			demandRespond(c, nil, &service.DemandError{Status: 400, Code: "DEMAND_CURSOR_INVALID", Message: "Invalid demand cursor"})
			return
		}
		q = q.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.At, cursor.At, cursor.ID)
	}
	items := []model.Demand{}
	if err = q.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&items).Error; err != nil {
		demandRespond(c, nil, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	views := []service.DemandView{}
	for _, d := range items {
		v, e := service.ProjectDemand(model.DB, demandActor(c), d)
		if e != nil {
			demandRespond(c, nil, e)
			return
		}
		views = append(views, v)
	}
	cursor := ""
	if more && len(items) > 0 {
		last := items[len(items)-1]
		b, _ := common.Marshal(demandListCursor{last.CreatedAt, last.ID})
		cursor = base64.RawURLEncoding.EncodeToString(b)
	}
	demandRespond(c, gin.H{"items": views, "next_before_id": cursor, "has_more": more}, nil)
}
func GetDemandUnread(c *gin.Context) {
	a := demandActor(c)
	q, err := demandListQuery(c)
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	var demands []model.Demand
	// Aggregate with indexed EXISTS/count subqueries; do not load every message.
	if err = q.Select("id").Find(&demands).Error; err != nil {
		demandRespond(c, nil, err)
		return
	}
	ids := make([]string, 0, len(demands))
	for _, d := range demands {
		ids = append(ids, d.ID)
	}
	var total int64
	var unreadDemands int64
	if len(ids) > 0 {
		var rows []struct {
			DemandID string
			Count    int64
		}
		err = model.DB.Model(&model.DemandMessage{}).Select("demand_messages.demand_id, COUNT(*) AS count").Joins("LEFT JOIN demand_read_cursors r ON r.demand_id = demand_messages.demand_id AND r.user_id = ?", a.UserID).Where("demand_messages.demand_id IN ? AND demand_messages.sender_id <> ? AND demand_messages.seq > COALESCE(r.last_read_seq,0)", ids, a.UserID).Group("demand_messages.demand_id").Scan(&rows).Error
		for _, r := range rows {
			total += r.Count
			unreadDemands++
		}
	}
	demandRespond(c, gin.H{"unread_count": total, "unread_demands": unreadDemands, "server_time": time.Now().Unix()}, err)
}
func GetDemandAudit(c *gin.Context) {
	if _, err := service.DemandAccess(model.DB, demandActor(c), c.Param("id")); err != nil {
		demandRespond(c, nil, err)
		return
	}
	after, err := strconv.ParseUint(c.DefaultQuery("after_id", "0"), 10, 64)
	if err != nil {
		demandRespond(c, nil, &service.DemandError{Status: 400, Code: "DEMAND_CURSOR_INVALID", Message: "Invalid demand cursor"})
		return
	}
	limit, err := demandLimit(c, 100, 200)
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	items := []model.DemandAudit{}
	err = model.DB.Where("demand_id = ? AND id > ?", c.Param("id"), after).Order("id ASC").Limit(limit + 1).Find(&items).Error
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	next := after
	if len(items) > 0 {
		next = items[len(items)-1].ID
	}
	demandRespond(c, gin.H{"items": items, "next_after_id": next, "has_more": more}, err)
}
func CreateDemandUpload(c *gin.Context) {
	var in service.DemandUploadCreate
	if !demandBind(c, &in) {
		return
	}
	v, err := service.CreateDemandUpload(model.DB, demandActor(c), in)
	demandRespond(c, v, err)
}
func GetDemandUpload(c *gin.Context) {
	v, err := service.GetDemandUpload(model.DB, demandActor(c), c.Param("upload_id"))
	demandRespond(c, v, err)
}
func PutDemandUploadChunk(c *gin.Context) {
	offset, err := strconv.ParseInt(c.GetHeader("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 || !strings.HasPrefix(c.GetHeader("Content-Type"), "application/octet-stream") {
		demandRespond(c, nil, &service.DemandError{Status: 400, Code: "DEMAND_UPLOAD_INVALID", Message: "Invalid demand attachment chunk"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (8<<20)+1)
	v, err := service.PutDemandUploadChunk(model.DB, demandActor(c), c.Param("upload_id"), offset, c.Request.Body)
	demandRespond(c, v, err)
}
func CompleteDemandUpload(c *gin.Context) {
	var in struct {
		SHA256 string `json:"sha256"`
	}
	if !demandBind(c, &in) {
		return
	}
	v, err := service.CompleteDemandUpload(model.DB, demandActor(c), c.Param("upload_id"), in.SHA256)
	demandRespond(c, v, err)
}
func GetDemandAttachmentLink(c *gin.Context) {
	var in struct {
		Disposition string `json:"disposition"`
	}
	if !demandBind(c, &in) {
		return
	}
	v, err := service.SignDemandAttachmentLink(model.DB, demandActor(c), c.Param("attachment_id"), in.Disposition)
	demandRespond(c, v, err)
}
func ServeDemandAttachment(c *gin.Context) {
	v, file, disposition, err := service.OpenDemandAttachment(model.DB, c.Param("attachment_id"), c.Query("token"))
	if err != nil {
		demandRespond(c, nil, err)
		return
	}
	defer file.Close()
	c.Header("Content-Type", v.MimeType)
	c.Header("Content-Disposition", disposition)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	http.ServeContent(c.Writer, c.Request, v.Name, time.Time{}, file)
}
