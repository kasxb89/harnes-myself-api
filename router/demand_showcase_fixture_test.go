package router

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// This fixture only owns fresh content-case databases. It never widens the old
// demand fixture guard or uses a release qualification/manual database.
func newShowcaseFixture(t *testing.T) *demandFixture {
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
	if dsn := os.Getenv("SHOWCASE_TEST_POSTGRES_DSN"); dsn != "" {
		parsed, e := url.Parse(dsn)
		require.NoError(t, e)
		require.Equal(t, "127.0.0.1:54063", parsed.Host)
		require.Regexp(t, `^/test_content_cases_[a-z0-9_]{8,64}$`, parsed.Path)
		dialect = postgres.Open(dsn)
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	} else if dsn := os.Getenv("SHOWCASE_TEST_MYSQL_DSN"); dsn != "" {
		require.True(t, regexp.MustCompile(`^[^@]+@tcp\(127\.0\.0\.1:54064\)/test_content_cases_[a-z0-9_]{8,64}(?:\?.*)?$`).MatchString(dsn), "Dedicated local MySQL content-case database required")
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
	if os.Getenv("SHOWCASE_TEST_POSTGRES_DSN") != "" || os.Getenv("SHOWCASE_TEST_MYSQL_DSN") != "" {
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
	f.engine.Use(apiResponseCompression())
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
