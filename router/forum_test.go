package router

import (
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
