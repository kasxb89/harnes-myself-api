package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"time"
)

func newCommunityFixture(t *testing.T) *demandFixture {
	t.Helper()
	f := newDemandFixture(t)
	if os.Getenv("DEMAND_TEST_POSTGRES_DSN") != "" || os.Getenv("DEMAND_TEST_MYSQL_DSN") != "" {
		require.NoError(t, f.db.Migrator().DropTable(model.CommunityTables()...))
		t.Cleanup(func() { require.NoError(t, f.db.Migrator().DropTable(model.CommunityTables()...)) })
	}
	require.NoError(t, f.db.AutoMigrate(model.CommunityTables()...))
	require.NoError(t, f.db.AutoMigrate(model.CommunityTables()...))
	SetCommunityDemandRoutes(f.engine.Group("/api"))
	f.request(t, "admin", "PUT", "/api/community-demand-management/config", service.CommunityConfig{Enabled: true}, 200)
	return f
}

type communityReceipt struct {
	Order    service.CommunityOrderView    `json:"order"`
	Message  *service.CommunityMessageView `json:"message"`
	Replayed bool                          `json:"replayed"`
}

func (f *demandFixture) communityCreate(t *testing.T, key string) communityReceipt {
	t.Helper()
	created := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", service.CommunityCreate{ClientRequestID: key, PublishToCommunity: true, Title: "Synthetic user project", PublicSummary: "Public overview only", PrivateText: "PRIVATE-COMMUNITY-SECRET"}, 201))
	return f.communityApprove(t, created)
}
func (f *demandFixture) communityApprove(t *testing.T, created communityReceipt) communityReceipt {
	t.Helper()
	approved := demandData[communityReceipt](t, f.request(t, "admin", "POST", "/api/community-demand-management/orders/"+created.Order.ID+"/actions", service.CommunityAction{ClientActionID: "approve:" + created.Order.ID, Revision: created.Order.Revision, Action: "approve_listing"}, 200))
	created.Order = approved.Order
	return created
}
func communityPath(id string) string { return "/api/product/community-demands/orders/" + id }
func TestCommunityExplicitPublicationPreservesLegacyPrivacy(t *testing.T) {
	f := newCommunityFixture(t)
	original := f.create(t, "original-official-private")
	denied := service.CommunityCreate{ClientRequestID: "not-public", Title: "Original confidential demand", PublicSummary: "Overview", PrivateText: "private"}
	f.request(t, "owner", "POST", "/api/product/community-demands/orders", denied, 400)
	var count int64
	require.NoError(t, f.db.Model(&model.CommunityOrder{}).Count(&count).Error)
	assert.Zero(t, count)
	first := f.communityCreate(t, "public-order-key")
	require.NotNil(t, first.Message)
	assert.Equal(t, "publisher", first.Message.AuthorKind)
	assert.Equal(t, "create:public-order-key", first.Message.ClientMessageID)
	var page struct {
		Items []service.CommunityListingView `json:"items"`
	}
	raw := f.request(t, "other", "GET", "/api/product/community-demands/listings", nil, 200)
	page = demandData[struct {
		Items []service.CommunityListingView `json:"items"`
	}](t, raw)
	require.Len(t, page.Items, 1)
	assert.Equal(t, first.Order.ID, page.Items[0].ID)
	assert.NotContains(t, string(raw), "PRIVATE-COMMUNITY-SECRET")
	assert.NotContains(t, string(raw), "synthetic-owner")
	assert.NotContains(t, string(raw), "phone_e164")
	f.request(t, "other", "GET", communityPath(first.Order.ID), nil, 404)
	f.request(t, "other", "GET", communityPath(first.Order.ID)+"/messages", nil, 404)
	f.request(t, "owner", "POST", communityPath(first.Order.ID)+"/accept", map[string]string{"client_action_id": "own-claim"}, 403)
	same := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", service.CommunityCreate{ClientRequestID: "public-order-key", PublishToCommunity: true, Title: "Synthetic user project", PublicSummary: "Public overview only", PrivateText: "PRIVATE-COMMUNITY-SECRET"}, 200))
	assert.True(t, same.Replayed)
	assert.Equal(t, first.Order.ID, same.Order.ID)
	require.NotNil(t, same.Message)
	assert.Equal(t, first.Message.ID, same.Message.ID)
	changed := service.CommunityCreate{ClientRequestID: "public-order-key", PublishToCommunity: true, Title: "Changed", PublicSummary: "Other"}
	f.request(t, "owner", "POST", "/api/product/community-demands/orders", changed, 409)
	legacy := demandData[map[string]any](t, f.request(t, "owner", "GET", "/api/product/demands", nil, 200))
	items, ok := legacy["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	row, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, original.Demand.ID, row["id"])
	cfg := demandData[map[string]any](t, f.request(t, "owner", "GET", "/api/product/demands/config", nil, 200))
	capabilities, ok := cfg["capabilities"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, capabilities["community_orders_v1"])
	f.request(t, "admin", "PUT", "/api/community-demand-management/config", service.CommunityConfig{Revision: 1, Enabled: false}, 200)
	f.request(t, "owner", "GET", communityPath(first.Order.ID), nil, 200)
	f.request(t, "owner", "POST", communityPath(first.Order.ID)+"/messages", service.CommunitySend{ClientMessageID: "after-entry-closed", Text: "Keep original private work"}, 201)
	require.NoError(t, f.db.Model(&model.CommunityOrder{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
func TestCommunityConcurrentAcceptanceAndMessageReceipts(t *testing.T) {
	f := newCommunityFixture(t)
	first := f.communityCreate(t, "race-order")
	type result struct {
		actor   string
		status  int
		receipt communityReceipt
	}
	results := make(chan result, 20)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			actor := []string{"other", "engineer"}[i%2]
			body, _ := common.Marshal(map[string]string{"client_action_id": "race-" + string(rune('a'+i))})
			req := httptest.NewRequest("POST", communityPath(first.Order.ID)+"/accept", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+f.tokens[actor])
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.engine.ServeHTTP(w, req)
			var envelope struct {
				Data communityReceipt `json:"data"`
			}
			_ = common.Unmarshal(w.Body.Bytes(), &envelope)
			results <- result{actor, w.Code, envelope.Data}
		}(i)
	}
	group.Wait()
	close(results)
	winner := ""
	fresh := 0
	for r := range results {
		require.Contains(t, []int{200, 409}, r.status)
		if r.status == 200 && !r.receipt.Replayed {
			winner = r.actor
			fresh++
		}
	}
	require.Equal(t, 1, fresh)
	require.NotEmpty(t, winner)
	other := "engineer"
	if winner == "engineer" {
		other = "other"
	}
	f.request(t, other, "GET", communityPath(first.Order.ID), nil, 404)
	view := demandData[struct {
		Order service.CommunityOrderView `json:"order"`
	}](t, f.request(t, winner, "GET", communityPath(first.Order.ID), nil, 200))
	assert.Equal(t, "acceptor", view.Order.MyRole)
	require.NotNil(t, view.Order.Acceptor)
	assert.Equal(t, f.users[winner].Id, view.Order.Acceptor.ID)
	assert.Equal(t, "PRIVATE-COMMUNITY-SECRET", view.Order.PrivateText)
	msg := service.CommunitySend{ClientMessageID: "message-recovery", Text: "Actual user reply"}
	sent := demandData[communityReceipt](t, f.request(t, winner, "POST", communityPath(first.Order.ID)+"/messages", msg, 201))
	require.NotNil(t, sent.Message)
	assert.Equal(t, "acceptor", sent.Message.AuthorKind)
	assert.Equal(t, f.users[winner].Id, sent.Message.SenderID)
	replay := demandData[communityReceipt](t, f.request(t, winner, "POST", communityPath(first.Order.ID)+"/messages", msg, 200))
	assert.True(t, replay.Replayed)
	require.NotNil(t, replay.Message)
	assert.Equal(t, sent.Message.ID, replay.Message.ID)
	assert.Equal(t, sent.Message.Seq, replay.Message.Seq)
	msg.Text = "Changed"
	f.request(t, winner, "POST", communityPath(first.Order.ID)+"/messages", msg, 409)
	second := f.communityCreate(t, "other-order")
	f.request(t, winner, "POST", communityPath(second.Order.ID)+"/accept", map[string]string{"client_action_id": "another-claim"}, 200)
	msg.Text = "Actual user reply"
	f.request(t, winner, "POST", communityPath(second.Order.ID)+"/messages", msg, 409)
	var total int64
	require.NoError(t, f.db.Model(&model.CommunityMessage{}).Where("sender_id = ? AND client_message_id = ?", f.users[winner].Id, "message-recovery").Count(&total).Error)
	assert.EqualValues(t, 1, total)
	f.request(t, "owner", "POST", communityPath(first.Order.ID)+"/read", map[string]int64{"last_read_seq": sent.Order.LastSeq}, 200)
	read := demandData[map[string]int64](t, f.request(t, "owner", "POST", communityPath(first.Order.ID)+"/read", map[string]int64{"last_read_seq": 0}, 200))
	assert.Equal(t, sent.Order.LastSeq, read["last_read_seq"])
	var orders int64
	require.NoError(t, f.db.Model(&model.CommunityOrder{}).Count(&orders).Error)
	assert.EqualValues(t, 2, orders)
}
func TestCommunityLifecycleAdminVisibilityAndCancellation(t *testing.T) {
	f := newCommunityFixture(t)
	o := f.communityCreate(t, "lifecycle")
	act := func(actor, key, action, text, reason string, revision int64, status int) communityReceipt {
		return demandData[communityReceipt](t, f.request(t, actor, "POST", communityPath(o.Order.ID)+"/actions", service.CommunityAction{ClientActionID: key, Revision: revision, Action: action, Text: text, Reason: reason}, status))
	}
	cancelled := act("owner", "cancel-empty", "cancel", "", "", o.Order.Revision, 200)
	assert.Equal(t, "cancelled", cancelled.Order.Status)
	reopened := act("owner", "reopen-empty", "reopen", "", "", cancelled.Order.Revision, 200)
	assert.Equal(t, "open", reopened.Order.Status)
	assert.Equal(t, o.Order.ID, reopened.Order.ID)
	accepted := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(o.Order.ID)+"/accept", map[string]string{"client_action_id": "accept-lifecycle"}, 200))
	f.request(t, "owner", "POST", communityPath(o.Order.ID)+"/actions", service.CommunityAction{ClientActionID: "bad-cancel", Revision: accepted.Order.Revision, Action: "cancel"}, 403)
	delivered := act("other", "delivery", "deliver", "Ready for review", "", accepted.Order.Revision, 200)
	assert.Equal(t, "awaiting_acceptance", delivered.Order.Status)
	changes := act("owner", "feedback", "request_changes", "Please adjust the layout", "", delivered.Order.Revision, 200)
	assert.Equal(t, "accepted", changes.Order.Status)
	delivered = act("other", "delivery-final", "deliver", "Updated work", "", changes.Order.Revision, 200)
	complete := act("owner", "accept-delivery", "accept_delivery", "", "", delivered.Order.Revision, 200)
	assert.Equal(t, "completed", complete.Order.Status)
	f.request(t, "other", "POST", communityPath(o.Order.ID)+"/messages", service.CommunitySend{ClientMessageID: "closed-write", Text: "No write"}, 409)
	raw := f.request(t, "admin", "GET", "/api/community-demand-management/orders/"+o.Order.ID, nil, 200)
	assert.Contains(t, string(raw), "PRIVATE-COMMUNITY-SECRET")
	assert.Contains(t, string(raw), "phone_e164")
	second := f.communityCreate(t, "cancel-accepted")
	o = second
	accepted = demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(o.Order.ID)+"/accept", map[string]string{"client_action_id": "claim-cancelled"}, 200))
	f.request(t, "admin", "POST", "/api/community-demand-management/orders/"+o.Order.ID+"/actions", service.CommunityAction{ClientActionID: "missing-reason", Revision: accepted.Order.Revision, Action: "cancel"}, 400)
	ended := demandData[communityReceipt](t, f.request(t, "admin", "POST", "/api/community-demand-management/orders/"+o.Order.ID+"/actions", service.CommunityAction{ClientActionID: "admin-cancel", Revision: accepted.Order.Revision, Action: "cancel", Reason: "Synthetic moderation"}, 200))
	assert.Equal(t, "cancelled", ended.Order.Status)
	f.request(t, "owner", "POST", communityPath(o.Order.ID)+"/actions", service.CommunityAction{ClientActionID: "cannot-reopen", Revision: ended.Order.Revision, Action: "reopen"}, 409)
	f.request(t, "admin", "GET", "/api/community-demand-management/orders/"+o.Order.ID+"/messages", nil, 200)
}

func communityPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, img))
	return b.Bytes()
}
func (f *demandFixture) communityUpload(t *testing.T, actor, key, scope, orderID, name, mimeType string, body []byte) string {
	t.Helper()
	var created struct {
		ID         string                           `json:"id"`
		Offset     int64                            `json:"offset"`
		Attachment *service.CommunityAttachmentView `json:"attachment"`
	}
	created = demandData[struct {
		ID         string                           `json:"id"`
		Offset     int64                            `json:"offset"`
		Attachment *service.CommunityAttachmentView `json:"attachment"`
	}](t, f.request(t, actor, "POST", "/api/product/community-demands/uploads", service.CommunityUploadCreate{ClientUploadID: key, Scope: scope, OrderID: orderID, Name: name, MimeType: mimeType, SizeBytes: int64(len(body))}, 200))
	assert.Nil(t, created.Attachment)
	req := httptest.NewRequest("PUT", "/api/product/community-demands/uploads/"+created.ID+"/content", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.tokens[actor])
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Upload-Offset", "0")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	sum := sha256.Sum256(body)
	ready := demandData[map[string]any](t, f.request(t, actor, "POST", "/api/product/community-demands/uploads/"+created.ID+"/complete", map[string]string{"sha256": hex.EncodeToString(sum[:])}, 200))
	asset, ok := ready["attachment"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ready", asset["status"])
	return created.ID
}
func TestCommunityPublicAndPrivateAttachmentACLRangeAndRevocation(t *testing.T) {
	f := newCommunityFixture(t)
	data := communityPNG(t)
	public := f.communityUpload(t, "owner", "public-png", "listing", "", "Public.png", "image/png", data)
	private := f.communityUpload(t, "owner", "private-png", "conversation", "", "Private.png", "image/png", data)
	order := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", service.CommunityCreate{ClientRequestID: "media-order", PublishToCommunity: true, Title: "Media project", PublicSummary: "Only the selected public image", PublicAttachmentIDs: []string{public}, PrivateText: "Private attachment", PrivateAttachmentIDs: []string{private}}, 201))
	order = f.communityApprove(t, order)
	listing := f.request(t, "other", "GET", "/api/product/community-demands/listings/"+order.Order.ID, nil, 200)
	assert.Contains(t, string(listing), "Public.png")
	assert.NotContains(t, string(listing), "Private.png")
	assert.NotContains(t, string(listing), private)
	f.request(t, "other", "POST", "/api/product/community-demands/attachments/"+private+"/link", map[string]string{"disposition": "inline"}, 404)
	link := demandData[service.CommunityAttachmentLink](t, f.request(t, "other", "POST", "/api/product/community-demands/attachments/"+public+"/link", map[string]string{"disposition": "inline"}, 200))
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", u.RequestURI(), nil)
	req.Header.Set("Range", "bytes=0-31")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 206, w.Code, w.Body.String())
	assert.Equal(t, data[:32], w.Body.Bytes())
	assert.Equal(t, "bytes 0-31/"+stringInt(len(data)), w.Header().Get("Content-Range"))
	f.request(t, "admin", "POST", "/api/community-demand-management/orders/"+order.Order.ID+"/actions", service.CommunityAction{ClientActionID: "hide-public", Revision: order.Order.Revision, Action: "hide_listing", Reason: "Local moderation"}, 200)
	f.request(t, "other", "GET", u.RequestURI(), nil, 404)
	current := demandData[struct {
		Order service.CommunityOrderView `json:"order"`
	}](t, f.request(t, "owner", "GET", communityPath(order.Order.ID), nil, 200))
	f.request(t, "admin", "POST", "/api/community-demand-management/orders/"+order.Order.ID+"/actions", service.CommunityAction{ClientActionID: "show-public", Revision: current.Order.Revision, Action: "show_listing", Reason: "Local moderation passed"}, 200)
	accepted := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(order.Order.ID)+"/accept", map[string]string{"client_action_id": "media-claim"}, 200))
	link = demandData[service.CommunityAttachmentLink](t, f.request(t, "other", "POST", "/api/product/community-demands/attachments/"+private+"/link", map[string]string{"disposition": "attachment"}, 200))
	u, err = url.Parse(link.URL)
	require.NoError(t, err)
	req = httptest.NewRequest("HEAD", u.RequestURI(), nil)
	w = httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "Private.png")
	wrong := service.CommunityCreate{ClientRequestID: "illegal-scope", PublishToCommunity: true, Title: "Wrong public scope", PublicSummary: "Do not expose private images", PublicAttachmentIDs: []string{private}}
	f.request(t, "owner", "POST", "/api/product/community-demands/orders", wrong, 409)
	f.request(t, "replacement", "GET", communityPath(order.Order.ID), nil, 404)
	assert.Equal(t, "accepted", accepted.Order.Status)
}
func stringInt(v int) string { return strconv.Itoa(v) }

