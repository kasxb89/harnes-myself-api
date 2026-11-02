package controller

import (
	"maps"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	service.RefreshBlackrxCatalog(c.Request.Context())
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	var group string
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	usableGroup = service.GetUserUsableGroups(group)
	pricing = filterPricingByUsableGroups(pricing, usableGroup)
	groups := make([]string, 0, len(usableGroup))
	for usable := range usableGroup {
		groups = append(groups, usable)
	}
	names := make([]string, 0, len(pricing))
	for _, item := range pricing {
		names = append(names, item.ModelName)
	}
	available := make(map[string]bool, len(names))
	for _, name := range service.FilterUnavailableCatalogModels(names, groups) {
		available[name] = true
	}
	filteredPricing := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if available[item.ModelName] {
			filteredPricing = append(filteredPricing, item)
		}
	}
	pricing = service.NarrowBlackrxPricingParameters(filteredPricing, groups)
	pricing = service.NarrowH3PricingParameters(c.Request.Context(), pricing, groups)
	capacityGroups := service.CapacityAccountGroups(c)
	if len(capacityGroups) == 1 && capacityGroups[0] == "" {
		// Anonymous pricing has no selected request group. Keep its existing
		// conservative view across the publicly usable groups.
		capacityGroups = groups
	}
	var err error
	pricing, err = service.ProjectPricingModelCapacities(pricing, capacityGroups)
	if err != nil {
		c.JSON(503, gin.H{"error": gin.H{"code": "model_capacity_unavailable", "message": "Model capacity configuration is unavailable"}})
		return
	}
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}
	for i := range pricing {
		switch pricing[i].ModelName {
		case "ys-grok-imagine-image-2.0", "ys-grok-imagine-image", "ys-gemini-3.1-flash-image-preview":
			if len(pricing[i].RetailPriceRows) != 1 {
				continue
			}
			// These qualified routes enforce n=1 and retain one image. Publish
			// the equivalent per-image retail display without changing the
			// source's per-request account cost or cached pricing rows.
			pricing[i].RetailPriceRows = []map[string]any{{
				"params":     map[string]any{"mode": "text-to-image", "count": 1},
				"retail_cny": pricing[i].RetailPriceRows[0]["retail_cny"],
				"unit":       "CNY / image",
			}}
		}
	}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            model.GetVendors(),
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": model.GetSupportedEndpointMap(),
		"auto_groups":        service.GetUserAutoGroup(group),
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccessT(c, "Model ratios reset", nil)
}
