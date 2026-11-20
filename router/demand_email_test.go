package router

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const demandEmailTestBase = "/api/demand-management/email-notifications"

func configureDemandEmail(t *testing.T, f *demandFixture) service.DemandEmailConfig {
	t.Helper()
	cfg, err := service.LoadDemandEmailConfig(f.db)
	require.NoError(t, err)
	cfg.Enabled = true
	cfg.Recipients = []string{"first@example.test", "second@example.test"}
	cfg.AdminBaseURL = "https://console.example.test/harness"
	cfg.MessageMergeSeconds = 120
	cfg, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	return cfg
}
func syntheticDemandSMTPOptions(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	current := maps.Clone(previous)
	if current == nil {
		current = map[string]string{}
	}
	for key, value := range map[string]string{"SMTPServer": "127.0.0.1", "SMTPPort": "2525", "SMTPAccount": "sender@example.test", "SMTPFrom": "sender@example.test", "SMTPToken": "SYNTHETIC_SMTP_SECRET_NOT_MAIL_CONTENT", "SystemName": "Synthetic demand email", "SMTPSSLEnabled": "false", "SMTPStartTLSEnabled": "false", "SMTPInsecureSkipVerify": "false", "SMTPForceAuthLogin": "false"} {
		current[key] = value
	}
	common.OptionMap = current
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = previous; common.OptionMapRWMutex.Unlock() })
}
func demandEmailDeliveries(t *testing.T, f *demandFixture) []model.DemandEmailDelivery {
	t.Helper()
	var out []model.DemandEmailDelivery
	require.NoError(t, f.db.Order("created_at,id").Find(&out).Error)
	return out
}
func demandEmailCount(t *testing.T, f *demandFixture, v any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, f.db.Model(v).Count(&n).Error)
	return n
}
func demandEmailProcess(t *testing.T, f *demandFixture, now time.Time, sender service.DemandEmailSender) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worked, err := service.ProcessDemandEmailOnce(ctx, f.db, now, sender)
	require.NoError(t, err)
	return worked
}
func demandEmailAcceptedSender(t *testing.T, f *demandFixture, calls *[]common.DemandSMTPMessage) service.DemandEmailSender {
	t.Helper()
	return func(ctx context.Context, cfg common.DemandSMTPConfig, msg common.DemandSMTPMessage, beforeData func() error) common.DemandSMTPResult {
		// This independent DB read would block on SQLite's single connection if SMTP
		// still ran inside the business/claim transaction. The actual sending fence
		// must already be committed before any SMTP network attempt begins.
		var row model.DemandEmailDelivery
		require.NoError(t, f.db.WithContext(ctx).Where("message_id = ? AND status = ?", msg.MessageID, "sending").First(&row).Error)
		require.NoError(t, beforeData())
		*calls = append(*calls, msg)
		return common.DemandSMTPResult{State: "accepted", Stage: "accepted"}
	}
}

func TestDemandEmailConfigDefaultsPermissionsValidationAndTestReceipts(t *testing.T) {
	f := newCommunityFixture(t)
	syntheticDemandSMTPOptions(t)
	var configReply struct {
		Config    service.DemandEmailConfig `json:"config"`
		SMTPReady bool                      `json:"smtp_ready"`
	}
	configReply = demandData[struct {
		Config    service.DemandEmailConfig `json:"config"`
		SMTPReady bool                      `json:"smtp_ready"`
	}](t, f.request(t, "admin", "GET", demandEmailTestBase+"/config", nil, 200))
	assert.False(t, configReply.Config.Enabled)
	assert.Empty(t, configReply.Config.Recipients)
	assert.EqualValues(t, 120, configReply.Config.MessageMergeSeconds)
	assert.True(t, configReply.SMTPReady)
	f.request(t, "owner", "GET", demandEmailTestBase+"/config", nil, 403)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["admin"].Id).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, authz.PermissionsMap{"demands": {"read": true, "write": true}, "community_demands": {"read": false, "write": false}}))
	f.request(t, "admin", "GET", demandEmailTestBase+"/config", nil, 403)
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, authz.PermissionsMap{"demands": {"read": true, "write": true}, "community_demands": {"read": true, "write": true}}))
	cfg := service.DefaultDemandEmailConfig()
	cfg.Recipients = []string{" First@Example.test ", "first@example.TEST", "second@example.test"}
	cfg.AdminBaseURL = "https://console.example.test/harness"
	cfg.Enabled = true
	saved := demandData[struct {
		Config service.DemandEmailConfig `json:"config"`
	}](t, f.request(t, "admin", "PUT", demandEmailTestBase+"/config", cfg, 200)).Config
	assert.Equal(t, []string{"first@example.test", "second@example.test"}, saved.Recipients)
	f.request(t, "admin", "PUT", demandEmailTestBase+"/config", cfg, 409)
	for _, tc := range []struct {
		name   string
		change func(*service.DemandEmailConfig)
	}{
		{"header injection", func(c *service.DemandEmailConfig) {
			c.Recipients = []string{"victim@example.test\r\nBcc:attacker@example.test"}
		}},
		{"display name recipient", func(c *service.DemandEmailConfig) { c.Recipients = []string{"Name <first@example.test>"} }},
		{"userinfo deep link", func(c *service.DemandEmailConfig) { c.AdminBaseURL = "https://secret@console.example.test" }},
		{"query credentials", func(c *service.DemandEmailConfig) { c.AdminBaseURL = "https://console.example.test?token=secret" }},
		{"empty query delimiter", func(c *service.DemandEmailConfig) { c.AdminBaseURL = "https://console.example.test?" }},
		{"empty fragment delimiter", func(c *service.DemandEmailConfig) { c.AdminBaseURL = "https://console.example.test#" }},
		{"public insecure deep link", func(c *service.DemandEmailConfig) { c.AdminBaseURL = "http://console.example.test" }},
		{"invalid merge window", func(c *service.DemandEmailConfig) { c.MessageMergeSeconds = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := saved
			bad.Recipients = append([]string{}, saved.Recipients...)
			tc.change(&bad)
			f.request(t, "admin", "PUT", demandEmailTestBase+"/config", bad, 400)
		})
	}
	type receipt struct {
		EventID     string   `json:"event_id"`
		DeliveryIDs []string `json:"delivery_ids"`
		Replayed    bool     `json:"replayed"`
	}
	action := uuid.NewString()
	body := map[string]any{"client_action_id": action, "revision": saved.Revision}
	first := demandData[receipt](t, f.request(t, "admin", "POST", demandEmailTestBase+"/test", body, 200))
	require.NotEmpty(t, first.EventID)
	require.Len(t, first.DeliveryIDs, 2)
	assert.False(t, first.Replayed)
	replay := demandData[receipt](t, f.request(t, "admin", "POST", demandEmailTestBase+"/test", body, 200))
	assert.Equal(t, first.EventID, replay.EventID)
	assert.ElementsMatch(t, first.DeliveryIDs, replay.DeliveryIDs)
	assert.True(t, replay.Replayed)
	original := demandData[receipt](t, f.request(t, "admin", "GET", demandEmailTestBase+"/test-receipts/"+action, nil, 200))
	assert.Equal(t, first.EventID, original.EventID)
	f.request(t, "admin", "POST", demandEmailTestBase+"/test", map[string]any{"client_action_id": action, "revision": saved.Revision + 1}, 409)
	raw := f.request(t, "admin", "GET", demandEmailTestBase+"/config", nil, 200)
	assert.NotContains(t, string(raw), "SYNTHETIC_SMTP_SECRET_NOT_MAIL_CONTENT")
	assert.NotContains(t, string(raw), "sender@example.test")
	assert.NotContains(t, string(raw), "SMTPToken")
	assert.EqualValues(t, 1, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	assert.EqualValues(t, 2, demandEmailCount(t, f, &model.DemandEmailEventRecipient{}))
}

