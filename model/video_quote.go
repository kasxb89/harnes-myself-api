package model

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

// GetVideoQuoteAbilities reads only the caller's relevant routing set. A
// conservative row cap also bounds auto-group/provider quote evaluation.
func GetVideoQuoteAbilities(ctx context.Context, groups, models []string) ([]Ability, error) {
	if len(groups) == 0 || len(models) == 0 {
		return []Ability{}, nil
	}
	var abilities []Ability
	err := DB.WithContext(ctx).Select("group", "model", "channel_id", "enabled").
		Where(commonGroupCol+" IN ? AND model IN ? AND enabled = ?", groups, models, true).
		Limit(65).Find(&abilities).Error
	if err != nil {
		return nil, err
	}
	if len(abilities) > 64 {
		return nil, fmt.Errorf("too many video quote routes")
	}
	return abilities, nil
}

// GetVideoQuoteChannels copies only routing fields under the cache lock. It
// neither reads channel credentials nor selects/advances a multi-key cursor.
func GetVideoQuoteChannels(ids []int) ([]Channel, error) {
	if !common.MemoryCacheEnabled {
		var channels []Channel
		err := DB.Select("id", "type", "status", "models", "group", "model_mapping", "setting", "settings", "param_override", "header_override").Where("id IN ?", ids).Find(&channels).Error
		return channels, err
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	channels := make([]Channel, 0, len(ids))
	for _, id := range ids {
		channel := channelsIDM[id]
		if channel == nil {
			continue
		}
		copy := Channel{Id: channel.Id, Type: channel.Type, Status: channel.Status,
			Models: channel.Models, Group: channel.Group, OtherSettings: channel.OtherSettings}
		for _, field := range []struct {
			from *string
			to   **string
		}{
			{channel.ModelMapping, &copy.ModelMapping}, {channel.Setting, &copy.Setting},
			{channel.ParamOverride, &copy.ParamOverride}, {channel.HeaderOverride, &copy.HeaderOverride},
		} {
			if field.from != nil {
				value := *field.from
				*field.to = &value
			}
		}
		channels = append(channels, copy)
	}
	return channels, nil
}
