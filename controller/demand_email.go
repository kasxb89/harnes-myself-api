package controller

import (
	"encoding/base64"
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"strconv"
	"time"
)

func demandEmailRespond(c *gin.Context, data any, err error) {
	if err == nil {
		common.ApiSuccess(c, data)
		return
	}
	var problem *service.DemandEmailError
	if !errors.As(err, &problem) {
		problem = &service.DemandEmailError{Status: 503, Code: "DEMAND_EMAIL_UNAVAILABLE", Message: "Demand email notifications are temporarily unavailable"}
	}
	c.JSON(problem.Status, gin.H{"success": false, "code": problem.Code, "message": problem.Message, "request_id": c.GetString(common.RequestIdKey)})
}
func demandEmailBind(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	if common.DecodeJson(c.Request.Body, v) != nil {
		demandEmailRespond(c, nil, &service.DemandEmailError{Status: 400, Code: "DEMAND_EMAIL_INVALID_INPUT", Message: "Invalid demand email settings"})
		return false
	}
	return true
}
func GetDemandEmailConfig(c *gin.Context) {
	v, err := service.LoadDemandEmailConfig(model.DB.WithContext(c.Request.Context()))
	demandEmailRespond(c, gin.H{"config": v, "smtp_ready": service.DemandEmailSMTPReady()}, err)
}
func SaveDemandEmailConfig(c *gin.Context) {
	var in service.DemandEmailConfig
	if !demandEmailBind(c, &in) {
		return
	}
	v, err := service.SaveDemandEmailConfig(model.DB.WithContext(c.Request.Context()), c.GetInt("id"), in)
	demandEmailRespond(c, gin.H{"config": v, "smtp_ready": service.DemandEmailSMTPReady()}, err)
}

type demandEmailDeliveryView struct {
	ID           string `json:"id"`
	Domain       string `json:"domain"`
	EntityID     string `json:"entity_id"`
	Type         string `json:"type"`
	Recipient    string `json:"recipient"`
	Status       string `json:"status"`
	Number       string `json:"number"`
	Title        string `json:"title"`
	EventCount   int64  `json:"event_count"`
	AttemptCount int    `json:"attempt_count"`
	DueAt        int64  `json:"due_at"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	SentAt       int64  `json:"sent_at"`
	ErrorCode    string `json:"error_code"`
	ErrorStage   string `json:"error_stage"`
	PauseReason  string `json:"pause_reason"`
	DetailURL    string `json:"detail_url"`
}
type demandEmailCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

func ListDemandEmailDeliveries(c *gin.Context) {
	invalid := &service.DemandEmailError{Status: 400, Code: "DEMAND_EMAIL_INVALID_INPUT", Message: "Invalid demand email settings"}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "30"))
	if err != nil || limit < 1 || limit > 100 {
		demandEmailRespond(c, nil, invalid)
		return
	}
	cursor := demandEmailCursor{}
	if raw := c.Query("cursor"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if len(raw) > 256 || e != nil || common.Unmarshal(b, &cursor) != nil || cursor.At < 0 || cursor.At > time.Now().Unix()+86400 {
			demandEmailRespond(c, nil, invalid)
			return
		}
		if _, e = uuid.Parse(cursor.ID); e != nil {
			demandEmailRespond(c, nil, invalid)
			return
		}
	}
	db := model.DB.WithContext(c.Request.Context())
	q := db.Model(&model.DemandEmailDelivery{})
	if status := c.Query("status"); status != "" {
		switch status {
		case "pending", "leased", "sending", "sent", "failed", "unknown", "paused":
			q = q.Where("status = ?", status)
		default:
			demandEmailRespond(c, nil, invalid)
			return
		}
	}
	if kind := c.Query("type"); kind != "" {
		switch kind {
		case "official_created", "community_pending", "customer_message", "test":
			q = q.Where("type = ?", kind)
		default:
			demandEmailRespond(c, nil, invalid)
			return
		}
	}
	if cursor.ID != "" {
		q = q.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.At, cursor.At, cursor.ID)
	}
	var rows []model.DemandEmailDelivery
	if err = q.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		demandEmailRespond(c, nil, err)
		return
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	next := ""
	if more {
		last := rows[len(rows)-1]
		b, _ := common.Marshal(demandEmailCursor{last.CreatedAt, last.ID})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	cfg, err := service.LoadDemandEmailConfig(db)
	if err != nil {
		demandEmailRespond(c, nil, err)
		return
	}
	views := make([]demandEmailDeliveryView, 0, len(rows))
	for _, d := range rows {
		views = append(views, demandEmailDeliveryView{d.ID, d.Domain, d.EntityID, d.Type, d.Recipient, d.Status, d.Number, d.Title, d.EventCount, d.AttemptCount, d.DueAt, d.CreatedAt, d.UpdatedAt, d.SentAt, d.ErrorCode, d.ErrorStage, d.PauseReason, service.DemandEmailDetailURL(cfg, d.Domain, d.EntityID)})
	}
	demandEmailRespond(c, gin.H{"items": views, "next_cursor": next, "has_more": more}, nil)
}
func CreateDemandEmailTest(c *gin.Context) {
	var in struct {
		ClientActionID string `json:"client_action_id"`
		Revision       *int64 `json:"revision"`
	}
	if !demandEmailBind(c, &in) {
		return
	}
	if in.Revision == nil {
		demandEmailRespond(c, nil, &service.DemandEmailError{Status: 400, Code: "DEMAND_EMAIL_INVALID_INPUT", Message: "Invalid demand email settings"})
		return
	}
	v, err := service.CreateDemandEmailTest(model.DB.WithContext(c.Request.Context()), c.GetInt("id"), in.ClientActionID, *in.Revision)
	demandEmailRespond(c, v, err)
}
func GetDemandEmailTestReceipt(c *gin.Context) {
	v, err := service.GetDemandEmailTestReceipt(model.DB.WithContext(c.Request.Context()), c.GetInt("id"), c.Param("client_action_id"))
	demandEmailRespond(c, v, err)
}
