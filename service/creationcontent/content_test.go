package creationcontent

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (*Store, Content, Asset) {
	t.Helper()
	s := NewStore(t.TempDir())
	content, err := s.Load(false)
	require.NoError(t, err)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aK1cAAAAASUVORK5CYII=")
	require.NoError(t, err)
	asset, err := s.Upload(bytes.NewReader(png))
	require.NoError(t, err)
	return s, content, asset
}
func TestContentPublicationAndRevision(t *testing.T) {
	s, content, asset := fixture(t)
	assert.Empty(t, content.Image.Templates)
	assert.Equal(t, "0", content.Revision)
	content.Image.Categories = []Category{{ID: "enabled", Label: LocalizedText{Zh: "商品"}, Enabled: true}, {ID: "hidden", Label: LocalizedText{Zh: "隐藏"}}}
	category := "enabled"
	hidden := "hidden"
	template := Template{ID: "visible", Title: LocalizedText{Zh: "商品图"}, Description: LocalizedText{Zh: "自然光"}, Prompt: LocalizedText{Zh: "白色背景商品摄影"}, CoverAssetID: &asset.ID, Status: "published", CategoryID: &category}
	draft := template
	draft.ID = "draft"
	draft.Status = "draft"
	disabled := template
	disabled.ID = "disabled"
	disabled.CategoryID = &hidden
	content.Image.Templates = []Template{template, draft, disabled}
	content.Video.Presets = []Preset{{ID: "camera", Label: LocalizedText{Zh: "推进"}, PromptSuffix: LocalizedText{Zh: "镜头缓慢推进"}, Status: "published"}, {ID: "camera-draft", Label: LocalizedText{Zh: "环绕"}, PromptSuffix: LocalizedText{Zh: "环绕运镜"}, Status: "draft"}}
	saved, err := s.Save(content)
	require.NoError(t, err)
	assert.NotEqual(t, "0", saved.Revision)
	public, err := s.Load(true)
	require.NoError(t, err)
	assert.Len(t, public.Image.Categories, 1)
	require.Len(t, public.Image.Templates, 1)
	assert.Equal(t, "visible", public.Image.Templates[0].ID)
	assert.Len(t, public.Video.Presets, 1)
	admin, err := NewStore(s.dir).Load(false)
	require.NoError(t, err)
	assert.Len(t, admin.Image.Templates, 3)
	_, err = s.Save(content)
	assert.ErrorIs(t, err, ErrConflict)
	content = saved
	content.Image.Title.Zh = "更新标题"
	next, err := s.Save(content)
	require.NoError(t, err)
	assert.NotEqual(t, saved.Revision, next.Revision)
}
func TestContentRejectsInvalidRelationshipsAndAssets(t *testing.T) {
	s, base, asset := fixture(t)
	valid := Template{ID: "template", Title: LocalizedText{Zh: "标题"}, Description: LocalizedText{Zh: "描述"}, Prompt: LocalizedText{Zh: "提示词"}, CoverAssetID: &asset.ID, Status: "published"}
	cases := []struct {
		name   string
		modify func(*Content)
	}{
		{"missing published cover", func(c *Content) { c.Image.Templates[0].CoverAssetID = nil }},
		{"remote asset", func(c *Content) { id := "https://example.com/a.png"; c.Image.Templates[0].CoverAssetID = &id }},
		{"missing category", func(c *Content) { id := "missing"; c.Image.Templates[0].CategoryID = &id }},
		{"duplicate across modes", func(c *Content) { c.Video.Templates = []Template{valid} }},
		{"long prompt", func(c *Content) { c.Image.Templates[0].Prompt.Zh = strings.Repeat("字", 7001) }},
		{"image in demo slot", func(c *Content) { c.Image.Templates[0].DemoVideoAssetID = &asset.ID }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			content := base
			content.Image.Templates = []Template{valid}
			test.modify(&content)
			_, err := s.Save(content)
			require.Error(t, err)
			after, err := s.Load(false)
			require.NoError(t, err)
			assert.Equal(t, "0", after.Revision)
		})
	}
}
func TestImmutableAssetsAndRange(t *testing.T) {
	s, _, asset := fixture(t)
	original, file, err := s.OpenAsset(asset.ID)
	require.NoError(t, err)
	data, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	require.NoError(t, file.Close())
	duplicate, err := s.Upload(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, original, duplicate)
	_, err = s.Upload(strings.NewReader("<svg><script>alert(1)</script></svg>"))
	assert.ErrorIs(t, err, ErrAsset)
	_, _, err = s.OpenAsset("../../content.json")
	assert.ErrorIs(t, err, ErrAsset)
	request := httptest.NewRequest("GET", asset.URL, nil)
	request.Header.Set("Range", "bytes=0-7")
	response := httptest.NewRecorder()
	s.ServeAsset(response, request, asset.ID)
	assert.Equal(t, 206, response.Code)
	assert.Equal(t, data[:8], response.Body.Bytes())
	assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	assert.Contains(t, response.Header().Get("Cache-Control"), "immutable")
	request = httptest.NewRequest("GET", asset.URL, nil)
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	s.ServeAsset(cached, request, asset.ID)
	assert.Equal(t, 304, cached.Code)
}
func TestStorageFailuresDoNotReplaceDocument(t *testing.T) {
	s, content, _ := fixture(t)
	saved, err := s.Save(content)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, "content.json"), []byte("broken"), 0600))
	_, err = s.Load(true)
	assert.ErrorIs(t, err, ErrStorage)
	_, err = s.Save(saved)
	assert.ErrorIs(t, err, ErrStorage)
	bytes, err := os.ReadFile(filepath.Join(s.dir, "content.json"))
	require.NoError(t, err)
	assert.Equal(t, "broken", string(bytes))
}

