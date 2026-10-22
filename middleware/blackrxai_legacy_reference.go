package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
	_ "golang.org/x/image/webp"
)

// normalizeBlackRxAILegacyReferences upgrades the bounded inline-media shape
// emitted by the published desktop client to the existing multipart protocol.
// Nothing is fetched or submitted here. All candidate decoders must belong to
// this provider; shared-provider and ordinary JSON requests retain their body.
func normalizeBlackRxAILegacyReferences(c *gin.Context, pinned pluginruntime.PinnedEndpoint) error {
	if pinned.Protocol != "openai_video" || pinned.Operation.Name != "create" || pinned.Plugin == nil {
		return nil
	}
	candidates := pinned.Candidates
	if len(candidates) == 0 {
		candidates = []pluginruntime.ProtocolBinding{{Plugin: pinned.Plugin}}
	}
	for _, candidate := range candidates {
		if candidate.Plugin == nil || candidate.Plugin.Meta.Key != "blackrxai-video" || !slices.Contains(candidate.Plugin.Meta.Models, pinned.Model) {
			return nil
		}
	}
	canonicalContentType := ""
	for _, header := range c.Request.Header.Values("Content-Type") {
		mediaType, parameters, parseErr := mime.ParseMediaType(header)
		if parseErr != nil {
			return fmt.Errorf("invalid Content-Type header")
		}
		current := mime.FormatMediaType(strings.ToLower(mediaType), parameters)
		if canonicalContentType != "" && canonicalContentType != current {
			return fmt.Errorf("conflicting Content-Type headers")
		}
		canonicalContentType = current
	}
	contentType, _, err := mime.ParseMediaType(canonicalContentType)
	if err != nil || contentType != "application/json" {
		return nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return err
	}
	if storage.Size() > 65<<20 {
		return fmt.Errorf("legacy video request exceeds the reference size limit")
	}
	raw, err := storage.Bytes()
	if err != nil || !bytes.Contains(raw, []byte("data:")) {
		return err
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("JSON body must be valid UTF-8")
	}
	var request map[string]any
	if err = common.Unmarshal(raw, &request); err != nil {
		return fmt.Errorf("invalid legacy video JSON body")
	}
	if model, _ := request["model"].(string); model != pinned.Model {
		return nil
	}
	references, consumed, err := blackRxAILegacyReferences(request)
	if err != nil {
		return err
	}
	hasInline := false
	for _, values := range references {
		for _, value := range values {
			hasInline = hasInline || strings.HasPrefix(value, "data:")
		}
	}
	if !hasInline {
		return nil
	}
	if err := c.Request.Context().Err(); err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range request {
		if consumed[key] {
			continue
		}
		var text string
		switch scalar := value.(type) {
		case string:
			text = scalar
		case float64, bool, nil:
			encoded, encodeErr := common.Marshal(scalar)
			if encodeErr != nil {
				return fmt.Errorf("invalid legacy video field")
			}
			text = string(encoded)
		default:
			return fmt.Errorf("unsupported legacy video field shape")
		}
		if len(text) > maxTaskPluginFieldValueBytes {
			return fmt.Errorf("legacy video field exceeds the size limit")
		}
		if err := writer.WriteField(key, text); err != nil {
			return err
		}
	}
	var total, imageTotal int64
	fileCount := 0
	for _, field := range []string{"images", "videos", "audios"} {
		inlineSeen := false
		for _, reference := range references[field] {
			if err := c.Request.Context().Err(); err != nil {
				return err
			}
			if !strings.HasPrefix(reference, "data:") {
				// Upstream represents URL and file lists separately. Never reorder
				// a first/last frame or a subject reference silently.
				if inlineSeen {
					return fmt.Errorf("legacy video references must place URLs before inline files")
				}
				urlField := map[string]string{"images": "image_urls", "videos": "video_urls", "audios": "audio_urls"}[field]
				if err := writer.WriteField(urlField, reference); err != nil {
					return err
				}
				continue
			}
			inlineSeen = true
			fileCount++
			if fileCount > maxTaskPluginFiles {
				return fmt.Errorf("legacy video references exceed the file limit")
			}
			data, mediaType, extension, decodeErr := decodeBlackRxAIInlineReference(c.Request.Context(), reference, field, &total)
			if decodeErr != nil {
				return decodeErr
			}
			if field == "images" {
				imageTotal += int64(len(data))
				if imageTotal > 12<<20 {
					return fmt.Errorf("legacy video reference images exceed the total size limit")
				}
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": "reference-" + strconv.Itoa(fileCount) + extension}))
			header.Set("Content-Type", mediaType)
			part, createErr := writer.CreatePart(header)
			if createErr != nil {
				return createErr
			}
			if _, err := part.Write(data); err != nil {
				return err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	requestLimitMB := constant.MaxRequestBodyMB
	if requestLimitMB <= 0 {
		requestLimitMB = 128
	}
	if int64(body.Len()) > int64(requestLimitMB)<<20 {
		return fmt.Errorf("legacy video request exceeds the request size limit")
	}
	converted, err := common.CreateBodyStorage(body.Bytes())
	if err != nil {
		return err
	}
	if err := c.Request.Context().Err(); err != nil {
		converted.Close()
		return err
	}
	// Publish only a fully validated replacement. Existing request cleanup owns
	// the new storage; closing the original also releases its temporary file.
	c.Set(common.KeyBodyStorage, converted)
	c.Set(common.KeyRequestBody, nil)
	storage.Close()
	c.Request.Body = io.NopCloser(converted)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request.ContentLength = int64(body.Len())
	return nil
}

func blackRxAILegacyReferences(request map[string]any) (map[string][]string, map[string]bool, error) {
	refs := map[string][]string{"images": {}, "videos": {}, "audios": {}}
	consumed := map[string]bool{}
	for _, pair := range [][2]string{{"image_url", "images"}, {"image_urls", "images"}, {"images", "images"}, {"video_urls", "videos"}, {"videos", "videos"}, {"audio_urls", "audios"}, {"audios", "audios"}} {
		value, present := request[pair[0]]
		if !present {
			continue
		}
		consumed[pair[0]] = true
		if pair[0] == "image_url" {
			value = []any{value}
		}
		items, ok := value.([]any)
		if !ok || len(items) > maxTaskPluginFiles {
			return nil, nil, fmt.Errorf("legacy video references must be bounded arrays of strings")
		}
		for _, item := range items {
			text, ok := item.(string)
			if !ok || text == "" {
				return nil, nil, fmt.Errorf("legacy video references must contain nonempty strings")
			}
			refs[pair[1]] = append(refs[pair[1]], text)
		}
	}
	metadataValue, present := request["metadata"]
	if !present {
		return refs, consumed, nil
	}
	metadata, ok := metadataValue.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("video metadata must be an object")
	}
	for key := range metadata {
		if !slices.Contains([]string{"ratio", "first_frame_image", "last_frame_image", "content", "reference_video", "reference_audio"}, key) {
			return nil, nil, fmt.Errorf("unsupported video metadata field")
		}
	}
	frames := []string{}
	for _, key := range []string{"first_frame_image", "last_frame_image"} {
		if value, present := metadata[key]; present {
			text, valid := value.(string)
			if !valid || text == "" || key == "last_frame_image" && len(frames) == 0 {
				return nil, nil, fmt.Errorf("legacy video frame metadata is invalid")
			}
			frames = append(frames, text)
		}
	}
	if len(frames) > 0 {
		if value, present := request["images"]; present {
			images, _ := value.([]any)
			if len(images) != len(frames) {
				return nil, nil, fmt.Errorf("frame metadata and images must agree")
			}
			for index, frame := range frames {
				if images[index] != frame {
					return nil, nil, fmt.Errorf("frame metadata and images must agree")
				}
			}
		} else {
			refs["images"] = append(refs["images"], frames...)
		}
	}
	if value, present := metadata["content"]; present {
		content, valid := value.([]any)
		if !valid || len(content) < 2 || len(content) > maxTaskPluginFiles+1 {
			return nil, nil, fmt.Errorf("legacy video content must preserve the prompt and reference images")
		}
		text, valid := content[0].(map[string]any)
		prompt, promptOK := request["prompt"].(string)
		if !valid || !promptOK || len(text) != 2 || text["type"] != "text" || text["text"] != prompt {
			return nil, nil, fmt.Errorf("legacy video content must preserve the prompt and reference images")
		}
		for _, value := range content[1:] {
			item, valid := value.(map[string]any)
			if !valid || len(item) != 3 || item["type"] != "image_url" || item["role"] != "reference_image" {
				return nil, nil, fmt.Errorf("unsupported video metadata reference")
			}
			imageURL, valid := item["image_url"].(map[string]any)
			if !valid || len(imageURL) != 1 {
				return nil, nil, fmt.Errorf("unsupported video metadata reference")
			}
			reference, valid := imageURL["url"].(string)
			if !valid || reference == "" {
				return nil, nil, fmt.Errorf("unsupported video metadata reference")
			}
			refs["images"] = append(refs["images"], reference)
		}
	}
	for _, pair := range [][2]string{{"reference_video", "videos"}, {"reference_audio", "audios"}} {
		if value, present := metadata[pair[0]]; present {
			items, valid := value.([]any)
			if !valid || len(items) > maxTaskPluginFiles {
				return nil, nil, fmt.Errorf("legacy video references must be bounded arrays of strings")
			}
			for _, value := range items {
				reference, valid := value.(string)
				if !valid || reference == "" {
					return nil, nil, fmt.Errorf("legacy video references must contain nonempty strings")
				}
				refs[pair[1]] = append(refs[pair[1]], reference)
			}
		}
	}
	if ratio, present := metadata["ratio"]; present {
		text, valid := ratio.(string)
		if !valid {
			return nil, nil, fmt.Errorf("video ratio must match a supported width and height")
		}
		if existing, present := request["ratio"]; present && existing != text {
			existingText, valid := existing.(string)
			if !valid {
				return nil, nil, fmt.Errorf("video ratio aliases must agree")
			}
			dimensions := [2][2]int{}
			for index, ratio := range []string{existingText, text} {
				width, height, found := strings.Cut(ratio, ":")
				if !found || len(width) == 0 || len(height) == 0 || len(width) > 5 || len(height) > 5 || width[0] < '1' || width[0] > '9' || height[0] < '1' || height[0] > '9' {
					return nil, nil, fmt.Errorf("video ratio aliases must agree")
				}
				var widthErr, heightErr error
				dimensions[index][0], widthErr = strconv.Atoi(width)
				dimensions[index][1], heightErr = strconv.Atoi(height)
				if widthErr != nil || heightErr != nil || dimensions[index][0] <= 0 || dimensions[index][1] <= 0 {
					return nil, nil, fmt.Errorf("video ratio aliases must agree")
				}
			}
			if int64(dimensions[0][0])*int64(dimensions[1][1]) != int64(dimensions[1][0])*int64(dimensions[0][1]) {
				return nil, nil, fmt.Errorf("video ratio aliases must agree")
			}
		}
		request["ratio"] = text
	}
	consumed["metadata"] = true
	return refs, consumed, nil
}

func decodeBlackRxAIInlineReference(ctx context.Context, reference, field string, total *int64) ([]byte, string, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", "", err
	}
	header, encoded, found := strings.Cut(reference, ",")
	if !found || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") {
		return nil, "", "", fmt.Errorf("legacy video reference must be a base64 data URL")
	}
	mediaType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	allowed := map[string]map[string]string{
		"images": {"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif"},
		"videos": {"video/mp4": ".mp4", "video/quicktime": ".mov", "video/webm": ".webm"},
		"audios": {"audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/x-m4a": ".m4a", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/aac": ".aac", "audio/flac": ".flac", "audio/ogg": ".ogg"},
	}
	extension := allowed[field][mediaType]
	if extension == "" {
		return nil, "", "", fmt.Errorf("legacy video reference MIME type is unsupported")
	}
	limit := map[string]int64{"images": 6 << 20, "videos": 32 << 20, "audios": 15 << 20}[field]
	globalLimitMB := constant.MaxFileDownloadMB
	if globalLimitMB <= 0 {
		globalLimitMB = 64
	}
	limit = min(limit, int64(globalLimitMB)<<20)
	totalLimit := min(int64(48<<20), int64(globalLimitMB)<<20)
	if int64(len(encoded)) > ((min(limit, totalLimit-*total)+2)/3)*4 || encoded == "" || strings.ContainsAny(encoded, "\r\n\t ") {
		return nil, "", "", fmt.Errorf("legacy video reference exceeds the size limit or has invalid base64")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || int64(len(data)) > limit || *total+int64(len(data)) > totalLimit {
		return nil, "", "", fmt.Errorf("legacy video reference exceeds the size limit or has invalid base64")
	}
	detected := http.DetectContentType(data)
	valid := false
	switch field {
	case "images":
		config, format, imageErr := image.DecodeConfig(bytes.NewReader(data))
		expected := map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/webp": "webp", "image/gif": "gif"}[mediaType]
		valid = imageErr == nil && format == expected && config.Width > 0 && config.Height > 0 && int64(config.Width)*int64(config.Height) <= 16777216
	case "videos":
		valid = len(data) >= 12 && string(data[4:8]) == "ftyp" && (mediaType == "video/mp4" || mediaType == "video/quicktime") || detected == "video/webm" && mediaType == "video/webm"
	case "audios":
		valid = detected == "audio/mpeg" && mediaType == "audio/mpeg" || detected == "audio/wave" && (mediaType == "audio/wav" || mediaType == "audio/x-wav") ||
			detected == "application/ogg" && mediaType == "audio/ogg" || bytes.HasPrefix(data, []byte("fLaC")) && mediaType == "audio/flac" ||
			len(data) >= 12 && string(data[4:8]) == "ftyp" && (mediaType == "audio/mp4" || mediaType == "audio/x-m4a") ||
			len(data) >= 2 && data[0] == 0xff && data[1]&0xf6 == 0xf0 && mediaType == "audio/aac"
	}
	if !valid {
		return nil, "", "", fmt.Errorf("legacy video reference bytes do not match its MIME type or image dimensions")
	}
	if err := ctx.Err(); err != nil {
		return nil, "", "", err
	}
	*total += int64(len(data))
	return data, mediaType, extension, nil
}
