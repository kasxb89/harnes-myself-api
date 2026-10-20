package middleware

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/image/vp8"
	"golang.org/x/image/vp8l"
	_ "golang.org/x/image/webp"
)

// Keep total in-flight image memory bounded before reading any uploaded bytes.
// The per-user limiter also applies across gateway processes when Redis is used.
var referenceImageUploadSlots = make(chan struct{}, 4)

// Full codec validation may expand a PNG to eight bytes per pixel. Decode one
// image at a time, separately from the bounded original upload buffers.
var referenceImageDecodeSlot = make(chan struct{}, 1)

// Allow the desktop's 4096-by-4096 output; this bounds total pixels, not sides.
const maxReferenceImagePixels int64 = 1 << 24

// PrepareReferenceImageUpload authenticates the dedicated contract, validates
// multipart bytes in memory, and pins the live generation before distribution.
// It does not execute plugin hooks, create tasks, or enter billing.
func PrepareReferenceImageUpload() gin.HandlerFunc {
	limit := userRateLimitFactory(30, 60, "reference-image-upload")
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		authorization := c.GetHeader("Authorization")
		if !strings.HasPrefix(strings.ToLower(authorization), "bearer ") || strings.TrimSpace(authorization[7:]) == "" {
			abortReferenceImageUpload(c, http.StatusUnauthorized, "invalid_api_key", i18n.MsgReferenceImageUploadKeyRequired)
			return
		}
		if encoding := strings.TrimSpace(c.GetHeader("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
			abortReferenceImageUpload(c, http.StatusUnsupportedMediaType, "image_upload_unsupported_type", i18n.MsgReferenceImageUploadCompressedUnsupported)
			return
		}
		if len(service.GetChannelConstraints(c).Pins) != 0 {
			abortReferenceImageUpload(c, http.StatusForbidden, "permission_denied", i18n.MsgReferenceImageUploadChannelSelectForbidden)
			return
		}
		if c.Request.URL.RawQuery != "" {
			abortReferenceImageUpload(c, http.StatusBadRequest, "invalid_request", i18n.MsgReferenceImageUploadFieldsOnly)
			return
		}
		limit(c)
		if c.IsAborted() {
			if c.Writer.Status() == http.StatusTooManyRequests {
				abortReferenceImageUpload(c, http.StatusTooManyRequests, "rate_limit_exceeded", i18n.MsgReferenceImageUploadRateLimited)
			}
			return
		}
		select {
		case referenceImageUploadSlots <- struct{}{}:
			defer func() { <-referenceImageUploadSlots }()
		default:
			c.Header("Retry-After", "1")
			abortReferenceImageUpload(c, http.StatusTooManyRequests, "rate_limit_exceeded", i18n.MsgReferenceImageUploadConcurrencyLimited)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		// A context alone cannot interrupt a stalled inbound socket read.
		responseController := http.NewResponseController(c.Writer)
		_ = responseController.SetReadDeadline(time.Now().Add(30 * time.Second))
		defer responseController.SetReadDeadline(time.Time{})
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxReferenceImageUploadBytes+(64<<10))
		defer c.Request.Body.Close()
		upload, status, code := parseReferenceImageUpload(c.Request)
		if status != 0 {
			if status == http.StatusTooManyRequests {
				c.Header("Retry-After", "1")
			}
			abortReferenceImageUpload(c, status, code, referenceImageUploadMessage(code))
			return
		}
		if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
			value, _ := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
			models, _ := value.(map[string]bool)
			if !TokenModelLimitAllows(models, upload.Model) {
				abortReferenceImageUpload(c, http.StatusForbidden, "permission_denied", i18n.MsgReferenceImageUploadModelForbidden)
				return
			}
		}
		generation := jsplugin.DefaultRegistry.Generation()
		plugin, ok := generation.Get(dto.ReferenceImageUploadPlugin)
		binding, routed := generation.LookupDeclaredRoute(http.MethodPost, "/toapis/v1/images/generations")
		if !ok || plugin == nil || !routed || binding.Plugin != plugin || binding.Route.Type != jsplugin.RouteTypeSubmit ||
			!slices.Contains(plugin.Meta.Models, upload.Model) || len(binding.Route.Models) > 0 && !slices.Contains(binding.Route.Models, upload.Model) {
			abortReferenceImageUpload(c, http.StatusServiceUnavailable, "image_upload_unavailable", i18n.MsgReferenceImageUploadUnavailable)
			return
		}
		c.Set(dto.ContextKeyReferenceImageUpload, upload)
		defer c.Set(dto.ContextKeyReferenceImageUpload, nil)
		c.Set("resolved_task_model", upload.Model)
		c.Set("expected_task_plugin_key", dto.ReferenceImageUploadPlugin)
		c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: plugin})
		c.Next()
	}
}

