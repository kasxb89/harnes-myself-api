package controller

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

func TestConfiguredModelPricebookDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	db, _ := newAuditTestDatabase(t, kind, os.Getenv("TEST_"+strings.ToUpper(kind)+"_DSN"))
	typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
	useLogExportDatabases(t, db, db, typ, typ)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Option{}, &model.Model{}, &model.Vendor{}, &model.User{}, &model.UserSession{}))
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	base := "https://private-user:secret-url-password@metaso.cn/api/minimax?api_key=secret-query"
	channels := []model.Channel{
		{Id: 16, Name: "Metaso H3", Type: 35, Status: 1, Models: "MiniMax-H3,MiniMax-H3", Key: "secret-channel-key", BaseURL: &base},
		{Id: 17, Name: "Disabled old channel", Type: 1, Status: 2, Models: "retired-old-model", Key: "secret-old-key"},
		{Id: 18, Name: "New YCAPI channel", Type: 1, Status: 1, Models: "ycapi-minimax-h3,ycapi-removed-model", Key: "secret-new-key"},
	}
	require.NoError(t, db.Create(&channels).Error)
	require.NoError(t, db.Create(&[]model.Model{{ModelName: "registry-only-model", Status: 1}, {ModelName: "disabled-metadata-model", Status: 2}}).Error)
	require.NoError(t, db.Create(&[]model.Ability{{ChannelId: 16, Model: "MiniMax-H3", Group: "default", Enabled: true}, {ChannelId: 16, Model: "MiniMax-H3", Group: "product", Enabled: true}, {ChannelId: 17, Model: "retired-old-model", Group: "default", Enabled: false}}).Error)
	price := 0.0897396
	expired := time.Now().Unix() - 100
	future := time.Now().Unix() + 86400
	_, err := model.SaveModelProfitCostRules(context.Background(), model.ModelProfitCostRules{SchemaVersion: 1, Rules: []model.ModelProfitCostRule{
		{ChannelID: 16, ModelName: "MiniMax-H3", EffectiveFrom: 1, Kind: "second", UnitCostCNY: &price, Source: "public reference estimate, not an account invoice"},
		{ChannelID: 16, ModelName: "MiniMax-H3", EffectiveFrom: 2, EffectiveUntil: &expired, Kind: "second", UnitCostCNY: &price, Source: "expired"},
		{ChannelID: 16, ModelName: "MiniMax-H3", EffectiveFrom: future, Kind: "second", UnitCostCNY: &price, Source: "future"},
	}})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Option{Key: "ModelRatio", Value: `{"retired-old-model":1}`}).Error)
	var beforeChannels []model.Channel
	require.NoError(t, db.Order("id").Find(&beforeChannels).Error)
	var before []model.Option
	require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&before).Error)
	users := []model.User{{Username: "pb-user", AffCode: "pb-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}, {Username: "pb-admin", AffCode: "pb-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}, {Username: "pb-root", AffCode: "pb-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}}
	require.NoError(t, db.Create(&users).Error)
	router := gin.New()
	router.GET("/api/model-pricebook", middleware.RootAuth(), GetConfiguredModelPricebook)
	assert.Equal(t, http.StatusUnauthorized, accessTokenRequest(router, http.MethodGet, "/api/model-pricebook", "", "", "").Code)
	for _, user := range users {
		_, jwt := createAccessTokenTestSession(t, user.Id, "pricebook-session-"+user.Username)
		response := accessTokenRequest(router, http.MethodGet, "/api/model-pricebook", jwt, "", "")
		if user.Role < common.RoleRootUser {
			assert.Equal(t, http.StatusForbidden, response.Code)
			continue
		}
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
		var envelope struct {
			Success bool `json:"success"`
			Data    struct {
				Rows []configuredPricebookRow `json:"rows"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		assert.Contains(t, response.Body.String(), "registry-only-model")
		assert.Contains(t, response.Body.String(), "ycapi-minimax-h3")
		require.Len(t, envelope.Data.Rows, 104)
		row := envelope.Data.Rows[0]
		assert.Equal(t, "MiniMax-H3", row.ModelName)
		assert.Equal(t, "metaso.cn", row.UpstreamHost)
		assert.Equal(t, []string{"default", "product"}, row.EnabledGroups)
		require.NotNil(t, row.CostRule)
		assert.Equal(t, "public reference estimate, not an account invoice", row.CostRule.Source)
		assert.Equal(t, price, *row.CostRule.UnitCostCNY)
		assert.Nil(t, envelope.Data.Rows[1].CostRule)
		assert.Empty(t, envelope.Data.Rows[1].EnabledGroups)
		assert.Equal(t, "enabled", row.State)
		require.NotNil(t, row.ApprovedQuote)
		assert.Equal(t, "public_reference_estimate", row.ApprovedQuote.Basis)
		assert.Equal(t, 0.0897396, row.ApprovedQuote.Prices[0].ProcurementCNY)
		assert.Len(t, row.ApprovedQuote.Prices, 16)
		assert.Equal(t, 0.1794792, row.ApprovedQuote.Prices[2].ProcurementCNY)
		require.NotNil(t, row.ApprovedQuote.Prices[2].RetailCNY)
		assert.Equal(t, 0.60000014, *row.ApprovedQuote.Prices[2].RetailCNY)
		assert.Equal(t, "disabled", envelope.Data.Rows[1].State)
		require.NotNil(t, envelope.Data.Rows[1].Pricing)
		assert.Equal(t, 1.0, envelope.Data.Rows[1].Pricing.ModelRatio)
		approved := 0
		codebloomCandidates := 0
		for _, entry := range envelope.Data.Rows {
			if entry.ApprovedQuote != nil {
				approved++
			}
			if entry.ChannelID == 0 && entry.Origin == "candidate" {
				assert.Equal(t, "not_configured", entry.State)
			}
			if entry.ModelName == "ycapi-removed-model" {
				assert.Equal(t, "excluded", entry.State)
			}
			if strings.HasPrefix(entry.ModelName, "codebloom-") {
				codebloomCandidates++
				assert.Equal(t, "not_configured", entry.State)
				assert.Equal(t, "codebloom.club", entry.UpstreamHost)
				assert.Nil(t, entry.Pricing, "unapproved retail must not use runtime fallback prices")
				assert.Nil(t, entry.CostRule, "account-credit quote is not an active cash-cost rule")
				require.NotNil(t, entry.ApprovedQuote)
				assert.Equal(t, "account_credit_snapshot", entry.ApprovedQuote.Basis)
				assert.Equal(t, "CNY_ACCOUNT_CREDIT", entry.ApprovedQuote.Currency)
				require.NotEmpty(t, entry.ApprovedQuote.Prices)
				for _, price := range entry.ApprovedQuote.Prices {
					require.NotNil(t, price.RetailCNY, "the approved markup must cover every cost tier")
					assert.Greater(t, *price.RetailCNY, price.ProcurementCNY)
				}
			}
		}
		assert.Equal(t, 37, codebloomCandidates)
		assert.Equal(t, 100, approved)
		for _, secret := range []string{"secret-channel-key", "secret-url-password", "secret-query", "secret-old-key", "secret-new-key"} {
			assert.NotContains(t, response.Body.String(), secret)
		}
	}
	var after []model.Option
	require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&after).Error)
	assert.Equal(t, before, after, "reading prices must not change options or cost history")
	var afterChannels []model.Channel
	require.NoError(t, db.Order("id").Find(&afterChannels).Error)
	assert.Equal(t, beforeChannels, afterChannels, "reading prices must not change channel state or credentials")
}
