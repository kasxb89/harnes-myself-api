package service

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWalletMediaRefundConfirmationUsesDurableOriginalFundingWithRedis(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.MediaAdmission{}, &model.MediaPool{}))
	useBillingConfirmationRedis(t)

	const userID = 918011
	seedUser(t, userID, 0)
	require.NoError(t, model.CreditUserGiftQuota(userID, 100, "synthetic-refund-redis"))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{UserId: userID, RequestId: "synthetic-original-redis-refund", IsPlayground: true, ForcePreConsume: true, ImageRequestCount: 1}
	session, apiErr := NewBillingSession(c, info, 50)
	require.Nil(t, apiErr)
	task := &model.Task{TaskID: "synthetic-original-media", UserId: userID, ChannelId: 42, Quota: 50, Status: model.TaskStatusUnknown, PrivateData: model.TaskPrivateData{MediaAdmissionID: "synthetic-original-admission"}}
	require.NoError(t, model.DB.Create(task).Error)
	admission := &model.MediaAdmission{ID: task.PrivateData.MediaAdmissionID, TaskDBID: task.ID, ChannelID: task.ChannelId, State: model.MediaReleased, BillingState: "refund_pending"}
	require.NoError(t, model.DB.Create(admission).Error)
	submission := &MediaSubmission{Task: task}
	c.Set(mediaSubmissionContextKey, submission)
	session.Refund(c)
	session.Refund(c)
	require.True(t, submission.RefundConfirmed, "a committed original wallet/token transaction is not deferred by Redis or batch settings")
	require.NoError(t, model.DB.First(admission, "id = ?", admission.ID).Error)
	require.Equal(t, "refunded", admission.BillingState)
	snapshot, err := model.GetWalletFundingSnapshot(userID, info.FundingRequestId, "relay")
	require.NoError(t, err)
	require.Zero(t, snapshot.Quota)
	require.Equal(t, 100, getUserQuota(t, userID))
}

func useBillingConfirmationRedis(t *testing.T) {
	t.Helper()
	oldRedis, oldClient, oldBatch := common.RedisEnabled, common.RDB, common.BatchUpdateEnabled
	options := &redis.Options{}
	if address := os.Getenv("REDIS_PREPARATION_ADDR"); address != "" {
		host, _, err := net.SplitHostPort(address)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1", host)
		require.NotEmpty(t, os.Getenv("REDIS_PREPARATION_PASSWORD"))
		options.Addr, options.Password, options.DB = address, os.Getenv("REDIS_PREPARATION_PASSWORD"), 15
	} else {
		options.Addr = miniredis.RunT(t).Addr()
	}
	options.MaxRetries = -1
	client := redis.NewClient(options)
	common.RedisEnabled, common.RDB, common.BatchUpdateEnabled = true, client, true
	t.Cleanup(func() {
		client.Close()
		common.RedisEnabled, common.RDB, common.BatchUpdateEnabled = oldRedis, oldClient, oldBatch
		model.DB.Exec("DELETE FROM media_admissions")
		model.DB.Exec("DELETE FROM media_pools")
	})
}

