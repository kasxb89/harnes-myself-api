package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// callLogHandler runs a log handler as the given user and returns the JSON
// lines of the response.
func callLogHandler(t *testing.T, handler gin.HandlerFunc, userId int, role int, query string) []map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/log?"+query, nil)
	ctx.Set("id", userId)
	ctx.Set("role", role)
	handler(ctx)
	var lines []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(recorder.Body.String()), "\n") {
		var value map[string]any
		require.NoError(t, common.UnmarshalJsonStr(line, &value))
		lines = append(lines, value)
	}
	return lines
}

func exportedRequestIds(lines []map[string]any) []any {
	ids := make([]any, 0, len(lines))
	for _, line := range lines[:len(lines)-1] {
		ids = append(ids, line["request_id"])
	}
	return ids
}

// seedExportLogs stores logs of two users: three in the last hour and one
// from 400 days ago.
func seedExportLogs(t *testing.T, mainDB *gorm.DB, logDB *gorm.DB, now int64) {
	t.Helper()
	require.NoError(t, mainDB.Create(&model.Channel{Id: 9, Name: "primary"}).Error)
	logs := []model.Log{
		{UserId: 1, Username: "alice", CreatedAt: now - 3600, Type: model.LogTypeConsume, ModelName: "gpt-a", ChannelId: 9, Quota: 10, RequestId: "a-1", Other: `{"admin_info":{"use_channel":["9"]}}`},
		{UserId: 2, Username: "bob", CreatedAt: now - 1800, Type: model.LogTypeConsume, ModelName: "gpt-b", ChannelId: 9, Quota: 20, RequestId: "b-1"},
		{UserId: 1, Username: "alice", CreatedAt: now - 60, Type: model.LogTypeError, ModelName: "gpt-a", RequestId: "a-2", UpstreamRequestId: "upstream-private",
			Content: `400 reasoning_content missing; Authorization: Bearer opaque-provider-secret`,
			Other:   `{"status_code":400,"admin_info":{"api_key":"opaque-provider-secret","diagnostic":"upstream rejected request"},"root_info":{"diagnostic":"root trace","password":"opaque-root-secret"}}`,
		},
		{UserId: 1, Username: "alice", CreatedAt: now - 400*24*3600, Type: model.LogTypeConsume, ModelName: "gpt-a", RequestId: "a-old"},
	}
	for i := range logs {
		require.NoError(t, logDB.Create(&logs[i]).Error)
	}
}

func useLogExportDatabases(t *testing.T, mainDB *gorm.DB, logDB *gorm.DB, mainType common.DatabaseType, logType common.DatabaseType) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousMemory := common.RedisEnabled, common.MemoryCacheEnabled
	model.DB, model.LOG_DB = mainDB, logDB
	common.SetDatabaseTypes(mainType, logType)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemory
	})
}

func TestLogExportDatabaseMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	for _, tc := range []struct {
		kind, env string
		typ       common.DatabaseType
	}{
		{"sqlite", "", common.DatabaseTypeSQLite},
		{"mysql", "AUDIT_MYSQL_DSN", common.DatabaseTypeMySQL},
		{"postgres", "AUDIT_POSTGRES_DSN", common.DatabaseTypePostgreSQL},
		{"clickhouse", "AUDIT_CLICKHOUSE_DSN", common.DatabaseTypeClickHouse},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if tc.env != "" && dsn == "" {
				t.Skip(tc.env + " is not configured")
			}
			db, isolatedDSN := newAuditTestDatabase(t, tc.kind, dsn)
			mainDB, mainType := db, tc.typ
			if tc.kind == "clickhouse" {
				mainDB, _ = newAuditTestDatabase(t, "sqlite", "")
				mainType = common.DatabaseTypeSQLite
			}
			useLogExportDatabases(t, mainDB, db, mainType, tc.typ)
			require.NoError(t, mainDB.AutoMigrate(&model.Channel{}))
			if tc.kind == "clickhouse" {
				t.Setenv("LOG_SQL_DSN", isolatedDSN)
				require.NoError(t, model.InitLogDB())
			} else {
				require.NoError(t, db.AutoMigrate(&model.Log{}))
			}
			versionSQL := "SELECT version()"
			if tc.kind == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, model.LOG_DB.Raw(versionSQL).Scan(&version).Error)
			t.Logf("log database: %s %s", tc.kind, version)
			now := time.Now().Unix()
			seedExportLogs(t, mainDB, model.LOG_DB, now)
			query := fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-2*24*3600, now+3600)

			listed := callLogHandler(t, GetUserLogs, 1, common.RoleCommonUser, query)[0]
			require.Equal(t, true, listed["success"])
			assert.EqualValues(t, 2, listed["data"].(map[string]any)["total"])
			userError := listed["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
			assert.Equal(t, "Server is busy. Please try again later", userError["content"])
			assert.Empty(t, userError["upstream_request_id"])
			listed = callLogHandler(t, GetAllLogs, 3, common.RoleAdminUser, query+"&username=alice")[0]
			require.Equal(t, true, listed["success"])
			assert.EqualValues(t, 2, listed["data"].(map[string]any)["total"])

			own := callLogHandler(t, ExportUserLogs, 1, common.RoleCommonUser, query)
			require.Len(t, own, 3)
			assert.Equal(t, []any{"a-2", "a-1"}, exportedRequestIds(own))
			assert.Equal(t, map[string]any{"success": true, "message": "", "data": map[string]any{"count": float64(2)}}, own[2])
			assert.Equal(t, float64(1), own[0]["id"], "user exports carry list positions, not row ids")
			assert.Equal(t, float64(2), own[1]["id"])
			assert.Empty(t, own[1]["channel_name"])
			assert.NotContains(t, own[1]["other"], "admin_info")
			assert.Equal(t, userError["content"], own[0]["content"])
			assert.Equal(t, userError["other"], own[0]["other"])
			assert.Empty(t, own[0]["upstream_request_id"])
			assert.NotContains(t, own[0]["other"], "reasoning_content")

			all := callLogHandler(t, ExportAllLogs, 3, common.RoleAdminUser, query+"&model_name=gpt-%25")
			require.Len(t, all, 4)
			assert.Equal(t, []any{"a-2", "b-1", "a-1"}, exportedRequestIds(all))
			assert.Equal(t, "primary", all[2]["channel_name"])
			assert.Contains(t, all[2]["other"], "admin_info")
			assert.Equal(t, true, all[3]["success"])
			for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
				listed := callLogHandler(t, GetAllLogs, 3, role, query+"&type=5")[0]
				require.Equal(t, true, listed["success"])
				errorLog := listed["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
				exported := callLogHandler(t, ExportAllLogs, 3, role, query+"&type=5")
				require.Len(t, exported, 2)
				assert.Equal(t, errorLog["content"], exported[0]["content"])
				assert.Equal(t, errorLog["other"], exported[0]["other"])
				assert.Contains(t, exported[0]["content"], "reasoning_content missing")
				assert.Contains(t, exported[0]["other"], "upstream rejected request")
				assert.NotContains(t, exported[0]["content"], "opaque-provider-secret")
				assert.NotContains(t, exported[0]["other"], "opaque-provider-secret")
				assert.NotContains(t, exported[0]["other"], "opaque-root-secret")
				if role < common.RoleRootUser {
					assert.NotContains(t, exported[0]["other"], "root trace")
				} else {
					assert.Contains(t, exported[0]["other"], "root trace")
				}
			}
		})
	}
}

