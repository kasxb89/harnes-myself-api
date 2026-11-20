package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/gif"
	"image/jpeg"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newForumFixture(t *testing.T) *demandFixture {
	t.Helper()
	f := newDemandFixture(t)
	if os.Getenv("DEMAND_TEST_POSTGRES_DSN") != "" || os.Getenv("DEMAND_TEST_MYSQL_DSN") != "" {
		require.NoError(t, f.db.Migrator().DropTable(model.ForumTables()...))
		t.Cleanup(func() { require.NoError(t, f.db.Migrator().DropTable(model.ForumTables()...)) })
	}
	require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	SetForumRoutes(f.engine.Group("/api"))
	return f
}
func (f *demandFixture) enableForum(t *testing.T) {
	t.Helper()
	f.request(t, "admin", "PUT", "/api/forum-management/config", service.ForumConfig{Enabled: true}, 200)
}

type forumPostReceipt struct {
	Post     service.ForumPostView `json:"post"`
	Replayed bool                  `json:"replayed"`
}
type forumCommentReceipt struct {
	Comment  service.ForumCommentView `json:"comment"`
	Replayed bool                     `json:"replayed"`
}
type forumPostPage struct {
	Items []service.ForumPostView `json:"items"`
	Next  string                  `json:"next_cursor"`
	More  bool                    `json:"has_more"`
}
type forumCommentPage struct {
	Items []service.ForumCommentView `json:"items"`
	Next  string                     `json:"next_cursor"`
	More  bool                       `json:"has_more"`
}

func TestForumImmediatePublicationLikesAndPublicationReceipts(t *testing.T) {
	f := newForumFixture(t)
	input := service.ForumCreate{ClientRequestID: "post-one", Title: "A genuine synthetic discussion", Body: "Public plain text <script>not HTML</script>"}
	f.request(t, "owner", "POST", "/api/product/forum/posts", input, 403)
	f.enableForum(t)
	first := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", input, 201))
	assert.False(t, first.Replayed)
	assert.Zero(t, first.Post.ReviewedAt)
	replay := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", input, 200))
	assert.True(t, replay.Replayed)
	assert.Equal(t, first.Post.ID, replay.Post.ID)
	changed := input
	changed.Body = "Different body"
	f.request(t, "owner", "POST", "/api/product/forum/posts", changed, 409)
	publicRaw := f.request(t, "other", "GET", "/api/product/forum/posts", nil, 200)
	page := demandData[forumPostPage](t, publicRaw)
	require.Len(t, page.Items, 1)
	assert.Equal(t, input.Body, page.Items[0].Body)
	for _, u := range f.users {
		if u.PhoneE164 != nil {
			assert.NotContains(t, string(publicRaw), *u.PhoneE164)
		}
		assert.NotContains(t, string(publicRaw), u.Username)
	}
	assert.NotContains(t, string(publicRaw), "client_request_id")
	assert.NotContains(t, string(publicRaw), "payload_hash")
	postPath := "/api/product/forum/posts/" + first.Post.ID
	for range 2 {
		liked := demandData[forumPostReceipt](t, f.request(t, "other", "PUT", postPath+"/like", map[string]bool{"liked": true}, 200))
		assert.EqualValues(t, 1, liked.Post.LikeCount)
		assert.True(t, liked.Post.Liked)
	}
	for range 2 {
		unliked := demandData[forumPostReceipt](t, f.request(t, "other", "PUT", postPath+"/like", map[string]bool{"liked": false}, 200))
		assert.Zero(t, unliked.Post.LikeCount)
		assert.False(t, unliked.Post.Liked)
	}
	comment := service.ForumCreate{ClientRequestID: "comment-one", Body: "Published before any moderation"}
	created := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", postPath+"/comments", comment, 201))
	assert.Zero(t, created.Comment.ReviewedAt)
	again := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", postPath+"/comments", comment, 200))
	assert.True(t, again.Replayed)
	assert.Equal(t, created.Comment.ID, again.Comment.ID)
	seen := demandData[forumCommentPage](t, f.request(t, "owner", "GET", postPath+"/comments", nil, 200))
	require.Len(t, seen.Items, 1)
	assert.Equal(t, comment.Body, seen.Items[0].Body)
	admin := demandData[forumCommentPage](t, f.request(t, "admin", "GET", "/api/forum-management/comments?review=unreviewed", nil, 200))
	require.Len(t, admin.Items, 1)
	assert.Equal(t, input.Title, admin.Items[0].PostTitle)
	f.request(t, "admin", "PUT", "/api/forum-management/comments/"+created.Comment.ID+"/review", map[string]bool{"reviewed": true}, 200)
	seen = demandData[forumCommentPage](t, f.request(t, "owner", "GET", postPath+"/comments", nil, 200))
	require.Len(t, seen.Items, 1)
	admin = demandData[forumCommentPage](t, f.request(t, "admin", "GET", "/api/forum-management/comments?review=reviewed", nil, 200))
	require.Len(t, admin.Items, 1)
	assert.Positive(t, admin.Items[0].ReviewedAt)
	changedComment := comment
	changedComment.Body = "Mismatched retry"
	f.request(t, "other", "POST", postPath+"/comments", changedComment, 409)
}

