package router

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Runs through the same real-database fixture as the existing media admission
// matrix. No supplier HTTP, billing, or generation occurs.
func TestMediaConcurrencyYCProviderDefaultsShareCredentialAcrossModels(t *testing.T) {
	f := newMediaConcurrencyFixture(t)
	ctx := context.Background()
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	for index, fixture := range []struct {
		kind   string
		models []string
	}{
		{model.MediaImage, []string{"ycapi-doubao-seedream-5-0-pro", "ycapi-flux-kontext"}},
		{model.MediaVideo, []string{"ycapi-grok-imagine-1.5", "ycapi-grok-imagine-video"}},
		{model.MediaAudio, []string{"ycapi-gpt-4o-mini-tts"}},
	} {
		t.Run(fixture.kind, func(t *testing.T) {
			key := "synthetic-ycapi-shared-" + fixture.kind
			for alias := range 2 {
				base := "https://ycapi.fixture"
				channel := model.Channel{Id: 960 + index*2 + alias, Type: constant.ChannelTypeTaskPlugin, Key: key, Name: "YC shared credential", Models: strings.Join(fixture.models, ","), Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
				channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
				require.NoError(t, channel.Insert())
			}
			require.Nil(t, model.GetMediaClientConcurrencyHint("ycapi", fixture.models[0], fixture.kind), "unreconciled credentials must not advertise atomic admission")
			require.NoError(t, model.RefreshMediaConcurrencyPools(ctx))
			for number := range 20 {
				task := &model.Task{TaskID: fmt.Sprintf("yc_shared_%s_%d", fixture.kind, number), Platform: "ycapi", ChannelId: 960 + index*2 + number%2, UserId: f.userID, Properties: model.Properties{OriginModelName: fixture.models[number%len(fixture.models)]}}
				require.NoError(t, model.AcquireMediaAdmission(ctx, task, key, fixture.kind))
			}
			blocked := &model.Task{TaskID: "yc_full_" + fixture.kind, Platform: "ycapi", ChannelId: 960 + index*2 + 1, UserId: f.userID, Properties: model.Properties{OriginModelName: fixture.models[0]}}
			assert.ErrorIs(t, model.AcquireMediaAdmission(ctx, blocked, key, fixture.kind), model.ErrMediaCapacity)
			hint := model.GetMediaClientConcurrencyHint("ycapi", fixture.models[0], fixture.kind)
			require.NotNil(t, hint)
			assert.Equal(t, 20, *hint, "hint is the configured ceiling, not free slots")
			snapshot, err := model.GetMediaConcurrencySnapshot(ctx)
			require.NoError(t, err)
			rowIndex := slices.IndexFunc(snapshot.Pools, func(pool model.MediaPoolSnapshot) bool {
				return slices.Contains(pool.ChannelIDs, 960+index*2) && pool.MediaKind == fixture.kind
			})
			require.NotEqual(t, -1, rowIndex)
			pool := snapshot.Pools[rowIndex]
			assert.ElementsMatch(t, []int{960 + index*2, 961 + index*2}, pool.ChannelIDs)
			assert.Equal(t, 20, pool.Occupied)
			assert.Equal(t, 20, pool.ConfiguredLimit)
			assert.Nil(t, pool.KnownUpstreamLimit, "gateway default20 is not a verified supplier cap")
			policy := snapshot.Policy
			defaults := policy.ProviderDefaults["ycapi"]
			switch fixture.kind {
			case model.MediaVideo:
				defaults.Video = 7
			case model.MediaImage:
				defaults.Image = 7
			case model.MediaAudio:
				defaults.Audio = 7
			}
			policy.ProviderDefaults["ycapi"] = defaults
			policy, err = model.SaveMediaConcurrencyPolicy(ctx, policy.Revision, policy)
			require.NoError(t, err)
			hint = model.GetMediaClientConcurrencyHint("ycapi", fixture.models[0], fixture.kind)
			require.NotNil(t, hint)
			assert.Equal(t, 7, *hint, "explicit provider default must be honored")
			policy.PoolOverrides = append(policy.PoolOverrides, model.MediaPoolOverride{PoolID: pool.PoolID, Limit: 64})
			_, err = model.SaveMediaConcurrencyPolicy(ctx, policy.Revision, policy)
			require.NoError(t, err)
			hint = model.GetMediaClientConcurrencyHint("ycapi", fixture.models[0], fixture.kind)
			require.NotNil(t, hint)
			assert.Equal(t, 64, *hint, "pool override wins over provider20, including video/audio")
			require.NoError(t, model.AcquireMediaAdmission(ctx, blocked, key, fixture.kind))
			original := map[string]any{"type": "object", "x-client-concurrency": map[string]any{"max_parallelism": 1}}
			parameters := model.WithMediaClientConcurrencyHint(original, "ycapi", fixture.models[0], fixture.kind)
			assert.Equal(t, 64, parameters["x-client-concurrency"].(map[string]any)["max_parallelism"])
			assert.Equal(t, 1, original["x-client-concurrency"].(map[string]any)["max_parallelism"], "catalog injection cannot mutate cached parameters")
			// A synthetic verified-cap fixture proves the precedence without
			// claiming this number is a real YC supplier limit.
			require.NoError(t, f.db.Model(&model.MediaPool{}).Where("id = ?", pool.PoolID).Update("known_limit", 7).Error)
			hint = model.GetMediaClientConcurrencyHint("ycapi", fixture.models[0], fixture.kind)
			require.NotNil(t, hint)
			assert.Equal(t, 7, *hint)
			snapshot, err = model.GetMediaConcurrencySnapshot(ctx)
			require.NoError(t, err)
			rowIndex = slices.IndexFunc(snapshot.Pools, func(row model.MediaPoolSnapshot) bool { return row.PoolID == pool.PoolID })
			assert.Equal(t, 21, snapshot.Pools[rowIndex].Occupied, "policy changes never clear existing work")
			assert.Zero(t, snapshot.Pools[rowIndex].AvailableSlots)
		})
	}
}

func TestMediaConcurrencyMixedProviderAliasesUseOnePool(t *testing.T) {
	newMediaConcurrencyFixture(t)
	ctx := context.Background()
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	base := "https://ycapi.fixture"
	channel := model.Channel{Id: 968, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-unused-blackrx-credential", Name: "YC alias of existing Blackrx key", Models: "ycapi-grok-imagine-1.5", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
	channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
	require.NoError(t, channel.Insert())
	require.NoError(t, model.RefreshMediaConcurrencyPools(ctx))
	hint := model.GetMediaClientConcurrencyHint("ycapi", channel.Models, model.MediaVideo)
	require.NotNil(t, hint)
	assert.Equal(t, 4, *hint, "the YC route must not multiply or raise the shared key's other supplier default")
	snapshot, err := model.GetMediaConcurrencySnapshot(ctx)
	require.NoError(t, err)
	var matches []model.MediaPoolSnapshot
	for _, pool := range snapshot.Pools {
		if slices.Contains(pool.ChannelIDs, channel.Id) {
			matches = append(matches, pool)
		}
	}
	require.Len(t, matches, 1)
	assert.Contains(t, matches[0].ChannelIDs, 871)
	assert.Nil(t, matches[0].KnownUpstreamLimit)
	policy := snapshot.Policy
	policy.PoolOverrides = []model.MediaPoolOverride{{PoolID: matches[0].PoolID, Limit: 20}}
	_, err = model.SaveMediaConcurrencyPolicy(ctx, policy.Revision, policy)
	require.NoError(t, err)
	hint = model.GetMediaClientConcurrencyHint("ycapi", channel.Models, model.MediaVideo)
	require.NotNil(t, hint)
	assert.Equal(t, 20, *hint, "explicit shared pool override wins over both provider defaults")
}
