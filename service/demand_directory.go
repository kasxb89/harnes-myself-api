package service

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var demandProfileCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

type DemandServiceProfileView struct {
	model.DemandServiceProfile
	CategoryIDs []string `json:"category_ids"`
	IntakeMode  string   `json:"intake_mode"`
	CanStart    bool     `json:"can_start"`
}

func ProjectDemandServiceProfile(p model.DemandServiceProfile) DemandServiceProfileView {
	categories := []string{}
	_ = common.Unmarshal([]byte(p.CategoryJSON), &categories)
	if categories == nil {
		categories = []string{}
	}
	return DemandServiceProfileView{p, categories, "platform", p.Enabled && p.Published}
}

type DemandCustomerProfileView struct {
	model.DemandCustomerProfile
	UserID int `json:"user_id"`
}

func ProjectDemandCustomerProfile(p model.DemandCustomerProfile) DemandCustomerProfileView {
	v := DemandCustomerProfileView{DemandCustomerProfile: p}
	if p.UserID != nil {
		v.UserID = *p.UserID
	}
	return v
}

// Shared avatar validation prevents message-bound private media becoming a directory image.
func validateDemandProfileAvatar(db *gorm.DB, id, scope string) error {
	if id == "" {
		return nil
	}
	// Visibility can change later, so opposing references include unpublished/disabled profiles.
	tables := []any{&model.DemandCustomerProfile{}}
	if scope == "customer" {
		tables = []any{&model.DemandServiceProfile{}, &model.DemandEngineer{}}
	}
	for _, table := range tables {
		var count int64
		if err := db.Model(table).Where("avatar_attachment_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return demandError(400, "UPLOAD_INVALID", "Customer and public profiles require separate avatar uploads")
		}
	}
	res := db.Model(&model.DemandAttachment{}).Where("id = ? AND status = ? AND kind = ? AND demand_id = ? AND message_id = ? AND (avatar_scope = ? OR avatar_scope = ? OR avatar_scope IS NULL)", id, "ready", "image", "", "", scope, "").Updates(map[string]any{"avatar_reserved": true, "avatar_scope": scope})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var count int64
		if err := db.Model(&model.DemandAttachment{}).Where("id = ? AND status = ? AND kind = ? AND demand_id = ? AND message_id = ? AND avatar_scope = ?", id, "ready", "image", "", "", scope).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return demandError(400, "UPLOAD_INVALID", "Customer and public profiles require separate avatar uploads")
		}
	}
	return nil
}
func SaveDemandServiceProfile(db *gorm.DB, id string, in DemandServiceProfileView) (DemandServiceProfileView, error) {
	if !demandProfileCode.MatchString(in.Code) || strings.TrimSpace(in.DisplayName) == "" || utf8.RuneCountInString(in.DisplayName) > 100 || strings.TrimSpace(in.ServiceDirection) == "" || utf8.RuneCountInString(in.ServiceDirection) > 200 || utf8.RuneCountInString(in.Introduction) > 2000 || len(in.CategoryIDs) > 30 || in.Sort < -100000 || in.Sort > 100000 {
		return in, demandError(400, "INVALID_INPUT", "Invalid service profile")
	}
	cfg, err := LoadDemandConfig(db)
	if err != nil {
		return in, err
	}
	for _, cat := range in.CategoryIDs {
		if !slices.ContainsFunc(cfg.Config.Categories, func(c DemandCategory) bool { return c.ID == cat }) {
			return in, demandError(400, "INVALID_INPUT", "Invalid demand category")
		}
	}
	if err = validateDemandProfileAvatar(db, in.AvatarAttachmentID, "public"); err != nil {
		return in, err
	}
	p := in.DemandServiceProfile
	raw, _ := common.Marshal(in.CategoryIDs)
	p.CategoryJSON = string(raw)
	p.ID = id
	p.Revision = in.Revision + 1
	if id == "" {
		if in.Revision != 0 {
			return in, demandError(400, "INVALID_INPUT", "Invalid profile revision")
		}
		p.ID = uuid.NewString()
		var count int64
		if err = db.Model(&p).Where("code = ?", p.Code).Count(&count).Error; err != nil {
			return in, err
		}
		if count != 0 {
			return in, demandError(409, "REVISION_CONFLICT", "Profile code already exists")
		}
		err = db.Create(&p).Error
	} else {
		var old model.DemandServiceProfile
		if db.First(&old, "id = ?", id).Error != nil {
			return in, demandError(404, "NOT_FOUND", "Service profile not found")
		}
		if old.Code != p.Code {
			return in, demandError(400, "INVALID_INPUT", "Profile code cannot be replaced")
		}
		res := db.Model(&p).Where("id = ? AND revision = ?", id, in.Revision).Updates(map[string]any{"display_name": p.DisplayName, "service_direction": p.ServiceDirection, "introduction": p.Introduction, "category_json": p.CategoryJSON, "avatar_attachment_id": p.AvatarAttachmentID, "published": p.Published, "enabled": p.Enabled, "sort": p.Sort, "revision": p.Revision})
		err = res.Error
		if err == nil && res.RowsAffected != 1 {
			err = demandError(409, "REVISION_CONFLICT", "Profile changed; reload before saving")
		}
	}
	return ProjectDemandServiceProfile(p), err
}
func SaveDemandCustomerProfile(db *gorm.DB, id string, in DemandCustomerProfileView) (DemandCustomerProfileView, error) {
	if !demandProfileCode.MatchString(in.Code) || strings.TrimSpace(in.DisplayName) == "" || utf8.RuneCountInString(in.DisplayName) > 100 || utf8.RuneCountInString(in.Organization) > 200 || utf8.RuneCountInString(in.Introduction) > 2000 || utf8.RuneCountInString(in.Notes) > 4000 || in.UserID < 0 {
		return in, demandError(400, "INVALID_INPUT", "Invalid customer profile")
	}
	if err := validateDemandProfileAvatar(db, in.AvatarAttachmentID, "customer"); err != nil {
		return in, err
	}
	p := in.DemandCustomerProfile
	p.ID = id
	p.Revision = in.Revision + 1
	p.UserID = nil
	if in.UserID > 0 {
		var u model.User
		if db.Select("id", "status", "role").First(&u, in.UserID).Error != nil || u.Status != common.UserStatusEnabled || u.Role != common.RoleCommonUser {
			return in, demandError(400, "INVALID_INPUT", "Customer must be an enabled ordinary user")
		}
		p.UserID = &in.UserID
		var count int64
		if err := db.Model(&p).Where("user_id = ? AND id <> ?", in.UserID, id).Count(&count).Error; err != nil {
			return in, err
		}
		if count != 0 {
			return in, demandError(409, "USER_ALREADY_LINKED", "User is already linked to a customer profile")
		}
	}
	var err error
	if id == "" {
		if in.Revision != 0 {
			return in, demandError(400, "INVALID_INPUT", "Invalid profile revision")
		}
		p.ID = uuid.NewString()
		var count int64
		if err = db.Model(&p).Where("code = ?", p.Code).Count(&count).Error; err != nil {
			return in, err
		}
		if count != 0 {
			return in, demandError(409, "REVISION_CONFLICT", "Profile code already exists")
		}
		err = db.Create(&p).Error
	} else {
		var old model.DemandCustomerProfile
		if db.First(&old, "id = ?", id).Error != nil {
			return in, demandError(404, "NOT_FOUND", "Customer profile not found")
		}
		if old.Code != p.Code {
			return in, demandError(400, "INVALID_INPUT", "Profile code cannot be replaced")
		}
		res := db.Model(&p).Where("id = ? AND revision = ?", id, in.Revision).Updates(map[string]any{"display_name": p.DisplayName, "organization": p.Organization, "introduction": p.Introduction, "notes": p.Notes, "avatar_attachment_id": p.AvatarAttachmentID, "user_id": p.UserID, "enabled": p.Enabled, "revision": p.Revision})
		err = res.Error
		if err == nil && res.RowsAffected != 1 {
			err = demandError(409, "REVISION_CONFLICT", "Profile changed; reload before saving")
		}
	}
	// A database uniqueness constraint also protects simultaneous real-user linking.
	if err != nil && p.UserID != nil {
		var count int64
		if db.Model(&p).Where("user_id = ? AND id <> ?", in.UserID, p.ID).Count(&count).Error == nil && count > 0 {
			err = demandError(409, "USER_ALREADY_LINKED", "User is already linked to a customer profile")
		}
	}
	return ProjectDemandCustomerProfile(p), err
}