func TestForumAdministratorRemovalRevokesVisibilityAndRequiresWritePermission(t *testing.T) {
	f := newForumFixture(t)
	f.enableForum(t)
	post := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: "moderate-post", Title: "Moderation test", Body: "Original content retained only for moderators after removal"}, 201)).Post
	path := "/api/product/forum/posts/" + post.ID
	c := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", path+"/comments", service.ForumCreate{ClientRequestID: "moderate-comment", Body: "Comment for removal"}, 201)).Comment
	f.request(t, "owner", "DELETE", "/api/forum-management/posts/"+post.ID, nil, 403)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["admin"].Id).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, authz.PermissionsMap{"forum": {"read": true, "write": false}}))
	f.request(t, "admin", "GET", "/api/forum-management/posts?review=unreviewed", nil, 200)
	f.request(t, "admin", "DELETE", "/api/forum-management/posts/"+post.ID, nil, 403)
	f.request(t, "admin", "PUT", "/api/forum-management/comments/"+c.ID+"/review", map[string]bool{"reviewed": true}, 403)
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, authz.PermissionsMap{"forum": {"read": true, "write": true}}))
	for range 2 {
		f.request(t, "admin", "DELETE", "/api/forum-management/comments/"+c.ID, map[string]string{"reason": "Unwanted comment"}, 200)
	}
	comments := demandData[forumCommentPage](t, f.request(t, "owner", "GET", path+"/comments", nil, 200))
	assert.Empty(t, comments.Items)
	current := demandData[forumPostReceipt](t, f.request(t, "other", "GET", path, nil, 200))
	assert.Zero(t, current.Post.CommentCount)
	var events int64
	require.NoError(t, f.db.Model(&model.ForumAudit{}).Where("target_id = ? AND action = ?", c.ID, "delete").Count(&events).Error)
	assert.EqualValues(t, 1, events)
	for range 2 {
		f.request(t, "admin", "DELETE", "/api/forum-management/posts/"+post.ID, map[string]string{"reason": "Removed by administrator"}, 200)
	}
	f.request(t, "owner", "GET", path, nil, 404)
	f.request(t, "other", "GET", path+"/comments", nil, 404)
	f.request(t, "other", "PUT", path+"/like", map[string]bool{"liked": true}, 404)
	f.request(t, "other", "POST", path+"/comments", service.ForumCreate{ClientRequestID: "after-removal", Body: "Must not publish"}, 404)
	public := demandData[forumPostPage](t, f.request(t, "other", "GET", "/api/product/forum/posts?review=deleted", nil, 200))
	assert.Empty(t, public.Items)
	removed := demandData[forumPostPage](t, f.request(t, "admin", "GET", "/api/forum-management/posts?review=deleted", nil, 200))
	require.Len(t, removed.Items, 1)
	assert.Equal(t, post.Body, removed.Items[0].Body)
	assert.Equal(t, "Removed by administrator", removed.Items[0].DeleteReason)
	require.NoError(t, f.db.Model(&model.ForumAudit{}).Where("target_id = ? AND action = ?", post.ID, "delete").Count(&events).Error)
	assert.EqualValues(t, 1, events)
}

