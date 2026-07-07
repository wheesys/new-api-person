package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetUsageAccumulationsClearsUsageWithoutClearingBalances(t *testing.T) {
	db := setupModelPriceTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Log{}, &QuotaData{}))

	require.NoError(t, db.Create(&User{
		Id:           1,
		Username:     "usage-user",
		Password:     "12345678",
		Quota:        1000,
		UsedQuota:    250,
		RequestCount: 3,
	}).Error)
	require.NoError(t, db.Create(&Token{
		Id:          1,
		UserId:      1,
		Key:         "test-key",
		Name:        "test-token",
		RemainQuota: 700,
		UsedQuota:   300,
	}).Error)
	require.NoError(t, db.Create(&[]Log{
		{UserId: 1, Type: LogTypeConsume, Quota: 10},
		{UserId: 1, Type: LogTypeRefund, Quota: -2},
		{UserId: 1, Type: LogTypeError, Quota: 0},
		{UserId: 1, Type: LogTypeManage, Content: "keep audit"},
		{UserId: 1, Type: LogTypeTopup, Content: "keep topup"},
	}).Error)
	require.NoError(t, db.Create(&QuotaData{
		UserID:    1,
		Username:  "usage-user",
		ModelName: "gpt-usage",
		CreatedAt: 1000,
		Quota:     10,
		Count:     1,
	}).Error)

	addNewRecord(BatchUpdateTypeUsedQuota, 1, 50)
	addNewRecord(BatchUpdateTypeRequestCount, 1, 2)
	CacheQuotaDataLock.Lock()
	CacheQuotaData["pending"] = &QuotaData{UserID: 1, Username: "usage-user", Quota: 5}
	CacheQuotaDataLock.Unlock()

	result, err := ResetUsageAccumulations(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.UsersUpdated, int64(1))
	assert.GreaterOrEqual(t, result.TokensUpdated, int64(1))
	assert.GreaterOrEqual(t, result.LogsDeleted, int64(3))
	assert.GreaterOrEqual(t, result.QuotaDataDeleted, int64(1))

	batchUpdate()

	var user User
	require.NoError(t, db.First(&user, "id = ?", 1).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 0, user.UsedQuota)
	assert.Equal(t, 0, user.RequestCount)

	var token Token
	require.NoError(t, db.First(&token, "id = ?", 1).Error)
	assert.Equal(t, 700, token.RemainQuota)
	assert.Equal(t, 0, token.UsedQuota)

	var usageLogCount int64
	require.NoError(t, db.Model(&Log{}).Where("type IN ?", []int{LogTypeConsume, LogTypeRefund, LogTypeError}).Count(&usageLogCount).Error)
	assert.Equal(t, int64(0), usageLogCount)

	var preservedLogCount int64
	require.NoError(t, db.Model(&Log{}).Where("type IN ?", []int{LogTypeManage, LogTypeTopup}).Count(&preservedLogCount).Error)
	assert.Equal(t, int64(2), preservedLogCount)

	var quotaDataCount int64
	require.NoError(t, db.Model(&QuotaData{}).Count(&quotaDataCount).Error)
	assert.Equal(t, int64(0), quotaDataCount)

	CacheQuotaDataLock.Lock()
	assert.Empty(t, CacheQuotaData)
	CacheQuotaDataLock.Unlock()
}
