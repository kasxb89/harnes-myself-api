package model

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestChannelCacheRespectsExactAbilityEnabledAcrossRebuilds(t *testing.T) {
	setupChannelStatusTest(t)
	oldGroups, oldChannels, oldCustom := group2model2channels, channelsIDM, channel2advancedCustomConfig
	t.Cleanup(func() {
		group2model2channels, channelsIDM, channel2advancedCustomConfig = oldGroups, oldChannels, oldCustom
	})
	channel := Channel{Name: "synthetic-ability-cache", Key: "synthetic-key", Status: common.ChannelStatusEnabled, Group: "default,product", Models: "allowed,blocked"}
	require.NoError(t, DB.Create(&channel).Error)
	abilities := []Ability{{Group: "default", Model: "allowed", ChannelId: channel.Id, Enabled: true}, {Group: "default", Model: "blocked", ChannelId: channel.Id, Enabled: false}, {Group: "product", Model: "allowed", ChannelId: channel.Id, Enabled: false}, {Group: "product", Model: "blocked", ChannelId: channel.Id, Enabled: true}}
	require.NoError(t, DB.Create(&abilities).Error)
	common.MemoryCacheEnabled = true
	for range 2 {
		InitChannelCache()
		require.False(t, IsChannelEnabledForGroupModel("default", "blocked", channel.Id), "cache rebuild must not revive a disabled ability")
		require.False(t, IsChannelEnabledForGroupModel("product", "allowed", channel.Id))
		require.True(t, IsChannelEnabledForGroupModel("default", "allowed", channel.Id))
		require.True(t, IsChannelEnabledForGroupModel("product", "blocked", channel.Id))
		got, err := GetRandomSatisfiedChannel("default", "blocked", 0, nil)
		require.NoError(t, err)
		require.Nil(t, got)
	}
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", false).Error)
	InitChannelCache()
	require.False(t, IsChannelEnabledForGroupModel("default", "allowed", channel.Id))
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ? AND model = ?", channel.Id, "allowed").Update("enabled", true).Error)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
	InitChannelCache()
	require.False(t, IsChannelEnabledForGroupModel("default", "allowed", channel.Id), "even enabled abilities cannot grant a disabled channel")
}

func TestRestoreBillingTokenQuotaRequiresOriginalUserAndAffectedToken(t *testing.T) {
	truncateTables(t)
	token := &Token{UserId: 918022, Key: "synthetic-restoration-token", RemainQuota: 50, UsedQuota: 50}
	require.NoError(t, DB.Create(token).Error)
	require.True(t, errors.Is(RestoreBillingTokenQuota(918023, token.Id, token.Key, 50), gorm.ErrRecordNotFound))
	require.True(t, errors.Is(RestoreBillingTokenQuota(918022, token.Id+1, token.Key, 50), gorm.ErrRecordNotFound))
	require.Error(t, RestoreBillingTokenQuota(918022, token.Id, "", 50))
	require.ErrorIs(t, RestoreBillingTokenQuota(918022, token.Id, "different-original-key", 50), gorm.ErrRecordNotFound)
	require.ErrorIs(t, RestoreBillingTokenQuota(918022, token.Id, "SYNTHETIC-RESTORATION-TOKEN", 50), gorm.ErrRecordNotFound)
	require.NoError(t, DB.First(token, token.Id).Error)
	require.Equal(t, 50, token.RemainQuota, "wrong/empty original keys cannot credit the token")
	require.Equal(t, 50, token.UsedQuota)
	require.NoError(t, RestoreBillingTokenQuota(918022, token.Id, token.Key, 50))
	require.NoError(t, DB.First(token, token.Id).Error)
	require.Equal(t, 100, token.RemainQuota)
	require.Zero(t, token.UsedQuota)
	require.NoError(t, IncreaseTokenQuota(0, "", 0), "legacy zero adjustment semantics stay unchanged")
}