func TestDemandEmailBusinessTransactionsReplayAndRollback(t *testing.T) {
	t.Run("disabled legacy and concurrent original receipt", func(t *testing.T) {
		f := newDemandFixture(t)
		syntheticDemandSMTPOptions(t)
		old := f.create(t, "email-disabled-original")
		assert.Zero(t, demandEmailCount(t, f, &model.DemandEmailEvent{}))
		configureDemandEmail(t, f)
		oldInput := service.DemandCreate{ClientRequestID: "email-disabled-original", EngineerID: f.engineers["engineer"], Text: "Synthetic website request"}
		got := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", oldInput, 200))
		assert.Equal(t, old.Demand.ID, got.Demand.ID)
		assert.True(t, got.Replayed)
		assert.Zero(t, demandEmailCount(t, f, &model.DemandEmailEvent{}))
		input := service.DemandCreate{ClientRequestID: uuid.NewString(), EngineerID: f.engineers["engineer"], Title: "A single committed request", Text: "Private initial message"}
		type result struct {
			r   service.DemandSendReceipt
			err error
		}
		done := make(chan result, 2)
		for range 2 {
			go func() {
				r, e := service.CreateDemand(f.db, service.DemandActor{UserID: f.users["owner"].Id}, input)
				done <- result{r, e}
			}()
		}
		a, b := <-done, <-done
		require.NoError(t, a.err)
		require.NoError(t, b.err)
		assert.Equal(t, a.r.Demand.ID, b.r.Demand.ID)
		assert.NotEqual(t, a.r.Replayed, b.r.Replayed)
		assert.EqualValues(t, 1, demandEmailCount(t, f, &model.DemandEmailEvent{}))
		assert.EqualValues(t, 2, demandEmailCount(t, f, &model.DemandEmailEventRecipient{}))
		assert.Len(t, demandEmailDeliveries(t, f), 2)
		// Treat the first response as lost. Recovering with the original key cannot
		// create a second event or second initial-message reminder.
		receipt := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", input, 200))
		assert.True(t, receipt.Replayed)
		assert.Equal(t, a.r.Demand.ID, receipt.Demand.ID)
		assert.EqualValues(t, 1, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	})
	t.Run("outbox persistence is atomic with business", func(t *testing.T) {
		f := newDemandFixture(t)
		configureDemandEmail(t, f)
		stmt := &gorm.Statement{DB: f.db}
		require.NoError(t, stmt.Parse(&model.DemandEmailEvent{}))
		table := stmt.Schema.Table
		require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("synthetic:email-outbox-failure", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == table {
				tx.AddError(errors.New("synthetic outbox unavailable"))
			}
		}))
		t.Cleanup(func() { _ = f.db.Callback().Create().Remove("synthetic:email-outbox-failure") })
		f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: uuid.NewString(), EngineerID: f.engineers["engineer"], Text: "Must roll back with its unavailable outbox"}, 503)
		assert.Zero(t, demandEmailCount(t, f, &model.Demand{}))
		assert.Zero(t, demandEmailCount(t, f, &model.DemandMessage{}))
		assert.Zero(t, demandEmailCount(t, f, &model.DemandEmailEvent{}))
		assert.Zero(t, demandEmailCount(t, f, &model.DemandEmailDelivery{}))
	})
}

