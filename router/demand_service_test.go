package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type demandFixture struct {
	engine    *gin.Engine
	db        *gorm.DB
	users     map[string]model.User
	tokens    map[string]string
	bundles   map[string]*service.AuthBundle
	engineers map[string]string
	config    service.DemandConfigVersion
}

func newDemandFixture(t *testing.T) *demandFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	oldMaster := common.IsMasterNode
	common.IsMasterNode = true
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMemory, oldRedis, oldSecure := common.MemoryCacheEnabled, common.RedisEnabled, common.SessionCookieSecure
	oldSecret, oldCrypto := common.SessionSecret, common.CryptoSecret
	oldCritical := common.CriticalRateLimitEnable
	oldPublic := system_setting.TaskPublicAddress
	oldMain, oldLogKind := common.MainDatabaseType(), common.LogDatabaseType()
	var dialect gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "demand-synthetic.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if dsn := os.Getenv("DEMAND_TEST_POSTGRES_DSN"); dsn != "" {
		require.True(t, strings.Contains(dsn, "127.0.0.1:43306/test_demand_") || strings.Contains(dsn, "127.0.0.1:43406/test_demand_"))
		dialect = postgres.Open(dsn)
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	} else if dsn := os.Getenv("DEMAND_TEST_MYSQL_DSN"); dsn != "" {
		require.True(t, strings.Contains(dsn, "tcp(127.0.0.1:43307)/test_demand_") || strings.Contains(dsn, "tcp(127.0.0.1:43407)/test_demand_"))
		dialect = mysql.Open(dsn)
		common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
	} else {
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	if !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		sqlDB.SetMaxOpenConns(4)
	}
	model.DB, model.LOG_DB = db, db
	common.MemoryCacheEnabled, common.RedisEnabled, common.CriticalRateLimitEnable = false, false, false
	common.SessionCookieSecure = true
	common.SessionSecret = "synthetic-demand-session-" + t.Name()
	common.CryptoSecret = "synthetic-demand-media-" + t.Name()
	system_setting.TaskPublicAddress = "http://127.0.0.1:43185"
	t.Setenv("DEMAND_STORAGE_DIR", t.TempDir())
	t.Cleanup(func() {
		common.IsMasterNode = oldMaster
		_ = sqlDB.Close()
		model.DB, model.LOG_DB = oldDB, oldLog
		common.MemoryCacheEnabled, common.RedisEnabled, common.SessionCookieSecure = oldMemory, oldRedis, oldSecure
		common.SessionSecret, common.CryptoSecret = oldSecret, oldCrypto
		common.CriticalRateLimitEnable = oldCritical
		system_setting.TaskPublicAddress = oldPublic
		common.SetDatabaseTypes(oldMain, oldLogKind)
	})
	tables := append(model.DemandTables(), &model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.AuthFlow{}, &model.Token{}, &model.Option{}, &model.AuditLog{}, &model.AuthzRole{}, &model.CasbinRule{}, &model.Task{}, &model.Log{}, &model.WalletFundingEvent{})
	if os.Getenv("DEMAND_TEST_POSTGRES_DSN") != "" || os.Getenv("DEMAND_TEST_MYSQL_DSN") != "" {
		require.NoError(t, db.Migrator().DropTable(tables...))
		t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })
	}
	require.NoError(t, db.AutoMigrate(tables...))
	require.NoError(t, db.AutoMigrate(model.DemandTables()...))
	require.NoError(t, authz.Init(db))
	f := &demandFixture{db: db, users: map[string]model.User{}, tokens: map[string]string{}, bundles: map[string]*service.AuthBundle{}, engineers: map[string]string{}}
	for _, name := range []string{"owner", "other", "engineer", "replacement", "admin"} {
		role := common.RoleCommonUser
		if name == "admin" {
			role = common.RoleRootUser
		}
		phone := "+1555000" + strconv.Itoa(1000+len(f.users))
		u := model.User{Username: "synthetic-" + name, DisplayName: name, AffCode: strings.ReplaceAll(uuid.NewString(), "-", "")[:12], Role: role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Quota: 12345, PhoneE164: &phone}
		require.NoError(t, db.Create(&u).Error)
		session, err := service.CreateLoginSession(u.Id, "synthetic-local-fixture", "127.0.0.1", "demand-contract")
		require.NoError(t, err)
		f.users[name] = u
		f.tokens[name] = session.AccessToken
		f.bundles[name] = session
	}
	f.config = service.DemandConfigVersion{Config: service.DefaultDemandConfig()}
	f.config.Config.Enabled = true
	f.config.Config.Categories = []service.DemandCategory{{ID: "software", Name: "Software", Enabled: true}}
	f.config, err = service.SaveDemandConfig(db, f.config)
	require.NoError(t, err)
	for _, name := range []string{"engineer", "replacement"} {
		v, err := service.SaveDemandEngineer(db, "", service.DemandEngineerView{UserID: f.users[name].Id, DisplayName: "SYNTHETIC " + name, CategoryIDs: []string{"software"}, Enabled: true, Published: true, Accepting: true, Capacity: 20})
		require.NoError(t, err)
		f.engineers[name] = v.ID
	}
	f.engine = gin.New()
	middleware.SetUpLogger(f.engine)
	f.engine.Use(middleware.RequestId())
	SetDemandRoutes(f.engine.Group("/api"))
	f.engine.GET("/api/user/self", middleware.UserAuth(), func(c *gin.Context) {
		u, err := model.GetUserById(c.GetInt("id"), false)
		if err != nil {
			c.Status(500)
			return
		}
		common.ApiSuccess(c, gin.H{"id": u.Id, "quota": u.Quota, "group": u.Group, "phone_e164": u.PhoneE164, "username": u.Username, "display_name": u.DisplayName, "role": u.Role, "status": u.Status, "permissions": gin.H{"admin_permissions": authz.Capabilities(u.Id, u.Role)}})
	})
	return f
}
func (f *demandFixture) request(t *testing.T, who, method, path string, body any, status int) []byte {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = common.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+f.tokens[who])
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, status, w.Code, w.Body.String())
	return w.Body.Bytes()
}
func demandData[T any](t *testing.T, b []byte) T {
	t.Helper()
	var out struct {
		Success bool `json:"success"`
		Data    T    `json:"data"`
	}
	require.NoError(t, common.Unmarshal(b, &out))
	require.True(t, out.Success, string(b))
	return out.Data
}
func (f *demandFixture) create(t *testing.T, key string) service.DemandSendReceipt {
	return demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: key, EngineerID: f.engineers["engineer"], Text: "Synthetic website request"}, 201))
}

