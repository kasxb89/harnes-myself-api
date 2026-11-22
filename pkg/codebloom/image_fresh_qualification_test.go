package codebloom

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreshImageQualificationPreservesAllHistoricalObjects(t *testing.T) {
	for _, fixture := range []struct {
		raw   []byte
		count int
		hash  string
	}{
		{catalogData, 38, "ff6a5188ecf6b2a3d7b790b6c279e29891555889416ac1525c6f2dcfed528cc3"},
		{imageQualificationData, 11, "e7375686fb444ffe5074f3f82d1c156e3ada97c2862f8ed93b1f347d5d809d06"},
	} {
		var raw struct {
			Models []common.RawMessage `json:"models"`
		}
		require.NoError(t, common.Unmarshal(fixture.raw, &raw))
		require.Len(t, raw.Models, fixture.count+3)
		var unchanged bytes.Buffer
		for _, object := range raw.Models[:fixture.count] {
			unchanged.Write(object)
			unchanged.WriteByte('\n')
		}
		hash := sha256.Sum256(unchanged.Bytes())
		assert.Equal(t, fixture.hash, hex.EncodeToString(hash[:]), "old objects including dates, prices and edit qualifications must remain byte-for-byte unchanged")
	}
	assert.Len(t, ImageEditModels(), 12, "nine existing qualified edits plus the three new exact edits")
	assert.NotContains(t, ImageEditModels(), "ys-grok-imagine-image")
	assert.NotContains(t, ImageEditModels(), "ys-grok-imagine-image-2.0")
}

func TestFreshImageQualificationRequiresBothOriginalMetersAndInputEvidence(t *testing.T) {
	proof := liveImages["ys-gpt-image-2.6"]
	require.NotEmpty(t, proof.ID)
	for name, mutate := range map[string]func(*imageQualification){
		"missing edit input":       func(p *imageQualification) { p.EditInputSHA256 = "" },
		"missing edit meter":       func(p *imageQualification) { p.EditMeterSHA256 = "" },
		"missing generation RID":   func(p *imageQualification) { p.GenerationRequestID = "" },
		"same request reused":      func(p *imageQualification) { p.EditRequestID = p.GenerationRequestID },
		"wrong checked date":       func(p *imageQualification) { p.CheckedAt = "2026-11-02" },
		"upstream URL as evidence": func(p *imageQualification) { p.EvidenceRelativePath = "https://synthetic.invalid/private" },
		"path traversal": func(p *imageQualification) {
			p.EvidenceRelativePath = "output/diagnostics/media-complete-integration-20261123/qualification/../QQ-RETAINED-SIX-REPLAY-REPORT.json"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := proof
			mutate(&changed)
			raw, err := common.Marshal(imageQualificationSnapshot{SchemaVersion: 1, Models: []imageQualification{changed}})
			require.NoError(t, err)
			_, err = loadImageQualifications(raw)
			require.Error(t, err)
		})
	}
	original, originalError := liveImages, imageQualificationError
	t.Cleanup(func() { liveImages, imageQualificationError = original, originalError })
	liveImages = map[string]imageQualification{}
	for id, p := range original {
		if !isFreshImageProtocolModel(id) {
			liveImages[id] = p
		}
	}
	for _, id := range []string{"ys-gpt-image-2.6", "ys-gpt-image-2.6-sunburst", "ys-gpt-image-2.6-flare"} {
		assert.False(t, PubliclyAvailable(id), "a catalog price alone cannot bypass missing exact-route qualification")
		candidate, ok := LookupImageProtocolCandidate(id)
		require.True(t, ok)
		assert.False(t, candidate.ActivationReady)
		assert.Nil(t, candidate.AccountCreditCostCNY)
	}
}
