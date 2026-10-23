package router

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const announcementOption = "console_setting.announcements"
const announcementEnabledOption = "console_setting.announcements_enabled"
const announcementMetadataOption = "ClientAnnouncementsMetadata"

func TestClientAnnouncementsContractAndPersistence(t *testing.T) {
	f := newVideoQuoteFixture(t)
	previousOptions := maps.Clone(common.OptionMap)
	previousGlobalLimit, previousCriticalLimit := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable
	previousSecure := common.SessionCookieSecure
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.SessionCookieSecure = false, false, true
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.SessionCookieSecure = previousGlobalLimit, previousCriticalLimit, previousSecure
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, f.db.Migrator().DropTable(&model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}))
	require.NoError(t, f.db.AutoMigrate(&model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}))
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	credentials := map[string]string{}
	for _, identity := range []struct {
		name string
		role int
	}{{"root", common.RoleRootUser}, {"admin", common.RoleAdminUser}, {"member", common.RoleCommonUser}} {
		user := model.User{Username: "announcement-" + identity.name, AffCode: "ann-" + identity.name, Role: identity.role, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "default"}
		require.NoError(t, f.db.Create(&user).Error)
		bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "isolated-announcement-contract")
		require.NoError(t, err)
		credentials[identity.name] = bundle.AccessToken
	}
	engine := newAccessTokenScopeTestEngine(t)
	request := func(t *testing.T, method, path, credential, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://example.com")
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		if output := os.Getenv("CLIENT_ANNOUNCEMENTS_FIXTURE_OUTPUT"); output != "" && path == "/api/client-announcements" {
			require.True(t, filepath.IsAbs(output))
			require.NoError(t, os.MkdirAll(filepath.Dir(output), 0o700))
			var response any
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			var fixtures []map[string]any
			if prior, err := os.ReadFile(output); err == nil {
				require.NoError(t, common.Unmarshal(prior, &fixtures))
			} else {
				require.True(t, os.IsNotExist(err))
			}
			fixtures = append(fixtures, map[string]any{"test": t.Name(), "database": common.MainDatabaseType(), "method": method, "path": path, "http_status": w.Code, "response": response, "outbound_requests": f.transport.attempts.Load()})
			encoded, err := common.Marshal(fixtures)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(output, encoded, 0o600))
		}
		return w
	}
	getFeed := func(t *testing.T) model.ClientAnnouncementsFeed {
		t.Helper()
		before := f.snapshot(t)
		w := request(t, http.MethodGet, "/api/client-announcements", "", "")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Header().Get("Cache-Control"), "no-store")
		var envelope struct {
			Success bool                          `json:"success"`
			Message string                        `json:"message"`
			Data    model.ClientAnnouncementsFeed `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		assert.Empty(t, envelope.Message)
		assert.Equal(t, 1, envelope.Data.SchemaVersion)
		assert.Equal(t, "plain_text", envelope.Data.ContentFormat)
		assert.NotNil(t, envelope.Data.Announcements)
		assert.Equal(t, before, f.snapshot(t), "anonymous GET must not write any database state")
		assert.Zero(t, f.transport.attempts.Load())
		return envelope.Data
	}
	setLegacy := func(t *testing.T, raw, enabled string) {
		t.Helper()
		require.NoError(t, f.db.Where(clause.IN{Column: clause.Column{Name: "key"}, Values: []any{announcementOption, announcementEnabledOption, announcementMetadataOption}}).Delete(&model.Option{}).Error)
		require.NoError(t, f.db.Create(&[]model.Option{{Key: announcementOption, Value: raw}, {Key: announcementEnabledOption, Value: enabled}}).Error)
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{announcementOption: raw, announcementEnabledOption: enabled}))
		common.OptionMapRWMutex.Lock()
		common.OptionMap[announcementOption], common.OptionMap[announcementEnabledOption] = raw, enabled
		common.OptionMapRWMutex.Unlock()
	}
	encodeRows := func(t *testing.T, rows []map[string]any) string {
		t.Helper()
		encoded, err := common.Marshal(rows)
		require.NoError(t, err)
		return string(encoded)
	}
	newRow := func(id int, content string) map[string]any {
		return map[string]any{"id": id, "content": content, "publishDate": "2026-10-23T01:02:03Z", "type": "warning", "extra": "note"}
	}
	adminRows := func(t *testing.T) []map[string]any {
		t.Helper()
		w := request(t, "GET", "/api/option/", credentials["root"], "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var envelope struct {
			Success bool           `json:"success"`
			Data    []model.Option `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		for _, option := range envelope.Data {
			assert.NotEqual(t, announcementMetadataOption, option.Key)
			if option.Key == announcementOption {
				var rows []map[string]any
				require.NoError(t, common.UnmarshalJsonStr(option.Value, &rows))
				return rows
			}
		}
		t.Fatal("announcement option missing from administrator response")
		return nil
	}

	t.Run("anonymous empty and legacy compatibility", func(t *testing.T) {
		for _, raw := range []string{"", "[]", "null"} {
			setLegacy(t, raw, "true")
			feed := getFeed(t)
			assert.Empty(t, feed.Announcements)
			assert.Nil(t, feed.UpdatedAt)
		}
		rows := []map[string]any{newRow(1, "old body"), newRow(1, "second body"), newRow(0, "third body")}
		rows[0]["private"] = "unknown-field-must-not-leak"
		rows[0]["updatedAt"] = "1999-01-01T00:00:00Z"
		delete(rows[2], "id")
		raw := encodeRows(t, rows)
		setLegacy(t, raw, "true")
		feed := getFeed(t)
		require.Len(t, feed.Announcements, 3)
		assert.Equal(t, []int64{1, 2, 3}, []int64{feed.Announcements[0].ID, feed.Announcements[1].ID, feed.Announcements[2].ID})
		assert.Equal(t, "old body", feed.Announcements[0].Title)
		assert.Nil(t, feed.Announcements[0].UpdatedAt)
		public := request(t, "GET", "/api/client-announcements", "", "")
		assert.NotContains(t, public.Body.String(), "unknown-field-must-not-leak")
		var projected map[string]any
		require.NoError(t, common.Unmarshal(public.Body.Bytes(), &projected))
		item := projected["data"].(map[string]any)["announcements"].([]any)[0].(map[string]any)
		assert.ElementsMatch(t, []string{"id", "title", "content", "publishDate", "updatedAt", "type", "extra"}, slices.Collect(maps.Keys(item)))
		require.NoError(t, model.UpdateOption("Notice", "independent legacy notice"))
		before := f.snapshot(t)
		status := request(t, "GET", "/api/status", "", "")
		var statusBody struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(status.Body.Bytes(), &statusBody))
		assert.Equal(t, true, statusBody.Data["announcements_enabled"])
		assert.Equal(t, "unknown-field-must-not-leak", statusBody.Data["announcements"].([]any)[0].(map[string]any)["private"], "old status retains its prior raw DTO")
		notice := request(t, "GET", "/api/notice", "", "")
		assert.Contains(t, notice.Body.String(), "independent legacy notice")
		assert.NotEmpty(t, notice.Header().Get("ETag"))
		assert.Equal(t, before, f.snapshot(t))
		require.NoError(t, model.UpdateOption(announcementEnabledOption, "false"))
		status = request(t, "GET", "/api/status", "", "")
		statusBody.Data = nil
		require.NoError(t, common.Unmarshal(status.Body.Bytes(), &statusBody))
		assert.Equal(t, false, statusBody.Data["announcements_enabled"])
		assert.NotContains(t, statusBody.Data, "announcements", "legacy disabled response still omits its old field")
	})

	t.Run("plain text projection removes active HTML", func(t *testing.T) {
		row := newRow(5, `<p>Hello <b>world</b> &amp; friends</p><script>script-secret</script><style>style-secret</style><iframe>frame-secret</iframe><svg><text>svg-secret</text></svg><a href="javascript:secret()" onclick="bad()">link text</a>`)
		row["title"], row["extra"] = `<img src=x onerror="bad()">Safe <b>title</b>`, `<style>hidden</style>visible note`
		setLegacy(t, encodeRows(t, []map[string]any{row}), "true")
		feed := getFeed(t)
		require.Len(t, feed.Announcements, 1)
		assert.Equal(t, "Safe title", feed.Announcements[0].Title)
		assert.Equal(t, "visible note", feed.Announcements[0].Extra)
		assert.Contains(t, feed.Announcements[0].Content, "Hello world & friends")
		assert.Contains(t, feed.Announcements[0].Content, "link text")
		for _, forbidden := range []string{"script-secret", "style-secret", "frame-secret", "svg-secret", "javascript:", "onclick", "<b>"} {
			assert.NotContains(t, feed.Announcements[0].Content, forbidden)
		}
	})

	t.Run("disabled malformed and database failure stay distinct", func(t *testing.T) {
		for _, raw := range []string{"not-json", `{}`, `[{"content":"missing date"}]`, `[{"content":"bad date","publishDate":"yesterday"}]`, strings.Repeat("x", 1024*1024+1)} {
			setLegacy(t, raw, "true")
			before := f.snapshot(t)
			w := request(t, "GET", "/api/client-announcements", "", "")
			assert.Equal(t, 503, w.Code)
			assert.Contains(t, w.Body.String(), `"code":"client_announcements_unavailable"`)
			assert.NotContains(t, w.Body.String(), "yesterday")
			assert.Equal(t, before, f.snapshot(t))
			require.NoError(t, f.db.Model(&model.Option{}).Where(&model.Option{Key: announcementEnabledOption}).Update("value", "false").Error)
			feed := getFeed(t)
			assert.False(t, feed.AnnouncementsEnabled)
			assert.Empty(t, feed.Announcements)
		}
		setLegacy(t, "[]", "true")
		before := f.snapshot(t)
		callback := "announcements:read-unavailable"
		require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "options" {
				tx.AddError(errors.New("private-dsn-secret"))
			}
		}))
		w := request(t, "GET", "/api/client-announcements", "", "")
		require.NoError(t, f.db.Callback().Query().Remove(callback))
		assert.Equal(t, 503, w.Code)
		assert.NotContains(t, w.Body.String(), "private-dsn-secret")
		assert.Equal(t, before, f.snapshot(t))
		require.NoError(t, f.db.Model(&model.Option{}).Where(&model.Option{Key: announcementEnabledOption}).Update("value", "invalid-bool").Error)
		assert.Equal(t, 503, request(t, "GET", "/api/client-announcements", "", "").Code)
		require.NoError(t, model.UpdateOption(announcementEnabledOption, "true"), "administrator can repair an invalid old enable value")
		assert.True(t, getFeed(t).AnnouncementsEnabled)
	})

	t.Run("root authorization and title boundaries", func(t *testing.T) {
		setLegacy(t, "[]", "true")
		for _, role := range []string{"anonymous", "member", "admin"} {
			want := 403
			if role == "anonymous" {
				want = 401
			}
			for _, method := range []string{"GET", "PUT"} {
				w := request(t, method, "/api/option/", credentials[role], `{"key":"console_setting.announcements","value":"[]"}`)
				assert.Equal(t, want, w.Code, role+" "+method+" "+w.Body.String())
			}
		}
		for _, tc := range []struct {
			name  string
			title any
			valid bool
		}{{"120 BMP", strings.Repeat("界", 120), true}, {"121 BMP", strings.Repeat("界", 121), false}, {"60 emoji", strings.Repeat("😀", 60), true}, {"61 emoji", strings.Repeat("😀", 61), false}, {"nontext", 42, false}, {"optional empty", "", true}} {
			row := newRow(7, "title boundary body")
			row["title"] = tc.title
			raw := encodeRows(t, []map[string]any{row})
			body, err := common.Marshal(map[string]any{"key": announcementOption, "value": raw})
			require.NoError(t, err)
			w := request(t, "PUT", "/api/option/", credentials["root"], string(body))
			var response struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			assert.Equal(t, tc.valid, response.Success, tc.name+" "+w.Body.String())
		}
		for _, tc := range []struct {
			field  string
			length int
			valid  bool
		}{{"content", 500, true}, {"content", 501, false}, {"extra", 100, true}, {"extra", 101, false}} {
			row := newRow(7, "body")
			row[tc.field] = strings.Repeat("x", tc.length)
			err := model.UpdateOption(announcementOption, encodeRows(t, []map[string]any{row}))
			assert.Equal(t, tc.valid, err == nil, tc.field)
		}
		for _, count := range []int{100, 101} {
			rows := make([]map[string]any, count)
			for i := range rows {
				rows[i] = newRow(i+1, "bounded body")
			}
			assert.Equal(t, count == 100, model.UpdateOption(announcementOption, encodeRows(t, rows)) == nil)
		}
		assert.Error(t, model.UpdateOption(announcementOption, strings.Repeat("x", 1024*1024+1)))
		assert.Error(t, model.UpdateOption(announcementMetadataOption, `{}`))
	})

	t.Run("server timestamps survive saves edits ordering and disable", func(t *testing.T) {
		row := newRow(1, "legacy unchanged")
		row["updatedAt"] = "1999-01-01T00:00:00Z"
		setLegacy(t, encodeRows(t, []map[string]any{row}), "true")
		assert.Nil(t, getFeed(t).Announcements[0].UpdatedAt)
		require.NoError(t, model.UpdateOption(announcementOption, encodeRows(t, []map[string]any{row})))
		assert.Nil(t, getFeed(t).Announcements[0].UpdatedAt)
		row["content"] = "edited by administrator"
		before := time.Now().UTC()
		require.NoError(t, model.UpdateOption(announcementOption, encodeRows(t, []map[string]any{row})))
		first := getFeed(t)
		require.NotNil(t, first.Announcements[0].UpdatedAt)
		updated, err := time.Parse(time.RFC3339Nano, *first.Announcements[0].UpdatedAt)
		require.NoError(t, err)
		assert.False(t, updated.Before(before))
		assert.False(t, updated.After(time.Now().UTC()))
		assert.True(t, strings.HasSuffix(*first.Announcements[0].UpdatedAt, "Z"))
		row["updatedAt"] = "2099-01-01T00:00:00Z"
		require.NoError(t, model.UpdateOption(announcementOption, encodeRows(t, []map[string]any{row})))
		assert.Equal(t, first.Announcements[0].UpdatedAt, getFeed(t).Announcements[0].UpdatedAt)
		secondDB, err := gorm.Open(f.db.Dialector, &gorm.Config{})
		require.NoError(t, err)
		model.DB = secondDB
		independent, readErr := model.GetClientAnnouncements(context.Background())
		model.DB = f.db
		secondSQL, sqlErr := secondDB.DB()
		require.NoError(t, sqlErr)
		require.NoError(t, secondSQL.Close())
		require.NoError(t, readErr)
		assert.Equal(t, first.Announcements[0].UpdatedAt, independent.Announcements[0].UpdatedAt, "timestamp is durable rather than process cache state")
		rows := []map[string]any{row, newRow(2, "second announcement")}
		require.NoError(t, model.UpdateOption(announcementOption, encodeRows(t, rows)))
		ordered := getFeed(t)
		rows[0], rows[1] = rows[1], rows[0]
		require.NoError(t, model.UpdateOption(announcementOption, encodeRows(t, rows)))
		reordered := getFeed(t)
		assert.Equal(t, int64(2), reordered.Announcements[0].ID)
		assert.NotEqual(t, ordered.UpdatedAt, reordered.UpdatedAt)
		assert.Equal(t, ordered.Announcements[0].UpdatedAt, reordered.Announcements[1].UpdatedAt)
		require.NoError(t, model.UpdateOption(announcementEnabledOption, "false"))
		disabled := getFeed(t)
		assert.Empty(t, disabled.Announcements)
		assert.NotEqual(t, reordered.UpdatedAt, disabled.UpdatedAt)
		admin := adminRows(t)
		require.Len(t, admin, 2)
		assert.Equal(t, *reordered.Announcements[0].UpdatedAt, admin[0]["updatedAt"])
		assert.NotEqual(t, "2099-01-01T00:00:00Z", admin[1]["updatedAt"])
		require.NoError(t, model.UpdateOption(announcementEnabledOption, "false"))
		assert.Equal(t, disabled.UpdatedAt, getFeed(t).UpdatedAt)
		require.NoError(t, model.UpdateOption(announcementEnabledOption, "true"))
		assert.Equal(t, reordered.Announcements[0].UpdatedAt, getFeed(t).Announcements[0].UpdatedAt)
		require.NoError(t, model.UpdateOption(announcementOption, "[]"))
		deleted := getFeed(t)
		assert.Empty(t, deleted.Announcements)
		assert.NotEqual(t, disabled.UpdatedAt, deleted.UpdatedAt)
		rows = []map[string]any{newRow(2, "second announcement")}
		require.NoError(t, model.UpdateOption(announcementOption, encodeRows(t, rows)))
		assert.NotEqual(t, reordered.Announcements[0].UpdatedAt, getFeed(t).Announcements[0].UpdatedAt, "deleted IDs do not inherit obsolete times")
		// An old writer changes raw bytes outside the trusted transaction.
		rows[0]["content"] = "old writer bypass edit"
		require.NoError(t, f.db.Model(&model.Option{}).Where(&model.Option{Key: announcementOption}).Update("value", encodeRows(t, rows)).Error)
		assert.Nil(t, getFeed(t).Announcements[0].UpdatedAt)
		assert.Nil(t, adminRows(t)[0]["updatedAt"], "stale admin cache cannot receive mismatched timestamps")
	})

	t.Run("bulk rollback protects timestamps and caches", func(t *testing.T) {
		setLegacy(t, "[]", "true")
		require.NoError(t, model.UpdateOptionsBulk(map[string]string{announcementOption: encodeRows(t, []map[string]any{newRow(1, "before rollback")}), "AnnouncementAtomicSentinel": "before"}))
		before := f.snapshot(t)
		beforeOptions := maps.Clone(common.OptionMap)
		injected := false
		callback := "announcements:atomic-write-failure"
		require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			if option, ok := tx.Statement.Dest.(*model.Option); ok && option.Key == "AnnouncementAtomicSentinel" {
				injected = true
				tx.AddError(errors.New("synthetic atomic option failure"))
			}
		}))
		err := model.UpdateOptionsBulk(map[string]string{announcementOption: encodeRows(t, []map[string]any{newRow(1, "must roll back")}), announcementEnabledOption: "false", "AnnouncementAtomicSentinel": "after"})
		require.NoError(t, f.db.Callback().Update().Remove(callback))
		require.True(t, injected)
		require.Error(t, err)
		assert.Equal(t, before, f.snapshot(t))
		assert.Equal(t, beforeOptions, common.OptionMap)
		assert.Equal(t, "before rollback", getFeed(t).Announcements[0].Content)
		assert.Error(t, model.UpdateOptionsBulk(map[string]string{announcementOption: "[]", "passkey.origins": "https://example.com"}))
		assert.Equal(t, before, f.snapshot(t))
	})

	t.Run("concurrent saves expose only complete durable revisions", func(t *testing.T) {
		setLegacy(t, "[]", "true")
		require.NoError(t, model.UpdateOption(announcementOption, encodeRows(t, []map[string]any{newRow(10, "before concurrent writes")})))
		before := getFeed(t)
		sqlDB, err := f.db.DB()
		require.NoError(t, err)
		previousLimit := sqlDB.Stats().MaxOpenConnections
		sqlDB.SetMaxOpenConns(4)
		defer sqlDB.SetMaxOpenConns(previousLimit)
		paused, release := make(chan struct{}), make(chan struct{})
		var pauseOnce, releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		callback := "announcements:pause-uncommitted-metadata"
		require.NoError(t, f.db.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
			values, ok := tx.Statement.Dest.(map[string]any)
			if !ok || tx.Statement.Table != "options" {
				return
			}
			value, ok := values["value"].(string)
			if ok && strings.Contains(value, `"config_fingerprint"`) {
				pauseOnce.Do(func() {
					close(paused)
					<-release
				})
			}
		}))
		defer func() { require.NoError(t, f.db.Callback().Update().Remove(callback)) }()
		firstRaw := encodeRows(t, []map[string]any{newRow(11, "concurrent A")})
		secondRaw := encodeRows(t, []map[string]any{newRow(22, "concurrent B")})
		firstDone, secondDone, secondStarted := make(chan error, 1), make(chan error, 1), make(chan struct{})
		go func() {
			firstDone <- model.UpdateOptionsBulk(map[string]string{announcementOption: firstRaw, announcementEnabledOption: "true"})
		}()
		select {
		case <-paused:
		case <-time.After(10 * time.Second):
			t.Fatal("first writer did not reach its uncommitted metadata update")
		}
		go func() {
			close(secondStarted)
			secondDone <- model.UpdateOptionsBulk(map[string]string{announcementOption: secondRaw, announcementEnabledOption: "true"})
		}()
		<-secondStarted
		assert.Equal(t, before, getFeed(t), "reader must not mix uncommitted metadata with the old raw content")
		releaseOnce.Do(func() { close(release) })
		select {
		case err = <-firstDone:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("first writer did not complete")
		}
		select {
		case err = <-secondDone:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("second writer did not complete")
		}
		final := getFeed(t)
		require.Len(t, final.Announcements, 1)
		assert.Equal(t, int64(22), final.Announcements[0].ID)
		assert.Equal(t, "concurrent B", final.Announcements[0].Content)
		assert.NotNil(t, final.Announcements[0].UpdatedAt, "persisted content and metadata must have matching fingerprints")
		assert.NotNil(t, final.UpdatedAt)
	})

	t.Run("metadata aliases and specialized writer cannot bypass protection", func(t *testing.T) {
		setLegacy(t, "[]", "true")
		before := f.snapshot(t)
		for _, values := range []map[string]string{{announcementMetadataOption: `{}`}, {"clientannouncementsmetadata ": `{}`}} {
			assert.Error(t, model.UpdateOptionsBulk(values))
			_, err := model.UpdatePasskeyDomainOptions(values, false, "")
			assert.Error(t, err)
		}
		assert.Equal(t, before, f.snapshot(t))
		for _, alias := range []string{strings.ToUpper(announcementOption), announcementOption + " ", strings.ToUpper(announcementEnabledOption), announcementEnabledOption + " "} {
			assert.Error(t, model.UpdateOption(alias, "not-json"), alias)
			assert.Error(t, model.UpdateOptionsBulk(map[string]string{alias: "not-json"}), alias)
			_, err := model.UpdatePasskeyDomainOptions(map[string]string{alias: "not-json"}, false, "")
			assert.Error(t, err, alias)
			assert.Equal(t, before, f.snapshot(t))
		}
		if common.MainDatabaseType() != common.DatabaseTypeMySQL {
			return
		}
		// MySQL's configured accent/case-insensitive collation is exercised by
		// actual lookups, both with and without an existing canonical row.
		for _, canonical := range []string{announcementOption, announcementEnabledOption, announcementMetadataOption} {
			alias := strings.Replace(canonical, "announcements", "announcéments", 1)
			if canonical == announcementMetadataOption {
				alias = "ClientAnnouncémentsMetadata"
			}
			for _, existing := range []bool{true, false} {
				if existing {
					value := "[]"
					if canonical == announcementEnabledOption {
						value = "true"
					}
					if canonical == announcementMetadataOption {
						value = "{}"
					}
					require.NoError(t, f.db.Save(&model.Option{Key: canonical, Value: value}).Error)
				} else {
					require.NoError(t, f.db.Where(&model.Option{Key: canonical}).Delete(&model.Option{}).Error)
				}
				beforeAlias := f.snapshot(t)
				assert.Error(t, model.UpdateOption(alias, "not-json"), canonical)
				assert.Equal(t, beforeAlias, f.snapshot(t))
				assert.Error(t, model.UpdateOptionsBulk(map[string]string{alias: "not-json"}), canonical)
				assert.Equal(t, beforeAlias, f.snapshot(t))
				_, err := model.UpdatePasskeyDomainOptions(map[string]string{alias: "not-json"}, false, "")
				assert.Error(t, err, canonical)
				assert.Equal(t, beforeAlias, f.snapshot(t))
			}
		}
	})
}