func TestDemandEmailCustomerMessagesMerge120SecondsWithoutReadInference(t *testing.T) {
	f := newCommunityFixture(t)
	syntheticDemandSMTPOptions(t)
	cfg := configureDemandEmail(t, f)
	cfg.Types.OfficialCreated = false
	cfg.Types.CommunityPending = false
	var err error
	cfg, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	official := f.create(t, "email-official-message-topic")
	assert.Zero(t, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	image := f.upload(t, "owner", "PRIVATE-phone-15550001000.png", "image/png", communityPNG(t))
	// Header-only synthetic MP4 exercises attachment classification, not playback.
	video := f.upload(t, "owner", "PRIVATE-customer-chat.mp4", "video/mp4", append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}, make([]byte, 64)...))
	send := service.DemandSend{ClientMessageID: uuid.NewString(), Text: "PRIVATE_CHAT_FULL_TEXT +15550001000 /Users/private sk-private-key-do-not-email", AttachmentIDs: []string{image.ID, video.ID}}
	one := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands/"+official.Demand.ID+"/messages", send, 200))
	f.request(t, "owner", "POST", "/api/product/demands/"+official.Demand.ID+"/messages", send, 200)
	first := demandEmailDeliveries(t, f)
	require.Len(t, first, 2)
	for _, r := range first {
		assert.EqualValues(t, 120, r.DueAt-r.CreatedAt)
		assert.EqualValues(t, 1, r.EventCount)
	}
	f.request(t, "owner", "POST", "/api/product/demands/"+official.Demand.ID+"/messages", service.DemandSend{ClientMessageID: uuid.NewString(), Text: "Second private message"}, 200)
	f.request(t, "admin", "POST", "/api/demand-management/"+official.Demand.ID+"/messages", service.DemandSend{ClientMessageID: uuid.NewString(), Text: "Administrator response, not a customer reminder"}, 200)
	f.request(t, "engineer", "POST", "/api/engineer/demands/"+official.Demand.ID+"/messages", service.DemandSend{ClientMessageID: uuid.NewString(), Text: "Engineer response, not a customer reminder"}, 200)
	merged := demandEmailDeliveries(t, f)
	require.Len(t, merged, 2)
	for i, r := range merged {
		assert.EqualValues(t, 2, r.EventCount)
		assert.Equal(t, first[i].DueAt, r.DueAt)
	}
	assert.EqualValues(t, 2, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	assert.EqualValues(t, 4, demandEmailCount(t, f, &model.DemandEmailEventRecipient{}))
	// A real admin read cursor must not be invented as a shared email identity or
	// suppress the new-message policy clarified in addendum01.
	f.request(t, "admin", "POST", "/api/demand-management/"+official.Demand.ID+"/read", map[string]int64{"last_read_seq": one.Message.Seq}, 200)
	var mail []common.DemandSMTPMessage
	sender := demandEmailAcceptedSender(t, f, &mail)
	assert.False(t, demandEmailProcess(t, f, time.Unix(first[0].DueAt-1, 0), sender))
	for range 2 {
		assert.True(t, demandEmailProcess(t, f, time.Unix(first[0].DueAt+1, 0), sender))
	}
	require.Len(t, mail, 2)
	for _, m := range mail {
		assert.NotContains(t, m.Text, "PRIVATE_CHAT_FULL_TEXT")
		assert.NotContains(t, m.Text, "+15550001000")
		assert.NotContains(t, m.Text, "/Users/private")
		assert.NotContains(t, m.Text, "sk-private-key")
		assert.NotContains(t, m.Text, "SYNTHETIC_SMTP_SECRET_NOT_MAIL_CONTENT")
		assert.NotContains(t, m.Text, image.Name)
		assert.NotContains(t, m.Text, video.Name)
		assert.NotContains(t, m.Text, image.ID)
		assert.NotContains(t, m.Text, video.ID)
		assert.Contains(t, m.Text, "文字消息 2 条、图片 1 张、视频 1 个")
		assert.Contains(t, m.Text, official.Demand.Number)
		link := strings.TrimPrefix(strings.Fields(m.Text)[len(strings.Fields(m.Text))-1], "后台详情：")
		u, err := url.Parse(link)
		require.NoError(t, err)
		assert.Equal(t, "https", u.Scheme)
		assert.Equal(t, "console.example.test", u.Host)
		assert.Equal(t, "/harness/demand-management", u.Path)
		assert.Equal(t, "demands", u.Query().Get("tab"))
		assert.Equal(t, official.Demand.ID, u.Query().Get("demandId"))
	}
	assert.NotContains(t, mail[0].Text, mail[1].Recipient)
	assert.NotContains(t, mail[1].Text, mail[0].Recipient)
	f.request(t, "owner", "POST", "/api/product/demands/"+official.Demand.ID+"/messages", service.DemandSend{ClientMessageID: uuid.NewString(), Text: "A later message starts another fixed window"}, 200)
	later := demandEmailDeliveries(t, f)
	assert.Len(t, later, 4)
	var pending int
	for _, r := range later {
		if r.Status == "pending" {
			pending++
			assert.EqualValues(t, 1, r.EventCount)
			assert.EqualValues(t, 120, r.DueAt-r.CreatedAt)
		}
	}
	assert.Equal(t, 2, pending)
	community := f.communityCreate(t, "email-community-topic")
	assert.EqualValues(t, 3, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	f.request(t, "other", "POST", communityPath(community.Order.ID)+"/accept", map[string]string{"client_action_id": uuid.NewString()}, 200)
	f.request(t, "owner", "POST", communityPath(community.Order.ID)+"/messages", service.CommunitySend{ClientMessageID: uuid.NewString(), Text: "Customer public-order conversation"}, 201)
	f.request(t, "other", "POST", communityPath(community.Order.ID)+"/messages", service.CommunitySend{ClientMessageID: uuid.NewString(), Text: "Ordinary accepting user's reply"}, 201)
	f.request(t, "admin", "POST", "/api/community-demand-management/orders/"+community.Order.ID+"/messages", service.CommunitySend{ClientMessageID: uuid.NewString(), Text: "Admin self reply must not notify"}, 201)
	assert.EqualValues(t, 5, demandEmailCount(t, f, &model.DemandEmailEvent{}))
}

func TestDemandEmailClaimedMessageBatchStartsANewWindow(t *testing.T) {
	f := newDemandFixture(t)
	cfg := configureDemandEmail(t, f)
	cfg.Types.OfficialCreated = false
	_, err := service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	official := f.create(t, "email-claimed-message-topic")
	f.request(t, "owner", "POST", "/api/product/demands/"+official.Demand.ID+"/messages", service.DemandSend{ClientMessageID: uuid.NewString(), Text: "First batch"}, 200)
	original := demandEmailDeliveries(t, f)
	require.Len(t, original, 2)
	for _, d := range original {
		require.NoError(t, f.db.Model(&model.DemandEmailDelivery{}).Where("id = ?", d.ID).Updates(map[string]any{"status": "leased", "lease_token": uuid.NewString(), "lease_until": time.Now().Add(time.Hour).Unix()}).Error)
	}
	input := service.DemandSend{ClientMessageID: uuid.NewString(), Text: "A new message after the original batch was claimed"}
	f.request(t, "owner", "POST", "/api/product/demands/"+official.Demand.ID+"/messages", input, 200)
	f.request(t, "owner", "POST", "/api/product/demands/"+official.Demand.ID+"/messages", input, 200)
	rows := demandEmailDeliveries(t, f)
	require.Len(t, rows, 4)
	var leased, pending int
	for _, d := range rows {
		assert.EqualValues(t, 1, d.EventCount)
		assert.EqualValues(t, 120, d.DueAt-d.CreatedAt)
		switch d.Status {
		case "leased":
			leased++
		case "pending":
			pending++
		}
	}
	assert.Equal(t, 2, leased)
	assert.Equal(t, 2, pending)
	assert.EqualValues(t, 2, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	assert.EqualValues(t, 4, demandEmailCount(t, f, &model.DemandEmailEventRecipient{}))
}

func TestDemandEmailPendingCommunityAndMetadataPrivacy(t *testing.T) {
	f := newCommunityFixture(t)
	syntheticDemandSMTPOptions(t)
	configureDemandEmail(t, f)
	input := service.CommunityCreate{ClientRequestID: uuid.NewString(), PublishToCommunity: true, Title: "PRIVATE +15550001000 sk-DO-NOT-EMAIL", PublicSummary: "Public description is not an email summary", PublicBudget: "Contact foo@example.test", PrivateText: "PRIVATE_MESSAGE_BODY_NOT_FOR_SMTP"}
	created := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", input, 201))
	assert.Equal(t, "pending", created.Order.Listing.ApprovalStatus)
	again := demandData[communityReceipt](t, f.request(t, "owner", "POST", "/api/product/community-demands/orders", input, 200))
	assert.True(t, again.Replayed)
	assert.Equal(t, created.Order.ID, again.Order.ID)
	var events []model.DemandEmailEvent
	require.NoError(t, f.db.Find(&events).Error)
	require.Len(t, events, 1)
	assert.Equal(t, "community.created:"+created.Order.ID, events[0].EventKey)
	assert.Equal(t, "community_pending", events[0].Type)
	assert.Zero(t, events[0].TextCount, "the creation message is represented by the create event, never another customer-message event")
	var mail []common.DemandSMTPMessage
	for range 2 {
		assert.True(t, demandEmailProcess(t, f, time.Now().Add(time.Minute), demandEmailAcceptedSender(t, f, &mail)))
	}
	require.Len(t, mail, 2)
	for _, m := range mail {
		assert.Contains(t, m.Text, "新社区需求待审核")
		assert.Contains(t, m.Text, created.Order.Number)
		assert.Contains(t, m.Text, "orderId="+created.Order.ID)
		for _, secret := range []string{"+15550001000", "sk-DO-NOT-EMAIL", "foo@example.test", "PRIVATE_MESSAGE_BODY_NOT_FOR_SMTP", input.PublicSummary} {
			assert.NotContains(t, m.Subject+m.Text, secret)
		}
	}
}

func TestDemandEmailBeforeDataConfigGuardNeverSendsPayload(t *testing.T) {
	for _, change := range []string{"disabled", "recipient_removed", "type_disabled"} {
		t.Run(change, func(t *testing.T) {
			f := newDemandFixture(t)
			syntheticDemandSMTPOptions(t)
			configureDemandEmail(t, f)
			f.create(t, "email-final-guard-"+change)
			var guardedID string
			sender := func(ctx context.Context, cfg common.DemandSMTPConfig, m common.DemandSMTPMessage, beforeData func() error) common.DemandSMTPResult {
				var row model.DemandEmailDelivery
				require.NoError(t, f.db.WithContext(ctx).Where("message_id = ?", m.MessageID).First(&row).Error)
				guardedID = row.ID
				current, err := service.LoadDemandEmailConfig(f.db)
				require.NoError(t, err)
				switch change {
				case "disabled":
					current.Enabled = false
				case "recipient_removed":
					current.Recipients = []string{}
					for _, recipient := range []string{"first@example.test", "second@example.test"} {
						if recipient != m.Recipient {
							current.Recipients = append(current.Recipients, recipient)
						}
					}
				case "type_disabled":
					current.Types.OfficialCreated = false
				}
				_, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, current)
				require.NoError(t, err)
				require.Error(t, beforeData(), "a final config change must prevent SMTP payload transfer")
				return common.DemandSMTPResult{State: "not_sent", Code: "smtp_pre_data_rejected", Stage: "pre_data"}
			}
			assert.True(t, demandEmailProcess(t, f, time.Now().Add(time.Minute), sender))
			var after model.DemandEmailDelivery
			require.NoError(t, f.db.First(&after, "id = ?", guardedID).Error)
			assert.Equal(t, "paused", after.Status)
			assert.Equal(t, change, after.PauseReason)
		})
	}
}

func TestDemandEmailPerRecipientFailureRetryAndUnknownAreIndependent(t *testing.T) {
	f := newDemandFixture(t)
	syntheticDemandSMTPOptions(t)
	configureDemandEmail(t, f)
	f.create(t, "email-delivery-outcomes")
	attempts := map[string]int{}
	ids := map[string]string{}
	sender := func(ctx context.Context, cfg common.DemandSMTPConfig, msg common.DemandSMTPMessage, beforeData func() error) common.DemandSMTPResult {
		attempts[msg.Recipient]++
		if prior := ids[msg.Recipient]; prior != "" {
			assert.Equal(t, prior, msg.MessageID)
		} else {
			ids[msg.Recipient] = msg.MessageID
		}
		if msg.Recipient == "first@example.test" {
			return common.DemandSMTPResult{State: "failed", Code: "smtp_connect_failed", Stage: "connect", Retryable: true}
		}
		require.NoError(t, beforeData())
		return common.DemandSMTPResult{State: "unknown", Code: "smtp_result_unknown", Stage: "data", Retryable: true}
	}
	now := time.Now().Add(time.Minute)
	for range 8 {
		demandEmailProcess(t, f, now, sender)
		now = now.Add(10 * time.Minute)
	}
	assert.Equal(t, 3, attempts["first@example.test"])
	assert.Equal(t, 1, attempts["second@example.test"])
	rows := demandEmailDeliveries(t, f)
	require.Len(t, rows, 2)
	for _, r := range rows {
		switch r.Recipient {
		case "first@example.test":
			assert.Equal(t, "failed", r.Status)
			assert.EqualValues(t, 3, r.AttemptCount)
		case "second@example.test":
			assert.Equal(t, "unknown", r.Status)
			assert.EqualValues(t, 1, r.AttemptCount)
		}
	}
	assert.False(t, demandEmailProcess(t, f, now.Add(24*time.Hour), sender))
	assert.Equal(t, 1, attempts["second@example.test"])
}

func TestDemandEmailLeaseCrashAndConcurrentWorkersNeverBlindResend(t *testing.T) {
	t.Run("expired sending is unknown, pre-send lease may recover", func(t *testing.T) {
		f := newDemandFixture(t)
		syntheticDemandSMTPOptions(t)
		configureDemandEmail(t, f)
		f.create(t, "email-crash-fence")
		rows := demandEmailDeliveries(t, f)
		require.Len(t, rows, 2)
		now := time.Now().Add(time.Minute)
		require.NoError(t, f.db.Model(&model.DemandEmailDelivery{}).Where("id = ?", rows[0].ID).Updates(map[string]any{"status": "sending", "lease_token": "dead-dispatch-fence", "lease_until": now.Unix() - 1, "attempt_count": 1}).Error)
		require.NoError(t, f.db.Model(&model.DemandEmailDelivery{}).Where("id = ?", rows[1].ID).Updates(map[string]any{"status": "leased", "lease_token": "dead-before-fence", "lease_until": now.Unix() - 1}).Error)
		var mail []common.DemandSMTPMessage
		sender := demandEmailAcceptedSender(t, f, &mail)
		for range 4 {
			demandEmailProcess(t, f, now, sender)
		}
		require.Len(t, mail, 1)
		assert.Equal(t, rows[1].Recipient, mail[0].Recipient)
		var unknown model.DemandEmailDelivery
		require.NoError(t, f.db.First(&unknown, "id = ?", rows[0].ID).Error)
		assert.Equal(t, "unknown", unknown.Status)
		assert.False(t, demandEmailProcess(t, f, now.Add(time.Hour), sender))
		assert.Len(t, mail, 1)
	})
	t.Run("two workers single lease per recipient", func(t *testing.T) {
		f := newDemandFixture(t)
		syntheticDemandSMTPOptions(t)
		configureDemandEmail(t, f)
		f.create(t, "email-worker-race")
		now := time.Now().Add(time.Minute)
		start := make(chan struct{})
		results := make(chan error, 2)
		var mu sync.Mutex
		calls := map[string]int{}
		sender := func(ctx context.Context, cfg common.DemandSMTPConfig, msg common.DemandSMTPMessage, beforeData func() error) common.DemandSMTPResult {
			if err := beforeData(); err != nil {
				return common.DemandSMTPResult{State: "not_sent", Code: "smtp_fence_rejected", Stage: "fence"}
			}
			mu.Lock()
			calls[msg.Recipient]++
			mu.Unlock()
			return common.DemandSMTPResult{State: "accepted", Stage: "accepted"}
		}
		for range 2 {
			go func() {
				<-start
				_, err := service.ProcessDemandEmailOnce(context.Background(), f.db, now, sender)
				results <- err
			}()
		}
		close(start)
		require.NoError(t, <-results)
		require.NoError(t, <-results)
		for range 2 {
			demandEmailProcess(t, f, now, sender)
		}
		assert.Equal(t, map[string]int{"first@example.test": 1, "second@example.test": 1}, calls)
	})
}

func TestDemandEmailDisableAndRecipientRemovalPauseWithoutBackfill(t *testing.T) {
	f := newDemandFixture(t)
	syntheticDemandSMTPOptions(t)
	cfg := configureDemandEmail(t, f)
	f.create(t, "email-before-disable")
	cfg.Enabled = false
	var err error
	cfg, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	var calls []common.DemandSMTPMessage
	sender := demandEmailAcceptedSender(t, f, &calls)
	assert.False(t, demandEmailProcess(t, f, time.Now().Add(time.Hour), sender))
	assert.Empty(t, calls)
	for _, r := range demandEmailDeliveries(t, f) {
		assert.Equal(t, "paused", r.Status)
	}
	f.create(t, "email-created-while-off")
	assert.EqualValues(t, 1, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	cfg.Enabled = true
	cfg, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	assert.False(t, demandEmailProcess(t, f, time.Now().Add(time.Hour), sender))
	f.create(t, "email-before-recipient-removal")
	cfg.Recipients = []string{"first@example.test"}
	cfg, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	for range 2 {
		demandEmailProcess(t, f, time.Now().Add(time.Hour), sender)
	}
	require.Len(t, calls, 1)
	assert.Equal(t, "first@example.test", calls[0].Recipient)
	cfg.Recipients = []string{"first@example.test", "second@example.test"}
	_, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	assert.False(t, demandEmailProcess(t, f, time.Now().Add(time.Hour), sender))
	assert.Len(t, calls, 1)
}

func TestDemandEmailAdditiveMigrationKeepsOriginalAuthAndFinancialEvidence(t *testing.T) {
	f := newDemandFixture(t)
	original := f.create(t, "pre-email-released-key")
	task := model.Task{TaskID: "synthetic-original-email-task", UserId: f.users["owner"].Id, Status: model.TaskStatusUnknown, Quota: 77, Data: []byte(`{"original":"unchanged"}`)}
	event := model.WalletFundingEvent{UserID: f.users["owner"].Id, RequestID: "synthetic-original-email-charge", Operation: "consume", DeltaQuota: -77, KnownCashRevenueCNY: "0.12"}
	log := model.Log{UserId: f.users["owner"].Id, Content: "synthetic-original-email-evidence", Quota: 77}
	require.NoError(t, f.db.Create(&task).Error)
	require.NoError(t, f.db.Create(&event).Error)
	require.NoError(t, f.db.Create(&log).Error)
	require.NoError(t, f.db.First(&task, task.ID).Error)
	var sessions []model.UserSession
	var users []model.User
	require.NoError(t, f.db.Order("sid").Find(&sessions).Error)
	require.NoError(t, f.db.Order("id").Find(&users).Error)
	// Latest released demand schema has no mail tables. Existing demand/auth/task
	// definitions remain unchanged; only these independent tables are introduced.
	emailTables := []any{&model.DemandEmailEventRecipient{}, &model.DemandEmailMergeHead{}, &model.DemandEmailDelivery{}, &model.DemandEmailEvent{}, &model.DemandEmailConfiguration{}}
	require.NoError(t, f.db.Migrator().DropTable(emailTables...))
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(model.DemandTables()...))
	}
	var afterSessions []model.UserSession
	var afterUsers []model.User
	require.NoError(t, f.db.Order("sid").Find(&afterSessions).Error)
	require.NoError(t, f.db.Order("id").Find(&afterUsers).Error)
	assert.Equal(t, sessions, afterSessions)
	assert.Equal(t, users, afterUsers)
	var gotTask model.Task
	var gotEvent model.WalletFundingEvent
	var gotLog model.Log
	require.NoError(t, f.db.First(&gotTask, task.ID).Error)
	require.NoError(t, f.db.First(&gotEvent, event.ID).Error)
	require.NoError(t, f.db.First(&gotLog, log.Id).Error)
	assert.Equal(t, task, gotTask)
	assert.Equal(t, event, gotEvent)
	assert.Equal(t, log, gotLog)
	replay := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: "pre-email-released-key", EngineerID: f.engineers["engineer"], Text: "Synthetic website request"}, 200))
	assert.True(t, replay.Replayed)
	assert.Equal(t, original.Demand.ID, replay.Demand.ID)
	assert.Zero(t, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	assert.Empty(t, demandEmailDeliveries(t, f))
}

