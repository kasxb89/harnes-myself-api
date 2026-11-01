package controller

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelpricebook"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type configuredPricebookRow struct {
	ApprovedQuote    *modelpricebook.Quote      `json:"approved_quote"`
	State            string                     `json:"state"`
	Origin           string                     `json:"origin"`
	ConfiguredGroups []string                   `json:"configured_groups"`
	Expressions      map[string]string          `json:"expressions"`
	ModelName        string                     `json:"model_name"`
	ChannelID        int                        `json:"channel_id"`
	ChannelName      string                     `json:"channel_name"`
	ChannelType      int                        `json:"channel_type"`
	UpstreamHost     string                     `json:"upstream_host"`
	ChannelStatus    int                        `json:"channel_status"`
	EnabledGroups    []string                   `json:"enabled_groups"`
	Pricing          *model.Pricing             `json:"pricing"`
	CostRule         *model.ModelProfitCostRule `json:"cost_rule"`
}

// Root-only, read-only view of existing channel configuration. The approved
// YCAPI candidate snapshot remains separate and does not replace old models.
func GetConfiguredModelPricebook(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	var channels []struct {
		ID      int
		Name    string
		Type    int
		Status  int
		Models  string
		BaseURL string `gorm:"column:base_url"`
	}
	var abilities []model.Ability
	db := model.DB.WithContext(c.Request.Context())
	if err := db.Model(&model.Channel{}).Select([]string{"id", "name", "type", "status", "models", "base_url"}).Order("id ASC").Find(&channels).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to load configured model prices"))
		return
	}
	if err := db.Select([]string{"channel_id", "model", "group", "enabled"}).Find(&abilities).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to load configured model prices"))
		return
	}
	costs, err := model.GetModelProfitCostRules(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to load configured model prices"))
		return
	}

	type identity struct {
		channel int
		name    string
	}
	groups := make(map[identity][]string)
	allGroups := make(map[identity][]string)
	for _, ability := range abilities {
		key := identity{ability.ChannelId, ability.Model}
		if !slices.Contains(allGroups[key], ability.Group) {
			allGroups[key] = append(allGroups[key], ability.Group)
		}
		if ability.Enabled {
			key := identity{ability.ChannelId, ability.Model}
			if !slices.Contains(groups[key], ability.Group) {
				groups[key] = append(groups[key], ability.Group)
			}
		}
	}
	now := time.Now().Unix()
	currentCosts := make(map[identity]*model.ModelProfitCostRule)
	for i := range costs.Rules {
		rule := &costs.Rules[i]
		if rule.EffectiveFrom > now || (rule.EffectiveUntil != nil && *rule.EffectiveUntil <= now) {
			continue
		}
		key := identity{rule.ChannelID, rule.ModelName}
		if previous := currentCosts[key]; previous == nil || previous.EffectiveFrom < rule.EffectiveFrom {
			currentCosts[key] = rule
		}
	}
	rows := make([]configuredPricebookRow, 0)
	for _, channel := range channels {
		host := ""
		base := channel.BaseURL
		if base == "" {
			base = constant.GetChannelBaseURL(channel.Type)
		}
		if parsed, err := url.Parse(base); err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
			host = strings.ToLower(parsed.Hostname())
		}
		seen := make(map[string]bool)
		for part := range strings.SplitSeq(channel.Models, ",") {
			name := strings.TrimSpace(part)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			key := identity{channel.ID, name}
			enabled := groups[key]
			if enabled == nil {
				enabled = []string{}
			}
			slices.Sort(enabled)
			state := "disabled"
			if channel.Status == common.ChannelStatusEnabled && len(enabled) > 0 {
				state = "enabled"
			}
			if strings.HasPrefix(name, "ycapi-") {
				if _, ok := ycapi.Lookup(name); !ok {
					state = "excluded"
				}
			}
			configured := allGroups[key]
			if configured == nil {
				configured = []string{}
			}
			slices.Sort(configured)
			rows = append(rows, configuredPricebookRow{State: state, Origin: "channel", ConfiguredGroups: configured, ModelName: name, ChannelID: channel.ID, ChannelName: channel.Name, ChannelType: channel.Type, UpstreamHost: host, ChannelStatus: channel.Status, EnabledGroups: enabled, CostRule: currentCosts[key]})
		}
	}
	// Include registered models without channels and approved candidates not yet
	// configured. A disabled channel never vanishes from this read-only inventory.
	known := make(map[string]bool)
	for _, row := range rows {
		known[row.ModelName] = true
	}
	var metadata []model.Model
	if err := db.Select([]string{"model_name"}).Order("model_name ASC").Find(&metadata).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to load configured model prices"))
		return
	}
	for _, meta := range metadata {
		name := strings.TrimSpace(meta.ModelName)
		if name == "" || known[name] {
			continue
		}
		known[name] = true
		state := "not_configured"
		if strings.HasPrefix(name, "ycapi-") {
			if _, ok := ycapi.Lookup(name); !ok {
				state = "excluded"
			}
		}
		rows = append(rows, configuredPricebookRow{State: state, Origin: "metadata", ModelName: name, EnabledGroups: []string{}, ConfiguredGroups: []string{}})
	}
	approved, err := ycapi.Models()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to load configured model prices"))
		return
	}
	for _, candidate := range approved {
		if known[candidate.ID] {
			continue
		}
		known[candidate.ID] = true
		rows = append(rows, configuredPricebookRow{State: "not_configured", Origin: "candidate", ModelName: candidate.ID, EnabledGroups: []string{}, ConfiguredGroups: []string{}})
	}
	quotes, err := modelpricebook.Snapshot()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to load configured model prices"))
		return
	}
	quoteByName := make(map[string]*modelpricebook.Quote, len(quotes))
	for i := range quotes {
		quote := &quotes[i]
		quoteByName[quote.ModelName] = quote
		if known[quote.ModelName] {
			continue
		}
		known[quote.ModelName] = true
		state := "not_configured"
		if quote.Provider == "YCAPI" {
			if _, active := ycapi.Lookup(quote.ModelName); !active {
				state = "excluded"
			}
		}
		host := ""
		if quote.Provider == "Metaso" {
			host = "metaso.cn"
		} else if quote.Provider == "CodeBloom" {
			host = "codebloom.club"
		}
		rows = append(rows, configuredPricebookRow{State: state, Origin: "quote_history", ModelName: quote.ModelName, UpstreamHost: host, EnabledGroups: []string{}, ConfiguredGroups: []string{}})
	}
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	slices.Sort(names)
	snapshot, err := model.GetModelPricingSnapshot(names)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Failed to load configured model prices"))
		return
	}
	entries := make(map[string]model.ModelPricingEntry, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries[entry.ModelName] = entry
	}
	for i := range rows {
		row := &rows[i]
		entry := entries[row.ModelName]
		quote := quoteByName[row.ModelName]
		if quote != nil && (quote.Provider != "Metaso" || row.ChannelID == 0 || row.UpstreamHost == "metaso.cn") && (quote.Provider != "CodeBloom" || row.ChannelID == 0 || row.UpstreamHost == "codebloom.club") {
			row.ApprovedQuote = quote
		}
		row.Expressions = make(map[string]string)
		if expression, ok := entry.Effective["billing_setting.billing_expr"].(string); ok && expression != "" {
			row.Expressions["model"] = expression
		}
		for _, variant := range entry.PluginVariants {
			expression := variant.Effective
			if expression == "" {
				expression = variant.Configured
			}
			if expression != "" {
				row.Expressions[variant.PluginKey] = expression
			}
		}
		// Planned prices stay in the approved table; runtime defaults must not be
		// presented as already configured prices for a new, unrouted candidate.
		if row.ChannelID > 0 || (row.Origin == "metadata" && len(entry.Configured) > 0) {
			row.Pricing = pricebookConfiguredPricing(entry, row.ConfiguredGroups)
		}
	}
	common.ApiSuccess(c, gin.H{"schema_version": 1, "generated_at": now, "usd_exchange_rate": operation_setting.USDExchangeRate, "group_ratios": ratio_setting.GetGroupRatioCopy(), "rows": rows})
}