func TestCommunityMigrationKeepsExistingSessionsTasksAndFinance(t *testing.T) {
	f := newCommunityFixture(t)
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
	require.NoError(t, f.db.Migrator().DropTable(model.CommunityTables()...))
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(model.CommunityTables()...))
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

func TestCommunitySessionPermissionsAndCursorIsolation(t *testing.T) {
	f := newCommunityFixture(t)
	o := f.communityCreate(t, "permissions")
	path := "/api/community-demand-management/orders/" + o.Order.ID
	f.request(t, "owner", "GET", path, nil, 403)
	f.request(t, "", "GET", "/api/product/community-demands/listings", nil, 401)
	raw := "nap_synthetic_community_only"
	pat := model.UserAccessToken{Name: "community-fixture-only", TokenHash: model.AccessTokenFingerprint(raw), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, pat.SetScopes([]string{"*"}))
	require.NoError(t, model.CreateUserAccessToken(f.users["admin"].Id, &pat, 10))
	f.tokens["pat"] = raw
	f.request(t, "pat", "GET", "/api/product/community-demands/config", nil, 403)
	f.request(t, "pat", "GET", path, nil, 403)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["admin"].Id).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, authz.PermissionsMap{"community_demands": {"read": true, "write": false}}))
	f.request(t, "admin", "GET", path, nil, 200)
	f.request(t, "admin", "POST", path+"/read", map[string]int64{"last_read_seq": o.Order.LastSeq}, 200)
	f.request(t, "admin", "POST", path+"/messages", service.CommunitySend{ClientMessageID: "read-only-admin", Text: "not allowed"}, 403)
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, authz.PermissionsMap{"community_demands": {"read": true, "write": true}}))
	reply := demandData[communityReceipt](t, f.request(t, "admin", "POST", path+"/messages", service.CommunitySend{ClientMessageID: "admin-reply", Text: "Administrator reply before acceptance"}, 201))
	require.NotNil(t, reply.Message)
	assert.Equal(t, "admin", reply.Message.AuthorKind)
	assert.Equal(t, f.users["admin"].Id, reply.Message.SenderID)
	assert.Equal(t, "admin", reply.Message.AuthorName)
	for _, query := range []string{"cursor=invalid", "limit=101", "q=" + strings.Repeat("x", 101)} {
		f.request(t, "owner", "GET", "/api/product/community-demands/orders?"+query, nil, 400)
	}
	f.communityCreate(t, "page-two")
	first := demandData[struct {
		Items []service.CommunityOrderView `json:"items"`
		Next  string                       `json:"next_cursor"`
	}](t, f.request(t, "owner", "GET", "/api/product/community-demands/orders?limit=1", nil, 200))
	require.Len(t, first.Items, 1)
	require.NotEmpty(t, first.Next)
	next := demandData[struct {
		Items []service.CommunityOrderView `json:"items"`
	}](t, f.request(t, "owner", "GET", "/api/product/community-demands/orders?limit=1&cursor="+first.Next, nil, 200))
	require.Len(t, next.Items, 1)
	assert.NotEqual(t, first.Items[0].ID, next.Items[0].ID)
	private := demandData[struct {
		Items []service.CommunityOrderView `json:"items"`
	}](t, f.request(t, "other", "GET", "/api/product/community-demands/orders?cursor="+first.Next, nil, 200))
	assert.Empty(t, private.Items)
}