func TestForumPaginationSessionBoundaryAndAdditiveMigration(t *testing.T) {
	f := newForumFixture(t)
	var before []model.User
	require.NoError(t, f.db.Order("id").Find(&before).Error)
	originalDemand := f.create(t, "kept-demand-before-forum")
	require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	var after []model.User
	require.NoError(t, f.db.Order("id").Find(&after).Error)
	assert.Equal(t, before, after)
	f.request(t, "owner", "GET", "/api/product/demands/"+originalDemand.Demand.ID, nil, 200)
	f.request(t, "owner", "GET", "/api/user/self", nil, 200)
	f.enableForum(t)
	for _, key := range []string{"first-page", "second-page"} {
		f.request(t, "owner", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: key, Title: key, Body: "Cursor content"}, 201)
	}
	first := demandData[forumPostPage](t, f.request(t, "other", "GET", "/api/product/forum/posts?limit=1", nil, 200))
	require.Len(t, first.Items, 1)
	assert.True(t, first.More)
	second := demandData[forumPostPage](t, f.request(t, "other", "GET", "/api/product/forum/posts?limit=1&cursor="+first.Next, nil, 200))
	require.Len(t, second.Items, 1)
	assert.False(t, second.More)
	assert.NotEqual(t, first.Items[0].ID, second.Items[0].ID)
	f.request(t, "other", "GET", "/api/product/forum/posts?limit=101", nil, 400)
	f.request(t, "other", "GET", "/api/product/forum/posts?cursor=invalid", nil, 400)
	f.request(t, "owner", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: "too-long", Title: strings.Repeat("字", 201), Body: "No"}, 400)
	raw := "nap_synthetic_forum_session_boundary"
	pat := model.UserAccessToken{Name: "forum-fixture-only", TokenHash: model.AccessTokenFingerprint(raw), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, pat.SetScopes([]string{"*"}))
	require.NoError(t, model.CreateUserAccessToken(f.users["admin"].Id, &pat, 10))
	f.tokens["pat"] = raw
	f.request(t, "pat", "GET", "/api/product/forum/posts", nil, 403)
	f.request(t, "pat", "GET", "/api/forum-management/posts", nil, 403)
	f.request(t, "", "GET", "/api/product/forum/posts", nil, 401)
	f.request(t, "admin", "PUT", "/api/forum-management/config", service.ForumConfig{Revision: 0, Enabled: false}, 409)
	var tasks, logs, funds int64
	require.NoError(t, f.db.Model(&model.Task{}).Count(&tasks).Error)
	require.NoError(t, f.db.Model(&model.Log{}).Count(&logs).Error)
	require.NoError(t, f.db.Model(&model.WalletFundingEvent{}).Count(&funds).Error)
	assert.Zero(t, tasks)
	assert.Zero(t, logs)
	assert.Zero(t, funds)
	var users []model.User
	require.NoError(t, f.db.Order("id").Find(&users).Error)
	for i, u := range users {
		assert.Equal(t, before[i].Quota, u.Quota)
		assert.Equal(t, before[i].AuthVersion, u.AuthVersion)
	}
}

// Fresh opt-in loopback server; never reads production DB or existing manual fixtures.
func TestForumLocalFixtureServer(t *testing.T) {
	address := os.Getenv("FORUM_FIXTURE_ADDR")
	if address == "" {
		t.Skip("opt-in local forum fixture")
	}
	require.True(t, strings.HasPrefix(address, "127.0.0.1:"))
	f := newForumFixture(t)
	f.enableForum(t)
	serveDemandLocalFixture(t, f, address)
}

func TestForumCommentsNewestFirstAndDeletedAuthorProjection(t *testing.T) {
	f := newForumFixture(t)
	f.enableForum(t)
	p := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: "comment-page-post", Title: "Discussion", Body: "Body"}, 201)).Post
	path := "/api/product/forum/posts/" + p.ID + "/comments"
	older := demandData[forumCommentReceipt](t, f.request(t, "owner", "POST", path, service.ForumCreate{ClientRequestID: "older", Body: "Earlier"}, 201)).Comment
	newer := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", path, service.ForumCreate{ClientRequestID: "newer", Body: "Latest"}, 201)).Comment
	require.NoError(t, f.db.Model(&model.ForumComment{}).Where("id = ?", older.ID).Update("created_at", 100).Error)
	require.NoError(t, f.db.Model(&model.ForumComment{}).Where("id = ?", newer.ID).Update("created_at", 200).Error)
	require.NoError(t, f.db.Delete(&model.User{}, f.users["other"].Id).Error)
	var first struct {
		Items []service.ForumCommentView `json:"items"`
		Next  string                     `json:"next_cursor"`
		More  bool                       `json:"has_more"`
	}
	first = demandData[struct {
		Items []service.ForumCommentView `json:"items"`
		Next  string                     `json:"next_cursor"`
		More  bool                       `json:"has_more"`
	}](t, f.request(t, "owner", "GET", path+"?limit=1", nil, 200))
	require.Len(t, first.Items, 1)
	assert.Equal(t, newer.ID, first.Items[0].ID)
	assert.Equal(t, f.users["other"].Id, first.Items[0].Author.ID)
	assert.Empty(t, first.Items[0].Author.DisplayName)
	require.True(t, first.More)
	second := demandData[struct {
		Items []service.ForumCommentView `json:"items"`
	}](t, f.request(t, "owner", "GET", path+"?limit=1&cursor="+first.Next, nil, 200))
	require.Len(t, second.Items, 1)
	assert.Equal(t, older.ID, second.Items[0].ID)
}

