package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const mediaGenerationPrivateContextKey = "host_media_generation_v2_quote"

type mediaGenerationContext struct {
	Quote  model.MediaGenerationQuote
	Frozen MediaGenerationFrozen
}

func AttachMediaGenerationQuote(c *gin.Context, q *model.MediaGenerationQuote) error {
	if c == nil || q == nil {
		return errors.New("quote unavailable")
	}
	a, err := MediaGenerationActorFromContext(c)
	if err != nil || a.UserID != q.UserID || a.TokenID != q.TokenID || a.OwnerDigest != q.OwnerDigest {
		return errors.New("quote unavailable")
	}
	var frozen MediaGenerationFrozen
	if common.UnmarshalJsonStr(string(q.UsageFactsJSON), &frozen) != nil || frozen.Request.Model != q.Model || MediaGenerationRequestDigest(frozen.Request) != q.RequestSHA256 {
		return errors.New("quote unavailable")
	}
	route, err := MediaGenerationRouteFromContext(c)
	if err != nil || route.ChannelID != q.ChannelID || route.ConfigHash != q.ChannelConfigHash || route.CredentialDigest != q.CredentialDigest {
		return errors.New("quote_changed")
	}
	record, _, err := mediaGenerationRecord(q.Model)
	if err != nil || model.ModelExecutionConfigurationVersion(q.Model, record) != q.ModelConfigVersion {
		return errors.New("model_version_changed")
	}
	plugin, ok := jsplugin.DefaultRegistry.Get(q.PluginKey)
	if !ok || plugin.Meta.Version != q.PluginVersion || plugin.SourceHash != q.PluginSourceHash {
		return errors.New("quote_changed")
	}
	c.Set(mediaGenerationPrivateContextKey, mediaGenerationContext{*q, frozen})
	c.Request = c.Request.WithContext(model.WithMediaGenerationQuoteBinding(c.Request.Context(), MediaGenerationBinding(q)))
	return nil
}

// MediaGenerationPluginContext can only be constructed from the private Go
// context after authentication and database quote verification. Client JSON
// named mediaGenerationV2/nativeRequest is never consulted.
func MediaGenerationPluginContext(c *gin.Context, info *relaycommon.RelayInfo) (map[string]any, error) {
	if c == nil {
		return nil, nil
	}
	raw, ok := c.Get(mediaGenerationPrivateContextKey)
	if !ok {
		return nil, nil
	}
	private, ok := raw.(mediaGenerationContext)
	if !ok || info == nil || info.PublicTaskID == "" || info.OriginModelName != private.Quote.Model || info.ChannelId != private.Quote.ChannelID || common.GenerateHMAC("media-v2-key:"+info.ApiKey) != private.Quote.CredentialDigest {
		return nil, errors.New("invalid media quote context")
	}
	var params map[string]any
	if common.UnmarshalJsonStr(string(private.Quote.NativeParamsJSON), &params) != nil {
		return nil, errors.New("invalid media quote parameters")
	}
	native, err := ycapi.BuildMediaRuntimeRequest(private.Quote.Model, params, private.Quote.SupplierLane, info.PublicTaskID, private.Frozen.Request.Intent)
	if err != nil {
		return nil, err
	}
	return map[string]any{"nativeRequest": native, "usageFacts": private.Frozen.Facts, "kind": private.Quote.Kind, "retailCny": private.Quote.RetailCNY}, nil
}
func ValidateMediaGenerationQuotedPrice(c *gin.Context, info *relaycommon.RelayInfo) error {
	raw, ok := c.Get(mediaGenerationPrivateContextKey)
	if !ok {
		return nil
	}
	private, ok := raw.(mediaGenerationContext)
	if !ok || info == nil || info.PriceData.Quota != int(private.Quote.ReserveQuota) || info.ChannelId != private.Quote.ChannelID || info.OriginModelName != private.Quote.Model || common.GenerateHMAC("media-v2-key:"+info.ApiKey) != private.Quote.CredentialDigest {
		return errors.New("quote_changed")
	}
	return nil
}