func abortReferenceImageUpload(c *gin.Context, status int, code, message string) {
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	}
	c.AbortWithStatusJSON(status, dto.ReferenceImageUploadResponse{Error: &dto.ReferenceImageUploadError{Code: code, Type: kind, Message: i18n.T(c, message)}})
}

func referenceImageUploadMessage(code string) string {
	switch code {
	case "image_upload_too_large":
		return i18n.MsgReferenceImageUploadTooLarge
	case "image_upload_unsupported_type":
		return i18n.MsgReferenceImageUploadUnsupportedType
	case "image_upload_timeout":
		return i18n.MsgReferenceImageUploadTimeout
	case "rate_limit_exceeded":
		return i18n.MsgReferenceImageUploadBusy
	default:
		return i18n.MsgReferenceImageUploadInvalidRequest
	}
}

func referenceImageUploadReadFailure(err error) (int, string) {
	var tooLarge *http.MaxBytesError
	var timeout net.Error
	switch {
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge, "image_upload_too_large"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return http.StatusGatewayTimeout, "image_upload_timeout"
	default:
		return http.StatusBadRequest, "invalid_request"
	}
}

func parseReferenceImageUpload(request *http.Request) (*dto.ReferenceImageUploadRequest, int, string) {
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return nil, http.StatusBadRequest, "invalid_request"
	}
	reader := multipart.NewReader(request.Body, params["boundary"])
	upload := &dto.ReferenceImageUploadRequest{}
	modelSeen, fileSeen := false, false
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			status, code := referenceImageUploadReadFailure(err)
			return nil, status, code
		}
		for name := range part.Header {
			if name != "Content-Disposition" && name != "Content-Type" || len(part.Header.Values(name)) != 1 {
				return nil, http.StatusBadRequest, "invalid_request"
			}
		}
		switch part.FormName() {
		case "model":
			if modelSeen || part.FileName() != "" {
				return nil, http.StatusBadRequest, "invalid_request"
			}
			modelSeen = true
			data, err := io.ReadAll(io.LimitReader(part, 129))
			if err != nil {
				status, code := referenceImageUploadReadFailure(err)
				return nil, status, code
			}
			if string(data) != dto.ReferenceImageUploadModel {
				return nil, http.StatusBadRequest, "invalid_request"
			}
			upload.Model = string(data)
		case "file":
			if fileSeen || part.FileName() == "" {
				return nil, http.StatusBadRequest, "invalid_request"
			}
			fileSeen = true
			data, err := io.ReadAll(io.LimitReader(part, dto.MaxReferenceImageUploadBytes+1))
			if err != nil {
				status, code := referenceImageUploadReadFailure(err)
				return nil, status, code
			}
			if int64(len(data)) > dto.MaxReferenceImageUploadBytes {
				return nil, http.StatusRequestEntityTooLarge, "image_upload_too_large"
			}
			mimeType := http.DetectContentType(data)
			var extension, format string
			switch mimeType {
			case "image/png":
				extension, format = ".png", "png"
			case "image/jpeg":
				extension, format = ".jpg", "jpeg"
			case "image/webp":
				extension, format = ".webp", "webp"
			case "image/gif":
				extension, format = ".gif", "gif"
			default:
				return nil, http.StatusUnsupportedMediaType, "image_upload_unsupported_type"
			}
			if declared := part.Header.Get("Content-Type"); declared != "" {
				declaredType, _, err := mime.ParseMediaType(declared)
				if err != nil || declaredType != mimeType && declaredType != "application/octet-stream" {
					return nil, http.StatusUnsupportedMediaType, "image_upload_unsupported_type"
				}
			}
			config, actualFormat, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || actualFormat != format || config.Width <= 0 || config.Height <= 0 ||
				int64(config.Width) > maxReferenceImagePixels/int64(config.Height) {
				return nil, http.StatusUnsupportedMediaType, "image_upload_unsupported_type"
			}
			if format == "gif" && !validStaticReferenceGIF(data, config) || format == "webp" && !validStaticReferenceWebP(data, config) {
				return nil, http.StatusUnsupportedMediaType, "image_upload_unsupported_type"
			}
			select {
			case referenceImageDecodeSlot <- struct{}{}:
				validImage := validateReferenceImagePixels(data, format, config)
				if !validImage {
					return nil, http.StatusUnsupportedMediaType, "image_upload_unsupported_type"
				}
			default:
				return nil, http.StatusTooManyRequests, "rate_limit_exceeded"
			}
			upload.Image, upload.MIMEType, upload.Extension = data, mimeType, extension
		default:
			return nil, http.StatusBadRequest, "invalid_request"
		}
		if err := part.Close(); err != nil {
			status, code := referenceImageUploadReadFailure(err)
			return nil, status, code
		}
	}
	// Include multipart preamble/epilogue and decompression output in the bound.
	if _, err := io.Copy(io.Discard, request.Body); err != nil {
		status, code := referenceImageUploadReadFailure(err)
		return nil, status, code
	}
	if !modelSeen || !fileSeen {
		return nil, http.StatusBadRequest, "invalid_request"
	}
	return upload, 0, ""
}