func TestForumMigrationKeepsExistingSessionsTasksAndFinance(t *testing.T) {
	f := newForumFixture(t)
	// These unchanged model definitions are from the released backend base.
	task := model.Task{TaskID: "synthetic-original-task", UserId: f.users["owner"].Id, Status: model.TaskStatusUnknown, Quota: 77, Data: []byte(`{"original":"unchanged"}`)}
	event := model.WalletFundingEvent{UserID: f.users["owner"].Id, RequestID: "synthetic-original-charge", Operation: "consume", DeltaQuota: -77, KnownCashRevenueCNY: "0.12"}
	log := model.Log{UserId: f.users["owner"].Id, Content: "synthetic-original-evidence", Quota: 77}
	require.NoError(t, f.db.Create(&task).Error)
	require.NoError(t, f.db.Create(&event).Error)
	require.NoError(t, f.db.Create(&log).Error)
	require.NoError(t, f.db.First(&task, task.ID).Error) // Snapshot the actual native JSON representation before migration.
	var before []model.UserSession
	require.NoError(t, f.db.Order("sid").Find(&before).Error)
	require.NoError(t, f.db.Migrator().DropTable(model.ForumTables()...))
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	}
	var after []model.UserSession
	require.NoError(t, f.db.Order("sid").Find(&after).Error)
	assert.Equal(t, before, after)
	var gotTask model.Task
	var gotEvent model.WalletFundingEvent
	var gotLog model.Log
	require.NoError(t, f.db.First(&gotTask, task.ID).Error)
	require.NoError(t, f.db.First(&gotEvent, event.ID).Error)
	require.NoError(t, f.db.First(&gotLog, log.Id).Error)
	assert.Equal(t, task, gotTask)
	assert.Equal(t, event, gotEvent)
	assert.Equal(t, log, gotLog)
	f.request(t, "owner", "GET", "/api/product/demands/config", nil, 200)
	for _, original := range f.users {
		var now model.User
		require.NoError(t, f.db.First(&now, original.Id).Error)
		assert.Equal(t, original, now)
	}
}

type forumUploadReceipt struct {
	ID         string                       `json:"id"`
	Offset     int64                        `json:"offset"`
	Status     string                       `json:"status"`
	Attachment *service.ForumAttachmentView `json:"attachment"`
}

