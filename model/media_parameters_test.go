package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageEditCatalogFollowsPublishedControls(t *testing.T) {
	for _, tc := range []struct {
		name       string
		controls   string
		editable   bool
		maxImages  int
		resolution string
		size       string
	}{
		{"narrow resolution", `{"properties":{"resolution":{"type":"string","enum":["4K"]}}}`, true, 1, "4K", ""},
		{"generation only specification", `{"properties":{"resolution":{"type":"string","enum":["0.5K"]}}}`, false, 0, "", ""},
		{"references disabled", `{"properties":{"reference_images":false}}`, false, 0, "", ""},
		{"reference ceiling cannot exceed adapter", `{"properties":{"reference_images":{"type":"array","items":{"type":"string"},"maxItems":3}}}`, true, 1, "", ""},
		{"explicit specification combinations", `{"properties":{"resolution":{"type":"string","enum":["1K","4K"]},"size":{"type":"string","enum":["1:1","16:9"]}},"oneOf":[{"properties":{"resolution":{"enum":["4K"]},"size":{"enum":["1:1"]}}}]}`, true, 1, "4K", "1:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := ycapi.ImageEditClientParameters("ycapi-gemini-3.1-flash-image")
			require.NotNil(t, base["x-image-edit"])
			before, err := common.Marshal(base)
			require.NoError(t, err)
			var controls map[string]any
			require.NoError(t, common.Unmarshal([]byte(tc.controls), &controls))
			result := ProjectModelConfigurationParameters(base, controls)
			limits := result["x-reference-limits"].(map[string]any)
			assert.EqualValues(t, tc.maxImages, limits["images"])
			fields := result["properties"].(map[string]any)
			if refs, ok := fields["reference_images"].(map[string]any); ok {
				assert.EqualValues(t, tc.maxImages, refs["maxItems"])
			}
			edit, available := result["x-image-edit"].(map[string]any)
			require.Equal(t, tc.editable, available)
			if tc.editable {
				specifications := edit["specifications"].([]any)
				require.NotEmpty(t, specifications)
				if tc.resolution != "" {
					require.Len(t, specifications, 1)
					properties := specifications[0].(map[string]any)["properties"].(map[string]any)
					assert.Equal(t, []any{tc.resolution}, properties["resolution"].(map[string]any)["enum"])
					if tc.size != "" {
						assert.Equal(t, []any{tc.size}, properties["size"].(map[string]any)["enum"])
					}
				}
			}
			after, err := common.Marshal(base)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "catalog projection must not change shared adapter metadata")
		})
	}
}

func TestKnownMediaCatalogKindsPreserveReferenceContracts(t *testing.T) {
	resetPricingEndpointTestTables(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	image := taskPluginParameterSchema("toapis-image", "1.2.1", "yunshu-internal-gpt-image-2")
	require.NotNil(t, image)
	assert.Equal(t, MediaImage, image["x-model-kind"])
	assert.Equal(t, referenceLimits(6, 0, 0), image["x-reference-limits"])
	assert.NotContains(t, image, "x-image-edit", "do not invent a byte limit or break clients that require one for this marker")
	assert.NotContains(t, image["properties"], "mode", "this native adapter selects editing from references on its existing generate route")
	video := taskPluginParameterSchema("hailuo", "1.3.0", "MiniMax-H3")
	assert.Equal(t, MediaVideo, video["x-model-kind"])
	assert.Equal(t, referenceLimits(9, 3, 3), video["x-reference-limits"])
	textVideo := taskPluginParameterSchema("ycapi", "1.0.3", "ycapi-seedance-2-5")
	require.NotNil(t, textVideo)
	assert.EqualValues(t, 0, textVideo["x-reference-limits"].(map[string]any)["images"], "native-family capabilities cannot grant unsupported adapter inputs")
}

func TestVideoReferenceCatalogCannotExceedExecutingAdapter(t *testing.T) {
	resetPricingEndpointTestTables(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	for _, tc := range []struct {
		name, plugin, version, model, field, limit string
		maximum                                    int
	}{
		{"YC video has no image input", "ycapi", "1.0.3", "ycapi-seedance-2-5", "reference_images", "images", 0},
		{"H3 video references", "hailuo", "1.3.0", "MiniMax-H3", "reference_videos", "videos", 3},
		{"H3 frames", "hailuo", "1.3.0", "MiniMax-H3", "frame_images", "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := taskPluginParameterSchema(tc.plugin, tc.version, tc.model)
			controls := map[string]any{"properties": map[string]any{tc.field: map[string]any{
				"type": "array", "items": map[string]any{"type": "string"}, "maxItems": float64(6),
			}}}
			result := ProjectModelConfigurationParameters(base, controls)
			fields := result["properties"].(map[string]any)
			assert.EqualValues(t, tc.maximum, fields[tc.field].(map[string]any)["maxItems"])
			if tc.limit != "" {
				assert.EqualValues(t, tc.maximum, result["x-reference-limits"].(map[string]any)[tc.limit])
			} else {
				assert.EqualValues(t, tc.maximum, result["x-reference-frame-max-images"])
			}
		})
	}
}
