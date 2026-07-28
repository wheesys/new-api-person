package model

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIterateSmartRoutingConsumeLogsFiltersAndEnforcesLimit(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:smart-routing-log-query?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&Log{}))

	originalLogDatabase := LOG_DB
	LOG_DB = database
	t.Cleanup(func() { LOG_DB = originalLogDatabase })

	logs := []Log{
		{CreatedAt: 1100, Type: LogTypeConsume, ChannelId: 11, ModelName: "selected-a", Other: `{"smart_routing":{"enabled":true}}`},
		{CreatedAt: 1200, Type: LogTypeError, ChannelId: 12, ModelName: "selected-b", Other: `{"smart_routing":{"enabled":true}}`},
		{CreatedAt: 1300, Type: LogTypeConsume, ChannelId: 13, ModelName: "selected-c", Other: `{"billing_source":"wallet"}`},
		{CreatedAt: 1400, Type: LogTypeConsume, ChannelId: 14, ModelName: "selected-d", Other: `{"smart_routing":{"enabled":true}}`},
	}
	require.NoError(t, database.Create(&logs).Error)

	var projections []SmartRoutingLogProjection
	matched, err := IterateSmartRoutingConsumeLogs(context.Background(), 1000, 1500, 10, func(projection SmartRoutingLogProjection) error {
		projections = append(projections, projection)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, matched)
	require.Len(t, projections, 2)
	assert.Equal(t, 11, projections[0].ChannelID)
	assert.Equal(t, "selected-d", projections[1].ModelName)

	processed := 0
	matched, err = IterateSmartRoutingConsumeLogs(context.Background(), 1000, 1500, 1, func(SmartRoutingLogProjection) error {
		processed++
		return nil
	})
	assert.True(t, errors.Is(err, ErrSmartRoutingLogQueryLimitExceeded))
	assert.Equal(t, 2, matched)
	assert.Equal(t, 1, processed)
}