func (f *demandFixture) forumUpload(t *testing.T, actor, key, kind, post, mime string, body []byte) string {
	t.Helper()
	in := service.ForumUploadCreate{ClientUploadID: key, TargetKind: kind, PostID: post, Name: "private-account-name.png", MimeType: mime, SizeBytes: int64(len(body))}
	v := demandData[forumUploadReceipt](t, f.request(t, actor, "POST", "/api/product/forum/uploads", in, 200))
	assert.Nil(t, v.Attachment)
	replay := demandData[forumUploadReceipt](t, f.request(t, actor, "POST", "/api/product/forum/uploads", in, 200))
	assert.Equal(t, v.ID, replay.ID)
	req := httptest.NewRequest("PUT", "/api/product/forum/uploads/"+v.ID+"/chunks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.tokens[actor])
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Upload-Offset", "0")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	sum := sha256.Sum256(body)
	complete := demandData[forumUploadReceipt](t, f.request(t, actor, "POST", "/api/product/forum/uploads/"+v.ID+"/complete", map[string]string{"sha256": hex.EncodeToString(sum[:])}, 200))
	require.NotNil(t, complete.Attachment)
	assert.Equal(t, hex.EncodeToString(sum[:]), complete.Attachment.SHA256)
	f.request(t, actor, "POST", "/api/product/forum/uploads/"+v.ID+"/complete", map[string]string{"sha256": hex.EncodeToString(sum[:])}, 200)
	return v.ID
}
func TestForumImagesBindAtomicallyAndRevokeSignedMedia(t *testing.T) {
	f := newForumFixture(t)
	f.enableForum(t)
	data := communityPNG(t)
	upload := f.forumUpload(t, "owner", "post-image", "post", "", "image/png", data)
	f.request(t, "other", "GET", "/api/product/forum/uploads/"+upload, nil, 404)
	in := service.ForumCreate{ClientRequestID: "image-publication", Title: "Images 😄", AttachmentIDs: []string{upload}}
	f.request(t, "other", "POST", "/api/product/forum/posts", in, 400)
	var count int64
	require.NoError(t, f.db.Model(&model.ForumPost{}).Count(&count).Error)
	assert.Zero(t, count)
	first := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", in, 201))
	require.Len(t, first.Post.Attachments, 1)
	assert.Equal(t, "forum-image.png", first.Post.Attachments[0].Name)
	again := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", in, 200))
	assert.Equal(t, first.Post.ID, again.Post.ID)
	assert.True(t, again.Replayed)
	receipt := demandData[forumPostReceipt](t, f.request(t, "owner", "GET", "/api/product/forum/receipts?kind=post&client_request_id=image-publication", nil, 200))
	assert.Equal(t, first.Post.ID, receipt.Post.ID)
	f.request(t, "other", "GET", "/api/product/forum/receipts?kind=post&client_request_id=image-publication", nil, 404)
	changed := in
	changed.AttachmentIDs = nil
	changed.Body = "changed"
	f.request(t, "owner", "POST", "/api/product/forum/posts", changed, 409)
	duplicate := in
	duplicate.ClientRequestID = "different-publication"
	f.request(t, "owner", "POST", "/api/product/forum/posts", duplicate, 400)
	commentUpload := f.forumUpload(t, "other", "comment-image", "comment", first.Post.ID, "image/png", data)
	comment := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", "/api/product/forum/posts/"+first.Post.ID+"/comments", service.ForumCreate{ClientRequestID: "comment-image-request", AttachmentIDs: []string{commentUpload}}, 201))
	require.Len(t, comment.Comment.Attachments, 1)
	linkPath := "/api/product/forum/attachments/" + commentUpload + "/link"
	link := demandData[service.ForumAttachmentLink](t, f.request(t, "owner", "POST", linkPath, map[string]string{"disposition": "inline"}, 200))
	target, err := url.Parse(link.URL)
	require.NoError(t, err)
	for _, method := range []string{"GET", "HEAD"} {
		req := httptest.NewRequest(method, target.RequestURI(), nil)
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		if method == "GET" {
			assert.Equal(t, data, w.Body.Bytes())
		}
	}
	req := httptest.NewRequest("GET", target.RequestURI(), nil)
	req.Header.Set("Range", "bytes=0-31")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 206, w.Code)
	assert.Equal(t, data[:32], w.Body.Bytes())
	f.request(t, "admin", "DELETE", "/api/forum-management/comments/"+comment.Comment.ID, map[string]string{"reason": "remove"}, 200)
	for _, method := range []string{"GET", "HEAD"} {
		req := httptest.NewRequest(method, target.RequestURI(), nil)
		req.Header.Set("Range", "bytes=0-31")
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		assert.Equal(t, 404, w.Code)
	}
	f.request(t, "owner", "POST", linkPath, map[string]string{"disposition": "inline"}, 404)
	f.request(t, "admin", "POST", "/api/forum-management/attachments/"+commentUpload+"/link", map[string]string{"disposition": "attachment"}, 200)
	bad := service.ForumUploadCreate{ClientUploadID: "bad-mime", TargetKind: "post", Name: "bad.svg", MimeType: "image/svg+xml", SizeBytes: 10}
	f.request(t, "owner", "POST", "/api/product/forum/uploads", bad, 415)
	bad.ClientUploadID = "oversize"
	bad.MimeType = "image/png"
	bad.SizeBytes = 21 << 20
	f.request(t, "owner", "POST", "/api/product/forum/uploads", bad, 413)
	unready := demandData[forumUploadReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/uploads", service.ForumUploadCreate{ClientUploadID: "unready", TargetKind: "post", Name: "pending.png", MimeType: "image/png", SizeBytes: 20}, 200))
	f.request(t, "owner", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: "no-unready-bind", Title: "Not ready", AttachmentIDs: []string{unready.ID}}, 400)
	expired := f.forumUpload(t, "owner", "expired", "post", "", "image/png", data)
	require.NoError(t, f.db.Model(&model.ForumAttachment{}).Where("id = ?", expired).Update("expires_at", 1).Error)
	f.request(t, "owner", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: "no-expired-bind", Title: "Expired", AttachmentIDs: []string{expired}}, 400)
	f.request(t, "owner", "POST", "/api/product/forum/attachments/"+expired+"/link", map[string]string{"disposition": "inline"}, 404)
}
func TestForumRepliesMyActivityNotificationsAndPublicIdentity(t *testing.T) {
	f := newForumFixture(t)
	f.enableForum(t)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["owner"].Id).Update("display_name", *f.users["owner"].PhoneE164).Error)
	beforeUsers := []model.User{}
	beforeSessions := []model.UserSession{}
	require.NoError(t, f.db.Order("id").Find(&beforeUsers).Error)
	require.NoError(t, f.db.Order("sid").Find(&beforeSessions).Error)
	for _, name := range []string{*f.users["owner"].PhoneE164, "+60 1270 78625", "+86****5937", f.users["owner"].Username, "Bad\nName"} {
		f.request(t, "owner", "PATCH", "/api/product/forum/profile", map[string]any{"revision": 0, "display_name": name}, 400)
	}
	profile := demandData[map[string]service.ForumProfileView](t, f.request(t, "owner", "PATCH", "/api/product/forum/profile", map[string]any{"revision": 0, "display_name": "云游者 😄"}, 200))["profile"]
	assert.Equal(t, "云游者 😄", profile.DisplayName)
	f.request(t, "owner", "PATCH", "/api/product/forum/profile", map[string]any{"revision": 0, "display_name": "云游者 😄"}, 200)
	f.request(t, "owner", "PATCH", "/api/product/forum/profile", map[string]any{"revision": 0, "display_name": "Different"}, 409)
	p := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: "activity-post", Title: "Forum discussion", Body: "😀"}, 201)).Post
	path := "/api/product/forum/posts/" + p.ID
	root := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", path+"/comments", service.ForumCreate{ClientRequestID: "root-comment", Body: "First comment"}, 201)).Comment
	// A reply to the post author as well as that author's comment creates ONE notification.
	own := demandData[forumCommentReceipt](t, f.request(t, "owner", "POST", path+"/comments", service.ForumCreate{ClientRequestID: "self-comment", Body: "Author's comment"}, 201)).Comment
	replyInput := service.ForumCreate{ClientRequestID: "targeted-reply", Body: "Reply 😄", ReplyToCommentID: own.ID}
	reply := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", path+"/comments", replyInput, 201)).Comment
	require.NotNil(t, reply.ReplyToAuthor)
	assert.Equal(t, "云游者 😄", reply.ReplyToAuthor.DisplayName)
	f.request(t, "other", "POST", path+"/comments", replyInput, 200)
	modified := replyInput
	modified.ReplyToCommentID = root.ID
	f.request(t, "other", "POST", path+"/comments", modified, 409)
	another := demandData[forumPostReceipt](t, f.request(t, "other", "POST", "/api/product/forum/posts", service.ForumCreate{ClientRequestID: "other-post", Title: "Another", Body: "Text"}, 201)).Post
	f.request(t, "owner", "POST", "/api/product/forum/posts/"+another.ID+"/comments", service.ForumCreate{ClientRequestID: "cross-post-reply", Body: "Must fail", ReplyToCommentID: root.ID}, 404)
	for range 2 {
		f.request(t, "other", "PUT", path+"/like", map[string]bool{"liked": true}, 200)
	}
	f.request(t, "owner", "PUT", path+"/like", map[string]bool{"liked": true}, 200)
	type notices struct {
		Items  []service.ForumNotificationView `json:"items"`
		Unread int64                           `json:"unread_count"`
	}
	raw := f.request(t, "owner", "GET", "/api/product/forum/notifications", nil, 200)
	seen := demandData[notices](t, raw)
	assert.EqualValues(t, 3, seen.Unread)
	require.Len(t, seen.Items, 3)
	for _, u := range f.users {
		assert.NotContains(t, string(raw), u.Username)
		assert.NotContains(t, string(raw), *u.PhoneE164)
	}
	id := seen.Items[0].ID
	f.request(t, "other", "POST", "/api/product/forum/notifications/read", map[string]any{"notification_ids": []string{id}}, 200)
	unchanged := demandData[notices](t, f.request(t, "owner", "GET", "/api/product/forum/notifications", nil, 200))
	assert.EqualValues(t, 3, unchanged.Unread)
	for range 2 {
		f.request(t, "owner", "POST", "/api/product/forum/notifications/read", map[string]any{"notification_ids": []string{id}}, 200)
	}
	seen = demandData[notices](t, f.request(t, "owner", "GET", "/api/product/forum/notifications", nil, 200))
	assert.EqualValues(t, 2, seen.Unread)
	f.request(t, "other", "PUT", path+"/like", map[string]bool{"liked": false}, 200)
	f.request(t, "other", "PUT", path+"/like", map[string]bool{"liked": true}, 200)
	seen = demandData[notices](t, f.request(t, "owner", "GET", "/api/product/forum/notifications", nil, 200))
	assert.Len(t, seen.Items, 3)
	assert.EqualValues(t, 2, seen.Unread)
	mine := demandData[forumPostPage](t, f.request(t, "owner", "GET", "/api/product/forum/me/posts?limit=1", nil, 200))
	require.Len(t, mine.Items, 1)
	assert.Equal(t, p.ID, mine.Items[0].ID)
	comments := demandData[forumCommentPage](t, f.request(t, "other", "GET", "/api/product/forum/me/comments?limit=1", nil, 200))
	require.Len(t, comments.Items, 1)
	assert.True(t, comments.More)
	next := demandData[forumCommentPage](t, f.request(t, "other", "GET", "/api/product/forum/me/comments?limit=1&cursor="+comments.Next, nil, 200))
	require.Len(t, next.Items, 1)
	assert.NotEqual(t, comments.Items[0].ID, next.Items[0].ID)
	f.request(t, "owner", "GET", path+"/comments/"+reply.ID, nil, 200)
	f.request(t, "admin", "DELETE", "/api/forum-management/posts/"+p.ID, map[string]string{"reason": "hidden"}, 200)
	seen = demandData[notices](t, f.request(t, "owner", "GET", "/api/product/forum/notifications", nil, 200))
	assert.Empty(t, seen.Items)
	assert.Zero(t, seen.Unread)
	afterUsers := []model.User{}
	afterSessions := []model.UserSession{}
	require.NoError(t, f.db.Order("id").Find(&afterUsers).Error)
	require.NoError(t, f.db.Order("sid").Find(&afterSessions).Error)
	assert.Equal(t, beforeUsers, afterUsers)
	assert.Equal(t, beforeSessions, afterSessions)
}