// Adapt the existing effective pricing snapshot for display even when every
// channel is disabled. No quota calculation, price mutation or route activation.
func pricebookConfiguredPricing(entry model.ModelPricingEntry, groups []string) *model.Pricing {
	p := &model.Pricing{ModelName: entry.ModelName, EnableGroup: groups, BillingUsageSchema: entry.UsageSchema}
	effective := entry.Effective
	known := false
	if price, ok := effective["ModelPrice"].(float64); ok {
		p.QuotaType = 1
		p.ModelPrice = price
		known = true
	} else if ratio, ok := effective["ModelRatio"].(float64); ok {
		p.ModelRatio = ratio
		known = true
	}
	p.CompletionRatio, _ = effective["CompletionRatio"].(float64)
	for key, target := range map[string]**float64{"CacheRatio": &p.CacheRatio, "CreateCacheRatio": &p.CreateCacheRatio, "ImageRatio": &p.ImageRatio, "AudioRatio": &p.AudioRatio, "AudioCompletionRatio": &p.AudioCompletionRatio} {
		if value, ok := effective[key].(float64); ok {
			*target = &value
		}
	}
	if effective["billing_setting.billing_mode"] == billing_setting.BillingModeTieredExpr {
		p.BillingExpr, _ = effective["billing_setting.billing_expr"].(string)
		if strings.TrimSpace(p.BillingExpr) != "" {
			p.BillingMode = billing_setting.BillingModeTieredExpr
			known = true
		}
	}
	for _, variant := range entry.PluginVariants {
		if variant.Stale || !variant.Compatible || strings.TrimSpace(variant.Effective) == "" {
			continue
		}
		p.BillingPluginVariants = append(p.BillingPluginVariants, model.PricingPluginVariant{PluginKey: variant.PluginKey, PluginName: variant.PluginName, Icon: variant.Icon, BillingMode: billing_setting.BillingModeTieredExpr, BillingExpr: variant.Effective, BillingUsageSchema: variant.UsageSchema, BillingUsageExamples: variant.UsageExamples})
		known = true
	}
	if len(entry.PluginVariants) > 0 && len(p.BillingPluginVariants) == 0 && p.BillingExpr == "" {
		return nil
	}
	if !known {
		return nil
	}
	return p
}
