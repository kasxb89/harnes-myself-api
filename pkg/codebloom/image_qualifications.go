package codebloom

import (
	_ "embed"
	"fmt"
	"regexp"

	"github.com/QuantumNous/new-api/common"
)

// Live qualification is independent of the historical price snapshot. A known
// price, a supplier model-list entry or a mocked HTTP response cannot activate it.
//
//go:embed image_qualifications.json
var imageQualificationData []byte

type imageQualification struct {
	ID                     string `json:"id"`
	UpstreamID             string `json:"upstream_id"`
	CheckedAt              string `json:"checked_at"`
	Generation             string `json:"generation"`
	BillableUsage          string `json:"billable_usage"`
	ImageDecoded           bool   `json:"image_decoded"`
	RequestSHA256          string `json:"request_sha256"`
	ResponseSHA256         string `json:"response_sha256"`
	ImageSHA256            string `json:"image_sha256"`
	MeterEvidenceSHA256    string `json:"meter_evidence_sha256"`
	RetainedProtocolSHA256 string `json:"retained_protocol_sha256"`
	Width                  int    `json:"width"`
	Height                 int    `json:"height"`
	EditRequestSHA256      string `json:"edit_request_sha256,omitempty"`
	EditResponseSHA256     string `json:"edit_response_sha256,omitempty"`
	EditImageSHA256        string `json:"edit_image_sha256,omitempty"`
}

type imageQualificationSnapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	Models        []imageQualification `json:"models"`
}

var liveImages, imageQualificationError = loadImageQualifications(imageQualificationData)

func loadImageQualifications(data []byte) (map[string]imageQualification, error) {
	var snapshot imageQualificationSnapshot
	if err := common.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.SchemaVersion != 1 || snapshot.Models == nil || len(snapshot.Models) > 11 {
		return nil, fmt.Errorf("invalid live image qualification schema")
	}
	qualified := map[string]imageQualification{}
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, proof := range snapshot.Models {
		var candidate *Model
		for i := range approved.Models {
			if approved.Models[i].ID == proof.ID {
				candidate = &approved.Models[i]
				break
			}
		}
		if candidate == nil || candidate.Kind != "image" || proof.UpstreamID != candidate.UpstreamID || proof.CheckedAt == "" || qualified[proof.ID].ID != "" || proof.Generation != "verified" || proof.BillableUsage != "verified" || !proof.ImageDecoded || proof.Width <= 0 || proof.Height <= 0 {
			return nil, fmt.Errorf("incomplete exact-route image qualification")
		}
		for _, value := range []string{proof.RequestSHA256, proof.ResponseSHA256, proof.ImageSHA256, proof.MeterEvidenceSHA256, proof.RetainedProtocolSHA256} {
			if !hex.MatchString(value) {
				return nil, fmt.Errorf("missing image qualification evidence digest")
			}
		}
		if proof.EditRequestSHA256 != "" || proof.EditResponseSHA256 != "" || proof.EditImageSHA256 != "" {
			for _, value := range []string{proof.EditRequestSHA256, proof.EditResponseSHA256, proof.EditImageSHA256} {
				if !hex.MatchString(value) {
					return nil, fmt.Errorf("incomplete image edit qualification")
				}
			}
		}
		if candidate.Transport == "images_generations" && (proof.Width != 1024 || proof.Height != 1024) {
			return nil, fmt.Errorf("image response differs from the verified request size")
		}
		qualified[proof.ID] = proof
	}
	return qualified, nil
}

// ImageEditModels returns only exact routes whose reference input produced a
// real decoded image. The returned identifiers carry no price or credentials.
func ImageEditModels() []string {
	var ids []string
	if imageQualificationError != nil {
		return ids
	}
	for _, candidate := range approved.Models {
		if proof, ok := liveImages[candidate.ID]; ok && proof.EditImageSHA256 != "" {
			ids = append(ids, candidate.ID)
		}
	}
	return ids
}

func withImageQualification(m Model) Model {
	if imageQualificationError == nil {
		if _, ok := liveImages[m.ID]; ok {
			m.ActivationReady = true
			m.PendingReasons = []string{"cash_procurement_conversion_unknown"}
			m.CapabilityStatus["complete_generation_contract"] = "verified"
			m.CapabilityStatus["billable_usage"] = "verified"
			if liveImages[m.ID].EditImageSHA256 != "" {
				m.CapabilityStatus["image_to_image"] = "verified"
			}
		}
	}
	return m
}