// Representative released comment schema: no reply target column or forum media/profile tables.
type releasedForumComment struct {
	ID              string `gorm:"type:varchar(40);primaryKey"`
	PostID          string `gorm:"type:varchar(40);index:idx_forum_comment_feed,priority:1"`
	AuthorID        int    `gorm:"uniqueIndex:idx_forum_comment_request,priority:1"`
	ClientRequestID string `gorm:"type:varchar(80);uniqueIndex:idx_forum_comment_request,priority:2"`
	PayloadHash     string `gorm:"type:varchar(64)"`
	Body            string `gorm:"type:text"`
	CreatedAt       int64  `gorm:"autoCreateTime:false;index:idx_forum_comment_feed,priority:3"`
	ReviewedAt      int64  `gorm:"index"`
	ReviewedBy      int
	DeletedAt       int64 `gorm:"index:idx_forum_comment_feed,priority:2"`
	DeletedBy       int
	DeleteReason    string `gorm:"type:varchar(1000)"`
}

func (releasedForumComment) TableName() string { return "forum_comments" }
func TestForumReleasedSchemaUpgradePreservesFrozenTextReceipts(t *testing.T) {
	f := newForumFixture(t)
	f.enableForum(t)
	postInput := service.ForumCreate{ClientRequestID: "released-post-key", Title: "Original", Body: "Text before forum media"}
	post := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", postInput, 201)).Post
	// Freeze the released exact wire bytes independently of the new optional fields.
	raw := []byte(`{"client_request_id":"released-post-key","title":"Original","body":"Text before forum media"}`)
	sum := sha256.Sum256(raw)
	var row model.ForumPost
	require.NoError(t, f.db.First(&row, "id = ?", post.ID).Error)
	assert.Equal(t, hex.EncodeToString(sum[:]), row.PayloadHash)
	require.NoError(t, f.db.Migrator().DropTable(&model.ForumComment{}, &model.ForumAttachment{}, &model.ForumUploadPart{}, &model.ForumProfile{}, &model.ForumNotification{}))
	require.NoError(t, f.db.Migrator().CreateTable(&releasedForumComment{}))
	commentRaw := []byte(`{"PostID":"` + post.ID + `","Input":{"client_request_id":"released-comment-key","title":"","body":"Original reply"}}`)
	commentSum := sha256.Sum256(commentRaw)
	old := releasedForumComment{ID: "frozen-released-comment", PostID: post.ID, AuthorID: f.users["other"].Id, ClientRequestID: "released-comment-key", Body: "Original reply", PayloadHash: hex.EncodeToString(commentSum[:]), CreatedAt: 100}
	require.NoError(t, f.db.Create(&old).Error)
	before := []model.UserSession{}
	require.NoError(t, f.db.Order("sid").Find(&before).Error)
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(model.ForumTables()...))
	}
	oldReplay := demandData[forumCommentReceipt](t, f.request(t, "other", "POST", "/api/product/forum/posts/"+post.ID+"/comments", service.ForumCreate{ClientRequestID: old.ClientRequestID, Body: old.Body}, 200))
	assert.True(t, oldReplay.Replayed)
	assert.Equal(t, old.ID, oldReplay.Comment.ID)
	assert.Empty(t, oldReplay.Comment.ReplyToCommentID)
	replay := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", postInput, 200))
	assert.True(t, replay.Replayed)
	assert.Equal(t, post.ID, replay.Post.ID)
	var got model.ForumComment
	require.NoError(t, f.db.First(&got, "id = ?", old.ID).Error)
	assert.Equal(t, old.PayloadHash, got.PayloadHash)
	require.NoError(t, f.db.First(&row, "id = ?", post.ID).Error)
	assert.Equal(t, hex.EncodeToString(sum[:]), row.PayloadHash)
	// Same title/body with an independent operation is a different legitimate post.
	postInput.ClientRequestID = "independent-same-text"
	separate := demandData[forumPostReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/posts", postInput, 201))
	assert.NotEqual(t, post.ID, separate.Post.ID)
	after := []model.UserSession{}
	require.NoError(t, f.db.Order("sid").Find(&after).Error)
	assert.Equal(t, before, after)
}