func TestDemandHTTPIdempotencyRecoveryAndIsolation(t *testing.T) {
	f := newDemandFixture(t)
	first := f.create(t, "create-demand-001")
	id := first.Demand.ID
	input := service.DemandCreate{ClientRequestID: "create-demand-001", EngineerID: f.engineers["engineer"], Text: "Synthetic website request"}
	replay := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", input, 200))
	assert.True(t, replay.Replayed)
	assert.Equal(t, id, replay.Demand.ID)
	input.Text = "Different body"
	f.request(t, "owner", "POST", "/api/product/demands", input, 409)
	second := f.create(t, "create-demand-002")
	assert.NotEqual(t, id, second.Demand.ID)
	for _, path := range []string{"/api/product/demands/" + id, "/api/product/demands/" + id + "/messages"} {
		f.request(t, "other", "GET", path, nil, 404)
		f.request(t, "", "GET", path, nil, 401)
	}
	message := service.DemandSend{ClientMessageID: "message-client-001", Text: "Supplement"}
	r := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands/"+id+"/messages", message, 200))
	assert.EqualValues(t, 2, r.Message.Seq)
	again := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands/"+id+"/messages", message, 200))
	assert.Equal(t, r.Message.ID, again.Message.ID)
	assert.True(t, again.Replayed)
	page := demandData[struct {
		Items []service.DemandMessageView `json:"items"`
		Next  int64                       `json:"next_after_seq"`
		More  bool                        `json:"has_more"`
	}](t, f.request(t, "engineer", "GET", "/api/engineer/demands/"+id+"/messages?after_seq=0&limit=1", nil, 200))
	require.Len(t, page.Items, 1)
	assert.True(t, page.More)
	assert.EqualValues(t, 1, page.Next)
	read := demandData[map[string]int64](t, f.request(t, "engineer", "POST", "/api/engineer/demands/"+id+"/read", map[string]int{"last_read_seq": 2}, 200))
	assert.EqualValues(t, 0, read["unread_count"])
	read = demandData[map[string]int64](t, f.request(t, "engineer", "POST", "/api/engineer/demands/"+id+"/read", map[string]int{"last_read_seq": 1}, 200))
	assert.EqualValues(t, 2, read["last_read_seq"])
	f.request(t, "engineer", "POST", "/api/engineer/demands/"+id+"/read", map[string]int{"last_read_seq": 3}, 400)
	var count int64
	require.NoError(t, f.db.Model(&model.Demand{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	require.NoError(t, f.db.Model(&model.DemandMessage{}).Where("demand_id = ?", id).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	for name, original := range f.users {
		var now model.User
		require.NoError(t, f.db.First(&now, original.Id).Error)
		assert.Equal(t, original.Quota, now.Quota, name)
		assert.Equal(t, original.AuthVersion, now.AuthVersion, name)
	}
}

func TestDemandAssignmentDeliveryAndPrivateNotes(t *testing.T) {
	f := newDemandFixture(t)
	first := f.create(t, "create-transfer-001")
	id := first.Demand.ID
	revision := first.Demand.Revision
	note := service.DemandAction{ClientActionID: "note-admin-001", Action: "internal_note", Revision: revision, Text: "PRIVATE operator note"}
	r := demandData[service.DemandActionResult](t, f.request(t, "admin", "POST", "/api/demand-management/"+id+"/actions", note, 200))
	assert.EqualValues(t, 1, r.Demand.LastSeq)
	for _, who := range []string{"owner", "engineer"} {
		body := f.request(t, who, "GET", "/api/product/demands/"+id+"/messages", nil, 200)
		assert.NotContains(t, string(body), note.Text)
		f.request(t, who, "GET", "/api/demand-management/"+id+"/audit", nil, 403)
	}
	assign := service.DemandAction{ClientActionID: "assign-admin-001", Action: "assign", Revision: r.Demand.Revision, EngineerID: f.engineers["replacement"]}
	r = demandData[service.DemandActionResult](t, f.request(t, "admin", "POST", "/api/demand-management/"+id+"/actions", assign, 200))
	assert.Equal(t, "pending", r.Demand.Status)
	f.request(t, "engineer", "GET", "/api/engineer/demands/"+id, nil, 404)
	f.request(t, "engineer", "POST", "/api/engineer/demands/"+id+"/actions", service.DemandAction{ClientActionID: "old-accept-001", Action: "accept", Revision: r.Demand.Revision}, 404)
	f.request(t, "admin", "POST", "/api/demand-management/"+id+"/actions", service.DemandAction{ClientActionID: "assign-stale-001", Action: "assign", Revision: revision, EngineerID: f.engineers["engineer"]}, 409)
	for _, action := range []string{"accept", "start", "deliver"} {
		r = demandData[service.DemandActionResult](t, f.request(t, "replacement", "POST", "/api/engineer/demands/"+id+"/actions", service.DemandAction{ClientActionID: "replacement-" + action, Action: action, Revision: r.Demand.Revision, Text: "Synthetic delivery"}, 200))
	}
	assert.Equal(t, "awaiting_acceptance", r.Demand.Status)
	require.NotEmpty(t, r.Demand.DeliveryID)
	accept := service.DemandAction{ClientActionID: "accept-delivery-001", Action: "accept_delivery", Revision: r.Demand.Revision, DeliveryID: r.Demand.DeliveryID}
	r = demandData[service.DemandActionResult](t, f.request(t, "owner", "POST", "/api/product/demands/"+id+"/actions", accept, 200))
	assert.Equal(t, "completed", r.Demand.Status)
	again := demandData[service.DemandActionResult](t, f.request(t, "owner", "POST", "/api/product/demands/"+id+"/actions", accept, 200))
	assert.True(t, again.Replayed)
	f.request(t, "owner", "POST", "/api/product/demands/"+id+"/messages", service.DemandSend{ClientMessageID: "late-message-001", Text: "late"}, 409)
	for _, eid := range f.engineers {
		var e model.DemandEngineer
		require.NoError(t, f.db.First(&e, "id = ?", eid).Error)
		assert.Zero(t, e.OpenDemandCount)
	}
}

func (f *demandFixture) upload(t *testing.T, who, name, mime string, raw []byte) model.DemandAttachment {
	t.Helper()
	start := demandData[service.DemandUploadResult](t, f.request(t, who, "POST", "/api/product/demands/uploads", service.DemandUploadCreate{ClientUploadID: uuid.NewString(), Name: name, MimeType: mime, SizeBytes: int64(len(raw))}, 200))
	id := start.Attachment.ID
	for _, offset := range []int{0, len(raw) / 2} {
		end := len(raw) / 2
		if offset > 0 {
			end = len(raw)
		}
		req := httptest.NewRequest("PUT", "/api/product/demands/uploads/"+id+"/chunks", bytes.NewReader(raw[offset:end]))
		req.Header.Set("Authorization", "Bearer "+f.tokens[who])
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Upload-Offset", strconv.Itoa(offset))
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	sum := sha256.Sum256(raw)
	done := demandData[service.DemandUploadResult](t, f.request(t, who, "POST", "/api/product/demands/uploads/"+id+"/complete", map[string]string{"sha256": hex.EncodeToString(sum[:])}, 200))
	return done.Attachment
}
func TestDemandPrivateResumableMediaRangeAndRevocation(t *testing.T) {
	var accessLogs bytes.Buffer
	oldWriter := gin.DefaultWriter
	gin.DefaultWriter = &accessLogs
	t.Cleanup(func() { gin.DefaultWriter = oldWriter })
	f := newDemandFixture(t)
	var imageData bytes.Buffer
	require.NoError(t, png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	image := f.upload(t, "owner", "synthetic.png", "image/png", imageData.Bytes())
	assert.Equal(t, "ready", image.Status)
	// A minimal ISO-BMFF header is sufficient to test transport/Range, not codec playback.
	videoData := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte{0}, 1024)...)
	video := f.upload(t, "owner", "synthetic.mp4", "video/mp4", videoData)
	first := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: "media-demand-001", EngineerID: f.engineers["engineer"], AttachmentIDs: []string{image.ID, video.ID}}, 201))
	id := first.Demand.ID
	assert.NotEmpty(t, id)
	for _, attachment := range []model.DemandAttachment{image, video} {
		f.request(t, "other", "POST", "/api/product/demands/attachments/"+attachment.ID+"/link", map[string]string{"disposition": "inline"}, 404)
	}
	link := demandData[service.DemandAttachmentLink](t, f.request(t, "engineer", "POST", "/api/product/demands/attachments/"+video.ID+"/link", map[string]string{"disposition": "inline"}, 200))
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", u.RequestURI(), nil)
	req.Header.Set("Range", "bytes=0-15")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 206, w.Code, w.Body.String())
	assert.NotContains(t, accessLogs.String(), u.Query().Get("token"), "signed attachment credentials must not enter access logs")
	assert.Equal(t, videoData[:16], w.Body.Bytes())
	assert.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	f.request(t, "admin", "POST", "/api/demand-management/"+id+"/actions", service.DemandAction{ClientActionID: "reassign-media-001", Action: "assign", Revision: first.Demand.Revision, EngineerID: f.engineers["replacement"]}, 200)
	w = httptest.NewRecorder()
	f.engine.ServeHTTP(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	assert.Equal(t, 404, w.Code, "old signed links lose access after transfer")
	f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: "media-demand-002", EngineerID: f.engineers["engineer"], AttachmentIDs: []string{image.ID}}, 409)
	u.RawQuery = "token=tampered"
	w = httptest.NewRecorder()
	f.engine.ServeHTTP(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	assert.Equal(t, 404, w.Code)
}