type DemandProfileImportInput struct {
	BatchCode        string                      `json:"batch_code"`
	ServiceProfiles  []DemandServiceProfileView  `json:"service_profiles"`
	CustomerProfiles []DemandCustomerProfileView `json:"customer_profiles"`
}
type DemandProfileImportResult struct {
	model.DemandProfileImport
	Replayed bool `json:"replayed"`
}

func ImportDemandProfiles(db *gorm.DB, in DemandProfileImportInput) (DemandProfileImportResult, error) {
	var out DemandProfileImportResult
	if !demandProfileCode.MatchString(in.BatchCode) || len(in.ServiceProfiles) > 100 || len(in.CustomerProfiles) > 100 || len(in.ServiceProfiles)+len(in.CustomerProfiles) == 0 {
		return out, demandError(400, "INVALID_INPUT", "Invalid profile import")
	}
	return importDemandProfiles(db, in, demandHash(in))
}

func importDemandProfiles(db *gorm.DB, in DemandProfileImportInput, hash string) (DemandProfileImportResult, error) {
	var out DemandProfileImportResult
	err := db.Transaction(func(tx *gorm.DB) error {
		var receipt model.DemandProfileImport
		err := tx.First(&receipt, "batch_code = ?", in.BatchCode).Error
		if err == nil {
			if receipt.PayloadHash != hash {
				return demandError(409, "IDEMPOTENCY_CONFLICT", "Import batch was already used for different content")
			}
			out = DemandProfileImportResult{receipt, true}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		receipt = model.DemandProfileImport{BatchCode: in.BatchCode, PayloadHash: hash}
		if err = tx.Create(&receipt).Error; err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, p := range in.ServiceProfiles {
			if !demandProfileCode.MatchString(p.Code) || seen["s:"+p.Code] {
				return demandError(400, "INVALID_INPUT", "Duplicate or invalid profile code")
			}
			seen["s:"+p.Code] = true
			var count int64
			if err = tx.Model(&model.DemandServiceProfile{}).Where("code = ?", p.Code).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				receipt.SkippedServiceProfiles++
				continue
			}
			if _, err = SaveDemandServiceProfile(tx, "", p); err != nil {
				return err
			}
			receipt.CreatedServiceProfiles++
		}
		for _, p := range in.CustomerProfiles {
			if !demandProfileCode.MatchString(p.Code) || seen["c:"+p.Code] {
				return demandError(400, "INVALID_INPUT", "Duplicate or invalid profile code")
			}
			seen["c:"+p.Code] = true
			var count int64
			if err = tx.Model(&model.DemandCustomerProfile{}).Where("code = ?", p.Code).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				receipt.SkippedCustomerProfiles++
				continue
			}
			if _, err = SaveDemandCustomerProfile(tx, "", p); err != nil {
				return err
			}
			receipt.CreatedCustomerProfiles++
		}
		if err = tx.Model(&receipt).Select("created_service_profiles", "created_customer_profiles", "skipped_service_profiles", "skipped_customer_profiles").Updates(&receipt).Error; err != nil {
			return err
		}
		out = DemandProfileImportResult{receipt, false}
		return nil
	})
	if err != nil {
		var receipt model.DemandProfileImport
		if db.First(&receipt, "batch_code = ?", in.BatchCode).Error == nil {
			if receipt.PayloadHash != hash {
				return out, demandError(409, "IDEMPOTENCY_CONFLICT", "Import batch was already used for different content")
			}
			return DemandProfileImportResult{receipt, true}, nil
		}
	}
	return out, err
}

// Profile references reserve an avatar against being rebound to private conversation media.
func demandAvatarReserved(db *gorm.DB, id string) (bool, error) {
	for _, table := range []any{&model.DemandEngineer{}, &model.DemandServiceProfile{}, &model.DemandCustomerProfile{}} {
		var count int64
		if err := db.Model(table).Where("avatar_attachment_id = ?", id).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

// Resolve the published intake entry inside the demand creation transaction.
func demandServiceIntake(tx *gorm.DB, id string) (model.DemandServiceProfile, error) {
	var p model.DemandServiceProfile
	if err := tx.First(&p, "id = ?", id).Error; err != nil {
		return p, demandError(409, "PROFILE_UNAVAILABLE", "Service profile is not accepting new demands")
	}
	if !p.Enabled || !p.Published {
		return p, demandError(409, "PROFILE_UNAVAILABLE", "Service profile is not accepting new demands")
	}
	return p, nil
}
