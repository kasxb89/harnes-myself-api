// Package creationcontent stores operator-authored creation templates without database coupling.
package creationcontent

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

var (
	ErrInvalid   = errors.New("Invalid creation content")
	ErrConflict  = errors.New("Creation content changed; reload before saving")
	ErrAsset     = errors.New("Invalid creation asset; use PNG, JPEG, WebP, MP4 or WebM within the size limit")
	ErrStorage   = errors.New("Creation content storage is unavailable")
	assetPattern = regexp.MustCompile(`^cc_[a-f0-9]{64}$`)
	entryPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)
	stores       sync.Map
)

const MaxImageSize int64 = 10 << 20
const MaxVideoSize int64 = 100 << 20

type LocalizedText struct {
	Zh string `json:"zh"`
	En string `json:"en,omitempty"`
}
type Category struct {
	ID        string        `json:"id"`
	Label     LocalizedText `json:"label"`
	SortOrder int           `json:"sortOrder"`
	Enabled   bool          `json:"enabled"`
}
type Template struct {
	ID               string        `json:"id"`
	CategoryID       *string       `json:"categoryId"`
	Title            LocalizedText `json:"title"`
	Description      LocalizedText `json:"description"`
	Prompt           LocalizedText `json:"prompt"`
	CoverAssetID     *string       `json:"coverAssetId"`
	DemoVideoAssetID *string       `json:"demoVideoAssetId"`
	SortOrder        int           `json:"sortOrder"`
	Status           string        `json:"status"`
}
type Preset struct {
	ID           string        `json:"id"`
	Label        LocalizedText `json:"label"`
	PromptSuffix LocalizedText `json:"promptSuffix"`
	SortOrder    int           `json:"sortOrder"`
	Status       string        `json:"status"`
}
type ModeContent struct {
	PromptPlaceholder LocalizedText `json:"promptPlaceholder"`
	Title             LocalizedText `json:"title"`
	Description       LocalizedText `json:"description"`
	Categories        []Category    `json:"categories"`
	Templates         []Template    `json:"templates"`
	Presets           []Preset      `json:"presets"`
}
type Content struct {
	SchemaVersion int         `json:"schemaVersion"`
	Revision      string      `json:"revision"`
	Image         ModeContent `json:"image"`
	Video         ModeContent `json:"video"`
}
type Asset struct {
	ID        string `json:"id"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	URL       string `json:"url"`
}
type Store struct {
	dir string
	mu  sync.Mutex
}

// Default uses an operator-controlled directory and one process-wide lock per path.
func Default() *Store {
	dir := os.Getenv("CREATION_CONTENT_DIR")
	if dir == "" {
		dir = filepath.Join("data", "creation-content")
	}
	dir, _ = filepath.Abs(dir)
	value, _ := stores.LoadOrStore(dir, &Store{dir: dir})
	return value.(*Store)
}
func NewStore(dir string) *Store { return &Store{dir: dir} }
func emptyMode() ModeContent {
	return ModeContent{Categories: []Category{}, Templates: []Template{}, Presets: []Preset{}}
}
func (s *Store) load() (Content, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "content.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Content{SchemaVersion: 1, Revision: "0", Image: emptyMode(), Video: emptyMode()}, nil
	}
	if err != nil || len(data) > 4<<20 {
		return Content{}, ErrStorage
	}
	var content Content
	if common.Unmarshal(data, &content) != nil || s.validate(content) != nil {
		return Content{}, ErrStorage
	}
	return content, nil
}
func (s *Store) Load(public bool) (Content, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := s.load()
	if err != nil {
		return Content{}, err
	}
	if public {
		content.Image = publishedMode(content.Image)
		content.Video = publishedMode(content.Video)
	}
	return content, nil
}
func publishedMode(mode ModeContent) ModeContent {
	enabled := map[string]bool{}
	categories := make([]Category, 0, len(mode.Categories))
	for _, item := range mode.Categories {
		if item.Enabled {
			categories = append(categories, item)
			enabled[item.ID] = true
		}
	}
	templates := make([]Template, 0, len(mode.Templates))
	for _, item := range mode.Templates {
		if item.Status == "published" && (item.CategoryID == nil || enabled[*item.CategoryID]) {
			templates = append(templates, item)
		}
	}
	presets := make([]Preset, 0, len(mode.Presets))
	for _, item := range mode.Presets {
		if item.Status == "published" {
			presets = append(presets, item)
		}
	}
	slices.SortStableFunc(categories, func(a, b Category) int { return a.SortOrder - b.SortOrder })
	slices.SortStableFunc(templates, func(a, b Template) int { return a.SortOrder - b.SortOrder })
	slices.SortStableFunc(presets, func(a, b Preset) int { return a.SortOrder - b.SortOrder })
	mode.Categories = categories
	mode.Templates = templates
	mode.Presets = presets
	return mode
}
func validText(value LocalizedText, limit int, required bool) bool {
	return utf8.ValidString(value.Zh) && utf8.ValidString(value.En) && utf8.RuneCountInString(value.Zh) <= limit && utf8.RuneCountInString(value.En) <= limit && (!required || strings.TrimSpace(value.Zh) != "")
}
func (s *Store) validate(content Content) error {
	if content.SchemaVersion != 1 || len(content.Revision) > 100 {
		return ErrInvalid
	}
	ids := map[string]bool{}
	for _, mode := range []ModeContent{content.Image, content.Video} {
		if !validText(mode.Title, 200, false) || !validText(mode.Description, 2000, false) || !validText(mode.PromptPlaceholder, 1000, false) || mode.Categories == nil || mode.Templates == nil || mode.Presets == nil || len(mode.Categories) > 100 || len(mode.Templates) > 500 || len(mode.Presets) > 100 {
			return ErrInvalid
		}
		categories := map[string]bool{}
		for _, item := range mode.Categories {
			if !entryPattern.MatchString(item.ID) || ids[item.ID] || !validText(item.Label, 100, true) || item.SortOrder < -100000 || item.SortOrder > 100000 {
				return ErrInvalid
			}
			ids[item.ID] = true
			categories[item.ID] = true
		}
		for _, item := range mode.Templates {
			if !entryPattern.MatchString(item.ID) || ids[item.ID] || !validText(item.Title, 200, true) || !validText(item.Description, 2000, true) || !validText(item.Prompt, 7000, true) || item.SortOrder < -100000 || item.SortOrder > 100000 || (item.Status != "draft" && item.Status != "published") {
				return ErrInvalid
			}
			if item.CategoryID != nil && !categories[*item.CategoryID] {
				return ErrInvalid
			}
			ids[item.ID] = true
			for _, ref := range []struct {
				id     *string
				prefix string
			}{{item.CoverAssetID, "image/"}, {item.DemoVideoAssetID, "video/"}} {
				if ref.id == nil {
					continue
				}
				asset, file, err := s.OpenAsset(*ref.id)
				if err != nil {
					return ErrAsset
				}
				file.Close()
				if !strings.HasPrefix(asset.MediaType, ref.prefix) {
					return ErrAsset
				}
			}
			if item.Status == "published" && item.CoverAssetID == nil {
				return ErrAsset
			}
		}
		for _, item := range mode.Presets {
			if !entryPattern.MatchString(item.ID) || ids[item.ID] || !validText(item.Label, 100, true) || !validText(item.PromptSuffix, 2000, true) || item.SortOrder < -100000 || item.SortOrder > 100000 || (item.Status != "draft" && item.Status != "published") {
				return ErrInvalid
			}
			ids[item.ID] = true
		}
	}
	return nil
}
func (s *Store) Save(content Content) (Content, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load()
	if err != nil {
		return Content{}, err
	}
	if content.Revision != current.Revision {
		return Content{}, ErrConflict
	}
	if err = s.validate(content); err != nil {
		return Content{}, err
	}
	revision := make([]byte, 16)
	if _, err = rand.Read(revision); err != nil {
		return Content{}, ErrStorage
	}
	content.Revision = hex.EncodeToString(revision)
	data, err := common.Marshal(content)
	if err != nil {
		return Content{}, ErrStorage
	}
	if len(data) > 4<<20 {
		return Content{}, ErrInvalid
	}
	if err = os.MkdirAll(s.dir, 0700); err != nil {
		return Content{}, ErrStorage
	}
	file, err := os.CreateTemp(s.dir, ".content-*")
	if err != nil {
		return Content{}, ErrStorage
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return Content{}, ErrStorage
	}
	if err = os.Rename(file.Name(), filepath.Join(s.dir, "content.json")); err != nil {
		return Content{}, ErrStorage
	}
	return content, nil
}
func mediaType(data []byte, totalSize int64) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return "image/png"
	}
	if len(data) >= 3 && bytes.Equal(data[:3], []byte{255, 216, 255}) {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(data) >= 24 && string(data[4:8]) == "ftyp" {
		size := int(binary.BigEndian.Uint32(data[:4]))
		if size >= 16 && int64(size) <= totalSize {
			for offset := 8; offset+4 <= min(size, len(data)); offset += 4 {
				if offset == 12 {
					continue
				}
				if slices.Contains([]string{"isom", "iso2", "iso3", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "M4V ", "dash", "MSNV", "qt  "}, string(data[offset:offset+4])) {
					return "video/mp4"
				}
			}
		}
	}
	if len(data) >= 16 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) && bytes.Contains(data[:min(len(data), 4096)], []byte{0x42, 0x82, 0x84, 'w', 'e', 'b', 'm'}) {
		return "video/webm"
	}
	return ""
}
func (s *Store) Upload(reader io.Reader) (Asset, error) {
	// A bounded read is required even when the multipart sender omits a length.
	data, err := io.ReadAll(io.LimitReader(reader, MaxVideoSize+1))
	if err != nil {
		return Asset{}, ErrStorage
	}
	kind := mediaType(data[:min(len(data), 4096)], int64(len(data)))
	if kind == "" || int64(len(data)) > MaxVideoSize || (strings.HasPrefix(kind, "image/") && int64(len(data)) > MaxImageSize) {
		return Asset{}, ErrAsset
	}
	if kind == "image/png" || kind == "image/jpeg" {
		config, _, decodeErr := image.DecodeConfig(bytes.NewReader(data))
		if decodeErr != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 32768 || config.Height > 32768 {
			return Asset{}, ErrAsset
		}
	}
	if kind == "image/webp" && (len(data) < 30 || int64(binary.LittleEndian.Uint32(data[4:8]))+8 != int64(len(data)) || !slices.Contains([]string{"VP8 ", "VP8L", "VP8X"}, string(data[12:16]))) {
		return Asset{}, ErrAsset
	}
	sum := sha256.Sum256(data)
	id := "cc_" + hex.EncodeToString(sum[:])
	dir := filepath.Join(s.dir, "assets")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return Asset{}, ErrStorage
	}
	file, err := os.CreateTemp(dir, ".asset-*")
	if err != nil {
		return Asset{}, ErrStorage
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return Asset{}, ErrStorage
	}
	// Hard-link publishes only complete files and never replaces a previously published hash.
	if err = os.Link(file.Name(), filepath.Join(dir, id)); err != nil && !errors.Is(err, os.ErrExist) {
		return Asset{}, ErrStorage
	}
	return Asset{ID: id, MediaType: kind, Size: int64(len(data)), URL: "/api/creation/assets/" + id}, nil
}
func (s *Store) OpenAsset(id string) (Asset, *os.File, error) {
	if !assetPattern.MatchString(id) {
		return Asset{}, nil, ErrAsset
	}
	file, err := os.Open(filepath.Join(s.dir, "assets", id))
	if err != nil {
		return Asset{}, nil, ErrAsset
	}
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > MaxVideoSize {
		file.Close()
		return Asset{}, nil, ErrAsset
	}
	prefix := make([]byte, min(stat.Size(), 4096))
	if _, err = io.ReadFull(file, prefix); err != nil {
		file.Close()
		return Asset{}, nil, ErrAsset
	}
	kind := mediaType(prefix, stat.Size())
	if kind == "" {
		file.Close()
		return Asset{}, nil, ErrAsset
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return Asset{}, nil, ErrStorage
	}
	return Asset{ID: id, MediaType: kind, Size: stat.Size(), URL: "/api/creation/assets/" + id}, file, nil
}

// ServeAsset delegates RFC Range and conditional request handling to net/http.
func (s *Store) ServeAsset(writer http.ResponseWriter, request *http.Request, id string) {
	asset, file, err := s.OpenAsset(id)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	writer.Header().Set("Content-Type", asset.MediaType)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writer.Header().Set("ETag", fmt.Sprintf(`"%s"`, id))
	stat, _ := file.Stat()
	http.ServeContent(writer, request, id, stat.ModTime(), file)
}
