package service

import (
	"encoding/base64"
	"errors"
	"mime"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ShowcaseCategories = []string{"pc", "web", "miniapp", "exe", "game", "image", "video"}

// Showcase media uses the existing upload/sniff/hash pipeline but reserves a new
// explicit scope. Neither conversation media nor private/public profile avatars
// may be repurposed. The durable reservation survives unpublication/removal.
const showcaseAssetScope = "showcase"

func showcaseError(status int, code, message string) error {
	return &DemandError{status, "SHOWCASE_" + code, message}
}
func showcaseUUID(s string) bool { v, err := uuid.Parse(s); return err == nil && v.String() == s }

type ShowcaseMediaInput struct {
	AttachmentID       string `json:"attachment_id"`
	PosterAttachmentID string `json:"poster_attachment_id,omitempty"`
}
type ShowcaseFields struct {
	Title             string               `json:"title"`
	Description       string               `json:"description"`
	Category          string               `json:"category"`
	CoverAttachmentID string               `json:"cover_attachment_id"`
	Media             []ShowcaseMediaInput `json:"media"`
	SourceName        string               `json:"source_name"`
	SourceURL         string               `json:"source_url"`
	Attribution       string               `json:"attribution"`
	Published         bool                 `json:"published"`
	Synthetic         bool                 `json:"synthetic"`
	SeedBatch         string               `json:"seed_batch"`
	Provenance        string               `json:"provenance"`
}
type ShowcaseCreate struct {
	ClientRequestID string `json:"client_request_id"`
	ShowcaseFields
}
type ShowcaseEdit struct {
	ClientActionID string `json:"client_action_id"`
	Revision       int64  `json:"revision"`
	ShowcaseFields
}
type ShowcaseMediaView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	PosterURL string `json:"poster_url,omitempty"`
}
type ShowcaseView struct {
	ID          string              `json:"id"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Category    string              `json:"category"`
	CoverURL    string              `json:"cover_url"`
	Media       []ShowcaseMediaView `json:"media"`
	SourceName  string              `json:"source_name"`
	SourceURL   string              `json:"source_url"`
	Attribution string              `json:"attribution"`
	CreatedAt   int64               `json:"created_at"`
	UpdatedAt   int64               `json:"updated_at"`
}
type AdminShowcaseView struct {
	ShowcaseView
	Synthetic         bool                 `json:"synthetic"`
	Revision          int64                `json:"revision"`
	Published         bool                 `json:"published"`
	CoverAttachmentID string               `json:"cover_attachment_id"`
	MediaInputs       []ShowcaseMediaInput `json:"media_inputs"`
	SeedBatch         string               `json:"seed_batch"`
	Provenance        string               `json:"provenance"`
}
type ShowcaseReceipt struct {
	Item     AdminShowcaseView `json:"item"`
	Replayed bool              `json:"replayed"`
}
type ShowcaseListInput struct {
	Limit                                         int
	Cursor, Category, Query, Published, SeedBatch string
	Admin                                         bool
}
type ShowcasePage struct {
	Items      any    `json:"items"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
	TotalCount int64  `json:"total_count"`
}
type showcaseCursor struct{ After, Filter string }