func TestDemandConcurrentFirstSendAndRevisionConflict(t *testing.T) {
	f := newDemandFixture(t)

	in := service.DemandCreate{ClientRequestID: "concurrent-create-001", EngineerID: f.engineers["engineer"], Text: "Atomic create"}
	var wg sync.WaitGroup
	results := make(chan service.DemandSendReceipt, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := service.CreateDemand(f.db, service.DemandActor{UserID: f.users["owner"].Id}, in)
			results <- r
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	ids := []string{}
	for r := range results {
		ids = append(ids, r.Demand.ID)
	}
	assert.Equal(t, ids[0], ids[1])
	var count int64
	require.NoError(t, f.db.Model(&model.Demand{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

// This opt-in loopback server is the client interop fixture, not a production
// login or a bypass route. It uses only disposable generated users and sessions.
func TestDemandLocalFixtureServer(t *testing.T) {
	address := os.Getenv("DEMAND_FIXTURE_ADDR")
	if address == "" {
		t.Skip("opt-in loopback fixture")
	}
	host, _, err := net.SplitHostPort(address)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", host)
	serveDemandLocalFixture(t, newDemandFixture(t), address)
}

func serveDemandLocalFixture(t *testing.T, f *demandFixture, address string) {
	t.Helper()
	// These metadata and login endpoints exist only in this opt-in test server.
	// Real session issuance, cookies and refresh still use the existing services.
	common.SessionCookieSecure = false
	f.engine.GET("/api/notice", func(c *gin.Context) { common.ApiSuccess(c, "") })
	f.engine.GET("/api/setup", func(c *gin.Context) { common.ApiSuccess(c, gin.H{"status": true, "root_init": true}) })
	f.engine.GET("/api/status", func(c *gin.Context) {
		common.ApiSuccess(c, gin.H{"version": "demand-local-synthetic", "password_login_enabled": true, "password_login_encryption_enabled": false, "system_name": "Yunshu local demands", "quota_per_unit": 500000, "usd_exchange_rate": 7.3})
	})
	f.engine.POST("/api/user/auth/refresh", controller.RefreshAuth)
	f.engine.POST("/api/user/auth/refresh/recoverable", controller.RefreshRecoverableAuth)
	f.engine.POST("/api/user/login", func(c *gin.Context) {
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&in) != nil || in.Password != "SYNTHETIC-LOCAL-ONLY" {
			c.Status(401)
			return
		}
		name := strings.TrimPrefix(in.Username, "synthetic-")
		if f.tokens[name] == "" {
			c.Status(401)
			return
		}
		u := f.users[name]
		bundle, err := service.CreateLoginSession(u.Id, "synthetic-browser-fixture", "127.0.0.1", "demand-browser")
		if err != nil {
			c.Status(500)
			return
		}
		service.WriteRefreshCookie(c, bundle.RefreshToken)
		common.ApiSuccess(c, gin.H{"access_token": bundle.AccessToken, "token_type": bundle.TokenType, "access_expires_at": bundle.AccessExpiresAt, "session": bundle.Session, "user": gin.H{"id": u.Id, "username": u.Username, "display_name": u.DisplayName, "role": u.Role, "status": u.Status, "permissions": gin.H{"admin_permissions": authz.Capabilities(u.Id, u.Role)}}})
	})
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	system_setting.TaskPublicAddress = "http://" + listener.Addr().String()
	server := &http.Server{Handler: f.engine, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	manifest := map[string]any{"schema_version": 1, "fixture_revision": 3, "fixture_id": uuid.NewString(), "source_commit": os.Getenv("DEMAND_FIXTURE_SOURCE_COMMIT"), "base_url": system_setting.TaskPublicAddress, "synthetic_only": true, "expires_at": time.Now().Add(15 * time.Minute).Unix(), "auth_bundles": f.bundles, "tokens": map[string]string{"customerA": f.tokens["owner"], "customerB": f.tokens["other"], "engineerA": f.tokens["engineer"], "engineerB": f.tokens["replacement"], "admin": f.tokens["admin"]}, "users": f.users, "engineers": f.engineers, "admin_page": "/demand-management", "engineer_page": "/engineer-workspace"}
	raw, err := common.Marshal(manifest)
	require.NoError(t, err)
	output := os.Getenv("DEMAND_FIXTURE_OUTPUT")
	require.NotEmpty(t, output)
	require.NoError(t, os.WriteFile(output, raw, 0600))
	t.Logf("Synthetic local demand fixture: %s", listener.Addr())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	select {
	case err = <-result:
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
		err = <-result
	}
	if err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}

func TestDemandAdditiveMigrationKeepsExistingSessionsTasksAndFinance(t *testing.T) {
	f := newDemandFixture(t)
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
	require.NoError(t, f.db.Migrator().DropTable(model.DemandTables()...))
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(model.DemandTables()...))
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

func TestDemandDisabledEntryKeepsOriginalReplayRepliesAndUploads(t *testing.T) {
	f := newDemandFixture(t)
	first := f.create(t, "disabled-first-001")
	config := f.config
	config.Config.Enabled = false
	demandData[service.DemandConfigVersion](t, f.request(t, "admin", "PUT", "/api/demand-management/config", config, 200))
	r := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: "disabled-first-001", EngineerID: f.engineers["engineer"], Text: "Synthetic website request"}, 200))
	assert.True(t, r.Replayed)
	f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: "disabled-new-001", EngineerID: f.engineers["engineer"], Text: "new request"}, 503)
	f.request(t, "owner", "POST", "/api/product/demands/"+first.Demand.ID+"/messages", service.DemandSend{ClientMessageID: "disabled-reply-001", Text: "Continue original"}, 200)
	f.request(t, "owner", "POST", "/api/product/demands/uploads", service.DemandUploadCreate{ClientUploadID: "disabled-upload-001", Name: "image.png", MimeType: "image/png", SizeBytes: 64}, 200)
	f.request(t, "other", "POST", "/api/product/demands/uploads", service.DemandUploadCreate{ClientUploadID: "disabled-upload-002", Name: "image.png", MimeType: "image/png", SizeBytes: 64}, 503)

	detail := demandData[struct {
		Demand service.DemandView `json:"demand"`
	}](t, f.request(t, "owner", "GET", "/api/product/demands/"+first.Demand.ID, nil, 200))
	current := detail.Demand
	require.Greater(t, current.Revision, int64(0))
	f.request(t, "owner", "GET", "/api/product/demands/"+first.Demand.ID+"/messages", nil, 200)
	for _, action := range []string{"accept", "start", "deliver"} {
		next := demandData[service.DemandActionResult](t, f.request(t, "engineer", "POST", "/api/engineer/demands/"+first.Demand.ID+"/actions", service.DemandAction{ClientActionID: "disabled-action-" + action, Action: action, Revision: current.Revision, Text: "Deliver original work while entry is closed"}, 200))
		current = next.Demand
	}
	done := demandData[service.DemandActionResult](t, f.request(t, "owner", "POST", "/api/product/demands/"+first.Demand.ID+"/actions", service.DemandAction{ClientActionID: "disabled-accept-delivery", Action: "accept_delivery", Revision: current.Revision, DeliveryID: current.DeliveryID}, 200))
	assert.Equal(t, "completed", done.Demand.Status)
}

