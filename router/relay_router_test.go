package router

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListModelsSupportsOpenAIAndGeminiAuthentication(t *testing.T) {
	setupRelayRouterTestDB(t)

	user := model.User{
		Username: "models-user",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100,
	}
	require.NoError(t, model.DB.Create(&user).Error)
	require.NoError(t, model.DB.Create(&model.Token{
		UserId:         user.Id,
		Key:            "modelstestkey",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	engine := gin.New()
	SetRelayRouter(engine)

	tests := []struct {
		name           string
		path           string
		headerName     string
		expectedObject string
		expectedField  string
	}{
		{
			name:           "OpenAI bearer token",
			path:           "/v1/models",
			headerName:     "Authorization",
			expectedObject: "list",
			expectedField:  "data",
		},
		{
			name:          "Gemini API key header",
			path:          "/v1/models",
			headerName:    "x-goog-api-key",
			expectedField: "models",
		},
		{
			name:          "Gemini API key query",
			path:          "/v1/models?key=modelstestkey",
			expectedField: "models",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.headerName != "" {
				value := "modelstestkey"
				if test.headerName == "Authorization" {
					value = "Bearer " + value
				}
				request.Header.Set(test.headerName, value)
			}

			engine.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code)
			var payload map[string]any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			assert.Contains(t, payload, test.expectedField)
			assert.NotContains(t, payload, "error")
			if test.expectedObject != "" {
				assert.Equal(t, test.expectedObject, payload["object"])
			}
		})
	}
}

func setupRelayRouterTestDB(t *testing.T) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	originalIsMasterNode := common.IsMasterNode
	originalRedisEnabled := common.RedisEnabled
	originalSQLitePath := common.SQLitePath
	originalDB, originalLogDB := model.DB, model.LOG_DB
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")

	common.IsMasterNode = false
	common.RedisEnabled = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	testDSN := os.Getenv("TEST_UPLOAD_SQL_DSN")
	if testDSN == "" {
		testDSN = "local"
	} else {
		// External runs must name a disposable test database, never a product DB.
		require.Contains(t, testDSN, "/test_image_upload_")
	}
	require.NoError(t, os.Setenv("SQL_DSN", testDSN))
	require.NoError(t, model.InitDB())
	model.LOG_DB = model.DB
	var databaseVersion string
	versionQuery := "SELECT version()"
	if common.MainDatabaseType() == common.DatabaseTypeSQLite {
		versionQuery = "SELECT sqlite_version()"
	}
	require.NoError(t, model.DB.Raw(versionQuery).Scan(&databaseVersion).Error)
	t.Logf("database: %s %s", common.MainDatabaseType(), databaseVersion)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Token{}, &model.Ability{}))

	t.Cleanup(func() {
		require.NoError(t, model.DB.Migrator().DropTable(&model.Ability{}, &model.Token{}, &model.User{}))
		if sqlDB, err := model.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
		model.DB, model.LOG_DB = originalDB, originalLogDB
		common.IsMasterNode = originalIsMasterNode
		common.RedisEnabled = originalRedisEnabled
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	})
}

const uploadTestModel = "yunshu-internal-gpt-image-2"
const uploadTestChannelSecret = "channel-secret-must-not-leak"

// imageUploadRoundTripper exercises the real HTTP client policy without making
// network requests or weakening the production vendor-host allowlist.
type imageUploadRoundTripper func(*http.Request) (*http.Response, error)