func TestForumDeclaredImageFormatsAndInvalidEncoding(t *testing.T) {
	f := newForumFixture(t)
	f.enableForum(t)
	var jpegData, gifData bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil))
	require.NoError(t, gif.Encode(&gifData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil))
	// Lossless WebP fixture from golang.org/x/image's BSD-licensed testdata.
	webpData, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
	require.NoError(t, err)
	for _, v := range []struct {
		mime string
		data []byte
	}{{"image/jpeg", jpegData.Bytes()}, {"image/gif", gifData.Bytes()}, {"image/webp", webpData}} {
		t.Run(v.mime, func(t *testing.T) { f.forumUpload(t, "owner", v.mime, "post", "", v.mime, v.data) })
	}
	bad := []byte("not a png image")
	init := demandData[forumUploadReceipt](t, f.request(t, "owner", "POST", "/api/product/forum/uploads", service.ForumUploadCreate{ClientUploadID: "invalid-content", TargetKind: "post", Name: "wrong.png", MimeType: "image/png", SizeBytes: int64(len(bad))}, 200))
	req := httptest.NewRequest("PUT", "/api/product/forum/uploads/"+init.ID+"/chunks", bytes.NewReader(bad))
	req.Header.Set("Authorization", "Bearer "+f.tokens["owner"])
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Upload-Offset", "0")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	hash := sha256.Sum256(bad)
	f.request(t, "owner", "POST", "/api/product/forum/uploads/"+init.ID+"/complete", map[string]string{"sha256": hex.EncodeToString(hash[:])}, 415)
	current := demandData[forumUploadReceipt](t, f.request(t, "owner", "GET", "/api/product/forum/uploads/"+init.ID, nil, 200))
	assert.Equal(t, "uploading", current.Status)
	assert.Nil(t, current.Attachment)
}
func TestForumConcurrentSamePublicationKeepsOneRowAndOneBinding(t *testing.T) {
	f := newForumFixture(t)
	f.enableForum(t)
	id := f.forumUpload(t, "owner", "concurrent-image", "post", "", "image/png", communityPNG(t))
	in := service.ForumCreate{ClientRequestID: "concurrent-original", Title: "One original publication", AttachmentIDs: []string{id}}
	type result struct {
		post     model.ForumPost
		replayed bool
		err      error
	}
	done := make(chan result, 2)
	for range 2 {
		go func() {
			p, r, e := service.CreateForumPost(f.db, service.ForumActor{UserID: f.users["owner"].Id}, in)
			done <- result{p, r, e}
		}()
	}
	first, second := <-done, <-done
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.Equal(t, first.post.ID, second.post.ID)
	assert.NotEqual(t, first.replayed, second.replayed)
	var n int64
	require.NoError(t, f.db.Model(&model.ForumPost{}).Count(&n).Error)
	assert.EqualValues(t, 1, n)
	require.NoError(t, f.db.Model(&model.ForumAttachment{}).Where("bound = ?", true).Count(&n).Error)
	assert.EqualValues(t, 1, n)
}