func TestCommunityMP4DeliveryAndOffsetRecovery(t *testing.T) {
	f := newCommunityFixture(t)
	data, err := os.ReadFile("testdata/community-synthetic.mp4")
	require.NoError(t, err)
	o := f.communityCreate(t, "video-delivery")
	accepted := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(o.Order.ID)+"/accept", map[string]string{"client_action_id": "video-claim"}, 200))
	video := f.communityUpload(t, "other", "real-mp4", "conversation", o.Order.ID, "交付原名.mp4", "video/mp4", data)
	// A lost chunk response is recovered by GET; stale offset reports the current committed value.
	req := httptest.NewRequest("PUT", "/api/product/community-demands/uploads/"+video+"/content", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+f.tokens["other"])
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Upload-Offset", "0")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 409, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"offset":`+strconv.Itoa(len(data)))
	f.request(t, "other", "GET", "/api/product/community-demands/uploads/"+video, nil, 200)
	f.request(t, "owner", "GET", "/api/product/community-demands/uploads/"+video, nil, 404)
	body := service.CommunityAction{ClientActionID: "video-deliver", Revision: accepted.Order.Revision, Action: "deliver", Text: "Synthetic MP4 delivery", AttachmentIDs: []string{video}}
	delivered := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(o.Order.ID)+"/actions", body, 200))
	require.NotNil(t, delivered.Message)
	require.Len(t, delivered.Message.Attachments, 1)
	replay := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(o.Order.ID)+"/actions", body, 200))
	require.NotNil(t, replay.Message)
	assert.Equal(t, delivered.Message.ID, replay.Message.ID)
	assert.True(t, replay.Replayed)
	link := demandData[service.CommunityAttachmentLink](t, f.request(t, "owner", "POST", "/api/product/community-demands/attachments/"+video+"/link", map[string]string{"disposition": "attachment"}, 200))
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	req = httptest.NewRequest("GET", u.RequestURI(), nil)
	req.Header.Set("Range", "bytes=0-31")
	w = httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 206, w.Code, w.Body.String())
	assert.Equal(t, data[:32], w.Body.Bytes())
	assert.Contains(t, w.Header().Get("Content-Disposition"), ".mp4")
	another := f.communityCreate(t, "video-cross-order")
	f.request(t, "other", "POST", communityPath(another.Order.ID)+"/accept", map[string]string{"client_action_id": "claim-other-video"}, 200)
	f.request(t, "other", "POST", communityPath(another.Order.ID)+"/messages", service.CommunitySend{ClientMessageID: "reuse-cross-order", AttachmentIDs: []string{video}}, 409)
	original := f.upload(t, "owner", "legacy.png", "image/png", communityPNG(t))
	f.request(t, "owner", "POST", communityPath(o.Order.ID)+"/messages", service.CommunitySend{ClientMessageID: "legacy-domain-id", AttachmentIDs: []string{original.ID}}, 409)
	complete := demandData[communityReceipt](t, f.request(t, "owner", "POST", communityPath(o.Order.ID)+"/actions", service.CommunityAction{ClientActionID: "video-complete", Revision: delivered.Order.Revision, Action: "accept_delivery"}, 200))
	assert.Equal(t, "completed", complete.Order.Status)
	f.request(t, "admin", "GET", "/api/community-demand-management/orders/"+o.Order.ID+"/audit", nil, 200)
	f.request(t, "replacement", "POST", "/api/product/community-demands/attachments/"+video+"/link", map[string]string{"disposition": "inline"}, 404)
}
func TestCommunityLocalFixtureServer(t *testing.T) {
	address := os.Getenv("COMMUNITY_FIXTURE_ADDR")
	if address == "" {
		t.Skip("opt-in local community fixture")
	}
	require.True(t, strings.HasPrefix(address, "127.0.0.1:"))
	f := newCommunityFixture(t)
	serveDemandLocalFixture(t, f, address)
}

