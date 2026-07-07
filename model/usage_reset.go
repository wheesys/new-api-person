package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type UsageResetResult struct {
	UsersUpdated     int64 `json:"users_updated"`
	TokensUpdated    int64 `json:"tokens_updated"`
	LogsDeleted      int64 `json:"logs_deleted"`
	QuotaDataDeleted int64 `json:"quota_data_deleted"`
}

var usageLogTypes = []int{LogTypeConsume, LogTypeRefund, LogTypeError}

func ResetUsageAccumulations(ctx context.Context) (UsageResetResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if DB == nil || LOG_DB == nil {
		return UsageResetResult{}, errors.New("database is not initialized")
	}

	batchUpdate()
	clearQuotaDataCache()

	result := UsageResetResult{}
	if err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		usersResult := tx.Unscoped().Model(&User{}).
			Where("used_quota <> 0 OR request_count <> 0").
			Updates(map[string]interface{}{
				"used_quota":    0,
				"request_count": 0,
			})
		if usersResult.Error != nil {
			return usersResult.Error
		}
		result.UsersUpdated = usersResult.RowsAffected

		tokensResult := tx.Unscoped().Model(&Token{}).
			Where("used_quota <> 0").
			Update("used_quota", 0)
		if tokensResult.Error != nil {
			return tokensResult.Error
		}
		result.TokensUpdated = tokensResult.RowsAffected

		var quotaDataCount int64
		if err := tx.Model(&QuotaData{}).Count(&quotaDataCount).Error; err != nil {
			return err
		}
		quotaDataResult := tx.Where("1 = 1").Delete(&QuotaData{})
		if quotaDataResult.Error != nil {
			return quotaDataResult.Error
		}
		result.QuotaDataDeleted = quotaDataCount
		return nil
	}); err != nil {
		return UsageResetResult{}, err
	}

	logsDeleted, err := deleteUsageLogs(ctx)
	if err != nil {
		return result, err
	}
	result.LogsDeleted = logsDeleted

	if err := invalidateAllTokenCaches(); err != nil {
		common.SysLog("failed to invalidate token caches after usage reset: " + err.Error())
	}
	return result, nil
}

func clearQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	CacheQuotaData = make(map[string]*QuotaData)
}

func deleteUsageLogs(ctx context.Context) (int64, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		var total int64
		if err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("type IN ?", usageLogTypes).Count(&total).Error; err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, nil
		}
		typeList := make([]string, 0, len(usageLogTypes))
		for _, logType := range usageLogTypes {
			typeList = append(typeList, strconv.Itoa(logType))
		}
		if err := LOG_DB.WithContext(ctx).Exec(
			fmt.Sprintf("ALTER TABLE logs DELETE WHERE type IN (%s) SETTINGS mutations_sync = 1", strings.Join(typeList, ",")),
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	result := LOG_DB.WithContext(ctx).Where("type IN ?", usageLogTypes).Delete(&Log{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func invalidateAllTokenCaches() error {
	if !common.RedisEnabled {
		return nil
	}
	var tokens []Token
	if err := DB.Unscoped().Select(commonKeyCol).Find(&tokens).Error; err != nil {
		return err
	}
	var firstErr error
	for _, token := range tokens {
		if token.Key == "" {
			continue
		}
		if err := cacheDeleteToken(token.Key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