func (transport imageUploadRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// imageUploadReadTracker proves rejected uploads never touch body bytes.
type imageUploadReadTracker struct {
	reads int
}

func (reader *imageUploadReadTracker) Read([]byte) (int, error) {
	reader.reads++
	return 0, errors.New("upload body must not be read")
}

func (reader *imageUploadReadTracker) Close() error { return nil }

type imageUploadPart struct {
	name, filename, mime string
	data                 []byte
}

func imageUploadMultipart(t *testing.T, parts ...imageUploadPart) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		if part.filename == "" {
			require.NoError(t, writer.WriteField(part.name, string(part.data)))
			continue
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": part.name, "filename": part.filename}))
		header.Set("Content-Type", part.mime)
		field, err := writer.CreatePart(header)
		require.NoError(t, err)
		_, err = field.Write(part.data)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func TestReferenceImageUploadRoute(t *testing.T) {
	require.NoError(t, i18n.Init())
	setupRelayRouterTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Task{}, &model.Log{}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Migrator().DropTable(&model.Channel{}, &model.Task{}, &model.Log{}))
	})
	oldMemory, oldBatch := common.MemoryCacheEnabled, common.BatchUpdateEnabled
	common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false
	var serverLog bytes.Buffer
	common.LogWriterMu.Lock()
	previousLogWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &serverLog
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previousLogWriter
		common.LogWriterMu.Unlock()
	})
	oldRegistry := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() {
		common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldMemory, oldBatch
		jsplugin.DefaultRegistry = oldRegistry
	})
	const pluginSource = `
export const meta={apiVersion:1,key:"toapis-image",name:"Upload test",version:"1.1.0",author:{name:"Test"},models:["yunshu-internal-gpt-image-2"],fetchMode:"per_task",routes:[{method:"POST",path:"/toapis/v1/images/generations",type:"submit",models:["yunshu-internal-gpt-image-2"],decode:"decode",render:"render"}]};
export const native={decode(ctx){return {kind:"submit",model:ctx.body.value.model,requestBody:ctx.body.value};},render(ctx,task){return task;}};
export function buildSubmitRequest(){return {};}
export function parseSubmitResponse(){return {};}
export function buildQueryRequest(){return {};}
export function parseTaskResult(){return {};}
`
	_, err := jsplugin.DefaultRegistry.Register(pluginSource, jsplugin.Options{})
	require.NoError(t, err)
	baseURL := "https://toapis.cn"
	channel := model.Channel{Type: constant.ChannelTypeTaskPlugin, Name: "upload-channel", Status: common.ChannelStatusEnabled, Key: uploadTestChannelSecret, Models: uploadTestModel, Group: "default", BaseURL: &baseURL}
	channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "toapis-image"})
	require.NoError(t, channel.Insert())

	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	client := service.GetHttpClient()
	oldTransport := client.Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	var calls int
	var upstreamStatus = http.StatusOK
	var upstreamBody string
	var upstreamErr error
	client.Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, "https", request.URL.Scheme)
		assert.Equal(t, "toapis.cn", request.URL.Host)
		assert.Equal(t, "/v1/uploads/images", request.URL.Path)
		assert.Empty(t, request.URL.RawQuery)
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "Bearer "+uploadTestChannelSecret, request.Header.Get("Authorization"))
		assert.Empty(t, request.Header.Get("Cookie"))
		assert.Empty(t, request.Header.Get("X-Forward-User-Secret"))
		if upstreamErr != nil {
			return nil, upstreamErr
		}
		mediaType, parameters, parseErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
		require.NoError(t, parseErr)
		assert.Equal(t, "multipart/form-data", mediaType)
		reader := multipart.NewReader(request.Body, parameters["boundary"])
		var fileCount int
		for {
			part, partErr := reader.NextPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			require.NoError(t, partErr)
			data, readErr := io.ReadAll(part)
			require.NoError(t, readErr)
			if part.FileName() != "" {
				fileCount++
				assert.Equal(t, "file", part.FormName())
				assert.NotEqual(t, "private-photo-name.png", part.FileName())
				assert.NotEmpty(t, data)
			} else {
				assert.Equal(t, "purpose", part.FormName())
				assert.Equal(t, "generation", string(data))
			}
		}
		assert.Equal(t, 1, fileCount)
		return &http.Response{StatusCode: upstreamStatus, Header: http.Header{"Content-Type": {"application/json"}, "X-Upstream-Secret": {uploadTestChannelSecret}, "Location": {"https://evil.example/steal"}}, Body: io.NopCloser(strings.NewReader(upstreamBody)), Request: request}, nil
	})
	engine := gin.New()
	SetRelayRouter(engine)
	SetTaskPluginProtocolRouter(engine)
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	modelPart := imageUploadPart{name: "model", data: []byte(uploadTestModel)}
	filePart := imageUploadPart{name: "file", filename: "private-photo-name.png", mime: "image/png", data: pngData.Bytes()}
	var userSequence int
	newCredential := func(t *testing.T) (model.User, model.Token) {
		t.Helper()
		userSequence++
		user := model.User{Username: fmt.Sprintf("upload%d", userSequence), Status: common.UserStatusEnabled, Group: "default", Quota: 500000, AffCode: fmt.Sprintf("up%d", userSequence)}
		require.NoError(t, model.DB.Create(&user).Error)
		token := model.Token{UserId: user.Id, Key: fmt.Sprintf("uploadkey%d", userSequence), Name: "test-relay", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 200000}
		require.NoError(t, model.DB.Create(&token).Error)
		return user, token
	}
	perform := func(token string, body []byte, contentType string, chunked bool, query string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/uploads/images"+query, bytes.NewReader(body))
		request.Header.Set("Content-Type", contentType)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		request.Header.Set("Cookie", "private-cookie")
		request.Header.Set("X-Forward-User-Secret", "private-header")
		request.RemoteAddr = "192.0.2.10:12345"
		if chunked {
			request.ContentLength = -1
			request.TransferEncoding = []string{"chunked"}
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assert.NotContains(t, recorder.Body.String(), uploadTestChannelSecret)
		assert.NotContains(t, recorder.Body.String(), "UPSTREAM_PRIVATE_DETAIL")
		assert.NotContains(t, recorder.Body.String(), "private-photo-name")
		assert.Empty(t, recorder.Header().Get("X-Upstream-Secret"))
		assert.Empty(t, recorder.Header().Get("Location"))
		return recorder
	}
	resetResponse := func() {
		calls, upstreamStatus, upstreamErr = 0, http.StatusOK, nil
		upstreamBody = fmt.Sprintf(`{"success":true,"data":{"id":"upload-test","url":"https://files.toapis.com/uploads/upload-test.png","mime_type":"image/png","size":%d},"secret":"%s"}`, len(filePart.data), uploadTestChannelSecret)
	}

	t.Run("authorized upload is isolated and unbilled", func(t *testing.T) {
		user, token := newCredential(t)
		resetResponse()
		body, contentType := imageUploadMultipart(t, modelPart, filePart)
		recorder := perform(token.Key, body, contentType, false, "")
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		assert.Equal(t, 1, calls)
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				ID       string `json:"id"`
				URL      string `json:"url"`
				MIMEType string `json:"mime_type"`
				Size     int    `json:"size"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.True(t, response.Success)
		assert.Equal(t, "upload-test", response.Data.ID)
		assert.Equal(t, "https://files.toapis.com/uploads/upload-test.png", response.Data.URL)
		assert.Equal(t, "image/png", response.Data.MIMEType)
		assert.Equal(t, len(filePart.data), response.Data.Size)
		var afterUser model.User
		var afterToken model.Token
		require.NoError(t, model.DB.First(&afterUser, user.Id).Error)
		require.NoError(t, model.DB.First(&afterToken, token.Id).Error)
		assert.Equal(t, user.Quota, afterUser.Quota)
		assert.Zero(t, afterUser.UsedQuota)
		assert.Zero(t, afterUser.RequestCount)
		assert.Equal(t, token.RemainQuota, afterToken.RemainQuota)
		assert.Zero(t, afterToken.UsedQuota)
		var tasks, consumeLogs int64
		require.NoError(t, model.DB.Model(&model.Task{}).Count(&tasks).Error)
		require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumeLogs).Error)
		assert.Zero(t, tasks)
		assert.Zero(t, consumeLogs)
	})

	for _, testCase := range []struct {
		name       string
		authorized bool
		status     int
	}{
		{"unauthenticated gzip body is unread", false, 401},
		{"compressed upload is rejected before reading", true, 415},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, token := newCredential(t)
			resetResponse()
			reader := &imageUploadReadTracker{}
			request := httptest.NewRequest(http.MethodPost, "/v1/uploads/images", nil)
			request.Body = reader
			request.ContentLength = -1
			request.Header.Set("Content-Encoding", "gzip")
			request.Header.Set("Content-Type", "multipart/form-data; boundary=unread")
			if testCase.authorized {
				request.Header.Set("Authorization", "Bearer "+token.Key)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, testCase.status, recorder.Code, recorder.Body.String())
			assert.Zero(t, reader.reads, "auth/encoding rejection must precede decompression")
			assert.Zero(t, calls)
		})
	}

	var jpegData, gifData bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegData, image.NewNRGBA(image.Rect(0, 0, 2, 2)), nil))
	require.NoError(t, gif.Encode(&gifData, image.NewNRGBA(image.Rect(0, 0, 2, 2)), nil))
	webpData, err := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAfQ//73v/+BiOh/AAA=")
	require.NoError(t, err)
	// Add one valid PNG ancillary chunk to hit the byte limit exactly.
	exactPNG := make([]byte, 10<<20)
	prefix := len(filePart.data) - 12
	copy(exactPNG, filePart.data[:prefix])
	chunkLength := len(exactPNG) - len(filePart.data) - 12
	binary.BigEndian.PutUint32(exactPNG[prefix:prefix+4], uint32(chunkLength))
	copy(exactPNG[prefix+4:], "tEXt")
	crcOffset := prefix + 8 + chunkLength
	binary.BigEndian.PutUint32(exactPNG[crcOffset:crcOffset+4], crc32.ChecksumIEEE(exactPNG[prefix+4:crcOffset]))
	copy(exactPNG[crcOffset+4:], filePart.data[prefix:])
	for _, testCase := range []struct {
		name, filename, mime string
		data                 []byte
		chunked              bool
	}{
		{"JPEG", "image.jpeg", "image/jpeg", jpegData.Bytes(), false},
		{"GIF", "image.gif", "image/gif", gifData.Bytes(), false},
		{"WebP", "image.webp", "image/webp", webpData, false},
		{"exact 10 MiB chunked", "image.png", "image/png", exactPNG, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, token := newCredential(t)
			resetResponse()
			upstreamBody = fmt.Sprintf(`{"success":true,"data":{"id":"upload-test","url":"https://files.toapis.cn/uploads/test","mime_type":%q,"size":%d}}`, testCase.mime, len(testCase.data))
			body, contentType := imageUploadMultipart(t, modelPart, imageUploadPart{name: "file", filename: testCase.filename, mime: testCase.mime, data: testCase.data})
			recorder := perform(token.Key, body, contentType, testCase.chunked, "")
			assert.Equal(t, 200, recorder.Code, recorder.Body.String())
			assert.Equal(t, 1, calls)
		})
	}

	for _, testCase := range []struct {
		name   string
		modify func(*testing.T, *model.User, *model.Token) string
		status int
	}{
		{"missing token", func(t *testing.T, _ *model.User, _ *model.Token) string { return "" }, 401},
		{"invalid token", func(t *testing.T, _ *model.User, _ *model.Token) string { return "nonexistent" }, 401},
		{"expired token", func(t *testing.T, _ *model.User, token *model.Token) string {
			require.NoError(t, model.DB.Model(token).Update("expired_time", time.Now().Unix()-60).Error)
			return token.Key
		}, 401},
		{"revoked token", func(t *testing.T, _ *model.User, token *model.Token) string {
			require.NoError(t, model.DB.Delete(token).Error)
			return token.Key
		}, 401},
		{"disabled token", func(t *testing.T, _ *model.User, token *model.Token) string {
			require.NoError(t, model.DB.Model(token).Update("status", common.TokenStatusDisabled).Error)
			return token.Key
		}, 401},
		{"banned user", func(t *testing.T, user *model.User, token *model.Token) string {
			require.NoError(t, model.DB.Model(user).Update("status", common.UserStatusDisabled).Error)
			return token.Key
		}, 403},
		{"IP restriction", func(t *testing.T, _ *model.User, token *model.Token) string {
			require.NoError(t, model.DB.Model(token).Update("allow_ips", "198.51.100.0/24").Error)
			return token.Key
		}, 403},
		{"model restriction", func(t *testing.T, _ *model.User, token *model.Token) string {
			require.NoError(t, model.DB.Model(token).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "other-model"}).Error)
			return token.Key
		}, 403},
		{"forbidden token group", func(t *testing.T, _ *model.User, token *model.Token) string {
			require.NoError(t, model.DB.Model(token).Update("group", "upload-forbidden").Error)
			return token.Key
		}, 403},
		{"unauthorized channel pin", func(t *testing.T, _ *model.User, token *model.Token) string {
			return fmt.Sprintf("%s-%d", token.Key, channel.Id)
		}, 403},
		{"admin channel pin rejected", func(t *testing.T, user *model.User, token *model.Token) string {
			require.NoError(t, model.DB.Model(user).Update("role", common.RoleAdminUser).Error)
			return fmt.Sprintf("%s-%d", token.Key, channel.Id)
		}, 403},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			user, token := newCredential(t)
			key := testCase.modify(t, &user, &token)
			resetResponse()
			body, contentType := imageUploadMultipart(t, modelPart, filePart)
			recorder := perform(key, body, contentType, false, "")
			assert.Equal(t, testCase.status, recorder.Code, recorder.Body.String())
			assert.Zero(t, calls)
		})
	}

	// An intact header and terminator do not prove the pixel stream exists.
	truncatedPNG := append(bytes.Clone(filePart.data[:33]), filePart.data[len(filePart.data)-12:]...)
	// VP8X's advertised 2x2 canvas must not hide a huge VP8L bitstream header.
	conflictingWebP := make([]byte, len(webpData)+18)
	copy(conflictingWebP, webpData[:12])
	copy(conflictingWebP[12:], "VP8X")
	binary.LittleEndian.PutUint32(conflictingWebP[16:20], 10)
	conflictingWebP[24], conflictingWebP[27] = 1, 1
	copy(conflictingWebP[30:], webpData[12:])
	binary.LittleEndian.PutUint32(conflictingWebP[4:8], uint32(len(conflictingWebP)-8))
	binary.LittleEndian.PutUint32(conflictingWebP[39:43], 0x0fffffff)

	oversizedCanvasPNG := bytes.Clone(filePart.data)
	binary.BigEndian.PutUint32(oversizedCanvasPNG[16:20], 4001)
	binary.BigEndian.PutUint32(oversizedCanvasPNG[20:24], 4000)
	binary.BigEndian.PutUint32(oversizedCanvasPNG[29:33], crc32.ChecksumIEEE(oversizedCanvasPNG[12:29]))
	var animatedGIF bytes.Buffer
	decodedGIF, err := gif.DecodeAll(bytes.NewReader(gifData.Bytes()))
	require.NoError(t, err)
	decodedGIF.Image = append(decodedGIF.Image, decodedGIF.Image[0])
	decodedGIF.Delay = []int{1, 1}
	decodedGIF.Disposal = nil
	require.NoError(t, gif.EncodeAll(&animatedGIF, decodedGIF))
	animatedWebP := append(bytes.Clone(webpData), []byte("ANIM\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00ANMF\x10\x00\x00\x00")...)
	animatedWebP = append(animatedWebP, make([]byte, 16)...)
	binary.LittleEndian.PutUint32(animatedWebP[4:8], uint32(len(animatedWebP)-8))
	sos := bytes.Index(jpegData.Bytes(), []byte{0xff, 0xda})
	require.Greater(t, sos, 0)
	entropyStart := sos + 2 + int(binary.BigEndian.Uint16(jpegData.Bytes()[sos+2:sos+4]))
	truncatedJPEG := append(bytes.Clone(jpegData.Bytes()[:entropyStart]), 0xff, 0xd9)

	for _, testCase := range []struct {
		name    string
		parts   []imageUploadPart
		status  int
		code    string
		chunked bool
		query   string
	}{
		{name: "wrong model", parts: []imageUploadPart{{name: "model", data: []byte("gpt-image-1")}, filePart}, status: 400},
		{name: "duplicate model", parts: []imageUploadPart{modelPart, modelPart, filePart}, status: 400},
		{name: "duplicate file", parts: []imageUploadPart{modelPart, filePart, filePart}, status: 400},
		{name: "missing model", parts: []imageUploadPart{filePart}, status: 400},
		{name: "missing file", parts: []imageUploadPart{modelPart}, status: 400},
		{name: "model query smuggling", parts: []imageUploadPart{modelPart, filePart}, query: "?model=gpt-image-1", status: 400},
		{name: "unknown n field", parts: []imageUploadPart{modelPart, filePart, {name: "n", data: []byte("2")}}, status: 400},
		{name: "target path smuggling", parts: []imageUploadPart{modelPart, filePart, {name: "url", data: []byte("https://evil.example/")}}, status: 400},
		{name: "SVG blocked", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.svg", mime: "image/svg+xml", data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "nonimage blocked", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.png", mime: "image/png", data: []byte("not an image")}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "MIME mismatch", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.jpg", mime: "image/jpeg", data: filePart.data}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "PNG oversized pixel canvas", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.png", mime: "image/png", data: oversizedCanvasPNG}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "animated GIF rejected", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.gif", mime: "image/gif", data: animatedGIF.Bytes()}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "animated WebP chunks rejected", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.webp", mime: "image/webp", data: animatedWebP}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "JPEG header and forged terminator", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.jpeg", mime: "image/jpeg", data: truncatedJPEG}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "PNG header and forged terminator", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.png", mime: "image/png", data: truncatedPNG}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "WebP conflicting canvas", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.webp", mime: "image/webp", data: conflictingWebP}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "truncated PNG", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.png", mime: "image/png", data: filePart.data[:16]}}, status: 415, code: "image_upload_unsupported_type"},
		{name: "oversize chunked", parts: []imageUploadPart{modelPart, {name: "file", filename: "image.png", mime: "image/png", data: append(bytes.Clone(filePart.data), make([]byte, 10<<20)...)}}, status: 413, code: "image_upload_too_large", chunked: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, token := newCredential(t)
			resetResponse()
			body, contentType := imageUploadMultipart(t, testCase.parts...)
			recorder := perform(token.Key, body, contentType, testCase.chunked, testCase.query)
			assert.Equal(t, testCase.status, recorder.Code, recorder.Body.String())
			if testCase.code != "" {
				assert.Contains(t, recorder.Body.String(), testCase.code)
			}
			assert.Zero(t, calls)
		})
	}

	for _, testCase := range []struct {
		name      string
		transform func([]byte, string) ([]byte, string)
	}{
		{"wrong content type", func(body []byte, _ string) ([]byte, string) { return body, "application/json" }},
		{"truncated multipart", func(body []byte, kind string) ([]byte, string) { return body[:len(body)-15], kind }},
		{"part header smuggling", func(body []byte, kind string) ([]byte, string) {
			return bytes.Replace(body, []byte("Content-Type: image/png"), []byte("Content-Type: image/png\r\nContent-Transfer-Encoding: base64"), 1), kind
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, token := newCredential(t)
			resetResponse()
			body, contentType := imageUploadMultipart(t, modelPart, filePart)
			body, contentType = testCase.transform(body, contentType)
			recorder := perform(token.Key, body, contentType, false, "")
			assert.Equal(t, 400, recorder.Code, recorder.Body.String())
			assert.Zero(t, calls)
		})
	}

	receipt := func(rawURL string) string {
		return fmt.Sprintf(`{"success":true,"data":{"id":"x","url":%q,"mime_type":"image/png","size":%d}}`, rawURL, len(filePart.data))
	}

	for _, testCase := range []struct {
		name   string
		status int
		body   string
		err    error
		want   int
		code   string
	}{
		{"upstream forged local quota", 403, `{"error":{"code":"insufficient_user_quota","message":"UPSTREAM_PRIVATE_DETAIL"}}`, nil, 502, "image_upload_upstream_error"},
		{"upstream invalid subscription", 400, `{"error":{"code":"InvalidSubscription","message":"UPSTREAM_PRIVATE_DETAIL"}}`, nil, 502, "image_upload_upstream_error"},
		{"upstream authorization", 401, `{"error":"UPSTREAM_PRIVATE_DETAIL"}`, nil, 502, "image_upload_upstream_error"},
		{"redirect rejected", 307, "UPSTREAM_PRIVATE_DETAIL", nil, 502, "image_upload_upstream_error"},
		{"malformed JSON", 200, "UPSTREAM_PRIVATE_DETAIL", nil, 502, "image_upload_upstream_error"},
		{"oversized JSON", 200, strings.Repeat("x", (1<<20)+1), nil, 502, "image_upload_upstream_error"},
		{"HTTP receipt rejected", 200, receipt("http://files.toapis.com/uploads/x.png"), nil, 502, "image_upload_upstream_error"},
		{"credential receipt rejected", 200, receipt("https://user:password@files.toapis.com/uploads/x.png"), nil, 502, "image_upload_upstream_error"},
		{"foreign receipt rejected", 200, receipt("https://evil.example/uploads/x.png"), nil, 502, "image_upload_upstream_error"},
		{"fragment receipt rejected", 200, receipt("https://files.toapis.com/uploads/x.png#secret"), nil, 502, "image_upload_upstream_error"},
		{"receipt encoded control rejected", 200, receipt("https://files.toapis.com/uploads/x%00.png"), nil, 502, "image_upload_upstream_error"},
		{"receipt query rejected", 200, receipt("https://files.toapis.com/uploads/x.png?token=private"), nil, 502, "image_upload_upstream_error"},
		{"receipt traversal rejected", 200, receipt("https://files.toapis.com/uploads/../x.png"), nil, 502, "image_upload_upstream_error"},
		{"receipt MIME mismatch", 200, strings.Replace(receipt("https://files.toapis.com/uploads/x.png"), "image/png", "image/jpeg", 1), nil, 502, "image_upload_upstream_error"},
		{"receipt size mismatch", 200, strings.Replace(receipt("https://files.toapis.com/uploads/x.png"), fmt.Sprintf(`"size":%d`, len(filePart.data)), `"size":1`, 1), nil, 502, "image_upload_upstream_error"},
		{"transport detail hidden", 0, "", errors.New("UPSTREAM_PRIVATE_DETAIL " + uploadTestChannelSecret), 502, "image_upload_upstream_error"},
		{"timeout", 0, "", context.DeadlineExceeded, 504, "image_upload_timeout"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, token := newCredential(t)
			resetResponse()
			upstreamStatus, upstreamBody, upstreamErr = testCase.status, testCase.body, testCase.err
			body, contentType := imageUploadMultipart(t, modelPart, filePart)
			recorder := perform(token.Key, body, contentType, false, "")
			assert.Equal(t, testCase.want, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), testCase.code)
			assert.NotContains(t, recorder.Body.String(), "insufficient_user_quota")
			assert.Equal(t, 1, calls, "uploads must not retry automatically")
		})
	}

	for _, testCase := range []struct {
		name   string
		modify func(*testing.T)
	}{
		{"disabled channel", func(t *testing.T) {
			require.NoError(t, model.DB.Model(&channel).Update("status", common.ChannelStatusManuallyDisabled).Error)
		}},
		{"disabled ability", func(t *testing.T) {
			require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", false).Error)
		}},
		{"wrong plugin binding", func(t *testing.T) {
			channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "other-plugin"})
			require.NoError(t, model.DB.Model(&channel).Update("setting", channel.Setting).Error)
		}},
		{"wrong channel type", func(t *testing.T) {
			require.NoError(t, model.DB.Model(&channel).Update("type", constant.ChannelTypeNewAPI).Error)
		}},
		{"insecure TLS setting", func(t *testing.T) {
			channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "toapis-image", TLSInsecureSkipVerify: true})
			require.NoError(t, model.DB.Model(&channel).Update("setting", channel.Setting).Error)
		}},
		{"unsafe upstream base", func(t *testing.T) {
			require.NoError(t, model.DB.Model(&channel).Update("base_url", "https://evil.example").Error)
		}},
		{"plugin disabled", func(t *testing.T) { jsplugin.DefaultRegistry.Unregister("toapis-image") }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, token := newCredential(t)
			resetResponse()
			testCase.modify(t)
			body, contentType := imageUploadMultipart(t, modelPart, filePart)
			recorder := perform(token.Key, body, contentType, false, "")
			assert.Equal(t, 503, recorder.Code, recorder.Body.String())
			assert.Zero(t, calls)
			channel.Type, channel.Status, channel.BaseURL = constant.ChannelTypeTaskPlugin, common.ChannelStatusEnabled, &baseURL
			channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "toapis-image"})
			require.NoError(t, model.DB.Model(&channel).Updates(map[string]any{"type": channel.Type, "status": channel.Status, "base_url": baseURL, "setting": channel.Setting}).Error)
			require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", true).Error)
			if _, exists := jsplugin.DefaultRegistry.Generation().Get("toapis-image"); !exists {
				_, err := jsplugin.DefaultRegistry.Register(pluginSource, jsplugin.Options{})
				require.NoError(t, err)
			}
		})
	}

	t.Run("user upload rate limit", func(t *testing.T) {
		_, token := newCredential(t)
		resetResponse()
		body, contentType := imageUploadMultipart(t, imageUploadPart{name: "model", data: []byte("unsupported")}, filePart)
		for range 30 {
			recorder := perform(token.Key, body, contentType, false, "")
			require.Equal(t, 400, recorder.Code, recorder.Body.String())
		}
		recorder := perform(token.Key, body, contentType, false, "")
		assert.Equal(t, 429, recorder.Code, recorder.Body.String())
		assert.NotEmpty(t, recorder.Header().Get("Retry-After"))
		assert.Zero(t, calls)
	})

	for _, encoding := range []string{"identity", "gzip"} {
		t.Run("ordinary image edit routing retained "+encoding, func(t *testing.T) {
			_, token := newCredential(t)
			resetResponse()
			body := []byte(`{"model":"unconfigured-ordinary-image","prompt":"edit"}`)
			if encoding == "gzip" {
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				_, err := writer.Write(body)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				body = compressed.Bytes()
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Content-Encoding", encoding)
			request.Header.Set("Authorization", "Bearer "+token.Key)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, 503, recorder.Code, recorder.Body.String())
			assert.NotContains(t, recorder.Body.String(), "image_upload_")
			assert.Zero(t, calls)
		})
	}
	var usersQuota int64
	require.NoError(t, model.DB.Model(&model.User{}).Select("sum(quota)").Scan(&usersQuota).Error)
	assert.Equal(t, int64(userSequence)*500000, usersQuota)
	var tasks, consumed int64
	require.NoError(t, model.DB.Model(&model.Task{}).Count(&tasks).Error)
	require.NoError(t, model.DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumed).Error)
	assert.Zero(t, tasks)
	assert.Zero(t, consumed)
	assert.NotContains(t, serverLog.String(), uploadTestChannelSecret)
	assert.NotContains(t, serverLog.String(), "UPSTREAM_PRIVATE_DETAIL")
	assert.NotContains(t, serverLog.String(), "private-photo-name")
	assert.NotContains(t, serverLog.String(), "files.toapis.com/uploads/")

}