func TestDemandSessionAndBusinessAuthorizationStaySeparate(t *testing.T) {
	f := newDemandFixture(t)
	f.request(t, "owner", "GET", "/api/engineer/demands", nil, 403)
	f.request(t, "engineer", "GET", "/api/demand-management", nil, 403)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["admin"].Id).Update("role", common.RoleAdminUser).Error)
	f.request(t, "admin", "GET", "/api/demand-management", nil, 200)
	raw := "nap_synthetic_demand_auth_token"
	pat := model.UserAccessToken{Name: "synthetic-only", TokenHash: model.AccessTokenFingerprint(raw), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, pat.SetScopes([]string{"*"}))
	require.NoError(t, model.CreateUserAccessToken(f.users["admin"].Id, &pat, 10))
	f.tokens["pat"] = raw
	f.request(t, "pat", "GET", "/api/demand-management", nil, 403)
	f.request(t, "pat", "GET", "/api/product/demands", nil, 403)
	before := time.Now().Unix()
	heartbeat := demandData[struct {
		ServerTime int64                      `json:"server_time"`
		Engineer   service.DemandEngineerView `json:"engineer"`
	}](t, f.request(t, "engineer", "POST", "/api/engineer/demands/heartbeat", map[string]any{"timestamp": 1, "accepting": false}, 200))
	assert.GreaterOrEqual(t, heartbeat.ServerTime, before)
	assert.Equal(t, "busy", heartbeat.Engineer.Presence)
	require.NoError(t, f.db.Model(&model.DemandEngineer{}).Where("id = ?", f.engineers["engineer"]).Update("enabled", false).Error)
	f.request(t, "engineer", "GET", "/api/engineer/demands", nil, 403)
}

func TestDemandPublishedAvatarAccessRevokesWithoutDeletingOriginalWork(t *testing.T) {
	f := newDemandFixture(t)
	var imageData bytes.Buffer
	require.NoError(t, png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	avatar := f.upload(t, "admin", "synthetic-avatar.png", "image/png", imageData.Bytes())
	profiles := demandData[struct {
		Items []service.DemandEngineerView `json:"items"`
	}](t, f.request(t, "admin", "GET", "/api/demand-management/engineers", nil, 200))
	var profile service.DemandEngineerView
	for _, item := range profiles.Items {
		if item.ID == f.engineers["engineer"] {
			profile = item
			break
		}
	}
	require.NotEmpty(t, profile.ID)
	profile.AvatarAttachmentID = avatar.ID
	profile = demandData[service.DemandEngineerView](t, f.request(t, "admin", "PUT", "/api/demand-management/engineers/"+profile.ID, profile, 200))
	original := f.create(t, "avatar-original-demand")
	linkPath := "/api/product/demands/attachments/" + avatar.ID + "/link"
	f.request(t, "", "POST", linkPath, map[string]string{"disposition": "inline"}, 401)
	link := demandData[service.DemandAttachmentLink](t, f.request(t, "other", "POST", linkPath, map[string]string{"disposition": "inline"}, 200))
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	request := httptest.NewRequest("GET", u.RequestURI(), nil)
	request.Header.Set("Range", "bytes=0-7")
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, request)
	require.Equal(t, 206, response.Code)
	assert.Equal(t, imageData.Bytes()[:8], response.Body.Bytes())
	profile.Published = false
	profile = demandData[service.DemandEngineerView](t, f.request(t, "admin", "PUT", "/api/demand-management/engineers/"+profile.ID, profile, 200))
	f.request(t, "other", "POST", linkPath, map[string]string{"disposition": "inline"}, 404)
	response = httptest.NewRecorder()
	f.engine.ServeHTTP(response, httptest.NewRequest("GET", u.RequestURI(), nil))
	assert.Equal(t, 404, response.Code, "an already-issued avatar URL must lose public-directory access immediately")
	f.request(t, "engineer", "GET", "/api/engineer/demands/"+original.Demand.ID, nil, 200)
	f.request(t, "admin", "POST", "/api/demand-management/attachments/"+avatar.ID+"/link", map[string]string{"disposition": "inline"}, 200)
	profile.Published = true
	profile = demandData[service.DemandEngineerView](t, f.request(t, "admin", "PUT", "/api/demand-management/engineers/"+profile.ID, profile, 200))
	link = demandData[service.DemandAttachmentLink](t, f.request(t, "other", "POST", linkPath, map[string]string{"disposition": "inline"}, 200))
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", profile.UserID).Update("status", common.UserStatusDisabled).Error)
	f.request(t, "other", "POST", linkPath, map[string]string{"disposition": "inline"}, 404)
	u, err = url.Parse(link.URL)
	require.NoError(t, err)
	response = httptest.NewRecorder()
	f.engine.ServeHTTP(response, httptest.NewRequest("GET", u.RequestURI(), nil))
	assert.Equal(t, 404, response.Code, "disabled engineer account must revoke its published avatar")
	f.request(t, "owner", "GET", "/api/product/demands/"+original.Demand.ID, nil, 200)
}

func TestDemandFixtureSelfMetadataSatisfiesHostAccountContract(t *testing.T) {
	f := newDemandFixture(t)
	for _, name := range []string{"owner", "other", "engineer", "replacement", "admin"} {
		metadata := demandData[struct {
			ID        int      `json:"id"`
			Quota     *float64 `json:"quota"`
			Group     string   `json:"group"`
			PhoneE164 *string  `json:"phone_e164"`
		}](t, f.request(t, name, "GET", "/api/user/self", nil, 200))
		require.Positive(t, metadata.ID)
		require.NotNil(t, metadata.Quota, "Host requires an explicit finite non-negative quota")
		assert.GreaterOrEqual(t, *metadata.Quota, float64(0))
		assert.EqualValues(t, f.users[name].Quota, *metadata.Quota)
		assert.Equal(t, f.users[name].Group, metadata.Group)
		assert.Equal(t, f.users[name].PhoneE164, metadata.PhoneE164)
	}
}

