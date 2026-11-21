package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (f *demandFixture) showcaseUpload(t *testing.T, mime string, raw []byte) model.DemandAttachment {
	t.Helper()
	start := demandData[service.DemandUploadResult](t, f.request(t, "admin", "POST", "/api/demand-showcase-management/uploads", service.DemandUploadCreate{ClientUploadID: uuid.NewString(), Name: "local-case-media", MimeType: mime, SizeBytes: int64(len(raw))}, 200))
	require.Equal(t, "uploading", start.Attachment.Status)
	id := start.Attachment.ID
	for _, offset := range []int{0, len(raw) / 2} {
		end := len(raw) / 2
		if offset > 0 {
			end = len(raw)
		}
		req := httptest.NewRequest("PUT", "/api/demand-showcase-management/uploads/"+id+"/chunks", bytes.NewReader(raw[offset:end]))
		req.Header.Set("Authorization", "Bearer "+f.tokens["admin"])
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Upload-Offset", strconv.Itoa(offset))
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	sum := sha256.Sum256(raw)
	out := demandData[service.DemandUploadResult](t, f.request(t, "admin", "POST", "/api/demand-showcase-management/uploads/"+id+"/complete", map[string]string{"sha256": hex.EncodeToString(sum[:])}, 200))
	require.Equal(t, "ready", out.Attachment.Status)
	return out.Attachment
}
func showcaseInput(imageID string) service.ShowcaseCreate {
	return service.ShowcaseCreate{ClientRequestID: uuid.NewString(), ShowcaseFields: service.ShowcaseFields{Title: "本地库存交互原型", Description: "原创合成界面演示，不是客户案例或真实成交。", Category: "web", CoverAttachmentID: imageID, Media: []service.ShowcaseMediaInput{{AttachmentID: imageID}}, SourceName: "本地原创演示", Synthetic: true, SeedBatch: "test-showcase-batch", Provenance: "synthetic, locally generated"}}
}
func showcaseMediaPath(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.RequestURI()
}

func TestShowcasePublicationReceiptsAndPublicMediaRevocation(t *testing.T) {
	f := newShowcaseFixture(t)
	picture := communityPNG(t)
	image := f.showcaseUpload(t, "image/png", picture)
	// Transport fixture only: codec playback is qualified separately with original ffmpeg assets.
	videoBytes := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte{0}, 1024)...)
	video := f.showcaseUpload(t, "video/mp4", videoBytes)
	input := showcaseInput(image.ID)
	input.Media = append(input.Media, service.ShowcaseMediaInput{AttachmentID: video.ID, PosterAttachmentID: image.ID})
	made := demandData[service.ShowcaseReceipt](t, f.request(t, "admin", "POST", "/api/demand-showcase-management", input, 200))
	require.False(t, made.Replayed)
	f.request(t, "", "GET", "/api/product/demand-showcases/"+made.Item.ID, nil, 404)
	page := demandData[service.ShowcasePage](t, f.request(t, "", "GET", "/api/product/demand-showcases", nil, 200))
	require.Zero(t, page.TotalCount)
	edit := service.ShowcaseEdit{ClientActionID: uuid.NewString(), Revision: made.Item.Revision, ShowcaseFields: input.ShowcaseFields}
	edit.Published = true
	published := demandData[service.ShowcaseReceipt](t, f.request(t, "admin", "PUT", "/api/demand-showcase-management/"+made.Item.ID, edit, 200))
	require.EqualValues(t, 2, published.Item.Revision)
	replay := demandData[service.ShowcaseReceipt](t, f.request(t, "admin", "PUT", "/api/demand-showcase-management/"+made.Item.ID, edit, 200))
	require.True(t, replay.Replayed)
	require.Equal(t, published.Item.Revision, replay.Item.Revision)
	detail := f.request(t, "", "GET", "/api/product/demand-showcases/"+made.Item.ID, nil, 200)
	for _, secret := range []string{"creator_id", "client_request_id", "payload_hash", "synthetic", "seed_batch", "provenance", "object_key", "username", "phone_e164", "uploaded_bytes"} {
		assert.NotContains(t, string(detail), `"`+secret+`"`)
	}
	for _, m := range published.Item.Media {
		path := showcaseMediaPath(t, m.URL)
		raw := f.request(t, "", "GET", path, nil, 200)
		expected := picture
		if m.Kind == "video" {
			expected = videoBytes
		}
		require.Equal(t, expected, raw)
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Range", "bytes=0-31")
		req.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, 206, w.Code)
		require.Equal(t, expected[:32], w.Body.Bytes())
		require.Empty(t, w.Header().Get("Content-Encoding"))
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		head := httptest.NewRecorder()
		headReq := httptest.NewRequest("HEAD", path, nil)
		headReq.Header.Set("Accept-Encoding", "gzip")
		f.engine.ServeHTTP(head, headReq)
		require.Equal(t, 200, head.Code)
		require.Empty(t, head.Body.Bytes())
	}
	hidden := edit
	hidden.ClientActionID = uuid.NewString()
	hidden.Revision = published.Item.Revision
	hidden.Published = false
	f.request(t, "admin", "PUT", "/api/demand-showcase-management/"+made.Item.ID, hidden, 200)
	f.request(t, "", "GET", showcaseMediaPath(t, published.Item.CoverURL), nil, 404)
	f.request(t, "", "GET", "/api/product/demand-showcases/"+made.Item.ID, nil, 404)
	original := demandData[service.ShowcaseReceipt](t, f.request(t, "admin", "POST", "/api/demand-showcase-management", input, 200))
	require.True(t, original.Replayed)
	require.Equal(t, made.Item.ID, original.Item.ID)
	require.False(t, original.Item.Published)
}
func TestShowcaseRejectsPrivateUploadsAndPermanentScopeRebinding(t *testing.T) {
	f := newShowcaseFixture(t)
	raw := communityPNG(t)
	private := f.upload(t, "owner", "customer-private.png", "image/png", raw)
	f.request(t, "admin", "POST", "/api/demand-showcase-management", showcaseInput(private.ID), 400)
	adminPrivate := f.upload(t, "admin", "admin-private.png", "image/png", raw)
	f.request(t, "admin", "POST", "/api/demand-showcase-management", showcaseInput(adminPrivate.ID), 400)
	var row model.DemandAttachment
	require.NoError(t, f.db.First(&row, "id = ?", adminPrivate.ID).Error)
	f.request(t, "admin", "POST", "/api/demand-showcase-management/uploads", service.DemandUploadCreate{ClientUploadID: row.ClientUploadID, Name: row.Name, MimeType: row.MimeType, SizeBytes: row.SizeBytes}, 409)
	public := f.showcaseUpload(t, "image/png", raw)
	f.request(t, "admin", "POST", "/api/demand-management/customer-profiles", service.DemandCustomerProfileView{DemandCustomerProfile: model.DemandCustomerProfile{Code: "forbidden-case-avatar", DisplayName: "内部客户", AvatarAttachmentID: public.ID}}, 400)
	f.request(t, "admin", "POST", "/api/demand-management/service-profiles", service.DemandServiceProfileView{DemandServiceProfile: model.DemandServiceProfile{Code: "forbidden-case-public-avatar", DisplayName: "服务入口", ServiceDirection: "合成接口", AvatarAttachmentID: public.ID}, CategoryIDs: []string{"software"}}, 400)
	f.request(t, "admin", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: uuid.NewString(), EngineerID: f.engineers["engineer"], Text: "不得重新绑定公开素材为私聊", AttachmentIDs: []string{public.ID}}, 409)
	f.request(t, "owner", "POST", "/api/demand-showcase-management", showcaseInput(public.ID), 403)
	f.request(t, "", "GET", "/api/product/demand-showcases/assets/"+uuid.NewString()+"/"+private.ID+"/content", nil, 404)
}
func TestShowcasePaginationFilterAndConcurrentOriginalCreate(t *testing.T) {
	f := newShowcaseFixture(t)
	image := f.showcaseUpload(t, "image/png", communityPNG(t))
	in := showcaseInput(image.ID)
	in.Published = true
	actor := service.DemandActor{UserID: f.users["admin"].Id, Admin: true}
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { out, err := service.CreateShowcase(f.db, actor, in); ids <- out.Item.ID; errs <- err })
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	require.Len(t, unique, 1)
	for _, source := range []string{"https://public.example/?access_token=not-allowed", "https://credential@public.example/", "javascript:alert(1)", "https://public.example/?sig=azure-sas", "https://public.example/?X-Amz-Signature=aws-signed", "https://public.example/?X-Amz-Credential=aws-credential", "https://public.example/?X-Amz-Security-Token=aws-temporary", "https://public.example/?X-Goog-Signature=google-signed", "https://public.example/?q=ok&bad=%ZZ", "https://public.example/?q=ok;sig=secret"} {
		invalid := in
		invalid.ClientRequestID = uuid.NewString()
		invalid.SourceURL = source
		f.request(t, "admin", "POST", "/api/demand-showcase-management", invalid, 400)
	}
	badName := in
	badName.ClientRequestID = uuid.NewString()
	badName.SourceName = "182****5937"
	f.request(t, "admin", "POST", "/api/demand-showcase-management", badName, 400)
	changed := in
	changed.Title = "同UUID换正文"
	f.request(t, "admin", "POST", "/api/demand-showcase-management", changed, 409)
	second := in
	second.ClientRequestID = uuid.NewString()
	second.Title = "独立交互原型"
	second.SourceURL = "https://public.example/source?q=booking&filter=upcoming&utm_source=showcase"
	madeSecond := demandData[service.ShowcaseReceipt](t, f.request(t, "admin", "POST", "/api/demand-showcase-management", second, 200))
	publicSecond := f.request(t, "", "GET", "/api/product/demand-showcases/"+madeSecond.Item.ID, nil, 200)
	require.Contains(t, string(publicSecond), "utm_source=showcase")
	one := demandData[service.ShowcasePage](t, f.request(t, "", "GET", "/api/product/demand-showcases?limit=1", nil, 200))
	require.EqualValues(t, 2, one.TotalCount)
	require.True(t, one.HasMore)
	require.NotEmpty(t, one.NextCursor)
	two := demandData[service.ShowcasePage](t, f.request(t, "", "GET", "/api/product/demand-showcases?limit=1&cursor="+url.QueryEscape(one.NextCursor), nil, 200))
	require.False(t, two.HasMore)
	f.request(t, "", "GET", "/api/product/demand-showcases?category=pc&cursor="+url.QueryEscape(one.NextCursor), nil, 400)
	f.request(t, "", "GET", "/api/product/demand-showcases?limit=101", nil, 400)
	f.request(t, "", "GET", "/api/product/demand-showcases?published=false", nil, 400)
	f.request(t, "", "GET", "/api/product/demand-showcases?query=%25", nil, 200)
	count := int64(0)
	require.NoError(t, f.db.Model(&model.DemandShowcase{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
	// Two repeated migrations must preserve publication, hashes, and original IDs.
	require.NoError(t, f.db.AutoMigrate(model.DemandShowcaseTables()...))
	require.NoError(t, f.db.AutoMigrate(model.DemandShowcaseTables()...))
	after := int64(0)
	require.NoError(t, f.db.Model(&model.DemandShowcase{}).Count(&after).Error)
	require.Equal(t, count, after)
}