// validateReferenceImagePixels decodes only after every allocation-driving
// canvas/frame dimension has passed the byte and pixel limits.
func validateReferenceImagePixels(data []byte, format string, config image.Config) bool {
	defer func() { <-referenceImageDecodeSlot }()
	if format == "gif" {
		decoded, err := gif.DecodeAll(bytes.NewReader(data))
		return err == nil && len(decoded.Image) == 1
	}
	decoded, actualFormat, err := image.Decode(bytes.NewReader(data))
	return err == nil && actualFormat == format && decoded.Bounds().Dx() == config.Width && decoded.Bounds().Dy() == config.Height
}

// validStaticReferenceGIF scans every frame before gif.DecodeAll can allocate.
// Animated GIFs are deliberately outside this upload contract.
func validStaticReferenceGIF(data []byte, config image.Config) bool {
	if len(data) < 14 {
		return false
	}
	position := 13
	if data[10]&0x80 != 0 {
		position += 3 << (uint(data[10]&7) + 1)
	}
	frames := 0
	for position < len(data) {
		block := data[position]
		position++
		switch block {
		case ';':
			return frames == 1 && position == len(data)
		case '!':
			if position >= len(data) {
				return false
			}
			position++ // Extension label; its data is length-prefixed sub-blocks.
		case ',':
			frames++
			if frames != 1 || len(data)-position < 9 {
				return false
			}
			left := int(binary.LittleEndian.Uint16(data[position : position+2]))
			top := int(binary.LittleEndian.Uint16(data[position+2 : position+4]))
			width := int(binary.LittleEndian.Uint16(data[position+4 : position+6]))
			height := int(binary.LittleEndian.Uint16(data[position+6 : position+8]))
			packed := data[position+8]
			position += 9
			if width <= 0 || height <= 0 || left+width > config.Width || top+height > config.Height {
				return false
			}
			if packed&0x80 != 0 {
				position += 3 << (uint(packed&7) + 1)
			}
			if position >= len(data) {
				return false
			}
			position++ // LZW minimum code size, validated by the complete decoder.
		default:
			return false
		}
		for {
			if position >= len(data) {
				return false
			}
			size := int(data[position])
			position++
			if size == 0 {
				break
			}
			if len(data)-position < size {
				return false
			}
			position += size
		}
	}
	return false
}

// validStaticReferenceWebP checks actual VP8/VP8L frame dimensions instead of
// trusting the optional VP8X canvas returned early by webp.DecodeConfig.
func validStaticReferenceWebP(data []byte, config image.Config) bool {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" ||
		uint64(binary.LittleEndian.Uint32(data[4:8])) != uint64(len(data)-8) {
		return false
	}
	frames, position := 0, 12
	seenExtended, seenAlpha := false, false
	for position < len(data) {
		if len(data)-position < 8 {
			return false
		}
		kind := string(data[position : position+4])
		size := uint64(binary.LittleEndian.Uint32(data[position+4 : position+8]))
		position += 8
		padded := size + size%2
		if padded > uint64(len(data)-position) {
			return false
		}
		chunk := data[position : position+int(size)]
		position += int(padded)
		if size%2 != 0 && data[position-1] != 0 {
			return false
		}
		switch kind {
		case "ANIM", "ANMF":
			return false
		case "VP8X":
			if seenExtended || frames != 0 || len(chunk) != 10 || chunk[0]&0x02 != 0 {
				return false
			}
			seenExtended = true
			width := int(uint32(chunk[4])|uint32(chunk[5])<<8|uint32(chunk[6])<<16) + 1
			height := int(uint32(chunk[7])|uint32(chunk[8])<<8|uint32(chunk[9])<<16) + 1
			if width != config.Width || height != config.Height {
				return false
			}
		case "ALPH":
			if !seenExtended || seenAlpha || frames != 0 {
				return false
			}
			seenAlpha = true
		case "VP8 ", "VP8L":
			frames++
			if frames != 1 {
				return false
			}
			var width, height int
			if kind == "VP8L" {
				frameConfig, err := vp8l.DecodeConfig(bytes.NewReader(chunk))
				if err != nil {
					return false
				}
				width, height = frameConfig.Width, frameConfig.Height
			} else {
				decoder := vp8.NewDecoder()
				decoder.Init(bytes.NewReader(chunk), len(chunk))
				frame, err := decoder.DecodeFrameHeader()
				if err != nil {
					return false
				}
				width, height = frame.Width, frame.Height
			}
			if width != config.Width || height != config.Height || width <= 0 || height <= 0 || int64(width) > maxReferenceImagePixels/int64(height) {
				return false
			}
		}
	}
	return frames == 1
}