func TestDemandHistoryAuthorNamesFollowSendersAfterReassignment(t *testing.T) {
	f := newDemandFixture(t)
	require.NoError(t, f.db.Model(&model.DemandEngineer{}).Where("id = ?", f.engineers["engineer"]).Update("display_name", "SYNTHETIC Engineer A").Error)
	require.NoError(t, f.db.Model(&model.DemandEngineer{}).Where("id = ?", f.engineers["replacement"]).Update("display_name", "SYNTHETIC Engineer B").Error)
	original := f.create(t, "historical-authors-001")
	id := original.Demand.ID
	first := demandData[service.DemandSendReceipt](t, f.request(t, "engineer", "POST", "/api/engineer/demands/"+id+"/messages", service.DemandSend{ClientMessageID: "author-a-original-message", Text: "Message from original Engineer A"}, 200))
	assigned := demandData[service.DemandActionResult](t, f.request(t, "admin", "POST", "/api/demand-management/"+id+"/actions", service.DemandAction{ClientActionID: "author-transfer-a-to-b", Action: "assign", Revision: first.Demand.Revision, EngineerID: f.engineers["replacement"]}, 200))
	require.Equal(t, f.engineers["replacement"], assigned.Demand.EngineerID)
	second := demandData[service.DemandSendReceipt](t, f.request(t, "replacement", "POST", "/api/engineer/demands/"+id+"/messages", service.DemandSend{ClientMessageID: "author-b-new-message", Text: "Message from new Engineer B"}, 200))
	require.NoError(t, f.db.Model(&model.DemandEngineer{}).Where("id = ?", f.engineers["engineer"]).Updates(map[string]any{"enabled": false, "published": false}).Error)
	type item struct {
		ID         string  `json:"id"`
		SenderID   int     `json:"sender_id"`
		Author     string  `json:"author"`
		AuthorName *string `json:"author_name"`
		Seq        int64   `json:"seq"`
	}
	body := f.request(t, "owner", "GET", "/api/product/demands/"+id+"/messages", nil, 200)
	history := demandData[struct {
		Items []item `json:"items"`
	}](t, body)
	require.Len(t, history.Items, 3)
	assert.Nil(t, history.Items[0].AuthorName, "user message labels stay client-owned")
	require.NotNil(t, history.Items[1].AuthorName)
	require.NotNil(t, history.Items[2].AuthorName)
	assert.Equal(t, "SYNTHETIC Engineer A", *history.Items[1].AuthorName)
	assert.Equal(t, "SYNTHETIC Engineer B", *history.Items[2].AuthorName)
	assert.Equal(t, first.Message.ID, history.Items[1].ID)
	assert.Equal(t, second.Message.ID, history.Items[2].ID)
	assert.EqualValues(t, 2, history.Items[1].Seq)
	assert.EqualValues(t, 3, history.Items[2].Seq)
	assert.Equal(t, f.users["engineer"].Id, history.Items[1].SenderID)
	assert.Equal(t, f.users["replacement"].Id, history.Items[2].SenderID)
	assert.NotContains(t, string(body), *f.users["engineer"].PhoneE164)
	assert.NotContains(t, string(body), *f.users["replacement"].PhoneE164)
	// Missing or unsafe profile data must omit the optional name, never borrow B's name for A.
	require.NoError(t, f.db.Delete(&model.DemandEngineer{}, "id = ?", f.engineers["engineer"]).Error)
	require.NoError(t, f.db.Model(&model.DemandEngineer{}).Where("id = ?", f.engineers["replacement"]).Update("display_name", "SYNTHETIC\tB").Error)
	history = demandData[struct {
		Items []item `json:"items"`
	}](t, f.request(t, "owner", "GET", "/api/product/demands/"+id+"/messages", nil, 200))
	require.Len(t, history.Items, 3)
	assert.Nil(t, history.Items[1].AuthorName)
	assert.Nil(t, history.Items[2].AuthorName)
}

type demandDirectoryPage[T any] struct {
	Items   []T    `json:"items"`
	Next    string `json:"next_after_id"`
	HasMore bool   `json:"has_more"`
	Total   int    `json:"total"`
}

func TestDemandDirectoryPresetImportPaginationAndPrivateAvatars(t *testing.T) {
	f := newDemandFixture(t)
	in := map[string]string{"batch_code": "test-catalog-v1"}
	receipt := demandData[service.DemandProfileImportResult](t, f.request(t, "admin", "POST", "/api/demand-management/profile-presets", in, 200))
	assert.Equal(t, 100, receipt.CreatedServiceProfiles)
	assert.Equal(t, 100, receipt.CreatedCustomerProfiles)
	var users int64
	require.NoError(t, f.db.Model(&model.User{}).Count(&users).Error)
	assert.EqualValues(t, 5, users)
	all := []service.DemandServiceProfileView{}
	cursor := ""
	for {
		page := demandData[demandDirectoryPage[service.DemandServiceProfileView]](t, f.request(t, "owner", "GET", "/api/product/demands/service-profiles?limit=30&after_id="+url.QueryEscape(cursor), nil, 200))
		assert.Equal(t, 100, page.Total)
		require.LessOrEqual(t, len(page.Items), 30)
		all = append(all, page.Items...)
		if !page.HasMore {
			break
		}
		require.NotEmpty(t, page.Next)
		cursor = page.Next
	}
	require.Len(t, all, 100)
	assert.Equal(t, "YS-SVC-001", all[0].Code)
	assert.Equal(t, "platform", all[0].IntakeMode)
	firstPage := demandData[demandDirectoryPage[service.DemandServiceProfileView]](t, f.request(t, "owner", "GET", "/api/product/demands/service-profiles?limit=1", nil, 200))
	f.request(t, "owner", "GET", "/api/product/demands/service-profiles?limit=1&q=changed&after_id="+url.QueryEscape(firstPage.Next), nil, 400)
	f.request(t, "owner", "GET", "/api/product/demands/service-profiles?limit=101", nil, 400)
	literal := demandData[demandDirectoryPage[service.DemandServiceProfileView]](t, f.request(t, "owner", "GET", "/api/product/demands/service-profiles?q=%25", nil, 200))
	assert.Empty(t, literal.Items)
	old := demandData[demandDirectoryPage[service.DemandEngineerView]](t, f.request(t, "owner", "GET", "/api/product/demands/engineers", nil, 200))
	assert.Len(t, old.Items, 2)
	avatar := all[0].AvatarAttachmentID
	require.NotEmpty(t, avatar)
	link := demandData[service.DemandAttachmentLink](t, f.request(t, "owner", "POST", "/api/product/demands/attachments/"+avatar+"/link", map[string]string{"disposition": "inline"}, 200))
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	request := httptest.NewRequest("GET", u.RequestURI(), nil)
	request.Header.Set("Range", "bytes=0-31")
	f.engine.ServeHTTP(w, request)
	assert.Equal(t, 206, w.Code)
	assert.Equal(t, []byte{137, 80, 78, 71}, w.Body.Bytes()[:4])
	edited := all[0]
	edited.DisplayName = "管理员修改保留"
	edited.Published = false
	edited = demandData[service.DemandServiceProfileView](t, f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+edited.ID, edited, 200))
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+edited.ID, all[0], 409)
	w = httptest.NewRecorder()
	f.engine.ServeHTTP(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	assert.Equal(t, 404, w.Code)
	repeated := demandData[service.DemandProfileImportResult](t, f.request(t, "admin", "POST", "/api/demand-management/profile-presets", in, 200))
	assert.True(t, repeated.Replayed)
	skipped := demandData[service.DemandProfileImportResult](t, f.request(t, "admin", "POST", "/api/demand-management/profile-presets", map[string]string{"batch_code": "test-catalog-v2"}, 200))
	assert.Equal(t, 100, skipped.SkippedServiceProfiles)
	assert.Equal(t, 100, skipped.SkippedCustomerProfiles)
	var kept model.DemandServiceProfile
	require.NoError(t, f.db.First(&kept, "id = ?", edited.ID).Error)
	assert.Equal(t, edited.DisplayName, kept.DisplayName)
	assert.False(t, kept.Published)
	customers := demandData[demandDirectoryPage[service.DemandCustomerProfileView]](t, f.request(t, "admin", "GET", "/api/demand-management/customer-profiles?limit=100", nil, 200))
	assert.Equal(t, 100, customers.Total)
	require.Len(t, customers.Items, 100)
	cp := customers.Items[0]
	assert.Zero(t, cp.UserID)
	f.request(t, "owner", "POST", "/api/product/demands/attachments/"+cp.AvatarAttachmentID+"/link", map[string]string{"disposition": "inline"}, 404)
	f.request(t, "owner", "GET", "/api/demand-management/customer-profiles", nil, 403)
	cp.UserID = f.users["owner"].Id
	cp.DisplayName = "已关联客户"
	cp = demandData[service.DemandCustomerProfileView](t, f.request(t, "admin", "PUT", "/api/demand-management/customer-profiles/"+cp.ID, cp, 200))
	duplicate := customers.Items[1]
	duplicate.UserID = cp.UserID
	f.request(t, "admin", "PUT", "/api/demand-management/customer-profiles/"+duplicate.ID, duplicate, 409)
	// Avatar reservation remains private and cannot be rebound into somebody else's conversation.
	f.request(t, "admin", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: "avatar-as-message-001", EngineerID: f.engineers["engineer"], AttachmentIDs: []string{avatar}}, 409)
}