func validateShowcaseFields(in ShowcaseFields) error {
	invalid := showcaseError(400, "INVALID_INPUT", "Invalid showcase input")
	if strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 200 || strings.TrimSpace(in.Description) == "" || utf8.RuneCountInString(in.Description) > 4000 || !slices.Contains(ShowcaseCategories, in.Category) || !showcaseUUID(in.CoverAttachmentID) || len(in.Media) < 1 || len(in.Media) > 30 || utf8.RuneCountInString(in.SourceName) > 100 || utf8.RuneCountInString(in.Attribution) > 1000 || utf8.RuneCountInString(in.Provenance) > 2000 || len(in.SeedBatch) > 80 || strings.ContainsAny(in.SeedBatch, "\x00\r\n") {
		return invalid
	}
	for _, v := range []string{in.Title, in.Description, in.SourceName, in.Attribution, in.Provenance} {
		for _, r := range v {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return invalid
			}
		}
	}
	// This is an explicit public material attribution, never derived from account data.
	digits := 0
	totalDigits := 0
	maskedName := strings.ContainsAny(in.SourceName, "*•●")
	for _, r := range in.SourceName {
		if unicode.IsDigit(r) {
			totalDigits++
			digits++
			if digits >= 7 {
				return invalid
			}
		} else if !unicode.IsSpace(r) && !strings.ContainsRune("+()-._", r) {
			digits = 0
		}
	}
	if maskedName && totalDigits >= 4 || strings.Contains(in.SourceName, "@") || strings.ContainsAny(in.SourceName, "\r\n") {
		return invalid
	}
	if in.SourceURL != "" {
		u, err := url.Parse(in.SourceURL)
		if err != nil || u.User != nil || u.Hostname() == "" || u.Fragment != "" {
			return invalid
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return invalid
		}
		for key := range query {
			if slices.Contains([]string{"token", "access_token", "refresh_token", "api_key", "apikey", "authorization", "password", "secret", "signature", "sig", "x-amz-signature", "x-amz-credential", "x-amz-security-token", "x-goog-signature", "x-goog-credential", "x-goog-security-token"}, strings.ToLower(key)) {
				return invalid
			}
		}
		if u.Scheme != "https" {
			ip := net.ParseIP(u.Hostname())
			if u.Scheme != "http" || !(u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()) {
				return invalid
			}
		}
	}
	seen := map[string]bool{}
	for _, m := range in.Media {
		if !showcaseUUID(m.AttachmentID) || seen[m.AttachmentID] || m.PosterAttachmentID != "" && !showcaseUUID(m.PosterAttachmentID) {
			return invalid
		}
		seen[m.AttachmentID] = true
	}
	return nil
}
func showcaseIDs(cover string, media []ShowcaseMediaInput) []string {
	ids := []string{cover}
	for _, m := range media {
		ids = append(ids, m.AttachmentID)
		if m.PosterAttachmentID != "" {
			ids = append(ids, m.PosterAttachmentID)
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(ids)))
}
func CreateShowcaseUpload(db *gorm.DB, a DemandActor, in DemandUploadCreate) (DemandUploadResult, error) {
	if !a.Admin {
		return DemandUploadResult{}, showcaseError(403, "FORBIDDEN", "Administrator permission is required")
	}
	return createDemandUploadForScope(db, a, in, showcaseAssetScope)
}
func CheckShowcaseUpload(db *gorm.DB, a DemandActor, id string) error {
	var count int64
	if !a.Admin {
		return showcaseError(403, "FORBIDDEN", "Administrator permission is required")
	}
	if err := db.Model(&model.DemandAttachment{}).Where("id = ? AND owner_id = ? AND avatar_scope = ? AND avatar_reserved = ? AND demand_id = ? AND message_id = ?", id, a.UserID, showcaseAssetScope, true, "", "").Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return showcaseError(404, "ATTACHMENT_NOT_FOUND", "Showcase attachment not found")
	}
	return nil
}
func validateShowcaseAssets(db *gorm.DB, a DemandActor, in ShowcaseFields, existingIDs []string) error {
	ids := showcaseIDs(in.CoverAttachmentID, in.Media)
	var rows []model.DemandAttachment
	if err := communityLocked(db).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return showcaseError(400, "UPLOAD_INVALID", "Showcase attachments must be ready public-purpose uploads")
	}
	byID := map[string]model.DemandAttachment{}
	for _, v := range rows {
		if v.OwnerID != a.UserID && !slices.Contains(existingIDs, v.ID) || v.Status != "ready" || v.AvatarScope != showcaseAssetScope || !v.AvatarReserved || v.DemandID != "" || v.MessageID != "" || !slices.Contains([]string{"image", "video"}, v.Kind) {
			return showcaseError(400, "UPLOAD_INVALID", "Showcase attachments must be ready public-purpose uploads")
		}
		byID[v.ID] = v
	}
	if byID[in.CoverAttachmentID].Kind != "image" {
		return showcaseError(400, "UPLOAD_INVALID", "Showcase cover must be an image")
	}
	for _, m := range in.Media {
		if m.PosterAttachmentID != "" && (byID[m.AttachmentID].Kind != "video" || byID[m.PosterAttachmentID].Kind != "image") {
			return showcaseError(400, "UPLOAD_INVALID", "Showcase poster must be an image for a video")
		}
	}
	return nil
}
func showcaseFields(row model.DemandShowcase) ShowcaseFields {
	var media []ShowcaseMediaInput
	_ = common.Unmarshal([]byte(row.MediaJSON), &media)
	return ShowcaseFields{row.Title, row.Description, row.Category, row.CoverAttachmentID, media, row.SourceName, row.SourceURL, row.Attribution, row.Published, row.Synthetic, row.SeedBatch, row.Provenance}
}
func showcaseRow(in ShowcaseFields) model.DemandShowcase {
	raw, _ := common.Marshal(in.Media)
	return model.DemandShowcase{Title: in.Title, Description: in.Description, Category: in.Category, CoverAttachmentID: in.CoverAttachmentID, MediaJSON: string(raw), SourceName: in.SourceName, SourceURL: in.SourceURL, Attribution: in.Attribution, Published: in.Published, Synthetic: in.Synthetic, SeedBatch: in.SeedBatch, Provenance: in.Provenance}
}
func showcaseAssetURL(caseID, assetID string) (string, error) {
	base := strings.TrimRight(system_setting.TaskPublicAddress, "/")
	if base == "" {
		base = strings.TrimRight(system_setting.ServerAddress, "/")
	}
	if err := ValidateTaskArtifactBaseURL(base); err != nil {
		return "", showcaseError(503, "STORAGE_UNAVAILABLE", "Showcase public address is not configured")
	}
	return base + "/api/product/demand-showcases/assets/" + url.PathEscape(caseID) + "/" + url.PathEscape(assetID) + "/content", nil
}
func projectShowcases(db *gorm.DB, rows []model.DemandShowcase) ([]AdminShowcaseView, error) {
	out := make([]AdminShowcaseView, 0, len(rows))
	var ids []string
	for _, r := range rows {
		f := showcaseFields(r)
		ids = append(ids, showcaseIDs(f.CoverAttachmentID, f.Media)...)
	}
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	var assets []model.DemandAttachment
	if len(ids) > 0 {
		if err := db.Where("id IN ? AND status = ? AND avatar_scope = ? AND avatar_reserved = ?", ids, "ready", showcaseAssetScope, true).Find(&assets).Error; err != nil {
			return nil, err
		}
	}
	byID := map[string]model.DemandAttachment{}
	for _, a := range assets {
		byID[a.ID] = a
	}
	for _, r := range rows {
		f := showcaseFields(r)
		cover, ok := byID[f.CoverAttachmentID]
		if !ok || cover.Kind != "image" {
			return nil, showcaseError(503, "STORAGE_UNAVAILABLE", "Showcase attachment is unavailable")
		}
		coverURL, err := showcaseAssetURL(r.ID, cover.ID)
		if err != nil {
			return nil, err
		}
		v := ShowcaseView{ID: r.ID, Title: r.Title, Description: r.Description, Category: r.Category, CoverURL: coverURL, Media: make([]ShowcaseMediaView, 0, len(f.Media)), SourceName: r.SourceName, SourceURL: r.SourceURL, Attribution: r.Attribution, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
		for _, m := range f.Media {
			a, ok := byID[m.AttachmentID]
			if !ok {
				return nil, showcaseError(503, "STORAGE_UNAVAILABLE", "Showcase attachment is unavailable")
			}
			u, err := showcaseAssetURL(r.ID, a.ID)
			if err != nil {
				return nil, err
			}
			mv := ShowcaseMediaView{ID: a.ID, Kind: a.Kind, MimeType: a.MimeType, SizeBytes: a.SizeBytes, SHA256: a.SHA256, URL: u}
			if m.PosterAttachmentID != "" {
				if _, ok := byID[m.PosterAttachmentID]; !ok {
					return nil, showcaseError(503, "STORAGE_UNAVAILABLE", "Showcase attachment is unavailable")
				}
				mv.PosterURL, err = showcaseAssetURL(r.ID, m.PosterAttachmentID)
				if err != nil {
					return nil, err
				}
			}
			v.Media = append(v.Media, mv)
		}
		out = append(out, AdminShowcaseView{ShowcaseView: v, Synthetic: r.Synthetic, Revision: r.Revision, Published: r.Published, CoverAttachmentID: r.CoverAttachmentID, MediaInputs: f.Media, SeedBatch: r.SeedBatch, Provenance: r.Provenance})
	}
	return out, nil
}
func GetShowcase(db *gorm.DB, id string, admin bool) (AdminShowcaseView, error) {
	var row model.DemandShowcase
	q := db.Where("id = ?", id)
	if !admin {
		q = q.Where("published = ?", true)
	}
	if err := q.First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return AdminShowcaseView{}, showcaseError(404, "NOT_FOUND", "Showcase not found")
	} else if err != nil {
		return AdminShowcaseView{}, err
	}
	rows, err := projectShowcases(db, []model.DemandShowcase{row})
	if err != nil {
		return AdminShowcaseView{}, err
	}
	return rows[0], nil
}
func CreateShowcase(db *gorm.DB, a DemandActor, in ShowcaseCreate) (ShowcaseReceipt, error) {
	var out ShowcaseReceipt
	if !a.Admin {
		return out, showcaseError(403, "FORBIDDEN", "Administrator permission is required")
	}
	if !showcaseUUID(in.ClientRequestID) {
		return out, showcaseError(400, "INVALID_INPUT", "Invalid showcase input")
	}
	hash := demandHash(in)
	var original model.DemandShowcase
	if err := db.Where("creator_id = ? AND client_request_id = ?", a.UserID, in.ClientRequestID).First(&original).Error; err == nil {
		if original.PayloadHash != hash {
			return out, showcaseError(409, "IDEMPOTENCY_CONFLICT", "Showcase key was already used for different content")
		}
		v, err := GetShowcase(db, original.ID, true)
		return ShowcaseReceipt{v, true}, err
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	if err := validateShowcaseFields(in.ShowcaseFields); err != nil {
		return out, err
	}
	row := showcaseRow(in.ShowcaseFields)
	row.ID = uuid.NewString()
	row.CreatorID = a.UserID
	row.ClientRequestID = in.ClientRequestID
	row.PayloadHash = hash
	row.Revision = 1
	row.CreatedAt = time.Now().Unix()
	row.UpdatedAt = row.CreatedAt
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := validateShowcaseAssets(tx, a, in.ShowcaseFields, nil); err != nil {
			return err
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return showcaseError(409, "IDEMPOTENCY_CONFLICT", "Showcase key was already used for different content")
		}
		return nil
	})
	if err != nil {
		if db.Where("creator_id = ? AND client_request_id = ?", a.UserID, in.ClientRequestID).First(&original).Error == nil && original.PayloadHash == hash {
			v, e := GetShowcase(db, original.ID, true)
			return ShowcaseReceipt{v, true}, e
		}
		return out, err
	}
	out.Item, err = GetShowcase(db, row.ID, true)
	return out, err
}
func EditShowcase(db *gorm.DB, a DemandActor, id string, in ShowcaseEdit) (ShowcaseReceipt, error) {
	var out ShowcaseReceipt
	if !a.Admin {
		return out, showcaseError(403, "FORBIDDEN", "Administrator permission is required")
	}
	if !showcaseUUID(in.ClientActionID) || !showcaseUUID(id) {
		return out, showcaseError(400, "INVALID_INPUT", "Invalid showcase input")
	}
	hash := demandHash(in)
	lookup := func(q *gorm.DB) (bool, error) {
		var r model.DemandShowcaseAction
		err := q.Where("showcase_id = ? AND actor_id = ? AND client_action_id = ?", id, a.UserID, in.ClientActionID).First(&r).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if r.PayloadHash != hash {
			return false, showcaseError(409, "IDEMPOTENCY_CONFLICT", "Showcase action key was already used for different content")
		}
		return true, nil
	}
	if replay, err := lookup(db); replay || err != nil {
		if err != nil {
			return out, err
		}
		v, e := GetShowcase(db, id, true)
		return ShowcaseReceipt{v, true}, e
	}
	if in.Revision < 1 {
		return out, showcaseError(400, "INVALID_INPUT", "Invalid showcase input")
	}
	if err := validateShowcaseFields(in.ShowcaseFields); err != nil {
		return out, err
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var row model.DemandShowcase
		if err := communityLocked(tx).First(&row, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return showcaseError(404, "NOT_FOUND", "Showcase not found")
			}
			return err
		}
		if replay, e := lookup(tx); replay || e != nil {
			out.Replayed = replay
			return e
		}
		if row.Revision != in.Revision {
			return showcaseError(409, "REVISION_CONFLICT", "Showcase changed; reload before saving")
		}
		if err := validateShowcaseAssets(tx, a, in.ShowcaseFields, showcaseIDs(row.CoverAttachmentID, showcaseFields(row).Media)); err != nil {
			return err
		}
		v := showcaseRow(in.ShowcaseFields)
		res := tx.Model(&model.DemandShowcase{}).Where("id = ? AND revision = ?", id, in.Revision).Updates(map[string]any{"title": v.Title, "description": v.Description, "category": v.Category, "cover_attachment_id": v.CoverAttachmentID, "media_json": v.MediaJSON, "source_name": v.SourceName, "source_url": v.SourceURL, "attribution": v.Attribution, "published": v.Published, "synthetic": v.Synthetic, "seed_batch": v.SeedBatch, "provenance": v.Provenance, "revision": in.Revision + 1, "updated_at": time.Now().Unix()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return showcaseError(409, "REVISION_CONFLICT", "Showcase changed; reload before saving")
		}
		return tx.Create(&model.DemandShowcaseAction{ShowcaseID: id, ActorID: a.UserID, ClientActionID: in.ClientActionID, PayloadHash: hash}).Error
	})
	if err != nil {
		if replay, e := lookup(db); replay && e == nil {
			v, x := GetShowcase(db, id, true)
			return ShowcaseReceipt{v, true}, x
		}
		return out, err
	}
	out.Item, err = GetShowcase(db, id, true)
	return out, err
}
func ListShowcases(db *gorm.DB, in ShowcaseListInput) (ShowcasePage, error) {
	var out ShowcasePage
	if in.Limit == 0 {
		in.Limit = 50
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Limit < 1 || in.Limit > 100 || utf8.RuneCountInString(in.Query) > 200 || len(in.SeedBatch) > 80 || in.Category != "" && !slices.Contains(ShowcaseCategories, in.Category) || !slices.Contains([]string{"", "true", "false"}, in.Published) {
		return out, showcaseError(400, "INVALID_INPUT", "Invalid showcase filter")
	}
	if !in.Admin && (in.Published != "" || in.SeedBatch != "") {
		return out, showcaseError(400, "INVALID_INPUT", "Invalid showcase filter")
	}
	binding := demandHash([]any{in.Category, in.Query, in.Published, in.SeedBatch, in.Admin})
	after := ""
	if in.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		var cur showcaseCursor
		if e != nil || len(raw) > 1024 || common.Unmarshal(raw, &cur) != nil || cur.Filter != binding || !showcaseUUID(cur.After) {
			return out, showcaseError(400, "INVALID_CURSOR", "Invalid showcase cursor")
		}
		after = cur.After
	}
	q := db.Model(&model.DemandShowcase{})
	if !in.Admin {
		q = q.Where("published = ?", true)
	} else if in.Published != "" {
		q = q.Where("published = ?", in.Published == "true")
	}
	if in.Category != "" {
		q = q.Where("category = ?", in.Category)
	}
	if in.SeedBatch != "" {
		q = q.Where("seed_batch = ?", in.SeedBatch)
	}
	if in.Query != "" {
		like := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(in.Query) + "%"
		q = q.Where("title LIKE ? ESCAPE '!' OR description LIKE ? ESCAPE '!'", like, like)
	}
	if err := q.Count(&out.TotalCount).Error; err != nil {
		return out, err
	}
	if after != "" {
		q = q.Where("id > ?", after)
	}
	var rows []model.DemandShowcase
	if err := q.Order("id ASC").Limit(in.Limit + 1).Find(&rows).Error; err != nil {
		return out, err
	}
	out.HasMore = len(rows) > in.Limit
	if out.HasMore {
		rows = rows[:in.Limit]
	}
	views, err := projectShowcases(db, rows)
	if err != nil {
		return out, err
	}
	if in.Admin {
		out.Items = views
	} else {
		public := make([]ShowcaseView, 0, len(views))
		for _, v := range views {
			public = append(public, v.ShowcaseView)
		}
		out.Items = public
	}
	if out.HasMore && len(rows) > 0 {
		raw, _ := common.Marshal(showcaseCursor{rows[len(rows)-1].ID, binding})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
func OpenShowcaseAsset(db *gorm.DB, caseID, assetID string) (model.DemandAttachment, *os.File, string, error) {
	var empty model.DemandAttachment
	fail := showcaseError(404, "ATTACHMENT_NOT_FOUND", "Showcase attachment not found")
	var row model.DemandShowcase
	if db.Where("id = ? AND published = ?", caseID, true).First(&row).Error != nil {
		return empty, nil, "", fail
	}
	f := showcaseFields(row)
	if !slices.Contains(showcaseIDs(f.CoverAttachmentID, f.Media), assetID) {
		return empty, nil, "", fail
	}
	var a model.DemandAttachment
	if db.Where("id = ? AND status = ? AND avatar_scope = ? AND avatar_reserved = ? AND demand_id = ? AND message_id = ?", assetID, "ready", showcaseAssetScope, true, "", "").First(&a).Error != nil {
		return empty, nil, "", fail
	}
	p, err := demandStoragePath(a.ObjectKey)
	if err != nil {
		return empty, nil, "", err
	}
	file, err := os.Open(p)
	if err != nil {
		return empty, nil, "", fail
	}
	return a, file, mime.FormatMediaType("inline", map[string]string{"filename": "showcase-media"}), nil
}
