package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newAvatarFixture(t *testing.T) *demandFixture {
	f := newShowcaseFixture(t)
	require.NoError(t, f.db.Migrator().DropTable(model.ForumTables()...))
	require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	t.Cleanup(func() { require.NoError(t, f.db.Migrator().DropTable(model.ForumTables()...)) })
	SetForumRoutes(f.engine.Group("/api"))
	f.enableForum(t)
	return f
}
func (f *demandFixture) avatarUpload(t *testing.T, who string, raw []byte) string {
	t.Helper()
	prefix := "/api/product/forum/profile/avatar/uploads"
	in := service.ForumUploadCreate{ClientUploadID: uuid.NewString(), Name: "private-original-avatar.png", MimeType: "image/png", SizeBytes: int64(len(raw))}
	v := demandData[forumUploadReceipt](t, f.request(t, who, "POST", prefix, in, 200))
	require.Nil(t, v.Attachment)
	req := httptest.NewRequest("PUT", prefix+"/"+v.ID+"/chunks", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+f.tokens[who])
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Upload-Offset", "0")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	sum := sha256.Sum256(raw)
	done := demandData[forumUploadReceipt](t, f.request(t, who, "POST", prefix+"/"+v.ID+"/complete", map[string]string{"sha256": hex.EncodeToString(sum[:])}, 200))
	require.NotNil(t, done.Attachment)
	return v.ID
}
func TestForumProfileAvatarPurposeBindingLegacyPreservationAndRevocation(t *testing.T) {
	f := newAvatarFixture(t)
	raw := communityPNG(t)
	f.request(t, "owner", "PATCH", "/api/product/forum/profile", map[string]any{"display_name": "青禾", "revision": 0}, 200)
	id := f.avatarUpload(t, "owner", raw)
	f.request(t, "other", "POST", "/api/product/forum/attachments/"+id+"/link", map[string]string{"disposition": "inline"}, 404)
	in := service.ForumProfileAvatarInput{ClientActionID: uuid.NewString(), Revision: 1, AvatarAttachmentID: &id}
	type receipt struct {
		Profile  service.ForumProfileView `json:"profile"`
		Replayed bool                     `json:"replayed"`
	}
	saved := demandData[receipt](t, f.request(t, "owner", "PUT", "/api/product/forum/profile/avatar", in, 200))
	require.EqualValues(t, 2, saved.Profile.Revision)
	require.NotNil(t, saved.Profile.Avatar)
	again := demandData[receipt](t, f.request(t, "owner", "PUT", "/api/product/forum/profile/avatar", in, 200))
	require.True(t, again.Replayed)
	require.EqualValues(t, 2, again.Profile.Revision)
	legacy := f.request(t, "owner", "GET", "/api/product/forum/profile", nil, 200)
	require.NotContains(t, string(legacy), `"avatar"`)
	post := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts?profile_avatar_version=1", service.ForumCreate{ClientRequestID: uuid.NewString(), Title: "清晰的信息入口", Body: "讨论信息层级"}, 201))
	require.NotNil(t, post.Post.Author.Avatar)
	oldPosts := f.request(t, "other", "GET", "/api/product/forum/posts", nil, 200)
	require.NotContains(t, string(oldPosts), `"avatar"`)
	viewed := demandData[forumPostReceipt](t, f.request(t, "other", "GET", "/api/product/forum/posts/"+post.Post.ID+"?profile_avatar_version=1", nil, 200))
	require.NotNil(t, viewed.Post.Author.Avatar)
	link, _ := url.Parse(viewed.Post.Author.Avatar.URL)
	r := httptest.NewRequest("GET", link.RequestURI(), nil)
	r.Header.Set("Range", "bytes=0-31")
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, r)
	require.Equal(t, 206, w.Code)
	require.Equal(t, raw[:32], w.Body.Bytes())
	require.Empty(t, w.Header().Get("Content-Encoding"))
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	f.request(t, "owner", "PATCH", "/api/product/forum/profile", map[string]any{"display_name": "青禾设计", "revision": 2}, 200)
	current := demandData[receipt](t, f.request(t, "owner", "GET", "/api/product/forum/profile?profile_avatar_version=1", nil, 200))
	require.EqualValues(t, 3, current.Profile.Revision)
	require.NotNil(t, current.Profile.Avatar)
	require.Equal(t, id, current.Profile.Avatar.ID)
	f.request(t, "", "GET", link.RequestURI(), nil, 404)
	empty := ""
	clear := service.ForumProfileAvatarInput{ClientActionID: uuid.NewString(), Revision: 3, AvatarAttachmentID: &empty}
	f.request(t, "owner", "PUT", "/api/product/forum/profile/avatar", clear, 200)
	cleared := demandData[receipt](t, f.request(t, "owner", "PUT", "/api/product/forum/profile/avatar", in, 200))
	require.True(t, cleared.Replayed)
	require.Nil(t, cleared.Profile.Avatar)
	rebound := in
	rebound.ClientActionID = uuid.NewString()
	rebound.Revision = 4
	f.request(t, "owner", "PUT", "/api/product/forum/profile/avatar", rebound, 200)
	f.request(t, "", "GET", link.RequestURI(), nil, 404)
	latest := demandData[receipt](t, f.request(t, "owner", "GET", "/api/product/forum/profile?profile_avatar_version=1", nil, 200))
	u, _ := url.Parse(latest.Profile.Avatar.URL)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["owner"].Id).Update("status", common.UserStatusDisabled).Error)
	f.request(t, "", "GET", u.RequestURI(), nil, 404)
}
func TestForumProfileAvatarRejectsMessageScopeAndMigratesOldProfile(t *testing.T) {
	f := newAvatarFixture(t)
	raw := communityPNG(t)
	key := uuid.NewString()
	before := int64(0)
	require.NoError(t, f.db.Model(&model.ForumAttachment{}).Count(&before).Error)
	f.request(t, "owner", "POST", "/api/product/forum/profile/avatar/uploads", service.ForumUploadCreate{ClientUploadID: key, Name: "not-avatar.mp4", MimeType: "video/mp4", SizeBytes: 30}, 415)
	f.request(t, "owner", "POST", "/api/product/forum/uploads", service.ForumUploadCreate{ClientUploadID: key, TargetKind: "profile-avatar", Name: "not-normal.png", MimeType: "image/png", SizeBytes: int64(len(raw))}, 400)
	after := int64(0)
	require.NoError(t, f.db.Model(&model.ForumAttachment{}).Count(&after).Error)
	require.Equal(t, before, after)
	private := f.forumUpload(t, "owner", uuid.NewString(), "post", "", "image/png", raw)
	f.request(t, "owner", "PUT", "/api/product/forum/profile/avatar", service.ForumProfileAvatarInput{ClientActionID: uuid.NewString(), Revision: 0, AvatarAttachmentID: &private}, 400)
	avatar := f.avatarUpload(t, "owner", raw)
	f.request(t, "other", "PUT", "/api/product/forum/profile/avatar", service.ForumProfileAvatarInput{ClientActionID: uuid.NewString(), Revision: 0, AvatarAttachmentID: &avatar}, 400)
	f.request(t, "owner", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: uuid.NewString(), Title: "不能复用头像作为正文", Body: "原目的保留", AttachmentIDs: []string{avatar}}, 400)
	count := int64(0)
	require.NoError(t, f.db.Model(&model.ForumPost{}).Count(&count).Error)
	require.Zero(t, count)
	// Simulate the actual old profile schema, then apply the additive migration twice.
	require.NoError(t, f.db.Migrator().DropColumn(&model.ForumProfile{}, "AvatarAttachmentID"))
	require.NoError(t, f.db.Exec("INSERT INTO forum_profiles (user_id, display_name, revision) VALUES (?, ?, ?)", f.users["other"].Id, "原昵称", 7).Error)
	require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	var row model.ForumProfile
	require.NoError(t, f.db.First(&row, "user_id = ?", f.users["other"].Id).Error)
	require.Equal(t, "原昵称", row.DisplayName)
	require.EqualValues(t, 7, row.Revision)
	require.Empty(t, row.AvatarAttachmentID)
}