func TestCommunityConcurrentGlobalMessageKeyCannotCrossOrders(t *testing.T) {
	f := newCommunityFixture(t)
	orders := []communityReceipt{f.communityCreate(t, "global-key-a"), f.communityCreate(t, "global-key-b")}
	for i, o := range orders {
		f.request(t, "other", "POST", communityPath(o.Order.ID)+"/accept", map[string]string{"client_action_id": "global-claim-" + strconv.Itoa(i)}, 200)
	}
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, order := range orders {
		go func(id string) {
			<-start
			raw, _ := common.Marshal(service.CommunitySend{ClientMessageID: "same-sender-global-key", Text: "A single logical message"})
			req := httptest.NewRequest("POST", communityPath(id)+"/messages", bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+f.tokens["other"])
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.engine.ServeHTTP(w, req)
			results <- w.Code
		}(order.Order.ID)
	}
	close(start)
	codes := []int{<-results, <-results}
	assert.ElementsMatch(t, []int{201, 409}, codes)
	var count int64
	require.NoError(t, f.db.Model(&model.CommunityMessage{}).Where("sender_id = ? AND client_message_id = ?", f.users["other"].Id, "same-sender-global-key").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestCommunityApprovalPendingCannotBeDiscoveredOrAccepted(t *testing.T) {
	f := newCommunityFixture(t)
	created := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", service.CommunityCreate{ClientRequestID: "pending-gate", PublishToCommunity: true, Title: "Requires review", PublicSummary: "Only public after admin approval"}, 201))
	page := demandData[struct {
		Items []service.CommunityListingView `json:"items"`
	}](t, f.request(t, "other", "GET", "/api/product/community-demands/listings", nil, 200))
	require.Empty(t, page.Items, "newly submitted listing must not be public before approval")
	f.request(t, "other", "GET", "/api/product/community-demands/listings/"+created.Order.ID, nil, 404)
	f.request(t, "other", "POST", communityPath(created.Order.ID)+"/accept", map[string]string{"client_action_id": "pending-claim"}, 409)
	f.request(t, "owner", "GET", communityPath(created.Order.ID), nil, 200)
}

func TestCommunityApprovalDatesReceiptsAndReasonPrivacy(t *testing.T) {
	f := newCommunityFixture(t)
	cfg := demandData[map[string]any](t, f.request(t, "owner", "GET", "/api/product/community-demands/config", nil, 200))
	assert.Equal(t, "approval_required", cfg["publication_policy"])
	assert.Equal(t, true, cfg["capabilities"].(map[string]any)["community_approval_v1"])
	for _, date := range []string{"2026-02-29", "2024-02-30", "2026-1-02", "0000-01-01", "2026-11-30T00:00:00Z", " 2026-11-30"} {
		f.request(t, "owner", "POST", "/api/product/community-demands/orders", service.CommunityCreate{ClientRequestID: "invalid:" + date, PublishToCommunity: true, Title: "Invalid date", PublicSummary: "summary", ExpectedDeliveryDate: date}, 400)
	}
	input := service.CommunityCreate{ClientRequestID: "dated-original", PublishToCommunity: true, Title: "A calendar date", PublicSummary: "summary", PublicBudget: "CNY 3000–5000", ExpectedDeliveryDate: "2028-02-29"}
	first := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", input, 201))
	assert.Equal(t, "2028-02-29", first.Order.Listing.ExpectedDeliveryDate)
	assert.Equal(t, "pending", first.Order.Listing.ApprovalStatus)
	assert.False(t, first.Order.Listing.CanAccept)
	path := "/api/community-demand-management/orders/" + first.Order.ID + "/actions"
	f.request(t, "owner", "POST", communityPath(first.Order.ID)+"/actions", service.CommunityAction{ClientActionID: "cannot-approve", Revision: first.Order.Revision, Action: "approve_listing"}, 403)
	reject := service.CommunityAction{ClientActionID: "reject-original", Revision: first.Order.Revision, Action: "reject_listing", Reason: "ONLY-OWNER-ADMIN-REJECTION"}
	rejected := demandData[communityReceipt](t, f.request(t, "admin", "POST", path, reject, 200))
	assert.Equal(t, "rejected", rejected.Order.Listing.ApprovalStatus)
	assert.Equal(t, reject.Reason, rejected.Order.Listing.ApprovalReason)
	again := demandData[communityReceipt](t, f.request(t, "admin", "POST", path, reject, 200))
	assert.True(t, again.Replayed)
	assert.Equal(t, rejected.Order.Revision, again.Order.Revision)
	assert.Nil(t, again.Message)
	f.request(t, "other", "GET", "/api/product/community-demands/listings/"+first.Order.ID, nil, 404)
	shown := demandData[communityReceipt](t, f.request(t, "admin", "POST", path, service.CommunityAction{ClientActionID: "show-rejected", Revision: rejected.Order.Revision, Action: "show_listing", Reason: "visibility is not approval"}, 200))
	assert.Equal(t, "rejected", shown.Order.Listing.ApprovalStatus)
	f.request(t, "other", "POST", communityPath(first.Order.ID)+"/accept", map[string]string{"client_action_id": "reject-claim"}, 409)
	approved := f.communityApprove(t, shown)
	assert.Equal(t, "approved", approved.Order.Listing.ApprovalStatus)
	accept := map[string]string{"client_action_id": "stable-approved-claim"}
	accepted := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(first.Order.ID)+"/accept", accept, 200))
	reject.ClientActionID = "revoke-after-accept"
	reject.Revision = accepted.Order.Revision
	f.request(t, "admin", "POST", path, reject, 200)
	replay := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(first.Order.ID)+"/accept", accept, 200))
	assert.True(t, replay.Replayed)
	assert.Equal(t, accepted.Order.ID, replay.Order.ID)
	assert.Equal(t, "accepted", replay.Order.Status)
	assert.Empty(t, replay.Order.Listing.ApprovalReason)
	observer := f.request(t, "other", "GET", communityPath(first.Order.ID), nil, 200)
	assert.NotContains(t, string(observer), reject.Reason)
	owner := f.request(t, "owner", "GET", communityPath(first.Order.ID), nil, 200)
	assert.Contains(t, string(owner), reject.Reason)
	f.request(t, "other", "POST", communityPath(first.Order.ID)+"/messages", service.CommunitySend{ClientMessageID: "after-review", Text: "Original accepted conversation remains usable"}, 201)
	recovered := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", input, 200))
	assert.True(t, recovered.Replayed)
	assert.Equal(t, first.Order.ID, recovered.Order.ID)
	for _, approval := range []string{"pending", "approved", "rejected"} {
		page := demandData[struct {
			Items []service.CommunityOrderView `json:"items"`
		}](t, f.request(t, "admin", "GET", "/api/community-demand-management/orders?approval_status="+approval, nil, 200))
		for _, item := range page.Items {
			assert.Equal(t, approval, item.Listing.ApprovalStatus)
		}
	}
}