func TestDemandPlatformIntakeAssignmentMediaAndLegacyReads(t *testing.T) {
	f := newDemandFixture(t)
	p := demandData[service.DemandServiceProfileView](t, f.request(t, "admin", "POST", "/api/demand-management/service-profiles", service.DemandServiceProfileView{DemandServiceProfile: model.DemandServiceProfile{Code: "service_web", DisplayName: "网站服务", ServiceDirection: "网站与表单", Enabled: true, Published: true}, CategoryIDs: []string{"software"}}, 200))
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	imageAttachment := f.upload(t, "owner", "brief.png", "image/png", pngData.Bytes())
	videoData := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte{0}, 1024)...)
	video := f.upload(t, "owner", "brief.mp4", "video/mp4", videoData)
	create := service.DemandCreate{ClientRequestID: "platform-order-001", ServiceProfileID: p.ID, Text: "平台统一分配", AttachmentIDs: []string{imageAttachment.ID, video.ID}}
	first := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", create, 201))
	d := first.Demand
	assert.False(t, d.Assigned)
	assert.Equal(t, "platform-intake", d.EngineerID)
	assert.Equal(t, "平台统一接待", d.Engineer.DisplayName)
	assert.Equal(t, "platform", d.IntakeMode)
	assert.Equal(t, p.DisplayName, d.ServiceProfileName)
	var dbDemand model.Demand
	require.NoError(t, f.db.First(&dbDemand, "id = ?", d.ID).Error)
	assert.Empty(t, dbDemand.EngineerID)
	f.request(t, "engineer", "GET", "/api/engineer/demands/"+d.ID, nil, 404)
	f.request(t, "other", "GET", "/api/product/demands/"+d.ID, nil, 404)
	queue := demandData[struct {
		Items []service.DemandView `json:"items"`
	}](t, f.request(t, "admin", "GET", "/api/demand-management?assignment=unassigned", nil, 200))
	require.Len(t, queue.Items, 1)
	p.Enabled = false
	p = demandData[service.DemandServiceProfileView](t, f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+p.ID, p, 200))
	replay := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", create, 200))
	assert.True(t, replay.Replayed)
	newCreate := create
	newCreate.ClientRequestID = "platform-order-002"
	newCreate.AttachmentIDs = nil
	f.request(t, "owner", "POST", "/api/product/demands", newCreate, 409)
	assigned := demandData[service.DemandActionResult](t, f.request(t, "admin", "POST", "/api/demand-management/"+d.ID+"/actions", service.DemandAction{ClientActionID: "platform-assign-001", Revision: d.Revision, Action: "assign", EngineerID: f.engineers["engineer"]}, 200))
	d = assigned.Demand
	assert.True(t, d.Assigned)
	assert.Equal(t, p.ID, d.ServiceProfileID)
	accepted := demandData[service.DemandActionResult](t, f.request(t, "engineer", "POST", "/api/engineer/demands/"+d.ID+"/actions", service.DemandAction{ClientActionID: "platform-accept-001", Revision: d.Revision, Action: "accept"}, 200))
	d = accepted.Demand
	link := demandData[service.DemandAttachmentLink](t, f.request(t, "engineer", "POST", "/api/product/demands/attachments/"+video.ID+"/link", map[string]string{"disposition": "inline"}, 200))
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", u.RequestURI(), nil)
	r.Header.Set("Range", "bytes=0-15")
	f.engine.ServeHTTP(w, r)
	assert.Equal(t, 206, w.Code)
	assert.Equal(t, videoData[:16], w.Body.Bytes())
	transferred := demandData[service.DemandActionResult](t, f.request(t, "admin", "POST", "/api/demand-management/"+d.ID+"/actions", service.DemandAction{ClientActionID: "platform-transfer-001", Revision: d.Revision, Action: "assign", EngineerID: f.engineers["replacement"]}, 200))
	d = transferred.Demand
	w = httptest.NewRecorder()
	f.engine.ServeHTTP(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	assert.Equal(t, 404, w.Code)
	accepted = demandData[service.DemandActionResult](t, f.request(t, "replacement", "POST", "/api/engineer/demands/"+d.ID+"/actions", service.DemandAction{ClientActionID: "platform-accept-002", Revision: d.Revision, Action: "accept"}, 200))
	d = accepted.Demand
	reply := demandData[service.DemandSendReceipt](t, f.request(t, "replacement", "POST", "/api/engineer/demands/"+d.ID+"/messages", service.DemandSend{ClientMessageID: "platform-reply-001", Text: "接单已确认"}, 200))
	d = reply.Demand
	deliveryFile := f.upload(t, "replacement", "delivery.png", "image/png", pngData.Bytes())
	delivery := demandData[service.DemandActionResult](t, f.request(t, "replacement", "POST", "/api/engineer/demands/"+d.ID+"/actions", service.DemandAction{ClientActionID: "platform-deliver-001", Revision: d.Revision, Action: "deliver", Text: "交付", AttachmentIDs: []string{deliveryFile.ID}}, 200))
	d = delivery.Demand
	final := demandData[service.DemandActionResult](t, f.request(t, "owner", "POST", "/api/product/demands/"+d.ID+"/actions", service.DemandAction{ClientActionID: "platform-confirm-001", Revision: d.Revision, Action: "accept_delivery", DeliveryID: d.DeliveryID}, 200))
	assert.Equal(t, "completed", final.Demand.Status)
	legacy := f.create(t, "legacy-direct-001")
	assert.Equal(t, "direct", legacy.Demand.IntakeMode)
	assert.True(t, legacy.Demand.Assigned)
	f.request(t, "owner", "GET", "/api/product/demands", nil, 200)
	create.ClientRequestID = "mutually-exclusive-001"
	create.EngineerID = f.engineers["engineer"]
	f.request(t, "owner", "POST", "/api/product/demands", create, 400)
}

