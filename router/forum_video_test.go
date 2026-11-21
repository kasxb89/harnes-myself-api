package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func (f *demandFixture) forumVideo(t *testing.T, actor, key, kind, post string, data []byte) string {
	t.Helper()
	in := service.ForumUploadCreate{ClientUploadID: key, TargetKind: kind, PostID: post, Name: "private-original-person.mp4", MimeType: "video/mp4", SizeBytes: int64(len(data))}
	v := demandData[forumUploadReceipt](t, f.request(t, actor, "POST", "/api/product/forum/uploads?media_version=2", in, 200))
	require.Nil(t, v.Attachment)
	again := demandData[forumUploadReceipt](t, f.request(t, actor, "POST", "/api/product/forum/uploads?media_version=2", in, 200))
	require.Equal(t, v.ID, again.ID)
	for attempt := range 2 {
		req := httptest.NewRequest("PUT", "/api/product/forum/uploads/"+v.ID+"/chunks?media_version=2", bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+f.tokens[actor])
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Upload-Offset", "0")
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		if attempt == 0 {
			require.Equal(t, 200, w.Code, w.Body.String())
		} else {
			require.Equal(t, 409, w.Code, w.Body.String())
			// Recover the same committed upload, not a replacement operation.
			current := demandData[forumUploadReceipt](t, f.request(t, actor, "GET", "/api/product/forum/uploads/"+v.ID+"?media_version=2", nil, 200))
			require.EqualValues(t, len(data), current.Offset)
		}
	}
	var parts int64
	require.NoError(t, f.db.Model(&model.ForumUploadPart{}).Where("upload_id = ?", v.ID).Count(&parts).Error)
	require.EqualValues(t, 1, parts, "the original duplicate chunk cannot append another part")
	sum := sha256.Sum256(data)
	ready := demandData[forumUploadReceipt](t, f.request(t, actor, "POST", "/api/product/forum/uploads/"+v.ID+"/complete?media_version=2", map[string]string{"sha256": hex.EncodeToString(sum[:])}, 200))
	require.NotNil(t, ready.Attachment)
	require.Equal(t, "video", ready.Attachment.Kind)
	require.Equal(t, "forum-video.mp4", ready.Attachment.Name)
	return v.ID
}
func newForumVideoFixture(t *testing.T) *demandFixture {
	t.Helper()
	f := newShowcaseFixture(t)
	require.NoError(t, f.db.Migrator().DropTable(model.ForumTables()...))
	require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	t.Cleanup(func() { require.NoError(t, f.db.Migrator().DropTable(model.ForumTables()...)) })
	SetForumRoutes(f.engine.Group("/api"))
	f.enableForum(t)
	return f
}
func TestForumVideoNegotiatedWireAndRevocation(t *testing.T) {
	f := newForumVideoFixture(t)
	oldConfig := f.request(t, "owner", "GET", "/api/product/forum/config", nil, 200)
	require.NotContains(t, string(oldConfig), "video/mp4")
	require.NotContains(t, string(oldConfig), "max_video_bytes")
	current := f.request(t, "owner", "GET", "/api/product/forum/config?media_version=2", nil, 200)
	require.Contains(t, string(current), "video/mp4")
	require.Contains(t, string(current), "forum_media_v2")
	data, err := os.ReadFile("testdata/community-synthetic.mp4")
	require.NoError(t, err)
	legacyUpload := service.ForumUploadCreate{ClientUploadID: uuid.NewString(), TargetKind: "post", Name: "test.mp4", MimeType: "video/mp4", SizeBytes: int64(len(data))}
	f.request(t, "owner", "POST", "/api/product/forum/uploads", legacyUpload, 415)
	var count int64
	require.NoError(t, f.db.Model(&model.ForumAttachment{}).Count(&count).Error)
	require.Zero(t, count)
	video := f.forumVideo(t, "owner", legacyUpload.ClientUploadID, "post", "", data)
	image := f.forumUpload(t, "owner", uuid.NewString(), "post", "", "image/png", communityPNG(t))
	in := service.ForumCreate{ClientRequestID: uuid.NewString(), Title: "Video and stills", Body: "A local synthetic workflow", AttachmentIDs: []string{image, video}}
	first := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts?media_version=2", in, 201))
	require.Len(t, first.Post.Attachments, 1)
	require.Len(t, first.Post.Media, 2)
	require.Equal(t, "video", first.Post.Media[1].Kind)
	again := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts?media_version=2", in, 200))
	require.True(t, again.Replayed)
	require.Equal(t, first.Post.ID, again.Post.ID)
	legacy := f.request(t, "other", "GET", "/api/product/forum/posts", nil, 200)
	require.NotContains(t, string(legacy), `"media"`)
	require.NotContains(t, string(legacy), `"kind":"video"`)
	require.NotContains(t, string(legacy), "private-original-person")
	me := demandData[forumPostPage](t, f.request(t, "owner", "GET", "/api/product/forum/me/posts?media_version=2", nil, 200))
	require.Len(t, me.Items[0].Media, 2)
	commentVideo := f.forumVideo(t, "other", uuid.NewString(), "comment", first.Post.ID, data)
	comment := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", "/api/product/forum/posts/"+first.Post.ID+"/comments?media_version=2", service.ForumCreate{ClientRequestID: uuid.NewString(), AttachmentIDs: []string{commentVideo}}, 201))
	require.Empty(t, comment.Comment.Attachments)
	require.Len(t, comment.Comment.Media, 1)
	link := demandData[service.ForumAttachmentLink](t, f.request(t, "owner", "POST", "/api/product/forum/attachments/"+commentVideo+"/link", map[string]string{"disposition": "inline"}, 200))
	target, err := url.Parse(link.URL)
	require.NoError(t, err)
	for _, method := range []string{"GET", "HEAD"} {
		request := httptest.NewRequest(method, target.RequestURI(), nil)
		request.Header.Set("Range", "bytes=0-31")
		recorder := httptest.NewRecorder()
		f.engine.ServeHTTP(recorder, request)
		require.Equal(t, 206, recorder.Code)
		require.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
		if method == "GET" {
			require.Equal(t, data[:32], recorder.Body.Bytes())
		}
	}
	f.request(t, "admin", "DELETE", "/api/forum-management/comments/"+comment.Comment.ID, map[string]string{"reason": "synthetic moderation"}, 200)
	for _, method := range []string{"GET", "HEAD"} {
		request := httptest.NewRequest(method, target.RequestURI(), nil)
		request.Header.Set("Range", "bytes=0-31")
		recorder := httptest.NewRecorder()
		f.engine.ServeHTTP(recorder, request)
		require.Equal(t, 404, recorder.Code)
	}
	var posts int64
	require.NoError(t, f.db.Model(&model.ForumPost{}).Count(&posts).Error)
	require.EqualValues(t, 1, posts)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(current, &decoded))
}

