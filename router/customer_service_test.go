package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/creationcontent"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type customerServiceNoNetwork struct{ calls int }

func (transport *customerServiceNoNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, fmt.Errorf("customer service configuration must not make outbound requests")
}

func TestCustomerServiceContractAndPersistence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	dsn := os.Getenv("TEST_CUSTOMER_SERVICE_SQL_DSN")
	if dsn == "" {
		dsn = "local"
	} else {
		require.True(t, strings.Contains(dsn, "test_customer_service_"), "use only a dedicated disposable customer service database")
	}
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", "")
	assetDir := t.TempDir()
	t.Setenv("CREATION_CONTENT_DIR", assetDir)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousPath := common.SQLitePath
	previousMaster, previousRedis, previousMemory := common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled
	previousGlobal, previousCritical := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable
	previousSecure, previousSecret := common.SessionCookieSecure, common.SessionSecret
	previousWeChat := common.WeChatAccountQRCodeImageURL
	common.OptionMapRWMutex.RLock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMapRWMutex.RUnlock()
	common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = false, false, false
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.SessionCookieSecure = false, false, true
	common.SQLitePath = filepath.Join(t.TempDir(), "customer-service.db")
	common.SessionSecret = "isolated-customer-service-contract"
	common.WeChatAccountQRCodeImageURL = "https://login.example.invalid/separate-login-code.png"
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{"WeChatAccountQRCodeImageURL": common.WeChatAccountQRCodeImageURL}
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, model.InitDB())
	db := model.DB
	model.LOG_DB = db
	common.SetDatabaseTypes(common.MainDatabaseType(), common.MainDatabaseType())
	// This fixture never drops a pre-existing table or resets a shared database.
	require.False(t, db.Migrator().HasTable(&model.Option{}), "the isolated database must be fresh")
	models := []any{&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Option{}, &model.AuditLog{}, &model.Token{}}
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, db.AutoMigrate(models...))
	var version string
	query := "SELECT version()"
	if common.MainDatabaseType() == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("Customer service actual database: %s %s", common.MainDatabaseType(), version)
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.SQLitePath = previousPath
		common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = previousMaster, previousRedis, previousMemory
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = previousGlobal, previousCritical
		common.SessionCookieSecure, common.SessionSecret = previousSecure, previousSecret
		common.WeChatAccountQRCodeImageURL = previousWeChat
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	transport := &customerServiceNoNetwork{}
	previousTransport, previousClientTransport := http.DefaultTransport, http.DefaultClient.Transport
	http.DefaultTransport, http.DefaultClient.Transport = transport, transport
	t.Cleanup(func() {
		http.DefaultTransport, http.DefaultClient.Transport = previousTransport, previousClientTransport
		assert.Zero(t, transport.calls, "no URL lookup, SMS, generation or provider request is permitted")
	})
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	legacy := "customer-service-legacy-root"
	credentials := map[string]string{}
	for _, identity := range []struct {
		name string
		role int
	}{{"admin", common.RoleAdminUser}, {"member", common.RoleCommonUser}, {"root", common.RoleRootUser}} {
		user := model.User{Username: "customer-service-" + identity.name, AffCode: "cs-" + identity.name, Role: identity.role, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "default"}
		if identity.name == "root" {
			user.AccessToken = &legacy
		}
		require.NoError(t, db.Create(&user).Error)
		bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "isolated-customer-service-contract")
		require.NoError(t, err)
		credentials[identity.name] = bundle.AccessToken
		if identity.name == "admin" {
			pat := model.AccessTokenPrefix + strings.Repeat("q", 43)
			credential := model.UserAccessToken{UserId: user.Id, Name: "customer-service-PAT-fixture", TokenHash: model.AccessTokenFingerprint(pat), TokenHint: model.AccessTokenHint(pat)}
			require.NoError(t, credential.SetScopes([]string{"option:read", "option:write"}))
			require.NoError(t, db.Create(&credential).Error)
			credentials["PAT"] = pat
			relayKey := strings.Repeat("s", 48)
			require.NoError(t, db.Create(&model.Token{UserId: user.Id, Key: relayKey, Status: common.TokenStatusEnabled, Name: "unused relay credential", UnlimitedQuota: true, ExpiredTime: -1}).Error)
			credentials["relay"] = "sk-" + relayKey
		}
	}
	credentials["legacy"] = legacy
	engine := newAccessTokenScopeTestEngine(t)
	request := func(method, path, credential, body, contentType, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("Origin", origin)
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		if output := os.Getenv("CUSTOMER_SERVICE_FIXTURE_OUTPUT"); output != "" {
			require.True(t, filepath.IsAbs(output))
			require.NoError(t, os.MkdirAll(filepath.Dir(output), 0o700))
			var response map[string]any
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			if path == "/api/status" {
				data, ok := response["data"].(map[string]any)
				require.True(t, ok)
				response = map[string]any{"success": response["success"], "message": response["message"], "data": map[string]any{"customer_service": data["customer_service"]}}
			}
			var fixtures []map[string]any
			if prior, err := os.ReadFile(output); err == nil {
				require.NoError(t, common.Unmarshal(prior, &fixtures))
			} else {
				require.True(t, os.IsNotExist(err))
			}
			fixtures = append(fixtures, map[string]any{"database": common.MainDatabaseType(), "method": method, "path": path, "http_status": w.Code, "cache_control": w.Header().Get("Cache-Control"), "response": response, "outbound_requests": transport.calls})
			encoded, err := common.Marshal(fixtures)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(output, encoded, 0o600))
		}
		return w
	}
	encode := func(config model.CustomerServiceConfig) string {
		data, err := common.Marshal(config)
		require.NoError(t, err)
		return string(data)
	}
	decode := func(w *httptest.ResponseRecorder) model.CustomerServiceConfig {
		var result struct {
			Success bool                        `json:"success"`
			Data    model.CustomerServiceConfig `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
		require.True(t, result.Success, w.Body.String())
		return result.Data
	}
	public := func() model.CustomerServiceConfig {
		w := request(http.MethodGet, "/api/status", "", "", "", "http://example.com")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Cache-Control"), "no-store")
		var result struct {
			Data struct {
				CustomerService model.CustomerServiceConfig `json:"customer_service"`
				WeChatQRCode    string                      `json:"wechat_qrcode"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
		assert.Equal(t, "https://login.example.invalid/separate-login-code.png", result.Data.WeChatQRCode)
		return result.Data.CustomerService
	}
	empty := model.CustomerServiceConfig{SchemaVersion: 1}
	valid := model.CustomerServiceConfig{SchemaVersion: 1, Title: "客服 <b>字面文字</b>", Description: "联系售后"}
	var pngData, jpegData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	require.NoError(t, jpeg.Encode(&jpegData, image.NewNRGBA(image.Rect(0, 0, 2, 2)), nil))
	asset, err := creationcontent.Default().Upload(bytes.NewReader(pngData.Bytes()))
	require.NoError(t, err)
	valid.QRCodeURL = asset.URL

	t.Run("missing resource is closed without creating any option", func(t *testing.T) {
		var before, after []model.Option
		require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&before).Error)
		assert.Equal(t, empty, public())
		w := request(http.MethodGet, "/api/customer-service", credentials["admin"], "", "", "http://example.com")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, empty, decode(w))
		require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&after).Error)
		assert.Equal(t, before, after)
	})
	t.Run("administrator session only and same origin writes", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			for _, identity := range []struct {
				name string
				code int
			}{{"", 401}, {"member", 403}, {"legacy", 403}, {"PAT", 403}, {"relay", 401}, {"admin", 200}, {"root", 200}} {
				w := request(method, "/api/customer-service", credentials[identity.name], encode(empty), "application/json", "http://example.com")
				assert.Equal(t, identity.code, w.Code, method+" "+identity.name+" "+w.Body.String())
				assert.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			}
		}
		w := request(http.MethodPut, "/api/customer-service", credentials["admin"], encode(empty), "application/json", "https://foreign.example")
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AUTH_ORIGIN_FORBIDDEN")
	})
	t.Run("disabled draft is retained only for administrator and enabled fields stay literal", func(t *testing.T) {
		w := request(http.MethodPut, "/api/customer-service", credentials["admin"], encode(valid), "application/json", "http://example.com")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, valid, decode(w))
		assert.Equal(t, empty, public())
		assert.Equal(t, valid, decode(request(http.MethodGet, "/api/customer-service", credentials["admin"], "", "", "http://example.com")))
		valid.Enabled = true
		w = request(http.MethodPut, "/api/customer-service", credentials["admin"], encode(valid), "application/json", "http://example.com")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, valid, decode(w))
		assert.Equal(t, valid, public())
		var stored model.Option
		require.NoError(t, db.Where(&model.Option{Key: "CustomerServiceConfig"}).Take(&stored).Error)
		assert.Equal(t, encode(valid), stored.Value)
		require.NoError(t, db.AutoMigrate(models...))
		model.InitOptionMap()
		assert.Equal(t, valid, model.CurrentCustomerServiceConfig(), "a fresh options load retains the committed complete object")
		assert.Equal(t, valid, public())
	})
	t.Run("strict JSON URL and text failures preserve last committed object", func(t *testing.T) {
		body := encode(valid)
		cases := map[string]string{
			"missing":           `{"schema_version":1,"enabled":false,"qr_code_url":"","title":""}`,
			"unknown":           strings.TrimSuffix(body, "}") + `,"other":false}`,
			"duplicate":         strings.TrimSuffix(body, "}") + `,"enabled":false}`,
			"escaped duplicate": strings.TrimSuffix(body, "}") + `,"\u0065nabled":false}`,
			"null":              strings.Replace(body, `"enabled":true`, `"enabled":null`, 1),
			"boolean string":    strings.Replace(body, `"enabled":true`, `"enabled":"true"`, 1),
			"schema":            strings.Replace(body, `"schema_version":1`, `"schema_version":2`, 1),
			"root array":        "[" + body + "]",
			"trailing":          body + "{}",
			"UTF8":              strings.TrimSuffix(body, "}") + string([]byte{0xff}) + "}",
		}
		for name, raw := range cases {
			t.Run(name, func(t *testing.T) {
				w := request(http.MethodPut, "/api/customer-service", credentials["admin"], raw, "application/json", "http://example.com")
				assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				assert.Equal(t, valid, model.CurrentCustomerServiceConfig())
			})
		}
		for _, url := range []string{"https://example.com/code.png", "https://user:password@example.com/code.png", "http://example.com/code.png", "//example.com/a", "data:image/png;base64,AA", "javascript:alert(1)", "file:///a", "/api/creation/assets/../secret", asset.URL + "?token=secret", asset.URL + "#x", strings.Replace(asset.URL, "cc_", "cc_%", 1), " /api/creation/assets/" + asset.ID, "/harness" + asset.URL, "/api/creation/assets/" + strings.ToUpper(asset.ID), strings.Repeat("a", 2049)} {
			config := valid
			config.QRCodeURL = url
			w := request(http.MethodPut, "/api/customer-service", credentials["admin"], encode(config), "application/json", "http://example.com")
			assert.Equal(t, http.StatusBadRequest, w.Code, url)
		}
		for _, config := range []model.CustomerServiceConfig{
			{SchemaVersion: 1, Enabled: true},
			{SchemaVersion: 1, Title: strings.Repeat("😀", 33)},
			{SchemaVersion: 1, Description: strings.Repeat("😀", 129)},
			{SchemaVersion: 1, Title: "客服\n"},
			{SchemaVersion: 1, Description: "说明\t"},
			{SchemaVersion: 1, Description: "\u0085"},
		} {
			assert.Equal(t, http.StatusBadRequest, request(http.MethodPut, "/api/customer-service", credentials["admin"], encode(config), "application/json", "http://example.com").Code)
		}
		assert.Equal(t, http.StatusBadRequest, request(http.MethodPut, "/api/customer-service", credentials["admin"], body, "text/plain", "http://example.com").Code)
		assert.Equal(t, http.StatusRequestEntityTooLarge, request(http.MethodPut, "/api/customer-service", credentials["admin"], strings.Repeat(" ", model.MaxCustomerServiceBodyBytes+1), "application/json", "http://example.com").Code)
		var stored model.Option
		require.NoError(t, db.Where(&model.Option{Key: "CustomerServiceConfig"}).Take(&stored).Error)
		assert.Equal(t, body, stored.Value)
		assert.Equal(t, valid, model.CurrentCustomerServiceConfig())
	})
	t.Run("uploaded raster bytes must match the hash and fully decode within limits", func(t *testing.T) {
		webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
		require.NoError(t, err)
		for name, data := range map[string][]byte{"jpeg": jpegData.Bytes(), "webp": webp} {
			t.Run(name, func(t *testing.T) {
				asset, err := creationcontent.Default().Upload(bytes.NewReader(data))
				require.NoError(t, err)
				config := valid
				config.QRCodeURL = asset.URL
				require.NoError(t, model.SaveCustomerServiceConfig(config))
				assert.Equal(t, config, model.CurrentCustomerServiceConfig())
			})
		}
		hugePixels := bytes.Clone(pngData.Bytes())
		binary.BigEndian.PutUint32(hugePixels[16:20], 4097)
		binary.BigEndian.PutUint32(hugePixels[20:24], 4096)
		binary.BigEndian.PutUint32(hugePixels[29:33], crc32.ChecksumIEEE(hugePixels[12:29]))
		hugeDimensions := bytes.Clone(pngData.Bytes())
		binary.BigEndian.PutUint32(hugeDimensions[16:20], 0x7fffffff)
		binary.BigEndian.PutUint32(hugeDimensions[20:24], 0x7fffffff)
		binary.BigEndian.PutUint32(hugeDimensions[29:33], crc32.ChecksumIEEE(hugeDimensions[12:29]))
		fakeWebP := append([]byte("RIFF"), make([]byte, 26)...)
		binary.LittleEndian.PutUint32(fakeWebP[4:8], uint32(len(fakeWebP)-8))
		copy(fakeWebP[8:], "WEBPVP8 ")
		video := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '2'}
		for name, data := range map[string][]byte{"truncated png": pngData.Bytes()[:33], "SVG": []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), "video": video, "spoofed WebP": fakeWebP, "over pixels": hugePixels, "huge dimensions": hugeDimensions, "over bytes": append(bytes.Clone(pngData.Bytes()), make([]byte, creationcontent.MaxImageSize)...)} {
			t.Run(name, func(t *testing.T) {
				id := fmt.Sprintf("cc_%x", sha256.Sum256(data))
				require.NoError(t, os.WriteFile(filepath.Join(assetDir, "assets", id), data, 0o600))
				config := valid
				config.QRCodeURL = "/api/creation/assets/" + id
				assert.ErrorIs(t, model.SaveCustomerServiceConfig(config), model.ErrCustomerServiceImage)
			})
		}
		config := valid
		config.QRCodeURL = "/api/creation/assets/cc_" + strings.Repeat("0", 64)
		assert.ErrorIs(t, model.SaveCustomerServiceConfig(config), model.ErrCustomerServiceImage, "missing file")
		require.NoError(t, os.WriteFile(filepath.Join(assetDir, "assets", "cc_"+strings.Repeat("0", 64)), pngData.Bytes(), 0o600))
		assert.ErrorIs(t, model.SaveCustomerServiceConfig(config), model.ErrCustomerServiceImage, "a correct image under the wrong content hash must fail")
		boundary := valid
		boundary.Title, boundary.Description = strings.Repeat("😀", 32), strings.Repeat("😀", 128)
		require.NoError(t, model.SaveCustomerServiceConfig(boundary))
		require.NoError(t, model.SaveCustomerServiceConfig(valid))
	})
	t.Run("generic option and mixed writers cannot bypass verification or alter other settings", func(t *testing.T) {
		for _, key := range []string{"CustomerServiceConfig", "customerserviceconfig", "CustomerServiceConfig "} {
			for _, writer := range []func() error{
				func() error { return model.UpdateOption(key, "{}") },
				func() error {
					return model.UpdateOptionsBulk(map[string]string{key: "{}", "WeChatAccountQRCodeImageURL": "overwritten"})
				},
				func() error {
					_, err := model.UpdatePasskeyDomainOptions(map[string]string{key: "{}", "ServerAddress": "https://example.invalid"}, false, "")
					return err
				},
			} {
				var before, after []model.Option
				require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&before).Error)
				assert.Error(t, writer(), key)
				require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&after).Error)
				assert.Equal(t, before, after)
			}
		}
		if common.MainDatabaseType() == common.DatabaseTypeMySQL {
			require.NoError(t, db.Where(&model.Option{Key: "CustomerServiceConfig"}).Delete(&model.Option{}).Error)
			for _, writer := range []func(string) error{
				func(key string) error { return model.UpdateOption(key, "{}") },
				func(key string) error {
					return model.UpdateOptionsBulk(map[string]string{key: "{}", "WeChatAccountQRCodeImageURL": "overwritten"})
				},
				func(key string) error {
					_, err := model.UpdatePasskeyDomainOptions(map[string]string{key: "{}"}, false, "")
					return err
				},
			} {
				assert.Error(t, writer("CustomérServiceConfig"))
				var count int64
				require.NoError(t, db.Model(&model.Option{}).Where(&model.Option{Key: "CustomerServiceConfig"}).Count(&count).Error)
				assert.Zero(t, count, "an alias-first insert must roll back")
			}
			require.NoError(t, db.Create(&model.Option{Key: "CustomérServiceConfig", Value: "preserved pre-existing alias"}).Error)
			assert.ErrorIs(t, model.SaveCustomerServiceConfig(valid), model.ErrCustomerServiceSave)
			var row model.Option
			require.NoError(t, db.Where(&model.Option{Key: "CustomerServiceConfig"}).Take(&row).Error)
			assert.Equal(t, "preserved pre-existing alias", row.Value)
			require.NoError(t, db.Where(&model.Option{Key: row.Key}).Delete(&model.Option{}).Error)
			require.NoError(t, model.SaveCustomerServiceConfig(valid))
		}
		assert.Equal(t, valid, model.CurrentCustomerServiceConfig())
		assert.Equal(t, "https://login.example.invalid/separate-login-code.png", common.WeChatAccountQRCodeImageURL)
	})
	t.Run("database failure does not publish an uncommitted replacement", func(t *testing.T) {
		const callback = "customer-service-forced-option-failure"
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "options" {
				tx.AddError(fmt.Errorf("isolated database failure"))
			}
		}))
		candidate := valid
		candidate.Title = "must not commit"
		w := request(http.MethodPut, "/api/customer-service", credentials["admin"], encode(candidate), "application/json", "http://example.com")
		require.NoError(t, db.Callback().Create().Remove(callback))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NotContains(t, w.Body.String(), "isolated database failure")
		assert.Equal(t, valid, model.CurrentCustomerServiceConfig())
		var stored model.Option
		require.NoError(t, db.Where(&model.Option{Key: "CustomerServiceConfig"}).Take(&stored).Error)
		assert.Equal(t, encode(valid), stored.Value)
	})
}