func TestDemandEmailDeliveriesStablePaginationFiltersAndSafeProjection(t *testing.T) {
	f := newCommunityFixture(t)
	syntheticDemandSMTPOptions(t)
	configureDemandEmail(t, f)
	f.create(t, "email-list-official-one")
	f.create(t, "email-list-official-two")
	f.request(t, "owner", "POST", "/api/product/community-demands/orders", service.CommunityCreate{ClientRequestID: uuid.NewString(), PublishToCommunity: true, Title: "A pending community project", PublicSummary: "Metadata only"}, 201)
	rows := demandEmailDeliveries(t, f)
	require.Len(t, rows, 6)
	// Force equal creation times to exercise the specified stable UUID tie-break.
	at := time.Now().Unix()
	require.NoError(t, f.db.Model(&model.DemandEmailDelivery{}).Where("1 = 1").Updates(map[string]any{"created_at": at, "lease_token": "PRIVATE_LEASE_TOKEN_NOT_A_DTO_FIELD", "message_id": "PRIVATE_MESSAGE_ID_NOT_A_DTO_FIELD"}).Error)
	unknownID := ""
	for _, d := range rows {
		if d.Type == "official_created" {
			unknownID = d.ID
			break
		}
	}
	require.NotEmpty(t, unknownID)
	require.NoError(t, f.db.Model(&model.DemandEmailDelivery{}).Where("id = ?", unknownID).Update("status", "unknown").Error)
	// Fields with snake-case names need explicit JSON tags for detail links.
	type view struct {
		ID        string `json:"id"`
		Domain    string `json:"domain"`
		EntityID  string `json:"entity_id"`
		Type      string `json:"type"`
		Recipient string `json:"recipient"`
		Status    string `json:"status"`
		DetailURL string `json:"detail_url"`
		CreatedAt int64  `json:"created_at"`
	}
	type result struct {
		Items      []view `json:"items"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	var gotIDs []string
	cursor := ""
	for i := 0; i < 3; i++ {
		raw := f.request(t, "admin", "GET", demandEmailTestBase+"/deliveries?limit=2&cursor="+url.QueryEscape(cursor), nil, 200)
		for _, hidden := range []string{"PRIVATE_LEASE_TOKEN_NOT_A_DTO_FIELD", "PRIVATE_MESSAGE_ID_NOT_A_DTO_FIELD", "lease_token", "lease_until", "recipient_key", "message_id", "SMTPToken", "SYNTHETIC_SMTP_SECRET_NOT_MAIL_CONTENT"} {
			assert.NotContains(t, string(raw), hidden)
		}
		p := demandData[result](t, raw)
		require.Len(t, p.Items, 2)
		for _, d := range p.Items {
			assert.Equal(t, at, d.CreatedAt)
			u, err := url.Parse(d.DetailURL)
			require.NoError(t, err)
			assert.Equal(t, "/harness/demand-management", u.Path)
			if d.Domain == "official" {
				assert.Equal(t, "demands", u.Query().Get("tab"))
				assert.Equal(t, d.EntityID, u.Query().Get("demandId"))
			} else {
				assert.Equal(t, "community", u.Query().Get("tab"))
				assert.Equal(t, d.EntityID, u.Query().Get("orderId"))
			}
			gotIDs = append(gotIDs, d.ID)
		}
		assert.Equal(t, i < 2, p.HasMore)
		if i < 2 {
			require.NotEmpty(t, p.NextCursor)
		} else {
			assert.Empty(t, p.NextCursor)
		}
		cursor = p.NextCursor
	}
	var expected []string
	for _, d := range rows {
		expected = append(expected, d.ID)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(expected)))
	assert.Equal(t, expected, gotIDs)
	filtered := demandData[result](t, f.request(t, "admin", "GET", demandEmailTestBase+"/deliveries?status=unknown&type=official_created", nil, 200))
	require.Len(t, filtered.Items, 1)
	assert.Equal(t, unknownID, filtered.Items[0].ID)
	community := demandData[result](t, f.request(t, "admin", "GET", demandEmailTestBase+"/deliveries?type=community_pending", nil, 200))
	require.Len(t, community.Items, 2)
	for _, d := range community.Items {
		assert.Equal(t, "community", d.Domain)
		assert.Equal(t, "pending", d.Status)
	}
	for _, query := range []string{"limit=0", "limit=101", "cursor=bad-cursor", "status=made-up", "type=made-up"} {
		f.request(t, "admin", "GET", demandEmailTestBase+"/deliveries?"+query, nil, 400)
	}
	f.request(t, "owner", "GET", demandEmailTestBase+"/deliveries", nil, 403)
}

func TestDemandEmailExplicitTestWhileOffKeepsOriginalReceiptAfterConfigChanges(t *testing.T) {
	f := newDemandFixture(t)
	syntheticDemandSMTPOptions(t)
	cfg := service.DefaultDemandEmailConfig()
	cfg.Recipients = []string{"first@example.test", "second@example.test"}
	cfg.AdminBaseURL = "https://console.example.test/harness"
	var err error
	cfg, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	key := uuid.NewString()
	body := map[string]any{"client_action_id": key, "revision": cfg.Revision}
	first := demandData[service.DemandEmailTestReceipt](t, f.request(t, "admin", "POST", demandEmailTestBase+"/test", body, 200))
	require.Len(t, first.DeliveryIDs, 2)
	cfg.Recipients = []string{"third@example.test"}
	cfg.AdminBaseURL = "https://console.example.test/changed"
	cfg, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
	require.NoError(t, err)
	replay := demandData[service.DemandEmailTestReceipt](t, f.request(t, "admin", "POST", demandEmailTestBase+"/test", body, 200))
	assert.True(t, replay.Replayed)
	assert.Equal(t, first.EventID, replay.EventID)
	assert.ElementsMatch(t, first.DeliveryIDs, replay.DeliveryIDs)
	f.request(t, "admin", "POST", demandEmailTestBase+"/test", map[string]any{"client_action_id": key, "revision": cfg.Revision}, 409)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["other"].Id).Update("role", common.RoleRootUser).Error)
	f.request(t, "other", "GET", demandEmailTestBase+"/test-receipts/"+key, nil, 404)
	f.request(t, "admin", "GET", demandEmailTestBase+"/test-receipts/"+uuid.NewString(), nil, 404)
	assert.EqualValues(t, 1, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	assert.EqualValues(t, 2, demandEmailCount(t, f, &model.DemandEmailEventRecipient{}))
	for _, d := range demandEmailDeliveries(t, f) {
		assert.NotEqual(t, "third@example.test", d.Recipient)
	}
}

func TestDemandEmailRoutesEnforceWritePermissionsSessionAndOrigin(t *testing.T) {
	f := newDemandFixture(t)
	syntheticDemandSMTPOptions(t)
	cfg := configureDemandEmail(t, f)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["admin"].Id).Update("role", common.RoleAdminUser).Error)
	permissions := authz.PermissionsMap{"demands": {"read": true, "write": true}, "community_demands": {"read": true, "write": false}}
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, permissions))
	f.request(t, "admin", "GET", demandEmailTestBase+"/config", nil, 200)
	f.request(t, "admin", "PUT", demandEmailTestBase+"/config", cfg, 403)
	f.request(t, "admin", "POST", demandEmailTestBase+"/test", map[string]any{"client_action_id": uuid.NewString(), "revision": cfg.Revision}, 403)
	permissions["community_demands"]["write"] = true
	permissions["demands"]["write"] = false
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, permissions))
	f.request(t, "admin", "PUT", demandEmailTestBase+"/config", cfg, 403)
	permissions["demands"]["write"] = true
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, permissions))
	rawPAT := "nap_synthetic_demand_email_auth_token"
	pat := model.UserAccessToken{Name: "synthetic-email-only", TokenHash: model.AccessTokenFingerprint(rawPAT), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, pat.SetScopes([]string{"*"}))
	require.NoError(t, model.CreateUserAccessToken(f.users["admin"].Id, &pat, 10))
	f.tokens["email-pat"] = rawPAT
	f.request(t, "email-pat", "GET", demandEmailTestBase+"/config", nil, 403)
	f.request(t, "email-pat", "GET", demandEmailTestBase+"/deliveries", nil, 403)
	f.request(t, "email-pat", "PUT", demandEmailTestBase+"/config", cfg, 403)
	f.request(t, "email-pat", "POST", demandEmailTestBase+"/test", map[string]any{"client_action_id": uuid.NewString(), "revision": cfg.Revision}, 403)
	// Dashboard auth accepts case-insensitive Bearer, while the inherited demand
	// Origin guard validates the non-canonical-header branch instead of skipping it.
	body, err := common.Marshal(cfg)
	require.NoError(t, err)
	req := httptest.NewRequest("PUT", "https://console.example.test"+demandEmailTestBase+"/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "bearer "+f.tokens["admin"])
	req.Header.Set("Origin", "https://untrusted-cross-origin.invalid")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	assert.Equal(t, 403, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "AUTH_ORIGIN_FORBIDDEN")
	after, err := service.LoadDemandEmailConfig(f.db)
	require.NoError(t, err)
	assert.Equal(t, cfg.Revision, after.Revision)
	assert.Zero(t, demandEmailCount(t, f, &model.DemandEmailEvent{}))
}

func TestDemandEmailTransientRetryBackoffStartsWhenSenderReturns(t *testing.T) {
	f := newDemandFixture(t)
	syntheticDemandSMTPOptions(t)
	configureDemandEmail(t, f)
	f.create(t, "email-finish-time-backoff")
	processAt := time.Now().Add(-time.Minute)
	require.NoError(t, f.db.Model(&model.DemandEmailDelivery{}).Where("1 = 1").Update("due_at", processAt.Unix()).Error)
	var deliveryID string
	var returnedAt int64
	sender := func(ctx context.Context, cfg common.DemandSMTPConfig, msg common.DemandSMTPMessage, beforeData func() error) common.DemandSMTPResult {
		var d model.DemandEmailDelivery
		require.NoError(t, f.db.Where("message_id = ?", msg.MessageID).First(&d).Error)
		deliveryID = d.ID
		returnedAt = time.Now().Unix()
		assert.GreaterOrEqual(t, d.LeaseUntil, returnedAt+80, "the sending fence must renew a stale preclaim lease from current wall time")
		return common.DemandSMTPResult{State: "failed", Code: "smtp_connect_failed", Stage: "connect", Retryable: true}
	}
	assert.True(t, demandEmailProcess(t, f, processAt, sender))
	var d model.DemandEmailDelivery
	require.NoError(t, f.db.First(&d, "id = ?", deliveryID).Error)
	assert.Equal(t, "pending", d.Status)
	assert.GreaterOrEqual(t, d.DueAt, returnedAt+5)
	assert.GreaterOrEqual(t, d.UpdatedAt, returnedAt)
}

func TestDemandEmailUnicodeAndInternationalMetadataAreRedactedWithoutChangingBusiness(t *testing.T) {
	for _, tc := range []struct{ name, title, budget string }{
		{"fullwidth", "联系１３８００１３８０００", "请联系＋６０１２７０７８６２５"},
		{"international", "Call +1 (555) 000-1000", "Contact +60 12 707 8625"},
		{"separators", "联系 138-0013-8000", "联系 (138) 0013 8000"},
		{"fullwidth key", "凭据 ｓｋ－ＦＵＬＬＷＩＤＴＨＫＥＹ", "凭据 ｔｏｋｅｎ＝ｐｒｉｖａｔｅ"},
		{"unicode digits and dashes", "联系١٣٨٠٠١٣٨٠٠٠", "联系 138—0013—8000"},
		{"format characters and spaces", "联系 1\u200b3\u200b8\u200b0\u200b0\u200b1\u200b3\u200b8\u200b0\u200b0\u200b0", "联系 138\u00a00013\u00a08000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDemandFixture(t)
			syntheticDemandSMTPOptions(t)
			configureDemandEmail(t, f)
			in := service.DemandCreate{ClientRequestID: uuid.NewString(), EngineerID: f.engineers["engineer"], Title: tc.title, Budget: tc.budget, Text: "Private body not copied into mail"}
			created := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", in, 201))
			assert.Equal(t, tc.title, created.Demand.Title)
			assert.Equal(t, tc.budget, created.Demand.Budget)
			replayed := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", in, 200))
			assert.True(t, replayed.Replayed)
			assert.Equal(t, created.Demand.ID, replayed.Demand.ID)
			var event model.DemandEmailEvent
			require.NoError(t, f.db.First(&event).Error)
			assert.NotEqual(t, tc.title, event.Title)
			assert.NotEqual(t, tc.budget, event.Budget)
			raw := f.request(t, "admin", "GET", demandEmailTestBase+"/deliveries", nil, 200)
			assert.NotContains(t, string(raw), tc.title)
			assert.NotContains(t, string(raw), tc.budget)
			var calls []common.DemandSMTPMessage
			for range 2 {
				demandEmailProcess(t, f, time.Now().Add(time.Minute), demandEmailAcceptedSender(t, f, &calls))
			}
			require.Len(t, calls, 2)
			for _, m := range calls {
				assert.NotContains(t, m.Subject+m.Text, tc.title)
				assert.NotContains(t, m.Subject+m.Text, tc.budget)
			}
		})
	}
}

func TestDemandEmailBaseURLAcceptsEncodedSubpath(t *testing.T) {
	f := newDemandFixture(t)
	cfg := configureDemandEmail(t, f)
	for _, tc := range []struct{ encoded, decoded string }{
		{"/control%20panel/subpath", "/control panel/subpath"},
		{"/control%3Fpanel/subpath", "/control?panel/subpath"},
		{"/control%23panel/subpath", "/control#panel/subpath"},
	} {
		cfg.AdminBaseURL = "https://console.example.test" + tc.encoded
		var err error
		cfg, err = service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
		require.NoError(t, err)
		link := service.DemandEmailDetailURL(cfg, "official", "12345678-1234-4234-8234-123456789abc")
		u, err := url.Parse(link)
		require.NoError(t, err)
		assert.Equal(t, tc.decoded+"/demand-management", u.Path)
		assert.Equal(t, tc.encoded+"/demand-management", u.EscapedPath())
		assert.Equal(t, url.Values{"tab": {"demands"}, "demandId": {"12345678-1234-4234-8234-123456789abc"}}, u.Query())
		assert.Empty(t, u.Fragment)
	}
}

func TestDemandEmailRetryAndRecoveredLeaseBatchesRemainFrozen(t *testing.T) {
	for _, mode := range []string{"transient retry", "recovered pre-send lease"} {
		t.Run(mode, func(t *testing.T) {
			f := newDemandFixture(t)
			syntheticDemandSMTPOptions(t)
			cfg := configureDemandEmail(t, f)
			cfg.Recipients = []string{"first@example.test"}
			cfg.Types.OfficialCreated = false
			_, err := service.SaveDemandEmailConfig(f.db, f.users["admin"].Id, cfg)
			require.NoError(t, err)
			d := f.create(t, uuid.NewString())
			f.request(t, "owner", "POST", "/api/product/demands/"+d.Demand.ID+"/messages", service.DemandSend{ClientMessageID: uuid.NewString(), Text: "Original frozen batch"}, 200)
			original := demandEmailDeliveries(t, f)
			require.Len(t, original, 1)
			id := original[0].ID
			if mode == "transient retry" {
				// Logical original window ended two seconds ago; transport failure now
				// opens only a retry schedule, never a new merge opportunity.
				at := time.Now().Add(-2 * time.Second)
				require.NoError(t, f.db.Model(&model.DemandEmailDelivery{}).Where("id = ?", id).Update("due_at", at.Unix()).Error)
				sender := func(context.Context, common.DemandSMTPConfig, common.DemandSMTPMessage, func() error) common.DemandSMTPResult {
					return common.DemandSMTPResult{State: "failed", Code: "smtp_connect_failed", Stage: "connect", Retryable: true}
				}
				assert.True(t, demandEmailProcess(t, f, at, sender))
			} else {
				// An expired pre-DATA lease is safe to recover, but its nonempty claim
				// token must keep that already claimed batch frozen even with a future
				// due time after a wall-clock adjustment. No SMTP attempt is needed.
				now := time.Now()
				require.NoError(t, f.db.Model(&model.DemandEmailDelivery{}).Where("id = ?", id).Updates(map[string]any{"status": "leased", "lease_token": uuid.NewString(), "lease_until": now.Unix() - 1, "due_at": now.Add(time.Minute).Unix()}).Error)
				assert.False(t, demandEmailProcess(t, f, now, func(context.Context, common.DemandSMTPConfig, common.DemandSMTPMessage, func() error) common.DemandSMTPResult {
					t.Error("no SMTP while recovered batch is not due")
					return common.DemandSMTPResult{State: "unknown"}
				}))
			}
			var frozen model.DemandEmailDelivery
			require.NoError(t, f.db.First(&frozen, "id = ?", id).Error)
			assert.Equal(t, "pending", frozen.Status)
			assert.NotEmpty(t, frozen.LeaseToken)
			assert.Greater(t, frozen.DueAt, time.Now().Unix())
			f.request(t, "owner", "POST", "/api/product/demands/"+d.Demand.ID+"/messages", service.DemandSend{ClientMessageID: uuid.NewString(), Text: "New message must own a separate fixed window"}, 200)
			rows := demandEmailDeliveries(t, f)
			require.Len(t, rows, 2)
			var next model.DemandEmailDelivery
			for _, row := range rows {
				if row.ID != id {
					next = row
				}
			}
			require.NotEmpty(t, next.ID)
			assert.EqualValues(t, 1, next.EventCount)
			assert.EqualValues(t, 120, next.DueAt-next.CreatedAt)
			var unchanged model.DemandEmailDelivery
			require.NoError(t, f.db.First(&unchanged, "id = ?", id).Error)
			assert.EqualValues(t, 1, unchanged.EventCount)
			assert.Equal(t, frozen.DueAt, unchanged.DueAt)
			var joins []model.DemandEmailEventRecipient
			require.NoError(t, f.db.Where("delivery_id = ?", id).Find(&joins).Error)
			require.Len(t, joins, 1)
			var calls []common.DemandSMTPMessage
			sender := demandEmailAcceptedSender(t, f, &calls)
			for range 2 {
				assert.True(t, demandEmailProcess(t, f, time.Now().Add(5*time.Minute), sender))
			}
			require.Len(t, calls, 2)
			for _, m := range calls {
				assert.Contains(t, m.Text, "新增 1 条客户消息")
				assert.Contains(t, m.Text, "文字消息 1 条")
			}
		})
	}
}

func TestDemandEmailConcurrentExplicitTestKeepsOneOriginalReceipt(t *testing.T) {
	f := newDemandFixture(t)
	syntheticDemandSMTPOptions(t)
	cfg := configureDemandEmail(t, f)
	key := uuid.NewString()
	type result struct {
		receipt service.DemandEmailTestReceipt
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			receipt, err := service.CreateDemandEmailTest(f.db, f.users["admin"].Id, key, cfg.Revision)
			results <- result{receipt, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	assert.NotEmpty(t, a.receipt.EventID)
	assert.Equal(t, a.receipt.EventID, b.receipt.EventID)
	assert.ElementsMatch(t, a.receipt.DeliveryIDs, b.receipt.DeliveryIDs)
	assert.NotEqual(t, a.receipt.Replayed, b.receipt.Replayed)
	assert.EqualValues(t, 1, demandEmailCount(t, f, &model.DemandEmailEvent{}))
	assert.EqualValues(t, 2, demandEmailCount(t, f, &model.DemandEmailEventRecipient{}))
	assert.Len(t, demandEmailDeliveries(t, f), 2)
}
