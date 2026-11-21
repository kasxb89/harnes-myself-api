package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func (f *demandFixture) serviceGalleryUpload(t *testing.T, key, mime string, content []byte) model.DemandAttachment {
	t.Helper()
	in := service.DemandUploadCreate{ClientUploadID: key, Name: "private-person-original", MimeType: mime, SizeBytes: int64(len(content))}
	first := demandData[service.DemandUploadResult](t, f.request(t, "admin", "POST", "/api/demand-management/service-media/uploads", in, 200))
	replay := demandData[service.DemandUploadResult](t, f.request(t, "admin", "POST", "/api/demand-management/service-media/uploads", in, 200))
	require.Equal(t, first.Attachment.ID, replay.Attachment.ID)
	req := httptest.NewRequest("PUT", "/api/demand-management/service-media/uploads/"+first.Attachment.ID+"/chunks", bytes.NewReader(content))
	req.Header.Set("Authorization", "Bearer "+f.tokens["admin"])
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Upload-Offset", "0")
	recorder := httptest.NewRecorder()
	f.engine.ServeHTTP(recorder, req)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	digest := sha256.Sum256(content)
	ready := demandData[service.DemandUploadResult](t, f.request(t, "admin", "POST", "/api/demand-management/service-media/uploads/"+first.Attachment.ID+"/complete", map[string]string{"sha256": hex.EncodeToString(digest[:])}, 200))
	require.Equal(t, "ready", ready.Attachment.Status)
	return ready.Attachment
}
func (f *demandFixture) newServiceProfile(t *testing.T, code string) service.DemandServiceProfileView {
	t.Helper()
	input := service.DemandServiceProfileView{DemandServiceProfile: model.DemandServiceProfile{Code: code, DisplayName: code, ServiceDirection: "A local service offering", Published: true, Enabled: true}, CategoryIDs: []string{"software"}}
	return demandData[service.DemandServiceProfileView](t, f.request(t, "admin", "POST", "/api/demand-management/service-profiles", input, 200))
}
func TestDemandServiceGalleryMediaAndScopedRevocation(t *testing.T) {
	f := newShowcaseFixture(t)
	p := f.newServiceProfile(t, "service-gallery-one")
	second := f.newServiceProfile(t, "service-gallery-two")
	image := f.serviceGalleryUpload(t, uuid.NewString(), "image/png", communityPNG(t))
	videoBytes, err := os.ReadFile("testdata/community-synthetic.mp4")
	require.NoError(t, err)
	video := f.serviceGalleryUpload(t, uuid.NewString(), "video/mp4", videoBytes)
	input := service.DemandServiceGalleryInput{ClientActionID: uuid.NewString(), CoverAttachmentID: image.ID, Media: []service.DemandServiceGalleryEntry{{AttachmentID: image.ID}, {AttachmentID: video.ID, PosterAttachmentID: image.ID}}}
	type saved struct {
		Gallery  service.DemandServiceGalleryView `json:"gallery"`
		Replayed bool                             `json:"replayed"`
	}
	first := demandData[saved](t, f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID+"/media", input, 200))
	require.False(t, first.Replayed)
	require.EqualValues(t, 1, first.Gallery.Revision)
	again := demandData[saved](t, f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID+"/media", input, 200))
	require.True(t, again.Replayed)
	require.Equal(t, first.Gallery.Revision, again.Gallery.Revision)
	input.ClientActionID = uuid.NewString()
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+second.ID+"/media", input, 200)
	old := f.request(t, "owner", "GET", "/api/product/demands/service-profiles", nil, 200)
	require.NotContains(t, string(old), `"gallery"`)
	current := f.request(t, "owner", "GET", "/api/product/demands/service-profiles?include_media=1", nil, 200)
	require.Contains(t, string(current), `"gallery"`)
	require.NotContains(t, string(current), "private-person-original")
	gallery := demandData[service.DemandServiceGalleryView](t, f.request(t, "owner", "GET", "/api/product/demands/service-profiles/"+p.ID+"/media", nil, 200))
	require.Len(t, gallery.Media, 2)
	require.NotEmpty(t, gallery.CoverURL)
	require.NotEmpty(t, gallery.Media[1].PosterURL)
	target, err := url.Parse(gallery.Media[1].URL)
	require.NoError(t, err)
	for _, method := range []string{"GET", "HEAD"} {
		req := httptest.NewRequest(method, target.RequestURI(), nil)
		req.Header.Set("Range", "bytes=0-31")
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, 206, w.Code, w.Body.String())
		if method == "GET" {
			require.Equal(t, videoBytes[:32], w.Body.Bytes())
		}
	}
	p.Introduction = "An administrator edit keeps the existing gallery"
	p = demandData[service.DemandServiceProfileView](t, f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID, p, 200))
	kept := demandData[service.DemandServiceGalleryView](t, f.request(t, "owner", "GET", "/api/product/demands/service-profiles/"+p.ID+"/media", nil, 200))
	require.Equal(t, gallery.Revision, kept.Revision)
	require.Len(t, kept.Media, 2)
	p.Published = false
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID, p, 200)
	for _, method := range []string{"GET", "HEAD"} {
		req := httptest.NewRequest(method, target.RequestURI(), nil)
		req.Header.Set("Range", "bytes=0-31")
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, 404, w.Code)
	}
	f.request(t, "owner", "POST", "/api/product/demands/service-profiles/"+p.ID+"/media/"+video.ID+"/link", map[string]string{"disposition": "inline"}, 404)
	other := demandData[service.DemandServiceGalleryView](t, f.request(t, "owner", "GET", "/api/product/demands/service-profiles/"+second.ID+"/media", nil, 200))
	u, err := url.Parse(other.Media[1].URL)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", u.RequestURI(), nil)
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Equal(t, videoBytes, w.Body.Bytes())
	var receipts int64
	require.NoError(t, f.db.Model(&model.DemandServiceGalleryReceipt{}).Count(&receipts).Error)
	require.EqualValues(t, 2, receipts)
}
func TestDemandServiceGalleryRejectsPrivateUploadsAndKeepsReservation(t *testing.T) {
	f := newShowcaseFixture(t)
	p := f.newServiceProfile(t, "gallery-private-boundary")
	private := f.upload(t, "admin", "private-chat-image.png", "image/png", communityPNG(t))
	input := service.DemandServiceGalleryInput{ClientActionID: uuid.NewString(), CoverAttachmentID: private.ID, Media: []service.DemandServiceGalleryEntry{{AttachmentID: private.ID}}}
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID+"/media", input, 400)
	// The public upload DTO correctly omits its original private operation key.
	// Load that existing synthetic row to test a genuine cross-purpose replay.
	require.NoError(t, f.db.First(&private, "id = ?", private.ID).Error)
	create := service.DemandUploadCreate{ClientUploadID: private.ClientUploadID, Name: private.Name, MimeType: private.MimeType, SizeBytes: private.SizeBytes}
	f.request(t, "admin", "POST", "/api/demand-management/service-media/uploads", create, 409)
	asset := f.serviceGalleryUpload(t, uuid.NewString(), "image/png", communityPNG(t))
	p.AvatarAttachmentID = asset.ID
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID, p, 400)
	p.AvatarAttachmentID = ""
	input.ClientActionID = uuid.NewString()
	input.CoverAttachmentID = asset.ID
	input.Media = []service.DemandServiceGalleryEntry{{AttachmentID: asset.ID}}
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID+"/media", input, 200)
	input.Revision = 1
	input.ClientActionID = uuid.NewString()
	input.CoverAttachmentID = ""
	input.Media = []service.DemandServiceGalleryEntry{}
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID+"/media", input, 200)
	p.AvatarAttachmentID = asset.ID
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID, p, 400)
	customer := service.DemandCustomerProfileView{DemandCustomerProfile: model.DemandCustomerProfile{Code: "internal-private-avatar", DisplayName: "Internal client", AvatarAttachmentID: asset.ID}}
	f.request(t, "admin", "POST", "/api/demand-management/customer-profiles", customer, 400)
	var original model.DemandAttachment
	require.NoError(t, f.db.First(&original, "id = ?", asset.ID).Error)
	require.Equal(t, "service-gallery", original.AvatarScope)
	require.True(t, original.AvatarReserved)
	f.request(t, "owner", "PUT", "/api/demand-management/service-profiles/"+p.ID+"/media", input, 403)
}
