package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type cacheQuotaResult int

const (
	cacheQuotaInsufficient cacheQuotaResult = iota
	cacheQuotaOK
	cacheQuotaMiss
)

const userQuotaDeltaScript = `
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= tonumber(ARGV[2])
  or tonumber(redis.call('HGET', KEYS[1], 'CacheSchema') or '0') ~= tonumber(ARGV[3])
  or redis.call('HEXISTS', KEYS[1], 'Quota') == 0 then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'Quota', ARGV[1])
return 1`

func quotaResultFromLua(result int, err error) (cacheQuotaResult, error) {
	if err != nil {
		return cacheQuotaMiss, err
	}
	switch result {
	case 1:
		return cacheQuotaOK, nil
	case 0:
		return cacheQuotaInsufficient, nil
	default:
		return cacheQuotaMiss, nil
	}
}

func cacheApplyUserQuotaDelta(userID int, delta int64) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), userQuotaDeltaScript,
		[]string{getUserCacheKey(userID)}, delta, userID, userCacheSchemaVersion).Int()
	return quotaResultFromLua(result, err)
}

func reserveUserQuotaDB(id int, quota int) (bool, error) {
	_, err := ApplyWalletFundingTarget(id, "legacy-reserve:"+common.GetRandomString(24), "balance_adjustment", quota, true)
	if errors.Is(err, ErrInsufficientWalletFunding) {
		return false, nil
	}
	return err == nil, err
}

func reserveTokenQuotaDB(id int, quota int) (bool, error) {
	result := DB.Model(&Token{}).
		Where("id = ? AND remain_quota >= ?", id, quota).
		Updates(map[string]any{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		})
	return result.RowsAffected == 1, result.Error
}

// TryReserveUserQuota atomically checks and deducts a user's wallet quota.
// The primary database commits quota and source allocation together. Redis
// and batch settings cannot defer the wallet or authorize a stale balance.
func TryReserveUserQuota(id int, quota int) (bool, error) {
	if quota < 0 {
		return false, errors.New("quota cannot be negative")
	}
	if quota == 0 {
		return true, nil
	}
	return reserveUserQuotaDB(id, quota)
}

// TryReserveTokenQuota atomically checks and deducts a token quota. Unlimited
// tokens skip the balance check but still update remain/used accounting.
// Both wallet and subscription requests commit token quota immediately to the
// same primary database; Redis and batch statistics never authorize spending.
func TryReserveTokenQuota(id int, key string, quota int, unlimited bool) (bool, error) {
	if quota < 0 {
		return false, errors.New("quota cannot be negative")
	}
	if quota == 0 {
		return true, nil
	}
	if unlimited {
		return true, DecreaseTokenQuota(id, key, quota)
	}
	if err := invalidateTokenCacheForMutation(key); err != nil {
		common.SysLog(common.LogText("failed to invalidate token quota cache: %s", err.Error()))
	}
	reserved, err := reserveTokenQuotaDB(id, quota)
	if err != nil {
		return false, err
	}
	if err := invalidateTokenCacheForMutation(key); err != nil {
		common.SysLog(common.LogText("failed to invalidate token quota cache: %s", err.Error()))
	}
	if !reserved {
		var count int64
		if err := DB.Model(&Token{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return false, err
		}
		if count == 0 {
			return false, gorm.ErrRecordNotFound
		}
	}
	return reserved, nil
}