func TestDemandDirectoryUpgradeKeepsLegacyDemandReceipts(t *testing.T) {
	f := newDemandFixture(t)
	legacy := f.create(t, "legacy-before-upgrade")
	var sessionsBefore []model.UserSession
	require.NoError(t, f.db.Find(&sessionsBefore).Error)
	require.NoError(t, f.db.Migrator().DropTable(&model.DemandServiceProfile{}, &model.DemandCustomerProfile{}, &model.DemandProfileImport{}))
	require.NoError(t, f.db.Migrator().DropColumn(&model.Demand{}, "service_profile_id"))
	require.NoError(t, f.db.Migrator().DropColumn(&model.Demand{}, "service_profile_name"))
	require.NoError(t, f.db.Migrator().DropColumn(&model.DemandAttachment{}, "avatar_reserved"))
	require.NoError(t, f.db.Migrator().DropColumn(&model.DemandAttachment{}, "avatar_scope"))
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(model.DemandTables()...))
	}
	// A real upgrade opens a new process/pool after migration. Reopen instead of retaining
	// pgx prepared SELECT * plans from before the deliberately removed/re-added columns.
	nextDB, err := gorm.Open(f.db.Dialector, &gorm.Config{})
	require.NoError(t, err)
	f.db = nextDB
	model.DB = nextDB
	model.LOG_DB = nextDB
	nextPool, err := nextDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, nextPool.Close()) })
	recovered := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: "legacy-before-upgrade", EngineerID: f.engineers["engineer"], Text: "Synthetic website request"}, 200))
	assert.True(t, recovered.Replayed)
	assert.Equal(t, legacy.Demand.ID, recovered.Demand.ID)
	var sessionsAfter []model.UserSession
	require.NoError(t, f.db.Find(&sessionsAfter).Error)
	assert.Equal(t, sessionsBefore, sessionsAfter)
	for _, before := range f.users {
		var after model.User
		require.NoError(t, f.db.First(&after, before.Id).Error)
		assert.Equal(t, before, after)
	}
	// The original per-user submission uniqueness still holds after the additive migration.
	var row model.Demand
	require.NoError(t, f.db.First(&row, "id = ?", legacy.Demand.ID).Error)
	row.ID = "duplicate-demand"
	row.Number = "duplicate-number"
	assert.Error(t, f.db.Create(&row).Error)
}

func TestDemandDirectoryAvatarScopesPreventCrossVisibilityAndRollbackImport(t *testing.T) {
	f := newDemandFixture(t)
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	private := f.upload(t, "admin", "internal-avatar.png", "image/png", pngData.Bytes())
	customer := service.DemandCustomerProfileView{DemandCustomerProfile: model.DemandCustomerProfile{Code: "internal_customer", DisplayName: "Internal customer", Enabled: true, AvatarAttachmentID: private.ID}}
	customer = demandData[service.DemandCustomerProfileView](t, f.request(t, "admin", "POST", "/api/demand-management/customer-profiles", customer, 200))
	public := service.DemandServiceProfileView{DemandServiceProfile: model.DemandServiceProfile{Code: "public_service", DisplayName: "Public service", ServiceDirection: "Software", Enabled: true, Published: false, AvatarAttachmentID: private.ID}, CategoryIDs: []string{}}
	// Unpublished public profiles can be listed later, so they must not accept an internal image either.
	f.request(t, "admin", "POST", "/api/demand-management/service-profiles", public, 400)
	var e model.DemandEngineer
	require.NoError(t, f.db.First(&e, "id = ?", f.engineers["engineer"]).Error)
	engineer := service.DemandEngineerPublic(e, true)
	engineer.AvatarAttachmentID = private.ID
	f.request(t, "admin", "PUT", "/api/demand-management/engineers/"+engineer.ID, engineer, 400)
	publicAvatar := f.upload(t, "admin", "public-avatar.png", "image/png", pngData.Bytes())
	public.AvatarAttachmentID = publicAvatar.ID
	public = demandData[service.DemandServiceProfileView](t, f.request(t, "admin", "POST", "/api/demand-management/service-profiles", public, 200))
	anotherCustomer := service.DemandCustomerProfileView{DemandCustomerProfile: model.DemandCustomerProfile{Code: "another_customer", DisplayName: "Another customer", Enabled: true, AvatarAttachmentID: publicAvatar.ID}}
	f.request(t, "admin", "POST", "/api/demand-management/customer-profiles", anotherCustomer, 400)
	// The ordinary upload owner also loses product-side access when its image is deliberately classified internal.
	owned := f.upload(t, "owner", "owned-internal.png", "image/png", pngData.Bytes())
	anotherCustomer.AvatarAttachmentID = owned.ID
	anotherCustomer = demandData[service.DemandCustomerProfileView](t, f.request(t, "admin", "POST", "/api/demand-management/customer-profiles", anotherCustomer, 200))
	f.request(t, "owner", "POST", "/api/product/demands/attachments/"+owned.ID+"/link", map[string]string{"disposition": "inline"}, 404)
	f.request(t, "admin", "POST", "/api/demand-management/attachments/"+owned.ID+"/link", map[string]string{"disposition": "inline"}, 200)
	batchAvatar := f.upload(t, "admin", "batch-avatar.png", "image/png", pngData.Bytes())
	batchService := service.DemandServiceProfileView{DemandServiceProfile: model.DemandServiceProfile{Code: "batch_service", DisplayName: "Batch service", ServiceDirection: "Software", Enabled: true, AvatarAttachmentID: batchAvatar.ID}, CategoryIDs: []string{}}
	batchCustomer := service.DemandCustomerProfileView{DemandCustomerProfile: model.DemandCustomerProfile{Code: "batch_customer", DisplayName: "Batch customer", Enabled: true, AvatarAttachmentID: batchAvatar.ID}}
	batch := service.DemandProfileImportInput{BatchCode: "mixed-batch-001", ServiceProfiles: []service.DemandServiceProfileView{batchService}, CustomerProfiles: []service.DemandCustomerProfileView{batchCustomer}}
	f.request(t, "admin", "POST", "/api/demand-management/profile-import", batch, 400)
	for _, table := range []any{&model.DemandServiceProfile{}, &model.DemandCustomerProfile{}} {
		var count int64
		require.NoError(t, f.db.Model(table).Where("code IN ?", []string{"batch_service", "batch_customer"}).Count(&count).Error)
		assert.Zero(t, count)
	}
	var receiptCount int64
	require.NoError(t, f.db.Model(&model.DemandProfileImport{}).Where("batch_code = ?", batch.BatchCode).Count(&receiptCount).Error)
	assert.Zero(t, receiptCount)
	var recovered model.DemandAttachment
	require.NoError(t, f.db.First(&recovered, "id = ?", batchAvatar.ID).Error)
	assert.Empty(t, recovered.AvatarScope)
	assert.False(t, recovered.AvatarReserved)
	// A failed cross-class import leaves the upload available for a corrected, single-class batch.
	batch.ServiceProfiles = nil
	imported := demandData[service.DemandProfileImportResult](t, f.request(t, "admin", "POST", "/api/demand-management/profile-import", batch, 200))
	assert.Equal(t, 1, imported.CreatedCustomerProfiles)
	replay := demandData[service.DemandProfileImportResult](t, f.request(t, "admin", "POST", "/api/demand-management/profile-import", batch, 200))
	assert.True(t, replay.Replayed)
	batch.CustomerProfiles[0].DisplayName = "Different payload"
	f.request(t, "admin", "POST", "/api/demand-management/profile-import", batch, 409)
	// Removing a customer reference does not silently reclassify an already internal upload as public.
	customer.AvatarAttachmentID = ""
	f.request(t, "admin", "PUT", "/api/demand-management/customer-profiles/"+customer.ID, customer, 200)
	public.AvatarAttachmentID = private.ID
	f.request(t, "admin", "PUT", "/api/demand-management/service-profiles/"+public.ID, public, 400)
}

