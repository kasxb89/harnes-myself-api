package codebloom

import (
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageProofFixture() imageQualification {
	hash := strings.Repeat("a", 64)
	return imageQualification{ID: "ys-gpt-image-2k", UpstreamID: "gpt-image-2k", CheckedAt: "2026-11-02", Generation: "verified", BillableUsage: "verified", ImageDecoded: true, RequestSHA256: hash, ResponseSHA256: hash, ImageSHA256: hash, MeterEvidenceSHA256: hash, RetainedProtocolSHA256: hash, Width: 1024, Height: 1024}
}

func TestImageQualificationRequiresExactRouteAndEveryProof(t *testing.T) {
	valid := imageProofFixture()
	for name, mutate := range map[string]func(*imageQualification){
		"unverified billing":    func(p *imageQualification) { p.BillableUsage = "unknown" },
		"unverified generation": func(p *imageQualification) { p.Generation = "unknown" },
		"not decoded":           func(p *imageQualification) { p.ImageDecoded = false },
		"missing meter":         func(p *imageQualification) { p.MeterEvidenceSHA256 = "" },
		"missing protocol":      func(p *imageQualification) { p.RetainedProtocolSHA256 = "" },
		"missing image":         func(p *imageQualification) { p.ImageSHA256 = "" },
		"missing request":       func(p *imageQualification) { p.RequestSHA256 = "" },
		"missing response":      func(p *imageQualification) { p.ResponseSHA256 = "" },
		"missing date":          func(p *imageQualification) { p.CheckedAt = "" },
		"wrong upstream":        func(p *imageQualification) { p.UpstreamID = "gpt-image-2.5-flare" },
		"raw alias":             func(p *imageQualification) { p.ID = p.UpstreamID },
		"legacy id":             func(p *imageQualification) { p.ID = "ys-1.0" },
		"text is not image":     func(p *imageQualification) { p.ID = "ys-gpt-6.1-sol"; p.UpstreamID = "gpt-6.1-sol" },
		"wrong declared size":   func(p *imageQualification) { p.Width = 2048 },
		"incomplete edit proof": func(p *imageQualification) { p.EditImageSHA256 = strings.Repeat("a", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			proof := valid
			mutate(&proof)
			data, err := common.Marshal(imageQualificationSnapshot{SchemaVersion: 1, Models: []imageQualification{proof}})
			require.NoError(t, err)
			_, err = loadImageQualifications(data)
			require.Error(t, err)
		})
	}
	data, err := common.Marshal(imageQualificationSnapshot{SchemaVersion: 1, Models: []imageQualification{valid, valid}})
	require.NoError(t, err)
	_, err = loadImageQualifications(data)
	require.Error(t, err, "duplicate route cannot create ambiguous qualification")
}

func TestQualifiedImageCannotMutateSnapshotOrActivateText(t *testing.T) {
	original, originalErr := liveImages, imageQualificationError
	t.Cleanup(func() { liveImages, imageQualificationError = original, originalErr })
	proof := imageProofFixture()
	liveImages = map[string]imageQualification{proof.ID: proof}
	imageQualificationError = nil
	image, ok := Lookup(proof.ID)
	require.True(t, ok)
	assert.True(t, image.ActivationReady)
	assert.True(t, PubliclyAvailable(proof.ID))
	assert.Nil(t, image.CashCostCNY)
	assert.Equal(t, "account_credit_snapshot", image.CostBasis)
	assert.Equal(t, "unverified", image.CapabilityStatus["image_edit"], "generation proof does not qualify reference input")
	assert.NotContains(t, ImageEditModels(), proof.ID)
	image.CapabilityStatus["billable_usage"] = "tampered"
	copy, _ := Lookup(proof.ID)
	assert.Equal(t, "verified", copy.CapabilityStatus["billable_usage"])
	assert.False(t, PubliclyAvailable("ys-deepseek-v4-flash"))
	assert.True(t, PubliclyAvailable("ys-1.0"))
	imageQualificationError = errors.New("invalid live proof")
	for _, candidate := range approved.Models {
		assert.False(t, PubliclyAvailable(candidate.ID), "invalid qualification must fail closed for exact reviewed IDs")
	}
	assert.True(t, PubliclyAvailable("ys-1.0"), "old ordinary names retain their availability semantics")
}
