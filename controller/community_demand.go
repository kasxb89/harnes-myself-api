package controller

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func CommunitySessionOnly(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		communityRespond(c, nil, &service.CommunityError{Status: 403, Code: "COMMUNITY_FORBIDDEN", Message: "A product login session is required"})
		c.Abort()
		return
	}
	c.Next()
}
func CommunityAdminScope(c *gin.Context) { c.Set("community_admin", true); c.Next() }
func communityActor(c *gin.Context) service.CommunityActor {
	return service.CommunityActor{UserID: c.GetInt("id"), Admin: c.GetBool("community_admin")}
}
func communityDB(c *gin.Context) *gorm.DB { return model.DB.WithContext(c.Request.Context()) }
func communityRespond(c *gin.Context, data any, err error) {
	if err == nil {
		common.ApiSuccess(c, data)
		return
	}
	var problem *service.CommunityError
	if !errors.As(err, &problem) {
		problem = &service.CommunityError{Status: 503, Code: "COMMUNITY_STORAGE_UNAVAILABLE", Message: "Community orders are temporarily unavailable"}
	}
	c.JSON(problem.Status, gin.H{"success": false, "code": problem.Code, "message": problem.Message, "request_id": c.GetString(common.RequestIdKey)})
}
func communityInvalid(c *gin.Context) {
	communityRespond(c, nil, &service.CommunityError{Status: 400, Code: "COMMUNITY_INVALID_INPUT", Message: "Invalid community request"})
}
func communityBind(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	if common.DecodeJson(c.Request.Body, v) != nil {
		communityInvalid(c)
		return false
	}
	return true
}
func GetCommunityConfig(c *gin.Context) {
	cfg, err := service.LoadCommunityConfig(communityDB(c))
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	shared, err := service.LoadDemandConfig(communityDB(c))
	shared.Config.UploadRules.MaxAttachments = min(8, shared.Config.UploadRules.MaxAttachments)
	communityRespond(c, gin.H{"schema_version": 1, "revision": cfg.Revision, "enabled": cfg.Enabled, "capabilities": gin.H{"community_orders_v1": true, "explicit_publication": true, "single_acceptor": true, "resumable_upload": true, "private_participant_messages": true}, "upload_rules": shared.Config.UploadRules}, err)
}
func PutCommunityConfig(c *gin.Context) {
	var in service.CommunityConfig
	if !communityBind(c, &in) {
		return
	}
	v, err := service.SaveCommunityConfig(communityDB(c), in)
	communityRespond(c, v, err)
}

type communityCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