func TestDemandAdminCanReplyBeforeAssignmentWithPrivateMediaAndStableReceipts(t *testing.T) {
	f := newDemandFixture(t)
	profile := demandData[service.DemandServiceProfileView](t, f.request(t, "admin", "POST", "/api/demand-management/service-profiles", service.DemandServiceProfileView{DemandServiceProfile: model.DemandServiceProfile{Code: "admin_intake", DisplayName: "Platform intake", ServiceDirection: "Customer support", Enabled: true, Published: true}, CategoryIDs: []string{"software"}}, 200))
	first := demandData[service.DemandSendReceipt](t, f.request(t, "owner", "POST", "/api/product/demands", service.DemandCreate{ClientRequestID: "admin-unassigned-demand", ServiceProfileID: profile.ID, Text: "Customer asks for help"}, 201))
	id := first.Demand.ID
	require.False(t, first.Demand.Assigned)
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	video := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte{0}, 64)...)
	var attachments []string
	for _, media := range []struct {
		name, mime string
		raw        []byte
	}{{"admin.png", "image/png", picture.Bytes()}, {"admin.mp4", "video/mp4", video}} {
		up := demandData[service.DemandUploadResult](t, f.request(t, "admin", "POST", "/api/demand-management/uploads", service.DemandUploadCreate{ClientUploadID: uuid.NewString(), Name: media.name, MimeType: media.mime, SizeBytes: int64(len(media.raw))}, 200))
		req := httptest.NewRequest("PUT", "/api/demand-management/uploads/"+up.Attachment.ID+"/chunks", bytes.NewReader(media.raw))
		req.Header.Set("Authorization", "Bearer "+f.tokens["admin"])
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Upload-Offset", "0")
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		sum := sha256.Sum256(media.raw)
		done := demandData[service.DemandUploadResult](t, f.request(t, "admin", "POST", "/api/demand-management/uploads/"+up.Attachment.ID+"/complete", map[string]string{"sha256": hex.EncodeToString(sum[:])}, 200))
		attachments = append(attachments, done.Attachment.ID)
	}
	body := service.DemandSend{ClientMessageID: "admin-public-reply-one", Text: "The platform can help before assigning an engineer", AttachmentIDs: attachments}
	reply := demandData[service.DemandSendReceipt](t, f.request(t, "admin", "POST", "/api/demand-management/"+id+"/messages", body, 200))
	assert.False(t, reply.Demand.Assigned)
	assert.Equal(t, "pending", reply.Demand.Status)
	assert.Equal(t, "system", reply.Message.Author)
	assert.Equal(t, f.users["admin"].Id, reply.Message.SenderID)
	require.Len(t, reply.Message.Attachments, 2)
	replay := demandData[service.DemandSendReceipt](t, f.request(t, "admin", "POST", "/api/demand-management/"+id+"/messages", body, 200))
	assert.True(t, replay.Replayed)
	assert.Equal(t, reply.Message.ID, replay.Message.ID)
	f.request(t, "admin", "POST", "/api/demand-management/"+id+"/read", map[string]int64{"last_read_seq": reply.Message.Seq}, 200)
	current := demandData[service.DemandActionResult](t, f.request(t, "admin", "POST", "/api/demand-management/"+id+"/actions", service.DemandAction{ClientActionID: "admin-private-note", Revision: reply.Demand.Revision, Action: "internal_note", Text: "PRIVATE ADMIN NOTE"}, 200))
	history := demandData[struct {
		Items []service.DemandMessageView `json:"items"`
	}](t, f.request(t, "owner", "GET", "/api/product/demands/"+id+"/messages", nil, 200))
	require.Len(t, history.Items, 2)
	assert.Equal(t, "engineer", history.Items[1].Author)
	assert.Equal(t, "平台客服", history.Items[1].AuthorName)
	assert.Equal(t, f.users["admin"].Id, history.Items[1].SenderID)
	require.Len(t, history.Items[1].Attachments, 2)
	assert.Equal(t, body.Text, history.Items[1].Text)
	for _, m := range history.Items {
		assert.NotContains(t, m.Text, "PRIVATE ADMIN NOTE")
	}
	for _, a := range reply.Message.Attachments {
		link := demandData[service.DemandAttachmentLink](t, f.request(t, "owner", "POST", "/api/product/demands/attachments/"+a.ID+"/link", map[string]string{"disposition": "inline"}, 200))
		u, err := url.Parse(link.URL)
		require.NoError(t, err)
		req := httptest.NewRequest("GET", u.RequestURI(), nil)
		req.Header.Set("Range", "bytes=0-3")
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		require.Equal(t, 206, w.Code)
		f.request(t, "other", "POST", "/api/product/demands/attachments/"+a.ID+"/link", map[string]string{"disposition": "inline"}, 404)
	}
	f.request(t, "engineer", "GET", "/api/engineer/demands/"+id+"/messages", nil, 404)
	f.request(t, "other", "POST", "/api/demand-management/"+id+"/messages", body, 403)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.users["admin"].Id).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, authz.PermissionsMap{"demands": {"read": true, "write": false}}))
	detail := demandData[struct {
		Demand service.DemandView `json:"demand"`
	}](t, f.request(t, "admin", "GET", "/api/demand-management/"+id, nil, 200))
	details := detail.Demand
	require.NotNil(t, details.Owner)
	assert.Equal(t, f.users["owner"].Id, details.Owner.ID)
	assert.Equal(t, f.users["owner"].PhoneE164, details.Owner.Phone)
	denied := body
	denied.ClientMessageID = "read-only-admin-must-not-send"
	f.request(t, "admin", "POST", "/api/demand-management/"+id+"/messages", denied, 403)
	f.request(t, "admin", "POST", "/api/demand-management/"+id+"/read", map[string]int64{"last_read_seq": reply.Message.Seq}, 200)
	require.NoError(t, authz.SetUserPermissions(f.users["admin"].Id, authz.PermissionsMap{"demands": {"read": true, "write": true}}))
	demandData[service.DemandActionResult](t, f.request(t, "admin", "POST", "/api/demand-management/"+id+"/actions", service.DemandAction{ClientActionID: "close-admin-conversation", Revision: current.Demand.Revision, Action: "close"}, 200))
	denied.ClientMessageID = "closed-admin-must-not-send"
	f.request(t, "admin", "POST", "/api/demand-management/"+id+"/messages", denied, 409)
	again := demandData[service.DemandSendReceipt](t, f.request(t, "admin", "POST", "/api/demand-management/"+id+"/messages", body, 200))
	assert.True(t, again.Replayed)
	assert.Equal(t, reply.Message.ID, again.Message.ID)
	var count int64
	require.NoError(t, f.db.Model(&model.DemandMessage{}).Where("demand_id = ? AND sender_id = ? AND kind = ?", id, f.users["admin"].Id, "message").Count(&count).Error)
	assert.EqualValues(t, 1, count)
	for _, table := range []any{&model.Task{}, &model.WalletFundingEvent{}, &model.Log{}} {
		require.NoError(t, f.db.Model(table).Count(&count).Error)
		assert.Zero(t, count)
	}
	var owner model.User
	require.NoError(t, f.db.First(&owner, f.users["owner"].Id).Error)
	assert.Equal(t, f.users["owner"].Quota, owner.Quota)
	assert.Equal(t, f.users["owner"].AuthVersion, owner.AuthVersion)
}
