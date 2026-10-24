package service

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

const taskClientSourceContextKey = "task_client_source_capture"

// CaptureTaskClientSource consumes the diagnostic declaration before any
// outbound task hook runs. Only a single exact value on creation is retained.
func CaptureTaskClientSource(ctx *gin.Context) {
	if ctx == nil || ctx.Request == nil {
		return
	}
	_, captured := ctx.Get(taskClientSourceContextKey)
	count, valid := 0, true
	for name, values := range ctx.Request.Header {
		if !strings.EqualFold(name, "X-Yunshu-Client") {
			continue
		}
		for _, value := range values {
			count++
			valid = valid && value == "desktop"
		}
		delete(ctx.Request.Header, name)
	}
	if captured {
		return
	}
	source := ""
	if ctx.Request.Method == http.MethodPost && count == 1 && valid {
		source = "desktop"
	}
	ctx.Set(taskClientSourceContextKey, source)
}

// TaskExecutionSnapshotFromContext captures immutable request and plugin
// provenance at submission time. It never copies plugin source or payloads.
func TaskExecutionSnapshotFromContext(ctx *gin.Context) *model.TaskExecutionSnapshot {
	if ctx == nil {
		return nil
	}
	snapshot := &model.TaskExecutionSnapshot{
		RequestID: ctx.GetString(common.RequestIdKey),
	}
	if ctx.Request != nil && ctx.Request.Method == http.MethodPost && ctx.GetString(taskClientSourceContextKey) == "desktop" {
		snapshot.ClientSource = "desktop"
	}
	if ctx.Request != nil && ctx.Request.URL != nil {
		snapshot.RequestPath = ctx.Request.URL.Path
	}

	pinnedValue, exists := ctx.Get(pluginruntime.ContextKeyPinnedPlugin)
	if exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedPlugin)
		if ok && pinned.Plugin != nil {
			generation := uint64(0)
			if pinned.Generation != nil {
				generation = pinned.Generation.Number
			}
			meta := pinned.Plugin.Meta
			snapshot.TaskPlugin = &model.TaskPluginSnapshot{
				SourceHash: pinned.Plugin.SourceHash,
				Key:        meta.Key,
				Name:       meta.Name,
				Version:    meta.Version,
				Author: &model.TaskPluginAuthorSnapshot{
					Name: meta.Author.Name,
					URL:  meta.Author.URL,
				},
				APIVersion: meta.APIVersion,
				Generation: generation,
			}
		}
	}

	if snapshot.RequestID == "" && snapshot.RequestPath == "" && snapshot.TaskPlugin == nil && snapshot.ClientSource == "" {
		return nil
	}
	return snapshot
}

// AppendTaskPluginAuditInfo writes role-separated, credential-free plugin
// provenance into a usage log.
func AppendTaskPluginAuditInfo(other *model.LogOther, snapshot *model.TaskPluginSnapshot) {
	if other == nil || snapshot == nil || snapshot.Key == "" {
		return
	}
	taskPlugin := map[string]any{
		"key":     snapshot.Key,
		"name":    snapshot.Name,
		"version": snapshot.Version,
	}
	if snapshot.Author != nil && snapshot.Author.Name != "" {
		author := map[string]any{"name": snapshot.Author.Name}
		if snapshot.Author.URL != "" {
			author["url"] = snapshot.Author.URL
		}
		taskPlugin["author"] = author
	}
	other.SetAdmin("task_plugin", taskPlugin)
	other.SetRoot("task_plugin", map[string]any{
		"key":         snapshot.Key,
		"version":     snapshot.Version,
		"api_version": snapshot.APIVersion,
		"generation":  snapshot.Generation,
	})
}

// AppendTaskPluginContextAuditInfo is used before a task row exists, such as
// an upstream submission error log.
func AppendTaskPluginContextAuditInfo(ctx *gin.Context, other *model.LogOther) {
	execution := TaskExecutionSnapshotFromContext(ctx)
	if execution == nil {
		return
	}
	AppendTaskPluginAuditInfo(other, execution.TaskPlugin)
}