func TestAssetSignaturesRejectSpoofedImagesAndNonVideoMP4Containers(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, data := range [][]byte{{137, 80, 78, 71, 13, 10, 26, 10}, []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00heicmif1"), []byte("RIFF\x16\x00\x00\x00WEBPSVG <script>alert(1)</script>")} {
		_, err := s.Upload(bytes.NewReader(data))
		assert.ErrorIs(t, err, ErrAsset)
	}
	_, err := s.Upload(bytes.NewReader(append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, make([]byte, MaxImageSize)...)))
	assert.ErrorIs(t, err, ErrAsset)
	mp4 := []byte("\x00\x00\x00\x18ftypisom\x00\x00\x00\x00isommp42")
	asset, err := s.Upload(bytes.NewReader(mp4))
	require.NoError(t, err)
	assert.Equal(t, "video/mp4", asset.MediaType)
	request := httptest.NewRequest("GET", asset.URL, nil)
	request.Header.Set("Range", "bytes=8-11")
	response := httptest.NewRecorder()
	s.ServeAsset(response, request, asset.ID)
	assert.Equal(t, 206, response.Code)
	assert.Equal(t, "isom", response.Body.String())
	assert.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
}
func TestConcurrentEditorsCannotOverwriteOneRevision(t *testing.T) {
	s, content, _ := fixture(t)
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() { <-start; _, err := s.Save(content); results <- err }()
	}
	close(start)
	failures := 0
	successes := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else {
			assert.ErrorIs(t, err, ErrConflict)
			failures++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, failures)
}

func TestOversizedEncodedManifestDoesNotPersistAnUnreadableDocument(t *testing.T) {
	s, content, _ := fixture(t)
	for i := range 101 {
		content.Image.Templates = append(content.Image.Templates, Template{ID: fmt.Sprintf("draft-%d", i), Title: LocalizedText{Zh: "标题"}, Description: LocalizedText{Zh: "说明"}, Prompt: LocalizedText{Zh: strings.Repeat("<", 7000)}, Status: "draft"})
	}
	_, err := s.Save(content)
	assert.ErrorIs(t, err, ErrInvalid)
	current, err := s.Load(false)
	require.NoError(t, err)
	assert.Equal(t, "0", current.Revision)
	assert.Empty(t, current.Image.Templates)
}

func TestLargeMP4FileTypeBoxRemainsReadableAfterUpload(t *testing.T) {
	s := NewStore(t.TempDir())
	data := make([]byte, 8192)
	binary.BigEndian.PutUint32(data[:4], 8192)
	copy(data[4:], "ftypisom")
	asset, err := s.Upload(bytes.NewReader(data))
	require.NoError(t, err)
	reopened, file, err := s.OpenAsset(asset.ID)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, "video/mp4", reopened.MediaType)
}
