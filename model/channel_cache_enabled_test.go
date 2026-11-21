package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

func setupChannelCacheReadFailureTest(t *testing.T) *Channel {
	t.Helper()
	setupChannelStatusTest(t)
	oldGroups, oldChannels, oldCustom := group2model2channels, channelsIDM, channel2advancedCustomConfig
	oldAliases := taskAliasViewPtr.Load()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		group2model2channels, channelsIDM, channel2advancedCustomConfig = oldGroups, oldChannels, oldCustom
		channelSyncLock.Unlock()
		taskAliasViewPtr.Store(oldAliases)
	})
	group2model2channels, channelsIDM, channel2advancedCustomConfig = nil, nil, nil
	channel := &Channel{
		Name: "synthetic-cache-read", Type: constant.ChannelTypeAdvancedCustom,
		Key: "last-good-a\nlast-good-b", Status: common.ChannelStatusEnabled,
		Group: "default", Models: "allowed,second,blocked",
		ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling, MultiKeyPollingIndex: 1},
	}
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(kitdto.AdvancedCustomRoute{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/chat/completions"}))
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&[]Ability{
		{Group: "default", Model: "allowed", ChannelId: channel.Id, Enabled: true},
		{Group: "default", Model: "second", ChannelId: channel.Id, Enabled: true},
		{Group: "default", Model: "blocked", ChannelId: channel.Id, Enabled: false},
	}).Error)
	common.MemoryCacheEnabled = true
	InitChannelCache()
	// These new DB values must not leak into only part of a failed snapshot.
	pending := *channel
	pending.SetOtherSettings(pricingEndpointAdvancedCustomConfig(kitdto.AdvancedCustomRoute{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses"}))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{"key": "pending-key", "settings": pending.OtherSettings}).Error)
	return channel
}

func injectChannelCacheReadFailure(t *testing.T, table string, partial bool) func() {
	t.Helper()
	name := "test:cache-read-failure:" + t.Name()
	enabled := true
	inject := func(tx *gorm.DB) {
		if !enabled || tx.Statement.Table != table {
			return
		}
		if partial {
			if rows, ok := tx.Statement.Dest.(*[]*Ability); ok && len(*rows) > 0 {
				*rows = (*rows)[:1]
			}
		}
		tx.AddError(errors.New("synthetic channel-cache read failure"))
	}
	callbacks := DB.Callback().Query()
	if partial {
		require.NoError(t, callbacks.After("gorm:query").Register(name, inject))
	} else {
		require.NoError(t, callbacks.Before("gorm:query").Register(name, inject))
	}
	t.Cleanup(func() { require.NoError(t, callbacks.Remove(name)) })
	return func() { enabled = false }
}

func assertChannelCacheLastGood(t *testing.T, original *Channel) {
	t.Helper()
	cached, err := CacheGetChannel(original.Id)
	require.NoError(t, err)
	assert.Equal(t, original.Key, cached.Key, "an incomplete refresh must not replace channel credentials")
	assert.Equal(t, original.ChannelInfo.MultiKeyPollingIndex, cached.ChannelInfo.MultiKeyPollingIndex)
	for _, modelName := range []string{"allowed", "second"} {
		selected, err := GetRandomSatisfiedChannel("default", modelName, 0, []dto.ChannelFilter{{Kind: dto.FilterRequestPath, RequestPath: "/v1/chat/completions"}})
		require.NoError(t, err)
		require.NotNil(t, selected, "last-good route must remain available: %s", modelName)
		assert.Equal(t, original.Id, selected.Id)
	}
	assert.False(t, IsChannelEnabledForGroupModel("default", "blocked", original.Id))
	configs := loadPricingAdvancedCustomConfigs([]AbilityWithChannel{{Ability: Ability{ChannelId: original.Id}, ChannelType: constant.ChannelTypeAdvancedCustom}})
	require.Contains(t, configs, original.Id)
	assert.Equal(t, "/v1/chat/completions", configs[original.Id].Routes[0].IncomingPath, "pricing must retain the same completed snapshot")
}

func TestChannelCachePreservesSnapshotOnChannelReadFailure(t *testing.T) {
	channel := setupChannelCacheReadFailureTest(t)
	injectChannelCacheReadFailure(t, "channels", false)
	InitChannelCache()
	assertChannelCacheLastGood(t, channel)
}

func TestChannelCachePreservesSnapshotOnAbilityReadFailure(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "query-error"
		if partial {
			name = "partial-rows-and-error"
		}
		t.Run(name, func(t *testing.T) {
			channel := setupChannelCacheReadFailureTest(t)
			injectChannelCacheReadFailure(t, "abilities", partial)
			InitChannelCache()
			assertChannelCacheLastGood(t, channel)
		})
	}
}

func TestChannelCacheColdReadFailureRemainsUnavailable(t *testing.T) {
	channel := setupChannelCacheReadFailureTest(t)
	group2model2channels, channelsIDM, channel2advancedCustomConfig = nil, nil, nil
	injectChannelCacheReadFailure(t, "abilities", false)
	InitChannelCache()
	selected, err := GetRandomSatisfiedChannel("default", "allowed", 0, nil)
	require.NoError(t, err)
	assert.Nil(t, selected)
	_, err = CacheGetChannel(channel.Id)
	assert.Error(t, err, "a failed cold refresh must not expose a partially loaded channel")
	assert.False(t, IsChannelEnabledForGroupModel("default", "allowed", channel.Id))
}

func TestChannelCacheRecoversAfterReadFailureAndAcceptsEmptyDatabase(t *testing.T) {
	channel := setupChannelCacheReadFailureTest(t)
	stopFailure := injectChannelCacheReadFailure(t, "channels", false)
	InitChannelCache()
	assertChannelCacheLastGood(t, channel)
	stopFailure()
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ? AND model = ?", channel.Id, "allowed").Update("enabled", false).Error)
	InitChannelCache()
	assert.False(t, IsChannelEnabledForGroupModel("default", "allowed", channel.Id))
	assert.True(t, IsChannelEnabledForGroupModel("default", "second", channel.Id))
	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, "pending-key", cached.Key)
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error)
	require.NoError(t, DB.Delete(&Channel{}, channel.Id).Error)
	InitChannelCache()
	selected, err := GetRandomSatisfiedChannel("default", "second", 0, nil)
	require.NoError(t, err)
	assert.Nil(t, selected, "a successful empty read must clear obsolete routes")
	_, err = CacheGetChannel(channel.Id)
	assert.Error(t, err)
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
