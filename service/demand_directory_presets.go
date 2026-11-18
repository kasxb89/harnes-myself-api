package service

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// These decorative profile avatars and internal draft records are imported only by an explicit administrator action.
//
//go:embed demand_presets/catalog.json demand_presets/avatars/*.png
var demandPresetFiles embed.FS

func ImportDemandPresetProfiles(db *gorm.DB, a DemandActor, batch string) (DemandProfileImportResult, error) {
	var out DemandProfileImportResult
	if !a.Admin || !demandProfileCode.MatchString(batch) {
		return out, demandError(400, "INVALID_INPUT", "Invalid profile import")
	}
	raw, err := demandPresetFiles.ReadFile("demand_presets/catalog.json")
	if err != nil {
		return out, err
	}
	hash := demandHash(struct {
		Batch   string
		Catalog string
	}{batch, string(raw)})
	var receipt model.DemandProfileImport
	err = db.First(&receipt, "batch_code = ?", batch).Error
	if err == nil {
		if receipt.PayloadHash != hash {
			return out, demandError(409, "IDEMPOTENCY_CONFLICT", "Import batch was already used for different content")
		}
		return DemandProfileImportResult{receipt, true}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	var catalog struct {
		Items []struct {
			Code         string   `json:"code"`
			Kind         string   `json:"kind"`
			DisplayName  string   `json:"display_name"`
			Introduction string   `json:"introduction"`
			Skills       []string `json:"skills"`
			Organization string   `json:"organization"`
			CategoryCode string   `json:"category_code"`
		} `json:"items"`
	}
	if err = common.Unmarshal(raw, &catalog); err != nil {
		return out, err
	}
	cfg, err := LoadDemandConfig(db)
	if err != nil {
		return out, err
	}
	in := DemandProfileImportInput{BatchCode: batch, ServiceProfiles: []DemandServiceProfileView{}, CustomerProfiles: []DemandCustomerProfileView{}}
	for _, item := range catalog.Items {
		// Existing codes are intentionally skipped before any avatar creation; edited records are never overwritten.
		var count int64
		var table any = &model.DemandServiceProfile{}
		if item.Kind == "customer" {
			table = &model.DemandCustomerProfile{}
		}
		if err = db.Model(table).Where("code = ?", item.Code).Count(&count).Error; err != nil {
			return out, err
		}
		avatar := ""
		if count == 0 {
			png, readErr := demandPresetFiles.ReadFile("demand_presets/avatars/" + item.Code + ".png")
			if readErr != nil {
				return out, readErr
			}
			sum := sha256.Sum256(png)
			uploadKey := demandHash([]string{"preset-avatar", batch, item.Code})
			upload, uploadErr := CreateDemandUpload(db, a, DemandUploadCreate{ClientUploadID: uploadKey, Name: item.Code + ".png", MimeType: "image/png", SizeBytes: int64(len(png))})
			if uploadErr != nil {
				return out, uploadErr
			}
			if upload.Attachment.Status != "ready" {
				if upload.Offset == 0 {
					upload, err = PutDemandUploadChunk(db, a, upload.Attachment.ID, 0, bytes.NewReader(png))
					if err != nil {
						return out, err
					}
				}
				upload, err = CompleteDemandUpload(db, a, upload.Attachment.ID, hex.EncodeToString(sum[:]))
				if err != nil {
					return out, err
				}
			}
			avatar = upload.Attachment.ID
		}
		if item.Kind == "service" {
			categories := []string{}
			if slices.ContainsFunc(cfg.Config.Categories, func(c DemandCategory) bool { return c.ID == item.CategoryCode }) {
				categories = append(categories, item.CategoryCode)
			}
			in.ServiceProfiles = append(in.ServiceProfiles, DemandServiceProfileView{DemandServiceProfile: model.DemandServiceProfile{Code: item.Code, DisplayName: item.DisplayName, ServiceDirection: strings.Join(item.Skills, "、"), Introduction: item.Introduction, AvatarAttachmentID: avatar, Enabled: true, Published: true}, CategoryIDs: categories})
		} else {
			in.CustomerProfiles = append(in.CustomerProfiles, DemandCustomerProfileView{DemandCustomerProfile: model.DemandCustomerProfile{Code: item.Code, DisplayName: item.DisplayName, Organization: item.Organization, Introduction: item.Introduction, AvatarAttachmentID: avatar, Enabled: true}})
		}
	}
	return importDemandProfiles(db, in, hash)
}
