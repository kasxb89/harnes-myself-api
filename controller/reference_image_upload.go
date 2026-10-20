package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// UploadReferenceImage forwards only validated bytes to the selected vendor.
// It intentionally does not create a task, preconsume quota, or retain a photo.
func UploadReferenceImage(c *gin.Context) {
	value, exists := c.Get(dto.ContextKeyReferenceImageUpload)
	upload, valid := value.(*dto.ReferenceImageUploadRequest)
	settingsValue, _ := common.GetContextKey(c, constant.ContextKeyChannelSetting)
	settings, configured := settingsValue.(kitdto.ChannelSettings)
	key := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	base := common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl)
	baseURL, err := url.Parse(base)
	channel, channelErr := model.CacheGetChannel(common.GetContextKeyInt(c, constant.ContextKeyChannelId))
	if !exists || !valid || upload == nil || upload.Model != dto.ReferenceImageUploadModel || !configured ||
		channelErr != nil || channel == nil || channel.Status != common.ChannelStatusEnabled ||
		channel.Type != constant.ChannelTypeTaskPlugin || channel.GetSetting().TaskPluginKey != dto.ReferenceImageUploadPlugin || channel.GetBaseURL() != base ||
		common.GetContextKeyInt(c, constant.ContextKeyChannelType) != constant.ChannelTypeTaskPlugin ||
		settings.TaskPluginKey != dto.ReferenceImageUploadPlugin || settings.TLSInsecureSkipVerify || common.TLSInsecureSkipVerify || key == "" ||
		err != nil || baseURL.Scheme != "https" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.ForceQuery || baseURL.Fragment != "" ||
		baseURL.Opaque != "" || baseURL.Port() != "" || baseURL.Path != "" && baseURL.Path != "/" ||
		baseURL.Host != "toapis.cn" && baseURL.Host != "toapis.com" {
		abortReferenceImageUploadResponse(c, http.StatusServiceUnavailable, "image_upload_unavailable", i18n.MsgReferenceImageUploadUnavailable)
		return
	}
	client, err := service.GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil || client == nil {
		abortReferenceImageUploadResponse(c, http.StatusServiceUnavailable, "image_upload_unavailable", i18n.MsgReferenceImageUploadUnavailable)
		return
	}
	// Never change the shared relay client, propagate its cookie jar, or follow
	// redirects carrying the private vendor credential to another endpoint.
	uploadClient := *client
	uploadClient.Jar = nil
	uploadClient.Timeout = 30 * time.Second
	uploadClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="reference`+upload.Extension+`"`)
	header.Set("Content-Type", upload.MIMEType)
	part, err := writer.CreatePart(header)
	if err == nil {
		_, err = part.Write(upload.Image)
	}
	if err == nil {
		err = writer.WriteField("purpose", "generation")
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		abortReferenceImageUploadResponse(c, http.StatusServiceUnavailable, "image_upload_unavailable", i18n.MsgReferenceImageUploadUnavailable)
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+baseURL.Host+"/v1/uploads/images", &body)
	if err != nil {
		abortReferenceImageUploadResponse(c, http.StatusServiceUnavailable, "image_upload_unavailable", i18n.MsgReferenceImageUploadUnavailable)
		return
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response, err := uploadClient.Do(request)
	if err != nil {
		status, code, message := http.StatusBadGateway, "image_upload_upstream_error", i18n.MsgReferenceImageUploadUpstreamError
		var timeout net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
			status, code, message = http.StatusGatewayTimeout, "image_upload_timeout", i18n.MsgReferenceImageUploadTimeout
		}
		abortReferenceImageUploadResponse(c, status, code, message)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		abortReferenceImageUploadResponse(c, http.StatusBadGateway, "image_upload_upstream_error", i18n.MsgReferenceImageUploadUpstreamError)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		status, code, message := http.StatusBadGateway, "image_upload_upstream_error", i18n.MsgReferenceImageUploadUpstreamError
		var timeout net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
			status, code, message = http.StatusGatewayTimeout, "image_upload_timeout", i18n.MsgReferenceImageUploadTimeout
		}
		abortReferenceImageUploadResponse(c, status, code, message)
		return
	}
	var result dto.ReferenceImageUploadResponse
	if common.Unmarshal(data, &result) != nil || !result.Success || result.Data == nil || result.Error != nil ||
		result.Data.ID == "" || len(result.Data.ID) > 256 || strings.ContainsFunc(result.Data.ID, unicode.IsControl) ||
		result.Data.MIMEType != upload.MIMEType || result.Data.Size != int64(len(upload.Image)) ||
		!validReferenceImageUploadURL(result.Data.URL) {
		abortReferenceImageUploadResponse(c, http.StatusBadGateway, "image_upload_upstream_error", i18n.MsgReferenceImageUploadUpstreamError)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, dto.ReferenceImageUploadResponse{Success: true, Data: result.Data})
}

func abortReferenceImageUploadResponse(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(status, dto.ReferenceImageUploadResponse{Error: &dto.ReferenceImageUploadError{Code: code, Type: "server_error", Message: i18n.T(c, message)}})
}

func validReferenceImageUploadURL(rawURL string) bool {
	if rawURL == "" || len(rawURL) > 2048 || strings.ContainsFunc(rawURL, unicode.IsSpace) || strings.ContainsFunc(rawURL, unicode.IsControl) || strings.Contains(rawURL, "\\") {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || parsed.Port() != "" || parsed.Host != "files.toapis.cn" && parsed.Host != "files.toapis.com" ||
		!strings.HasPrefix(parsed.Path, "/uploads/") || len(parsed.Path) == len("/uploads/") || parsed.RawPath != "" ||
		strings.ContainsFunc(parsed.Path, unicode.IsSpace) || strings.ContainsFunc(parsed.Path, unicode.IsControl) {
		return false
	}
	for segment := range strings.SplitSeq(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
