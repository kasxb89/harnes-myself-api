package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

const videoIngressObservedKey = "video_ingress_observation_started"

// VideoIngressObserver must be installed before route groups and their auth,
// body, resource and rate-limit middleware. It never reads a request payload.
func VideoIngressObserver(recorder *model.VideoIngressRecorder) gin.HandlerFunc {
	if recorder == nil {
		recorder = model.ProcessVideoIngressRecorder()
	}
	return func(c *gin.Context) {
		if c.GetBool(videoIngressObservedKey) || model.VideoIngressRequestObserved(c.Request.Context()) {
			c.Next()
			return
		}
		operation := videoIngressOperation(c.Request.Method, c.Request.URL.Path, c.FullPath())
		if operation == "" {
			c.Next()
			return
		}
		c.Set(videoIngressObservedKey, true)
		observation := recorder.Begin(operation, time.Now())
		c.Request = c.Request.WithContext(observation.Context(c.Request.Context()))
		defer func() { observation.Finish(false) }()
		c.Next()
	}
}
func videoIngressOperation(method, path, route string) string {
	switch method + " " + route {
	case "POST /v1/videos", "POST /v1/video/generations", "POST /v1/videos/:video_id/remix":
		return "submit"
	case "GET /v1/videos/:task_id", "GET /v1/video/generations/:task_id":
		return "status"
	}
	if method == http.MethodPost && route == "/v1/tasks/:key" {
		key := strings.TrimPrefix(path, "/v1/tasks/")
		if !strings.Contains(key, "/") {
			if plugin, ok := jsplugin.DefaultRegistry.Generation().Get(key); ok {
				videoModels := map[string]struct{}{}
				image := false
				for _, claim := range plugin.Meta.Protocols {
					if claim.Name == "openai_image" {
						image = true
					}
					if claim.Name == "openai_video" {
						names := claim.Models
						if len(names) == 0 {
							names = plugin.Meta.Models
						}
						for _, name := range names {
							videoModels[name] = struct{}{}
						}
					}
				}
				if !image && len(videoModels) > 0 && len(videoModels) == len(plugin.Meta.Models) {
					return "submit"
				}
			}
		}
	}
	if route != "" {
		return ""
	}

	if method != http.MethodPost && method != http.MethodGet {
		return ""
	}
	// Native URLs are served by an inner Gin engine, so the outer FullPath can
	// be empty. Match only validated declared templates of video-only plugins.
	for _, plugin := range jsplugin.DefaultRegistry.Generation().Plugins() {
		video, image := false, false
		for _, claim := range plugin.Meta.Protocols {
			video = video || claim.Name == "openai_video"
			image = image || claim.Name == "openai_image"
		}
		for _, declared := range plugin.Meta.Routes {
			if declared.Type == jsplugin.RouteTypeSubmit {
				for _, suffix := range []string{"/v1/videos", "/v1/video/generations"} {
					prefix, ok := strings.CutSuffix(declared.Path, suffix)
					if ok && (prefix == "" || strings.HasPrefix(prefix, "/") && !strings.Contains(strings.TrimPrefix(prefix, "/"), "/")) {
						video = true
					}
				}
				image = image || strings.HasSuffix(declared.Path, "/v1/images/generations") || strings.HasSuffix(declared.Path, "/v1/images/edits")
			}
		}
		if !video || image {
			continue
		}
		for _, declared := range plugin.Meta.Routes {
			if declared.Method != method || strings.Contains(declared.Path, "*") {
				continue
			}
			templateParts := strings.Split(declared.Path, "/")
			pathParts := strings.Split(path, "/")
			if len(templateParts) != len(pathParts) {
				continue
			}
			matched := true
			for index, segment := range templateParts {
				if strings.HasPrefix(segment, ":") {
					if pathParts[index] == "" {
						matched = false
						break
					}
				} else if segment != pathParts[index] {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			if declared.Type == jsplugin.RouteTypeSubmit && method == http.MethodPost {
				return "submit"
			}
			if declared.Type == jsplugin.RouteTypeQuery && method == http.MethodGet && !strings.HasSuffix(declared.Path, "/content") && !strings.HasSuffix(declared.Path, "/download") {
				return "status"
			}
		}
	}
	return ""
}