func TestCommunityApprovalRevokesPublicSignedGetHeadAndRange(t *testing.T) {
	f := newCommunityFixture(t)
	public := f.communityUpload(t, "owner", "approval-cover", "listing", "", "cover.png", "image/png", communityPNG(t))
	o := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", service.CommunityCreate{ClientRequestID: "approval-media", PublishToCommunity: true, Title: "Cover review", PublicSummary: "summary", PublicAttachmentIDs: []string{public}}, 201))
	linkPath := "/api/product/community-demands/attachments/" + public + "/link"
	f.request(t, "other", "POST", linkPath, map[string]string{"disposition": "inline"}, 404)
	o = f.communityApprove(t, o)
	link := demandData[service.CommunityAttachmentLink](t, f.request(t, "other", "POST", linkPath, map[string]string{"disposition": "inline"}, 200))
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	f.request(t, "other", "GET", u.RequestURI(), nil, 200)
	f.request(t, "admin", "POST", "/api/community-demand-management/orders/"+o.Order.ID+"/actions", service.CommunityAction{ClientActionID: "reject-cover", Revision: o.Order.Revision, Action: "reject_listing", Reason: "private moderation reason"}, 200)
	for _, method := range []string{"GET", "HEAD"} {
		for _, ranged := range []bool{false, true} {
			req := httptest.NewRequest(method, u.RequestURI(), nil)
			if ranged {
				req.Header.Set("Range", "bytes=0-31")
			}
			w := httptest.NewRecorder()
			f.engine.ServeHTTP(w, req)
			assert.Equal(t, 404, w.Code)
		}
	}
	f.request(t, "other", "POST", linkPath, map[string]string{"disposition": "inline"}, 404)
	f.request(t, "owner", "POST", linkPath, map[string]string{"disposition": "inline"}, 200)
}