func communityPage(c *gin.Context) (int, communityCursor, error) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "30"))
	cur := communityCursor{}
	bad := &service.CommunityError{Status: 400, Code: "COMMUNITY_CURSOR_INVALID", Message: "Invalid community page"}
	if err != nil || limit < 1 || limit > 100 {
		return 0, cur, bad
	}
	if raw := c.Query("cursor"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if len(raw) > 256 || e != nil || common.Unmarshal(b, &cur) != nil || cur.At < 1 || cur.ID == "" || len(cur.ID) > 40 {
			return 0, cur, bad
		}
	}
	return limit, cur, nil
}
func communityNext(o model.CommunityOrder) string {
	b, _ := common.Marshal(communityCursor{o.CreatedAt, o.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}
func ListCommunityOrders(c *gin.Context) {
	db := communityDB(c)
	a := communityActor(c)
	limit, cur, err := communityPage(c)
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	listings := c.GetBool("community_listings")
	q := db.Model(&model.CommunityOrder{})
	if listings {
		cfg, e := service.LoadCommunityConfig(db)
		if e != nil {
			communityRespond(c, nil, e)
			return
		}
		if !cfg.Enabled {
			communityRespond(c, gin.H{"items": []service.CommunityListingView{}, "next_cursor": "", "has_more": false}, nil)
			return
		}
		q = q.Where("visible = ? AND status = ? AND acceptor_id = 0", true, "open")
	} else if !a.Admin {
		switch c.DefaultQuery("view", "all") {
		case "published":
			q = q.Where("publisher_id = ?", a.UserID)
		case "accepted":
			q = q.Where("acceptor_id = ?", a.UserID)
		case "all":
			q = q.Where("(publisher_id = ? OR acceptor_id = ?)", a.UserID, a.UserID)
		default:
			communityInvalid(c)
			return
		}
	}
	if status := c.Query("status"); status != "" {
		if !service.CommunityStatusValid(status) {
			communityInvalid(c)
			return
		}
		q = q.Where("status = ?", status)
	}
	if profile := c.Query("service_profile_id"); profile != "" {
		if len(profile) > 40 {
			communityInvalid(c)
			return
		}
		q = q.Where("service_profile_id = ?", profile)
	}
	if text := strings.TrimSpace(c.Query("q")); text != "" {
		if utf8.RuneCountInString(text) > 100 {
			communityInvalid(c)
			return
		}
		q = q.Where("(title LIKE ? OR public_summary LIKE ?)", "%"+text+"%", "%"+text+"%")
	}
	if a.Admin {
		for _, field := range []string{"publisher_id", "acceptor_id"} {
			if raw := c.Query(field); raw != "" {
				id, e := strconv.Atoi(raw)
				if e != nil || id < 1 {
					communityInvalid(c)
					return
				}
				q = q.Where(field+" = ?", id)
			}
		}
		switch c.DefaultQuery("visibility", "all") {
		case "all":
		case "published":
			q = q.Where("visible = ?", true)
		case "hidden":
			q = q.Where("visible = ?", false)
		default:
			communityInvalid(c)
			return
		}
	}
	if cur.ID != "" {
		q = q.Where("created_at < ? OR (created_at = ? AND id < ?)", cur.At, cur.At, cur.ID)
	}
	var orders []model.CommunityOrder
	if err = q.Order("created_at DESC,id DESC").Limit(limit + 1).Find(&orders).Error; err != nil {
		communityRespond(c, nil, err)
		return
	}
	more := len(orders) > limit
	if more {
		orders = orders[:limit]
	}
	next := ""
	if more {
		next = communityNext(orders[len(orders)-1])
	}
	if listings {
		items, e := service.ProjectCommunityListings(db, a, orders)
		communityRespond(c, gin.H{"items": items, "next_cursor": next, "has_more": more}, e)
	} else {
		items, e := service.ProjectCommunityOrders(db, a, orders)
		communityRespond(c, gin.H{"items": items, "next_cursor": next, "has_more": more}, e)
	}
}
func GetCommunityListing(c *gin.Context) {
	db := communityDB(c)
	a := communityActor(c)
	o, err := service.CommunityListingAccess(db, a, c.Param("id"))
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	v, err := service.ProjectCommunityListings(db, a, []model.CommunityOrder{o})
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	communityRespond(c, gin.H{"listing": v[0]}, nil)
}
func GetCommunityOrder(c *gin.Context) {
	db := communityDB(c)
	a := communityActor(c)
	o, err := service.CommunityOrderAccess(db, a, c.Param("id"))
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	v, err := service.ProjectCommunityOrders(db, a, []model.CommunityOrder{o})
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	communityRespond(c, gin.H{"order": v[0]}, nil)
}
func communityReceipt(c *gin.Context, o model.CommunityOrder, m *model.CommunityMessage, replay, created bool, err error) {
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	db := communityDB(c)
	a := communityActor(c)
	orders, err := service.ProjectCommunityOrders(db, a, []model.CommunityOrder{o})
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	var message *service.CommunityMessageView
	if m != nil {
		views, e := service.ProjectCommunityMessages(db, a, o, []model.CommunityMessage{*m})
		if e != nil {
			communityRespond(c, nil, e)
			return
		}
		message = &views[0]
	}
	data := gin.H{"order": orders[0], "message": message, "replayed": replay}
	if created && !replay {
		c.JSON(201, gin.H{"success": true, "data": data})
		return
	}
	communityRespond(c, data, nil)
}
func CreateCommunityOrder(c *gin.Context) {
	var in service.CommunityCreate
	if !communityBind(c, &in) {
		return
	}
	o, m, r, err := service.CreateCommunityOrder(communityDB(c), communityActor(c), in)
	communityReceipt(c, o, m, r, true, err)
}
func AcceptCommunityOrder(c *gin.Context) {
	var in struct {
		ClientActionID string `json:"client_action_id"`
	}
	if !communityBind(c, &in) {
		return
	}
	o, m, r, err := service.AcceptCommunityOrder(communityDB(c), communityActor(c), c.Param("id"), in.ClientActionID)
	communityReceipt(c, o, m, r, false, err)
}
func SendCommunityMessage(c *gin.Context) {
	var in service.CommunitySend
	if !communityBind(c, &in) {
		return
	}
	o, m, r, err := service.SendCommunityMessage(communityDB(c), communityActor(c), c.Param("id"), in)
	communityReceipt(c, o, &m, r, true, err)
}
func PostCommunityAction(c *gin.Context) {
	var in service.CommunityAction
	if !communityBind(c, &in) {
		return
	}
	o, m, r, err := service.ActCommunityOrder(communityDB(c), communityActor(c), c.Param("id"), in)
	communityReceipt(c, o, m, r, false, err)
}
func PostCommunityRead(c *gin.Context) {
	var in struct {
		LastReadSeq int64 `json:"last_read_seq"`
	}
	if !communityBind(c, &in) {
		return
	}
	v, err := service.MarkCommunityRead(communityDB(c), communityActor(c), c.Param("id"), in.LastReadSeq)
	communityRespond(c, v, err)
}
func GetCommunityMessages(c *gin.Context) {
	db := communityDB(c)
	a := communityActor(c)
	o, err := service.CommunityOrderAccess(db, a, c.Param("id"))
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	after, err := strconv.ParseInt(c.DefaultQuery("after_seq", "0"), 10, 64)
	if err != nil || after < 0 {
		communityInvalid(c)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 100 {
		communityInvalid(c)
		return
	}
	var rows []model.CommunityMessage
	if err = db.Where("order_id = ? AND seq > ?", o.ID, after).Order("seq ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		communityRespond(c, nil, err)
		return
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	next := after
	if len(rows) > 0 {
		next = rows[len(rows)-1].Seq
	}
	items, err := service.ProjectCommunityMessages(db, a, o, rows)
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	orders, err := service.ProjectCommunityOrders(db, a, []model.CommunityOrder{o})
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	communityRespond(c, gin.H{"items": items, "last_seq": o.LastSeq, "next_after_seq": next, "has_more": more, "order": orders[0]}, nil)
}
func GetCommunityAudit(c *gin.Context) {
	db := communityDB(c)
	if _, err := service.CommunityOrderAccess(db, communityActor(c), c.Param("id")); err != nil {
		communityRespond(c, nil, err)
		return
	}
	after, err := strconv.ParseUint(c.DefaultQuery("after_id", "0"), 10, 64)
	if err != nil {
		communityInvalid(c)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 100 {
		communityInvalid(c)
		return
	}
	items := []model.CommunityAudit{}
	err = db.Where("order_id = ? AND id > ?", c.Param("id"), after).Order("id ASC").Limit(limit + 1).Find(&items).Error
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	next := after
	if len(items) > 0 {
		next = items[len(items)-1].ID
	}
	communityRespond(c, gin.H{"items": items, "next_after_id": next, "has_more": more}, err)
}
func communityUploadReply(c *gin.Context, v service.CommunityUploadResult, err error) {
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	f := service.ProjectCommunityAttachment(v.Attachment)
	var ready *service.CommunityAttachmentView
	if f.Status == "ready" {
		ready = &f
	}
	communityRespond(c, gin.H{"id": f.ID, "attachment_id": f.ID, "offset": v.Offset, "chunk_bytes": v.ChunkBytes, "status": f.Status, "expires_at": v.ExpiresAt, "attachment": ready}, nil)
}
func CreateCommunityUpload(c *gin.Context) {
	var in service.CommunityUploadCreate
	if !communityBind(c, &in) {
		return
	}
	v, err := service.CreateCommunityUpload(communityDB(c), communityActor(c), in)
	communityUploadReply(c, v, err)
}
func GetCommunityUpload(c *gin.Context) {
	v, err := service.GetCommunityUpload(communityDB(c), communityActor(c), c.Param("upload_id"))
	communityUploadReply(c, v, err)
}
func PutCommunityUploadChunk(c *gin.Context) {
	offset, err := strconv.ParseInt(c.GetHeader("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 || !strings.HasPrefix(c.GetHeader("Content-Type"), "application/octet-stream") {
		communityInvalid(c)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (8<<20)+1)
	v, err := service.PutCommunityUploadChunk(communityDB(c), communityActor(c), c.Param("upload_id"), offset, c.Request.Body)
	var conflict *service.CommunityError
	if errors.As(err, &conflict) && conflict.Code == "COMMUNITY_UPLOAD_OFFSET_CONFLICT" {
		current, readErr := service.GetCommunityUpload(communityDB(c), communityActor(c), c.Param("upload_id"))
		if readErr != nil {
			communityRespond(c, nil, readErr)
			return
		}
		c.JSON(conflict.Status, gin.H{"success": false, "code": conflict.Code, "message": conflict.Message, "request_id": c.GetString(common.RequestIdKey), "offset": current.Offset})
		return
	}
	communityUploadReply(c, v, err)
}
func CompleteCommunityUpload(c *gin.Context) {
	var in struct {
		SHA256 string `json:"sha256"`
	}
	if !communityBind(c, &in) {
		return
	}
	v, err := service.CompleteCommunityUpload(communityDB(c), communityActor(c), c.Param("upload_id"), in.SHA256)
	communityUploadReply(c, v, err)
}
func GetCommunityAttachmentLink(c *gin.Context) {
	var in struct {
		Disposition string `json:"disposition"`
	}
	if !communityBind(c, &in) {
		return
	}
	v, err := service.SignCommunityAttachmentLink(communityDB(c), communityActor(c), c.Param("attachment_id"), in.Disposition)
	communityRespond(c, v, err)
}
func ServeCommunityAttachment(c *gin.Context) {
	v, f, disposition, err := service.OpenCommunityAttachment(communityDB(c), c.Param("attachment_id"), c.Query("token"))
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		communityRespond(c, nil, err)
		return
	}
	c.Header("Content-Type", v.MimeType)
	c.Header("Content-Disposition", disposition)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("ETag", `"`+v.SHA256+`"`)
	http.ServeContent(c.Writer, c.Request, v.Name, info.ModTime(), f)
}
