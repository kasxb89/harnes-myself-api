package logger

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
)

// FormatQuota 是日志与提示里唯一把配额转成货币的地方，两位小数是它对用户的承诺。
func TestFormatQuotaMoneyDisplay(t *testing.T) {
	originalType := operation_setting.GetGeneralSetting().QuotaDisplayType
	originalRate := operation_setting.USDExchangeRate
	originalUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalType
		operation_setting.USDExchangeRate = originalRate
		common.QuotaPerUnit = originalUnit
	})
	operation_setting.USDExchangeRate = 7.3
	common.QuotaPerUnit = 500000

	cases := []struct {
		name    string
		display string
		quota   int
		want    string
	}{
		// 1 元按 500000/7.3 入账取整成 68493，换算回来是 0.99999778：
		// 六位小数会显示成 ¥0.999998，两位小数才是用户付的那个 ¥1.00。
		{"整元入账不露出小数尾巴", operation_setting.QuotaDisplayTypeCNY, 68493, "¥1.00"},
		{"普通金额两位小数", operation_setting.QuotaDisplayTypeCNY, 500000, "¥7.30"},
		{"非零小额不被舍成 0.00", operation_setting.QuotaDisplayTypeCNY, 1, "¥0.000015"},
		{"零", operation_setting.QuotaDisplayTypeCNY, 0, "¥0.00"},
		{"代币口径仍然是整数", operation_setting.QuotaDisplayTypeTokens, 68493, "68493"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = tc.display
			assert.Equal(t, tc.want, FormatQuota(tc.quota))
		})
	}
}