func TestCommunityApprovalOldPayloadHashAndAdditiveMigration(t *testing.T) {
	f := newCommunityFixture(t)
	// Exact pre-approval CommunityCreate serialization, including original null arrays.
	frozen := []byte(`{"client_request_id":"frozen-old-uuid","publish_to_community":true,"title":"Same title","public_summary":"Old summary","public_budget":"","service_profile_id":"","public_attachment_ids":null,"private_text":"Old private","private_attachment_ids":null}`)
	sum := sha256.Sum256(frozen)
	var input service.CommunityCreate
	require.NoError(t, common.Unmarshal(frozen, &input))
	created := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", input, 201))
	var old model.CommunityOrder
	require.NoError(t, f.db.First(&old, "id = ?", created.Order.ID).Error)
	assert.Equal(t, hex.EncodeToString(sum[:]), old.PayloadHash)
	created = f.communityApprove(t, created)
	claim := map[string]string{"client_action_id": "pre-migration-claim"}
	accepted := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(created.Order.ID)+"/accept", claim, 200))
	var sessions []model.UserSession
	require.NoError(t, f.db.Order("sid").Find(&sessions).Error)
	require.NoError(t, f.db.Migrator().DropIndex(&model.CommunityOrder{}, "idx_community_orders_approval_status"))
	for _, column := range []string{"ExpectedDeliveryDate", "ApprovalStatus", "ApprovalReason"} {
		require.NoError(t, f.db.Migrator().DropColumn(&model.CommunityOrder{}, column))
	}
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(model.CommunityTables()...))
	}
	// The new backend opens its own pool after additive migration, rather than
	// retaining PostgreSQL plans prepared against the deliberately old schema.
	previous := f.db
	next, err := gorm.Open(previous.Dialector, &gorm.Config{})
	require.NoError(t, err)
	pool, err := next.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		f.db, model.DB, model.LOG_DB = previous, previous, previous
		require.NoError(t, pool.Close())
	})
	f.db, model.DB, model.LOG_DB = next, next, next
	raw := f.request(t, "owner", "GET", communityPath(created.Order.ID), nil, 200)
	order := demandData[struct {
		Order service.CommunityOrderView `json:"order"`
	}](t, raw).Order
	assert.Equal(t, "pending", order.Listing.ApprovalStatus)
	assert.Empty(t, order.Listing.ExpectedDeliveryDate)
	assert.Equal(t, "accepted", order.Status)
	assert.Equal(t, accepted.Order.Acceptor, order.Acceptor)
	recovery := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", input, 200))
	assert.True(t, recovery.Replayed)
	assert.Equal(t, created.Order.ID, recovery.Order.ID)
	require.NotNil(t, recovery.Message)
	require.NotNil(t, created.Message)
	assert.Equal(t, created.Message.ID, recovery.Message.ID)
	replay := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(created.Order.ID)+"/accept", claim, 200))
	assert.True(t, replay.Replayed)
	input.ClientRequestID = "distinct-old-uuid"
	distinct := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", input, 201))
	assert.NotEqual(t, created.Order.ID, distinct.Order.ID)
	var sessionsAfter []model.UserSession
	require.NoError(t, f.db.Order("sid").Find(&sessionsAfter).Error)
	assert.Equal(t, sessions, sessionsAfter)
	// The original blank legacy field is also pending, and never a public approval.
	require.NoError(t, f.db.Model(&model.CommunityOrder{}).Where("id = ?", distinct.Order.ID).Update("approval_status", "").Error)
	page := demandData[struct {
		Items []service.CommunityListingView `json:"items"`
	}](t, f.request(t, "other", "GET", "/api/product/community-demands/listings", nil, 200))
	assert.Empty(t, page.Items)
	pending := demandData[struct {
		Items []service.CommunityOrderView `json:"items"`
	}](t, f.request(t, "admin", "GET", "/api/community-demand-management/orders?approval_status=pending", nil, 200))
	assert.Len(t, pending.Items, 2)
}