func TestLogExportRules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, db, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Log{}, &model.QuotaData{}))
	now := time.Now().Unix()
	seedExportLogs(t, db, db, now)
	logSetting := operation_setting.GetLogSetting()
	previousSetting := *logSetting
	t.Cleanup(func() { *logSetting = previousSetting })
	recent := fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-2*24*3600, now)
	tooLong := fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-366*24*3600, now)

	t.Run("time range", func(t *testing.T) {
		rejected := callLogHandler(t, ExportAllLogs, 3, common.RoleAdminUser, tooLong)
		require.Len(t, rejected, 1)
		assert.Equal(t, false, rejected[0]["success"])
		assert.Equal(t, "The time range cannot exceed {{days}} days", rejected[0]["message_key"])
		assert.Equal(t, map[string]any{"days": float64(365)}, rejected[0]["message_params"])

		assert.Equal(t, false, callLogHandler(t, GetUserLogs, 1, common.RoleCommonUser, tooLong)[0]["success"])
		assert.Equal(t, false, callLogHandler(t, GetLogsSelfStat, 1, common.RoleCommonUser, tooLong)[0]["success"])
		assert.Equal(t, true, callLogHandler(t, GetUserLogs, 3, common.RoleAdminUser, tooLong)[0]["success"], "administrators search their own logs without a limit")

		// The dashboard follows the same range instead of its old one-month limit.
		fortyDays := fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-40*24*3600, now)
		for _, handler := range []gin.HandlerFunc{GetUserQuotaDates, GetUserFlowQuotaDates} {
			assert.Equal(t, "The time range cannot exceed {{days}} days", callLogHandler(t, handler, 1, common.RoleCommonUser, tooLong)[0]["message_key"])
			assert.Equal(t, true, callLogHandler(t, handler, 1, common.RoleCommonUser, fortyDays)[0]["success"])
		}

		// Without a start, a user's search reaches back the whole range.
		listed := callLogHandler(t, GetUserLogs, 1, common.RoleCommonUser, "")[0]
		assert.Equal(t, true, listed["success"])
		assert.EqualValues(t, 2, listed["data"].(map[string]any)["total"])

		logSetting.MaxRangeDays = 400
		assert.Equal(t, true, callLogHandler(t, GetUserLogs, 1, common.RoleCommonUser, tooLong)[0]["success"])
		logSetting.MaxRangeDays = previousSetting.MaxRangeDays
	})

	t.Run("user export switch", func(t *testing.T) {
		logSetting.UserExportEnabled = false
		t.Cleanup(func() { logSetting.UserExportEnabled = previousSetting.UserExportEnabled })
		disabled := callLogHandler(t, ExportUserLogs, 1, common.RoleCommonUser, recent)
		require.Len(t, disabled, 1)
		assert.Equal(t, "Log export is disabled", disabled[0]["message_key"])
		assert.Equal(t, true, callLogHandler(t, ExportUserLogs, 3, common.RoleAdminUser, recent)[0]["success"], "administrators export their own logs even when users cannot")
	})

	t.Run("one export per user", func(t *testing.T) {
		release, ok, err := service.AcquireLogExportLock(1)
		require.NoError(t, err)
		require.True(t, ok)
		busy := callLogHandler(t, ExportUserLogs, 1, common.RoleCommonUser, recent)
		require.Len(t, busy, 1)
		assert.Equal(t, "Another log export is running. Try again after it finishes.", busy[0]["message_key"])
		other := callLogHandler(t, ExportUserLogs, 2, common.RoleCommonUser, recent)
		assert.Equal(t, true, other[len(other)-1]["success"], "other users are not blocked")
		release()
		assert.Len(t, callLogHandler(t, ExportUserLogs, 1, common.RoleCommonUser, recent), 3)
	})
}