func TestWalletMediaRefundRequiresCommittedBoundTransaction(t *testing.T) {
	for _, test := range []struct {
		name    string
		commit  bool
		confirm bool
	}{
		{"committed", true, true}, {"redis-unavailable", true, true}, {"write-failed", false, false}, {"token-deleted", false, false}, {"different-original-id", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			truncate(t)
			require.NoError(t, model.DB.AutoMigrate(&model.MediaAdmission{}, &model.MediaPool{}))
			useBillingConfirmationRedis(t)
			const userID = 918012
			seedUser(t, userID, 0)
			seedToken(t, userID, userID, "synthetic-bound-token", 100)
			require.NoError(t, model.CreditUserGiftQuota(userID, 100, "synthetic-bound-credit"))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			info := &relaycommon.RelayInfo{UserId: userID, TokenId: userID, TokenKey: "synthetic-bound-token", RequestId: "synthetic-bound-refund", ForcePreConsume: true, ImageRequestCount: 1}
			session, apiErr := NewBillingSession(c, info, 50)
			require.Nil(t, apiErr)
			originalID := info.FundingRequestId
			task := &model.Task{TaskID: "synthetic-bound-media", UserId: userID, ChannelId: 42, Quota: 50, Status: model.TaskStatusUnknown, PrivateData: model.TaskPrivateData{MediaAdmissionID: "synthetic-bound-admission"}}
			require.NoError(t, model.DB.Create(task).Error)
			admission := &model.MediaAdmission{ID: task.PrivateData.MediaAdmissionID, TaskDBID: task.ID, ChannelID: task.ChannelId, State: model.MediaReleased, BillingState: "refund_pending", SendingAt: 19, ProviderTerminal: true}
			require.NoError(t, model.DB.Create(admission).Error)
			submission := &MediaSubmission{Task: task}
			c.Set(mediaSubmissionContextKey, submission)
			switch test.name {
			case "redis-unavailable":
				require.NoError(t, common.RDB.Close())
			case "write-failed":
				name := "test:redis-bound-refund-failure"
				require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == "wallet_funding_requests" {
						tx.AddError(errors.New("synthetic refund commit failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(name)) })
			case "token-deleted":
				require.NoError(t, model.DB.Where("id = ?", userID).Delete(&model.Token{}).Error)
			case "different-original-id":
				info.FundingRequestId = "different-operation"
			}
			session.Refund(c)
			if test.confirm {
				session.Refund(c)
			}
			require.Equal(t, test.confirm, submission.RefundConfirmed)
			require.NoError(t, model.DB.First(admission, "id = ?", admission.ID).Error)
			require.Equal(t, int64(19), admission.SendingAt, "confirmation never removes the original send fence")
			require.Equal(t, model.MediaReleased, admission.State)
			expected := "refund_pending"
			if test.confirm {
				expected = "refunded"
			}
			require.Equal(t, expected, admission.BillingState)
			snapshot, err := model.GetWalletFundingSnapshot(userID, originalID, "relay")
			require.NoError(t, err)
			if test.commit {
				require.Zero(t, snapshot.Quota)
				require.Equal(t, 100, getUserQuota(t, userID))
			} else {
				require.Equal(t, int64(50), snapshot.Quota)
				require.Equal(t, 50, getUserQuota(t, userID))
			}
		})
	}
}

func TestSubscriptionMediaRefundNeedsFundingAndBoundTokenCommit(t *testing.T) {
	for _, test := range []struct {
		name                   string
		missingToken, wrongKey bool
	}{
		{name: "committed"}, {name: "token-missing", missingToken: true}, {name: "token-key-mismatch", wrongKey: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			truncate(t)
			useBillingConfirmationRedis(t)
			require.NoError(t, model.DB.AutoMigrate(&model.MediaAdmission{}, &model.MediaPool{}, &model.SubscriptionPreConsumeRecord{}))
			const userID = 918013
			seedUser(t, userID, 100)
			seedToken(t, userID, userID, "synthetic-subscription-token", 45)
			require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", userID).Update("used_quota", 55).Error)
			seedSubscription(t, userID, userID, 1000, 55)
			requestID := "synthetic-subscription-refund"
			record := &model.SubscriptionPreConsumeRecord{RequestId: requestID, UserId: userID, UserSubscriptionId: userID, PreConsumed: 50, Status: "consumed"}
			require.NoError(t, model.DB.Create(record).Error)
			t.Cleanup(func() { model.DB.Delete(record) })
			info := &relaycommon.RelayInfo{UserId: userID, TokenId: userID, TokenKey: "synthetic-subscription-token", RequestId: requestID, SubscriptionId: userID}
			session := &BillingSession{relayInfo: info, funding: &SubscriptionFunding{userId: userID, requestId: requestID, subscriptionId: userID, preConsumed: 50}, preConsumedQuota: 55, tokenConsumed: 55, extraReserved: 5}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			task := &model.Task{TaskID: "synthetic-subscription-media", UserId: userID, ChannelId: 42, Quota: 55, Status: model.TaskStatusUnknown, PrivateData: model.TaskPrivateData{MediaAdmissionID: "synthetic-subscription-admission"}}
			require.NoError(t, model.DB.Create(task).Error)
			admission := &model.MediaAdmission{ID: task.PrivateData.MediaAdmissionID, TaskDBID: task.ID, ChannelID: task.ChannelId, State: model.MediaReleased, BillingState: "refund_pending", SendingAt: 29, ProviderTerminal: true}
			require.NoError(t, model.DB.Create(admission).Error)
			submission := &MediaSubmission{Task: task}
			c.Set(mediaSubmissionContextKey, submission)
			if test.missingToken {
				require.NoError(t, model.DB.Delete(&model.Token{}, userID).Error)
			}
			if test.wrongKey {
				info.TokenKey = "different-original-token-key"
			}
			session.Refund(c)
			session.Refund(c)
			require.Equal(t, !test.missingToken && !test.wrongKey, submission.RefundConfirmed)
			require.NoError(t, model.DB.First(record, record.Id).Error)
			require.Equal(t, "refunded", record.Status)
			var subscription model.UserSubscription
			require.NoError(t, model.DB.First(&subscription, userID).Error)
			require.Zero(t, subscription.AmountUsed)
			require.Equal(t, 100, getUserQuota(t, userID), "subscription refunds do not manufacture wallet credits")
			if !test.missingToken && !test.wrongKey {
				require.Equal(t, 100, getTokenRemainQuota(t, userID))
				require.Zero(t, getTokenUsedQuota(t, userID))
			}
			if test.wrongKey {
				require.Equal(t, 45, getTokenRemainQuota(t, userID))
				require.Equal(t, 55, getTokenUsedQuota(t, userID))
			}
			require.NoError(t, model.DB.First(admission, "id = ?", admission.ID).Error)
			require.Equal(t, int64(29), admission.SendingAt)
			if test.missingToken || test.wrongKey {
				require.Equal(t, "refund_pending", admission.BillingState)
			} else {
				require.Equal(t, "refunded", admission.BillingState)
			}
		})
	}
}

func TestMediaPreconsumeUnchargedProofFollowsOriginalWalletTransaction(t *testing.T) {
	for _, dbFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "known-insufficient-wallet", true: "uncertain-database-failure"}[dbFailure], func(t *testing.T) {
			truncate(t)
			useBillingConfirmationRedis(t)
			require.NoError(t, model.DB.AutoMigrate(&model.MediaAdmission{}, &model.MediaPool{}))
			const userID = 918014
			seedUser(t, userID, 0)
			seedToken(t, userID, userID, "synthetic-preconsume-token", 100)
			require.NoError(t, model.CreditUserGiftQuota(userID, 25, "synthetic-preconsume-credit"))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			info := &relaycommon.RelayInfo{UserId: userID, TokenId: userID, TokenKey: "synthetic-preconsume-token", RequestId: "synthetic-preconsume-original", ForcePreConsume: true, ImageRequestCount: 1}
			funding := &WalletFunding{userId: userID, requestId: info.RequestId, purpose: "relay", tokenID: userID}
			session := &BillingSession{relayInfo: info, funding: funding}
			task := &model.Task{TaskID: "synthetic-preconsume-media", UserId: userID, ChannelId: 42, Status: model.TaskStatusUnknown, PrivateData: model.TaskPrivateData{MediaAdmissionID: "synthetic-preconsume-admission"}}
			require.NoError(t, model.DB.Create(task).Error)
			admission := &model.MediaAdmission{ID: task.PrivateData.MediaAdmissionID, TaskDBID: task.ID, ChannelID: task.ChannelId, State: model.MediaReserved}
			require.NoError(t, model.DB.Create(admission).Error)
			submission := &MediaSubmission{Task: task}
			c.Set(mediaSubmissionContextKey, submission)
			if dbFailure {
				name := "test:redis-preconsume-db-failure"
				require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == "users" {
						tx.AddError(errors.New("synthetic uncertain database read"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(name)) })
			}
			require.NotNil(t, session.preConsume(c, 50))
			require.Equal(t, dbFailure, submission.BillingUncertain)
			require.NoError(t, model.DB.First(admission, "id = ?", admission.ID).Error)
			if dbFailure {
				require.Equal(t, "preparing", admission.BillingState)
			} else {
				require.Equal(t, "not_charged", admission.BillingState)
			}
			require.Zero(t, admission.SendingAt)
			require.Equal(t, model.MediaReserved, admission.State)
			require.Equal(t, 100, getTokenRemainQuota(t, userID))
		})
	}
}