func TestForumVideoRequiresNegotiationBeforeWritesAndKeepsOriginalReceipts(t *testing.T) {
	f := newForumVideoFixture(t)
	data, err := os.ReadFile("testdata/community-synthetic.mp4")
	require.NoError(t, err)
	in := service.ForumUploadCreate{ClientUploadID: uuid.NewString(), TargetKind: "post", Name: "local.mp4", MimeType: "video/mp4", SizeBytes: int64(len(data))}
	upload := demandData[forumUploadReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/uploads?media_version=2", in, 200))
	sendChunk := func(mode string, status int) {
		req := httptest.NewRequest("PUT", "/api/product/forum/uploads/"+upload.ID+"/chunks"+mode, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+f.tokens["owner"])
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Upload-Offset", "0")
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, w.Body.String())
	}
	sendChunk("", 415)
	var row model.ForumAttachment
	require.NoError(t, f.db.First(&row, "id = ?", upload.ID).Error)
	require.Zero(t, row.UploadedBytes)
	var parts int64
	require.NoError(t, f.db.Model(&model.ForumUploadPart{}).Where("upload_id = ?", upload.ID).Count(&parts).Error)
	require.Zero(t, parts)
	sendChunk("?media_version=2", 200)
	sum := sha256.Sum256(data)
	complete := map[string]string{"sha256": hex.EncodeToString(sum[:])}
	f.request(t, "owner", "POST", "/api/product/forum/uploads/"+upload.ID+"/complete", complete, 415)
	require.NoError(t, f.db.First(&row, "id = ?", upload.ID).Error)
	require.Equal(t, "uploading", row.Status)
	require.Empty(t, row.SHA256)
	f.request(t, "owner", "POST", "/api/product/forum/uploads/"+upload.ID+"/complete?media_version=2", complete, 200)
	publication := service.ForumCreate{ClientRequestID: uuid.NewString(), Title: "Negotiated local video", AttachmentIDs: []string{upload.ID}}
	f.request(t, "owner", "POST", "/api/product/forum/posts", publication, 415)
	var count int64
	require.NoError(t, f.db.Model(&model.ForumPost{}).Count(&count).Error)
	require.Zero(t, count)
	first := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts?media_version=2", publication, 201))
	replay := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", publication, 200))
	require.True(t, replay.Replayed)
	require.Equal(t, first.Post.ID, replay.Post.ID)
	commentUpload := f.forumVideo(t, "other", uuid.NewString(), "comment", first.Post.ID, data)
	commentIn := service.ForumCreate{ClientRequestID: uuid.NewString(), AttachmentIDs: []string{commentUpload}}
	endpoint := "/api/product/forum/posts/" + first.Post.ID + "/comments"
	f.request(t, "other", "POST", endpoint, commentIn, 415)
	require.NoError(t, f.db.Model(&model.ForumComment{}).Count(&count).Error)
	require.Zero(t, count)
	comment := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", endpoint+"?media_version=2", commentIn, 201))
	commentReplay := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", endpoint, commentIn, 200))
	require.True(t, commentReplay.Replayed)
	require.Equal(t, comment.Comment.ID, commentReplay.Comment.ID)
	require.NoError(t, f.db.Model(&model.ForumPost{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, f.db.Model(&model.ForumComment{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