func TestCommunityApprovalDoesNotRewriteClosedParticipantHistory(t *testing.T) {
	f := newCommunityFixture(t)
	o := f.communityCreate(t, "historically-completed")
	accepted := demandData[communityReceipt](t, f.request(t, "other", "POST", communityPath(o.Order.ID)+"/accept", map[string]string{"client_action_id": "original-completed-claim"}, 200))
	require.NoError(t, f.db.Model(&model.CommunityOrder{}).Where("id = ?", o.Order.ID).Updates(map[string]any{"status": "completed", "closed_at": int64(1700000100)}).Error)
	reviewed := demandData[communityReceipt](t, f.request(t, "admin", "POST", "/api/community-demand-management/orders/"+o.Order.ID+"/actions", service.CommunityAction{ClientActionID: "review-old-completed", Revision: accepted.Order.Revision, Action: "reject_listing", Reason: "Private retrospective review"}, 200))
	assert.Equal(t, "completed", reviewed.Order.Status)
	assert.EqualValues(t, 1700000100, reviewed.Order.ClosedAt)
	assert.Equal(t, accepted.Order.LastSeq, reviewed.Order.LastSeq)
	for _, actor := range []string{"owner", "other"} {
		f.request(t, actor, "GET", communityPath(o.Order.ID)+"/messages", nil, 200)
	}
}
