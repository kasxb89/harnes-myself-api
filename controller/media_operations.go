package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

func GetMediaOperations(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	filter := model.MediaOperationsFilter{MediaKind: "all"}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			err = model.ErrMediaOperationsQueryFailed
			break
		}
		value := values[0]
		switch key {
		case "start_timestamp":
			filter.Start, err = strconv.ParseInt(value, 10, 64)
			if filter.Start <= 0 {
				err = model.ErrMediaOperationsQueryFailed
			}
		case "end_timestamp":
			filter.End, err = strconv.ParseInt(value, 10, 64)
			if filter.End <= 0 {
				err = model.ErrMediaOperationsQueryFailed
			}
		case "model":
			filter.Model = value
			if len(value) > 255 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsFunc(value, unicode.IsControl) {
				err = model.ErrMediaOperationsQueryFailed
			}
		case "media_kind":
			filter.MediaKind = value
			if value != "all" && value != "image" && value != "video" {
				err = model.ErrMediaOperationsQueryFailed
			}
		default:
			err = model.ErrMediaOperationsQueryFailed
		}
		if err != nil {
			break
		}
	}
	now := common.GetTimestamp()
	if filter.End == 0 {
		filter.End = now
	}
	if filter.Start == 0 {
		filter.Start = filter.End - 24*60*60
	}
	if err != nil || filter.Start < 0 || filter.End < filter.Start || filter.End > now || filter.End-filter.Start > 31*24*60*60 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	catalog := model.MediaOperationsCatalog{Models: make(map[string]string), Platforms: make(map[string]string), SubmitPaths: map[string]string{"/v1/videos": "video", "/v1/video/generations": "video", "/v1/images/generations": "image", "/v1/images/edits": "image"}}
	snapshot := jsplugin.DefaultRegistry.Snapshot()
	keys := make(map[string]struct{})
	for _, meta := range snapshot.Factory {
		keys[meta.Key] = struct{}{}
	}
	for _, meta := range snapshot.Override {
		keys[meta.Key] = struct{}{}
	}
	for key := range keys {
		plugin, ok := jsplugin.DefaultRegistry.Get(key)
		if !ok {
			continue
		}
		// Explicit media host protocols and canonical native media paths identify kind;
		// model-id substrings and executor concurrency are not capacity evidence.
		for _, claim := range plugin.Meta.Protocols {
			kind := ""
			switch claim.Name {
			case "openai_image":
				kind = "image"
			case "openai_video":
				kind = "video"
			}
			if kind == "" {
				continue
			}
			names := claim.Models
			if len(names) == 0 {
				names = plugin.Meta.Models
			}
			for _, name := range names {
				if old := catalog.Models[name]; old != "" && old != kind {
					catalog.Models[name] = "unknown"
				} else {
					catalog.Models[name] = kind
				}
			}
		}

		platformKinds := map[string]struct{}{}
		for _, claim := range plugin.Meta.Protocols {
			switch claim.Name {
			case "openai_image":
				platformKinds["image"] = struct{}{}
			case "openai_video":
				platformKinds["video"] = struct{}{}
			}
		}
		for _, route := range plugin.Meta.Routes {
			if route.Type != jsplugin.RouteTypeSubmit || route.Method != http.MethodPost || strings.ContainsAny(route.Path, ":*{") {
				continue
			}
			kind := ""
			for canonical, candidate := range map[string]string{"/v1/images/generations": "image", "/v1/images/edits": "image", "/v1/videos": "video", "/v1/video/generations": "video"} {
				prefix, matched := strings.CutSuffix(route.Path, canonical)
				if matched && (prefix == "" || strings.HasPrefix(prefix, "/") && !strings.Contains(strings.TrimPrefix(prefix, "/"), "/")) {
					kind = candidate
					break
				}
			}
			if kind == "" {
				continue
			}
			catalog.SubmitPaths[route.Path] = kind
			platformKinds[kind] = struct{}{}
			names := route.Models
			if len(names) == 0 {
				names = plugin.Meta.Models
			}
			for _, name := range names {
				if old := catalog.Models[name]; old != "" && old != kind {
					catalog.Models[name] = "unknown"
				} else {
					catalog.Models[name] = kind
				}
			}
		}
		if len(platformKinds) > 0 {
			kind := "unknown"
			if len(platformKinds) == 1 {
				for candidate := range platformKinds {
					kind = candidate
				}
			}
			catalog.Platforms[key] = kind
		}

	}
	result, err := model.GetMediaOperations(c.Request.Context(), filter, catalog)
	if err != nil {
		status, code, message := http.StatusInternalServerError, "MEDIA_OPERATIONS_QUERY_FAILED", "Failed to query logs"
		if errors.Is(err, model.ErrMediaOperationsQueryTooBroad) {
			status, code, message = http.StatusUnprocessableEntity, "MEDIA_OPERATIONS_QUERY_TOO_BROAD", "Narrow the time range or choose a model"
		}
		c.JSON(status, gin.H{"success": false, "message": message, "code": code})
		return
	}
	common.ApiSuccess(c, result)
}