func callReceiptBatchHandler(t *testing.T, userID, role int, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/log/self/batch?user_id=2", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("New-Api-User", "2")
	ctx.Request.Header.Set("X-User-Id", "2")
	ctx.Set("id", userID)
	ctx.Set("role", role)
	GetUserLogReceipts(ctx)
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func TestUserLogReceiptsDatabaseMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	for _, tc := range []struct {
		kind, env string
		typ       common.DatabaseType
	}{
		{"sqlite", "", common.DatabaseTypeSQLite},
		{"mysql", "AUDIT_MYSQL_DSN", common.DatabaseTypeMySQL},
		{"postgres", "AUDIT_POSTGRES_DSN", common.DatabaseTypePostgreSQL},
		{"clickhouse", "AUDIT_CLICKHOUSE_DSN", common.DatabaseTypeClickHouse},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if tc.env != "" && dsn == "" {
				t.Skip(tc.env + " is not configured")
			}
			logDB, isolatedDSN := newAuditTestDatabase(t, tc.kind, dsn)
			mainDB, mainType := logDB, tc.typ
			if tc.kind == "clickhouse" {
				mainDB, _ = newAuditTestDatabase(t, "sqlite", "")
				mainType = common.DatabaseTypeSQLite
			}
			useLogExportDatabases(t, mainDB, logDB, mainType, tc.typ)
			require.NoError(t, mainDB.AutoMigrate(&model.Channel{}))
			if tc.kind == "clickhouse" {
				t.Setenv("LOG_SQL_DSN", isolatedDSN)
				require.NoError(t, model.InitLogDB())
			} else {
				require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			}
			versionSQL := "SELECT version()"
			if tc.kind == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, model.LOG_DB.Raw(versionSQL).Scan(&version).Error)
			t.Logf("receipt log database: %s %s", tc.kind, version)
			now := time.Now().Unix()
			seedExportLogs(t, mainDB, model.LOG_DB, now)
			for _, log := range []model.Log{
				{UserId: 1, CreatedAt: now - 20, Type: model.LogTypeConsume, RequestId: "a-1", ModelName: "gpt-a", Quota: 15, PromptTokens: 12, CompletionTokens: 34, UseTime: 2,
					Content: "private-content", Ip: "192.0.2.1", TokenName: "private-token", ChannelId: 9, UpstreamRequestId: "private-upstream", Other: `{"admin_info":{"secret":"private-secret"}}`},
				{UserId: 2, CreatedAt: now - 10, Type: model.LogTypeConsume, RequestId: "a-1", ModelName: "other-user", Quota: 999},
				{UserId: 1, CreatedAt: now - 5, Type: model.LogTypeRefund, RequestId: "a-1", ModelName: "gpt-a", Quota: 15},
				{UserId: 1, CreatedAt: now - 4, Type: model.LogTypeError, RequestId: "a-1", Content: "private-error"},
				{UserId: 1, CreatedAt: now - 3, Type: model.LogTypeClientError, RequestId: "client-error", Content: "private-client-error"},
				{UserId: 1, CreatedAt: now - 2, Type: model.LogTypeConsume, RequestId: "zero", ModelName: "free", Quota: 0},
				{UserId: 2, CreatedAt: now - 1, Type: model.LogTypeConsume, RequestId: "' OR 1=1 --", Quota: 999},
			} {
				require.NoError(t, model.LOG_DB.Create(&log).Error)
			}

			t.Run("ordered unique own consumption and unknown financial state", func(t *testing.T) {
				recorder, response := callReceiptBatchHandler(t, 1, common.RoleCommonUser,
					`{"request_ids":["missing","a-1","b-1","a-1","zero","a-2","client-error","' OR 1=1 --"]}`)
				require.Equal(t, http.StatusOK, recorder.Code)
				assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
				require.Equal(t, true, response["success"])
				data := response["data"].(map[string]any)
				assert.EqualValues(t, 1, data["schema_version"])
				assert.InDelta(t, now-operation_setting.GetLogSetting().MaxRangeSeconds(), data["lookup_window_start_unix"], 5)
				items := data["items"].([]any)
				require.Len(t, items, 7)
				for i, expectedID := range []string{"missing", "a-1", "b-1", "zero", "a-2", "client-error", "' OR 1=1 --"} {
					item := items[i].(map[string]any)
					assert.Equal(t, expectedID, item["request_id"])
					assert.Equal(t, "unknown", item["settlement_status"])
					assert.Equal(t, "unknown", item["refund_status"])
					if i != 1 && i != 3 {
						assert.Equal(t, "not_found", item["status"])
						assert.Equal(t, []any{}, item["consume_logs"])
					}
				}
				found := items[1].(map[string]any)
				assert.Equal(t, "found", found["status"])
				rows := found["consume_logs"].([]any)
				require.Len(t, rows, 2)
				assert.Equal(t, map[string]any{"request_id": "a-1", "model_name": "gpt-a", "quota": float64(15), "prompt_tokens": float64(12),
					"completion_tokens": float64(34), "created_at": float64(now - 20), "use_time": float64(2)}, rows[0])
				assert.EqualValues(t, 10, rows[1].(map[string]any)["quota"])
				zero := items[3].(map[string]any)
				assert.Equal(t, "found", zero["status"])
				assert.EqualValues(t, 0, zero["consume_logs"].([]any)[0].(map[string]any)["quota"])
				assert.NotContains(t, recorder.Body.String(), "private-")
				assert.NotContains(t, recorder.Body.String(), "other-user")
			})

			t.Run("same history window and old GET contract", func(t *testing.T) {
				_, response := callReceiptBatchHandler(t, 1, common.RoleCommonUser, `{"request_ids":["a-old"]}`)
				assert.Equal(t, "not_found", response["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["status"])
				_, response = callReceiptBatchHandler(t, 1, common.RoleRootUser, `{"request_ids":["a-old","b-1"]}`)
				data := response["data"].(map[string]any)
				assert.EqualValues(t, 0, data["lookup_window_start_unix"])
				items := data["items"].([]any)
				assert.Equal(t, "found", items[0].(map[string]any)["status"])
				assert.Equal(t, "not_found", items[1].(map[string]any)["status"], "root remains limited to its own user id")
				listed := callLogHandler(t, GetUserLogs, 1, common.RoleCommonUser, "type=2&request_id=a-1")[0]
				require.Equal(t, true, listed["success"])
				assert.EqualValues(t, 2, listed["data"].(map[string]any)["total"])
				assert.Len(t, listed["data"].(map[string]any)["items"], 2)
			})

			t.Run("bounded complete results", func(t *testing.T) {
				logs := make([]model.Log, model.MaxReceiptBatchLogRows)
				for i := range logs {
					logs[i] = model.Log{UserId: 1, CreatedAt: now, Type: model.LogTypeConsume, RequestId: "many", ModelName: "gpt-a", Quota: 1}
				}
				// A single insert avoids emulated multi-batch transactions in a
				// separate ClickHouse log store, which has no SQL transactions.
				require.NoError(t, model.LOG_DB.Create(&logs).Error)
				recorder, response := callReceiptBatchHandler(t, 1, common.RoleCommonUser, `{"request_ids":["many"]}`)
				require.Equal(t, http.StatusOK, recorder.Code)
				assert.Len(t, response["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["consume_logs"], model.MaxReceiptBatchLogRows)
				require.NoError(t, model.LOG_DB.Create(&model.Log{UserId: 1, CreatedAt: now, Type: model.LogTypeConsume, RequestId: "many", Quota: 1}).Error)
				recorder, response = callReceiptBatchHandler(t, 1, common.RoleCommonUser, `{"request_ids":["many","missing"]}`)
				assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
				assert.Equal(t, false, response["success"])
				assert.Equal(t, "RECEIPT_RESULT_LIMIT_EXCEEDED", response["code"])
				assert.NotContains(t, response, "data", "overflow never publishes partial or false missing results")
			})
		})
	}
}

func TestUserLogReceiptBatchValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, db, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	for _, tc := range []struct {
		name, body string
	}{
		{"malformed", `{"request_ids":`},
		{"trailing value", `{"request_ids":["a"]} {}`},
		{"array root", `["a"]`},
		{"null root", `null`},
		{"missing ids", `{}`},
		{"unknown field", `{"request_ids":["a"],"extra":true}`},
		{"user selection", `{"request_ids":["a"],"user_id":2}`},
		{"null ids", `{"request_ids":null}`},
		{"empty ids", `{"request_ids":[]}`},
		{"wrong ids type", `{"request_ids":"a"}`},
		{"nonstring id", `{"request_ids":[1]}`},
		{"empty id", `{"request_ids":[""]}`},
		{"space id", `{"request_ids":[" a"]}`},
		{"unicode trailing space", `{"request_ids":["a\u3000"]}`},
		{"newline id", `{"request_ids":["a\nb"]}`},
		{"control id", `{"request_ids":["a\u0000b"]}`},
		{"nonprinting id", `{"request_ids":["a\u200bb"]}`},
		{"long id", `{"request_ids":["` + strings.Repeat("a", 65) + `"]}`},
		{"byte rather than rune limit", `{"request_ids":["` + strings.Repeat("你", 22) + `"]}`},
		{"invalid UTF8", string(append([]byte(`{"request_ids":["`), append([]byte{0xff}, []byte(`"]}`)...)...))},
		{"too many duplicate input positions", `{"request_ids":[` + strings.Repeat(`"a",`, 100) + `"a"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder, response := callReceiptBatchHandler(t, 1, common.RoleCommonUser, tc.body)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Equal(t, false, response["success"])
			assert.Equal(t, "RECEIPT_INVALID_REQUEST", response["code"])
			assert.NotContains(t, response, "data")
		})
	}
	t.Run("maximum input count and byte length", func(t *testing.T) {
		uniqueIDs := make([]string, model.MaxReceiptBatchRequestIDs)
		for i := range uniqueIDs {
			uniqueIDs[i] = fmt.Sprintf("request-%d", i)
		}
		encoded, err := common.Marshal(gin.H{"request_ids": uniqueIDs})
		require.NoError(t, err)
		recorder, response := callReceiptBatchHandler(t, 1, common.RoleCommonUser, string(encoded))
		require.Equal(t, http.StatusOK, recorder.Code)
		items := response["data"].(map[string]any)["items"].([]any)
		require.Len(t, items, model.MaxReceiptBatchRequestIDs)
		for i, item := range items {
			assert.Equal(t, uniqueIDs[i], item.(map[string]any)["request_id"])
		}
		body := `{"request_ids":[` + strings.Repeat(`"a",`, 99) + `"a"]}`
		recorder, response = callReceiptBatchHandler(t, 1, common.RoleCommonUser, body)
		require.Equal(t, http.StatusOK, recorder.Code)
		assert.Len(t, response["data"].(map[string]any)["items"], 1)
		body = `{"request_ids":["` + strings.Repeat("a", 64) + `","请求-一"]}`
		body += strings.Repeat(" ", model.MaxReceiptBatchBodyBytes-len(body))
		recorder, _ = callReceiptBatchHandler(t, 1, common.RoleCommonUser, body)
		assert.Equal(t, http.StatusOK, recorder.Code)
		recorder, response = callReceiptBatchHandler(t, 1, common.RoleCommonUser, body+" ")
		assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
		assert.Equal(t, "RECEIPT_BODY_TOO_LARGE", response["code"])
	})
	t.Run("authentication defense", func(t *testing.T) {
		recorder, response := callReceiptBatchHandler(t, 0, common.RoleCommonUser, `{"request_ids":["a"]}`)
		assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		assert.Equal(t, "RECEIPT_AUTH_REQUIRED", response["code"])
	})
	t.Run("JSON content type required", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/log/self/batch", strings.NewReader(`{"request_ids":["a"]}`))
		ctx.Request.Header.Set("Content-Type", "text/plain")
		ctx.Set("id", 1)
		GetUserLogReceipts(ctx)
		assert.Equal(t, http.StatusBadRequest, recorder.Code)
	})
	t.Run("database error stays safe", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		requestContext, cancel := context.WithCancel(context.Background())
		cancel()
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/log/self/batch", strings.NewReader(`{"request_ids":["a"]}`)).WithContext(requestContext)
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Set("id", 1)
		GetUserLogReceipts(ctx)
		assert.Equal(t, http.StatusInternalServerError, recorder.Code)
		var response map[string]any
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.Equal(t, "RECEIPT_QUERY_FAILED", response["code"])
		assert.Equal(t, "Failed to query logs", response["message"])
		assert.NotContains(t, recorder.Body.String(), "SELECT")
		assert.NotContains(t, recorder.Body.String(), "context canceled")
	})
}

func TestAcquireLogExportLock(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%v", useRedis), func(t *testing.T) {
			previousRedis, previousClient := common.RedisEnabled, common.RDB
			common.RedisEnabled = useRedis
			if useRedis {
				server := miniredis.RunT(t)
				common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
				require.NoError(t, common.RDB.Ping(context.Background()).Err())
			}
			t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })

			release, ok, err := service.AcquireLogExportLock(1)
			require.NoError(t, err)
			require.True(t, ok)
			_, ok, err = service.AcquireLogExportLock(1)
			require.NoError(t, err)
			assert.False(t, ok, "a second export of the same user must wait")
			releaseOther, ok, err := service.AcquireLogExportLock(2)
			require.NoError(t, err)
			assert.True(t, ok)
			releaseOther()
			release()
			releaseAgain, ok, err := service.AcquireLogExportLock(1)
			require.NoError(t, err)
			assert.True(t, ok, "a finished export frees the user")
			releaseAgain()
		})
	}
}
