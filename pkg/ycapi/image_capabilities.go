package ycapi

import (
	_ "embed"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Image ratios are request controls, separate from the immutable retail offers
// whose identifiers are retained in existing tasks and billing snapshots.
//
//go:embed image-capabilities.json
var imageCapabilityData []byte

type ImageRatioCapability struct {
	ModelID     string               `json:"model_id"`
	RatioField  string               `json:"ratio_field"`
	Ratios      []string             `json:"ratios"`
	OfferRatios map[string][]string  `json:"offer_ratios"`
	Edit        *ImageEditCapability `json:"edit,omitempty"`
}

// ImageEditCapability binds reference support to paid, decoded original-lane
// results. A model card or a dry-run alone never enables an edit offer.
type ImageEditCapability struct {
	MaxImages int               `json:"max_images"`
	MaxBytes  int               `json:"max_bytes"`
	Offers    map[string]string `json:"offers"` // original offer -> evidence SHA256
}

type imageCapabilitySnapshot struct {
	SchemaVersion int                    `json:"schema_version"`
	CheckedAt     string                 `json:"checked_at"`
	SourceURL     string                 `json:"source_url"`
	Models        []ImageRatioCapability `json:"models"`
}

var imageCapabilities, imageCapabilityError = loadImageCapabilities()

func loadImageCapabilities() (imageCapabilitySnapshot, error) {
	var snapshot imageCapabilitySnapshot
	if err := common.Unmarshal(imageCapabilityData, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.SchemaVersion != 1 || snapshot.CheckedAt == "" || snapshot.SourceURL == "" || len(snapshot.Models) != 8 {
		return snapshot, fmt.Errorf("invalid image capability snapshot")
	}
	seen := map[string]bool{}
	for _, capability := range snapshot.Models {
		m, ok := Lookup(capability.ModelID)
		if !ok || m.Kind != "image" || seen[m.ID] || !slices.Contains([]string{"size", "aspect_ratio"}, capability.RatioField) ||
			len(capability.Ratios) == 0 || len(capability.Ratios) > 32 || capability.Ratios[0] != "1:1" {
			return snapshot, fmt.Errorf("image capability and approved model differ")
		}
		seen[m.ID] = true
		ratios := map[string]bool{}
		for _, ratio := range capability.Ratios {
			if ratio == "" || len(ratio) > 16 || ratios[ratio] {
				return snapshot, fmt.Errorf("invalid image aspect ratio")
			}
			ratios[ratio] = true
		}
		for _, offer := range m.Offers {
			values := capability.OfferRatios[offer.ID]
			if offer.Params[capability.RatioField] != "1:1" || len(values) == 0 || values[0] != "1:1" {
				return snapshot, fmt.Errorf("image aspect ratio differs from the original offer")
			}
			seenOfferRatios := map[string]bool{}
			for _, value := range values {
				if !ratios[value] || seenOfferRatios[value] {
					return snapshot, fmt.Errorf("image offer aspect ratio is not supported")
				}
				seenOfferRatios[value] = true
			}
		}
		if len(capability.OfferRatios) != len(m.Offers) {
			return snapshot, fmt.Errorf("image capability offer count differs")
		}
		if edit := capability.Edit; edit != nil {
			if edit.MaxImages != 1 || edit.MaxBytes != 5*1024*1024 || len(edit.Offers) == 0 {
				return snapshot, fmt.Errorf("invalid verified image edit limits")
			}
			for offer, digest := range edit.Offers {
				if len(digest) != 64 || !slices.ContainsFunc(m.Offers, func(o Offer) bool { return o.ID == offer }) {
					return snapshot, fmt.Errorf("invalid image edit offer evidence")
				}
				for _, c := range digest {
					if !strings.ContainsRune("0123456789abcdef", c) {
						return snapshot, fmt.Errorf("invalid image edit evidence digest")
					}
				}
			}
		}
	}
	return snapshot, nil
}

// ImageRatios returns the reviewed upstream ratio field and supported values.
// The caller owns its returned slice; no procurement or price is exposed.
func ImageRatios(id string) (ImageRatioCapability, bool) {
	if imageCapabilityError != nil {
		return ImageRatioCapability{}, false
	}
	for _, capability := range imageCapabilities.Models {
		if capability.ModelID == id {
			capability.Ratios = slices.Clone(capability.Ratios)
			if capability.Edit != nil {
				edit := *capability.Edit
				edit.Offers = maps.Clone(edit.Offers)
				capability.Edit = &edit
			}
			offers := capability.OfferRatios
			capability.OfferRatios = make(map[string][]string, len(capability.OfferRatios))
			for offer, ratios := range offers {
				capability.OfferRatios[offer] = slices.Clone(ratios)
			}
			return capability, true
		}
	}
	return ImageRatioCapability{}, false
}

// ImageOfferRatios returns the values qualified for this exact original lane
// and retail tier. A model-wide ratio alone never authorizes another tier.
func ImageOfferRatios(id, offerID string) []string {
	capability, ok := ImageRatios(id)
	if !ok {
		return nil
	}
	return capability.OfferRatios[offerID]
}
