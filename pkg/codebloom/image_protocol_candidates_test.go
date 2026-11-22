package codebloom_test

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageProtocolCandidatesUseTheirOwnGenerationEditAndMeterProof(t *testing.T) {
	candidates := codebloom.ImageProtocolCandidates()
	require.Len(t, candidates, 3)
	oldModels, err := codebloom.Models()
	require.NoError(t, err)
	images := 0
	for _, old := range oldModels {
		if old.Kind == "image" {
			images++
		}
	}
	assert.Equal(t, 14, images)
	for _, candidate := range candidates {
		t.Run(candidate.ID, func(t *testing.T) {
			assert.Equal(t, "ys-"+candidate.UpstreamID, candidate.ID)
			assert.Equal(t, candidate.ID, candidate.Name)
			assert.True(t, candidate.ActivationReady)
			assert.True(t, codebloom.PubliclyAvailable(candidate.ID), "exact new generation/edit/meter/retained evidence qualifies only this route")
			assert.False(t, codebloom.PubliclyAvailable("codebloom-"+candidate.UpstreamID))
			require.NotNil(t, candidate.AccountMultiplier)
			assert.Equal(t, .75, *candidate.AccountMultiplier)
			require.NotNil(t, candidate.AccountCreditCostCNY)
			assert.Equal(t, .075, *candidate.AccountCreditCostCNY)
			assert.Nil(t, candidate.CashCostCNY)
			require.NotNil(t, candidate.RetailCNY)
			assert.Equal(t, .175, *candidate.RetailCNY)
			assert.Equal(t, .10, candidate.RetailMarkupCNY)
			assert.Equal(t, `tier("image", fixed(0.1)) * image_count`, candidate.PublicBillingExpression)
			assert.Len(t, codebloom.ClientPrices(candidate.ID), 1)
			parameters := codebloom.ImageEditClientParameters(candidate.ID)
			require.NotNil(t, parameters)
			assert.Equal(t, 1, parameters["x-reference-limits"].(map[string]any)["images"])
			assert.Equal(t, 5<<20, parameters["x-image-edit"].(map[string]any)["max_reference_bytes"])
			format := parameters["properties"].(map[string]any)["response_format"].(map[string]any)
			assert.Equal(t, []string{"url", "b64_json"}, format["enum"])
			assert.Equal(t, "url", format["x-native-response-format"])
			assert.Contains(t, codebloom.ImageEditModels(), candidate.ID)
			_, priced := codebloom.Lookup(candidate.ID)
			assert.True(t, priced)
			_, raw := codebloom.LookupImageProtocolCandidate(candidate.UpstreamID)
			assert.False(t, raw)
			copy, exists := codebloom.LookupImageProtocolCandidate(candidate.ID)
			require.True(t, exists)
			copy.PendingReasons[0], copy.CapabilityStatus["billable_usage"] = "tampered", "unverified"
			next, _ := codebloom.LookupImageProtocolCandidate(candidate.ID)
			assert.Equal(t, "cash_procurement_conversion_unknown", next.PendingReasons[0])
			assert.Equal(t, "verified", next.CapabilityStatus["billable_usage"])
		})
	}
	assert.NotContains(t, codebloom.ImageEditModels(), "ys-grok-imagine-image")
	assert.NotContains(t, codebloom.ImageEditModels(), "ys-grok-imagine-image-2.0")
}

func imageCandidateObject(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(raw, &result))
	return result
}

func TestImageProtocolCandidatesBuildOnlyBoundedNativeShapes(t *testing.T) {
	source, err := builtinplugins.Source("codebloom-image")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "codebloom-image"})
	require.NoError(t, err)
	require.Len(t, plugin.Meta.Models, 14)
	for _, candidate := range codebloom.ImageProtocolCandidates() {
		t.Run(candidate.ID, func(t *testing.T) {
			_, found := registry.Generation().LookupEndpoint("POST", "/v1/images/generations", candidate.ID)
			require.True(t, found)
			ctx := map[string]any{"model": candidate.ID, "upstreamModel": candidate.UpstreamID, "baseUrl": "https://supplier.invalid", "apiKey": "synthetic-key", "publicTaskId": "task_candidate_only", "requestBody": map[string]any{"model": candidate.ID, "prompt": "Synthetic protocol shape", "n": 1}}
			value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.NoError(t, err)
			wire := imageCandidateObject(t, value)
			assert.Equal(t, "https://supplier.invalid/v1/images/generations", wire["url"])
			assert.Equal(t, map[string]any{"model": candidate.UpstreamID, "prompt": "Synthetic protocol shape", "size": "1024x1024", "n": float64(1), "response_format": "url"}, wire["body"])
			multipart := map[string]any{"model": candidate.ID, "operation": "edit", "body": map[string]any{"kind": "multipart", "fields": map[string][]string{"model": {candidate.ID}, "prompt": {"Synthetic reference edit"}}, "files": []any{map[string]any{"field": "image", "ref": "request_file:image", "filename": "reference.png", "mimeType": "image/png", "size": 1234}}}}
			intent, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, multipart)
			require.NoError(t, err)
			ctx["requestBody"] = imageCandidateObject(t, intent)["requestBody"]
			value, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.NoError(t, err)
			wire = imageCandidateObject(t, value)
			assert.Equal(t, "https://supplier.invalid/v1/images/edits", wire["url"])
			assert.Equal(t, "multipart", wire["bodyType"])
			parts := wire["parts"].([]any)
			assert.Equal(t, map[string]any{"name": "model", "value": candidate.UpstreamID}, parts[0])
			assert.Equal(t, map[string]any{"name": "image", "fileRef": "request_file:image"}, parts[len(parts)-1])
			assert.True(t, codebloom.PubliclyAvailable(candidate.ID))
			for _, body := range []map[string]any{{"prompt": "x", "n": 2}, {"prompt": "x", "size": "2048x2048"}, {"prompt": "x", "quality": "high"}, {"prompt": "x", "response_format": "unknown"}, {"prompt": "x", "mask": "unqualified"}} {
				ctx["requestBody"] = body
				_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.Error(t, err)
			}
			ctx["requestBody"] = map[string]any{"prompt": "x"}
			_, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx, map[string]any{"statusCode": 200, "body": map[string]any{"data": []any{map[string]any{"url": "https://synthetic.invalid/one.png"}, map[string]any{"url": "https://synthetic.invalid/two.png"}}}})
			require.Error(t, err, "n1 proof cannot qualify silently selecting the first of multiple outputs")
			multipart["body"].(map[string]any)["files"].([]any)[0].(map[string]any)["size"] = 5<<20 + 1
			_, err = plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, multipart)
			require.Error(t, err)
		})
	}
}
