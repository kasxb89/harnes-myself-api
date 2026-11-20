package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net"
	"net/mail"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
)

type DemandEmailError struct {
	Status        int
	Code, Message string
}

func (e *DemandEmailError) Error() string { return e.Message }
func demandEmailError(status int, code, message string) error {
	return &DemandEmailError{status, "DEMAND_EMAIL_" + code, message}
}

type DemandEmailTypes struct {
	OfficialCreated  bool `json:"official_created"`
	CommunityPending bool `json:"community_pending"`
	CustomerMessage  bool `json:"customer_message"`
}
type DemandEmailConfig struct {
	Revision            int64            `json:"revision"`
	Enabled             bool             `json:"enabled"`
	Recipients          []string         `json:"recipients"`
	Types               DemandEmailTypes `json:"types"`
	MessageMergeSeconds int64            `json:"message_merge_seconds"`
	AdminBaseURL        string           `json:"admin_base_url"`
}

func DefaultDemandEmailConfig() DemandEmailConfig {
	return DemandEmailConfig{Recipients: []string{}, Types: DemandEmailTypes{true, true, true}, MessageMergeSeconds: 120}
}
func decodeDemandEmailConfig(row model.DemandEmailConfiguration) (DemandEmailConfig, error) {
	v := DefaultDemandEmailConfig()
	if row.ConfigJSON != "" {
		if err := common.Unmarshal([]byte(row.ConfigJSON), &v); err != nil {
			return v, err
		}
	}
	v.Revision = row.Revision
	if v.Recipients == nil {
		v.Recipients = []string{}
	}
	return v, nil
}
func LoadDemandEmailConfig(db *gorm.DB) (DemandEmailConfig, error) {
	var r model.DemandEmailConfiguration
	err := db.First(&r, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DefaultDemandEmailConfig(), nil
	}
	if err != nil {
		return DemandEmailConfig{}, err
	}
	return decodeDemandEmailConfig(r)
}
func lockedDemandEmailConfig(tx *gorm.DB) (DemandEmailConfig, error) {
	r, err := model.LockDemandEmailConfig(tx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DefaultDemandEmailConfig(), nil
	}
	if err != nil {
		return DemandEmailConfig{}, err
	}
	return decodeDemandEmailConfig(r)
}
func normalizeDemandEmailConfig(v DemandEmailConfig) (DemandEmailConfig, error) {
	invalid := demandEmailError(400, "INVALID_INPUT", "Invalid demand email settings")
	if v.Revision < 0 || v.MessageMergeSeconds < 30 || v.MessageMergeSeconds > 3600 || len(v.Recipients) > 100 {
		return v, invalid
	}
	recipients := []string{}
	seen := map[string]bool{}
	for _, raw := range v.Recipients {
		raw = strings.ToLower(strings.TrimSpace(raw))
		if raw == "" {
			continue
		}
		a, err := mail.ParseAddress(raw)
		if err != nil || len(raw) > 254 || a.Name != "" || a.Address != raw || strings.ContainsAny(raw, "\r\n\x00") {
			return v, invalid
		}
		if !seen[raw] {
			recipients = append(recipients, raw)
			seen[raw] = true
		}
	}
	if len(recipients) > 20 {
		return v, invalid
	}
	v.Recipients = recipients
	v.AdminBaseURL = strings.TrimRight(strings.TrimSpace(v.AdminBaseURL), "/")
	if v.AdminBaseURL != "" {
		u, err := url.Parse(v.AdminBaseURL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.Opaque != "" || strings.ContainsAny(v.AdminBaseURL, "?#\r\n\x00") || path.Clean(u.Path) != u.Path && u.Path != "" || strings.ContainsAny(u.Path, "\\\r\n\x00") {
			return v, invalid
		}
		ip := net.ParseIP(u.Hostname())
		loopback := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
		if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
			return v, invalid
		}
	}
	if v.Enabled && (len(v.Recipients) == 0 || v.AdminBaseURL == "") {
		return v, invalid
	}
	return v, nil
}
func SaveDemandEmailConfig(db *gorm.DB, actorID int, in DemandEmailConfig) (DemandEmailConfig, error) {
	var err error
	in, err = normalizeDemandEmailConfig(in)
	if err != nil {
		return in, err
	}
	if actorID < 1 {
		return in, demandEmailError(400, "INVALID_INPUT", "Invalid demand email settings")
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		current, e := lockedDemandEmailConfig(tx)
		if e != nil {
			return e
		}
		if current.Revision != in.Revision {
			return demandEmailError(409, "REVISION_CONFLICT", "Demand email settings changed. Reload and try again")
		}
		stored := in
		stored.Revision++
		raw, e := common.Marshal(stored)
		if e != nil {
			return e
		}
		row := model.DemandEmailConfiguration{ID: 1, Revision: stored.Revision, ConfigJSON: string(raw), UpdatedBy: actorID, UpdatedAt: time.Now().Unix()}
		var res *gorm.DB
		if in.Revision == 0 {
			res = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		} else {
			res = tx.Model(&row).Where("id = ? AND revision = ?", 1, in.Revision).Updates(map[string]any{"revision": row.Revision, "config_json": row.ConfigJSON, "updated_by": actorID, "updated_at": row.UpdatedAt})
		}
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return demandEmailError(409, "REVISION_CONFLICT", "Demand email settings changed. Reload and try again")
		}
		// Only not-yet-sending work is paused here. In-flight SMTP is rechecked before DATA.
		var rows []model.DemandEmailDelivery
		if e = tx.Where("status IN ?", []string{"pending", "leased"}).Find(&rows).Error; e != nil {
			return e
		}
		for _, d := range rows {
			reason := demandEmailPauseReason(in, d)
			if reason == "" {
				continue
			}
			if e = tx.Model(&model.DemandEmailDelivery{}).Where("id = ? AND status IN ?", d.ID, []string{"pending", "leased"}).Updates(map[string]any{"status": "paused", "pause_reason": reason, "updated_at": row.UpdatedAt}).Error; e != nil {
				return e
			}
		}
		return nil
	})
	if err == nil {
		in.Revision++
	}
	return in, err
}
func demandEmailPauseReason(cfg DemandEmailConfig, d model.DemandEmailDelivery) string {
	if !slices.Contains(cfg.Recipients, d.Recipient) {
		return "recipient_removed"
	}
	if d.Type == "test" {
		return ""
	}
	if !cfg.Enabled {
		return "disabled"
	}
	switch d.Type {
	case "official_created":
		if !cfg.Types.OfficialCreated {
			return "type_disabled"
		}
	case "community_pending":
		if !cfg.Types.CommunityPending {
			return "type_disabled"
		}
	case "customer_message":
		if !cfg.Types.CustomerMessage {
			return "type_disabled"
		}
	default:
		return "type_disabled"
	}
	return ""
}
func DemandEmailSMTPReady() bool {
	c := common.SnapshotDemandSMTPConfig()
	a, e := mail.ParseAddress(c.From)
	return e == nil && a.Address != "" && c.Server != "" && c.Port > 0 && c.Port <= 65535 && !strings.ContainsAny(c.Server, "\r\n\x00")
}
func demandEmailKey(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func DemandEmailDetailURL(cfg DemandEmailConfig, domain, id string) string {
	if cfg.AdminBaseURL == "" || id == "" || domain == "test" {
		return ""
	}
	q := url.Values{}
	if domain == "official" {
		q.Set("tab", "demands")
		q.Set("demandId", id)
	} else if domain == "community" {
		q.Set("tab", "community")
		q.Set("orderId", id)
	} else {
		return ""
	}
	return cfg.AdminBaseURL + "/demand-management?" + q.Encode()
}
