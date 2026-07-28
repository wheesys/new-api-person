package model

import (
	"context"
	"errors"
)

var ErrSmartRoutingLogQueryLimitExceeded = errors.New("smart routing log query limit exceeded")

type SmartRoutingLogProjection struct {
	CreatedAt int64  `gorm:"column:created_at"`
	ChannelID int    `gorm:"column:channel_id"`
	ModelName string `gorm:"column:model_name"`
	Other     string `gorm:"column:other"`
}

func IterateSmartRoutingConsumeLogs(
	ctx context.Context,
	startTimestamp int64,
	endTimestamp int64,
	limit int,
	consume func(SmartRoutingLogProjection) error,
) (int, error) {
	if ctx == nil || startTimestamp <= 0 || endTimestamp <= startTimestamp || limit <= 0 || consume == nil {
		return 0, nil
	}
	rows, err := LOG_DB.WithContext(ctx).
		Model(&Log{}).
		Select("created_at, channel_id, model_name, other").
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ? AND created_at <= ?", startTimestamp, endTimestamp).
		Where("other LIKE ?", `%"smart_routing"%`).
		Order("created_at ASC").
		Limit(limit + 1).
		Rows()
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	matched := 0
	for rows.Next() {
		matched++
		if matched > limit {
			return matched, ErrSmartRoutingLogQueryLimitExceeded
		}
		var projection SmartRoutingLogProjection
		if err := LOG_DB.ScanRows(rows, &projection); err != nil {
			return matched, err
		}
		if err := consume(projection); err != nil {
			return matched, err
		}
	}
	return matched, rows.Err()
}
