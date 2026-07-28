package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func GetSmartRoutingMetrics(c *gin.Context) {
	startTimestamp, endTimestamp, err := smartRoutingMetricsWindow(c, time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	result, err := service.QuerySmartRoutingMetrics(c.Request.Context(), startTimestamp, endTimestamp)
	if errors.Is(err, service.ErrSmartRoutingMetricsTooManyLogs) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"message": "smart routing metrics query matched too many logs; use a smaller time range",
		})
		return
	}
	if err != nil {
		logger.LogError(c, "failed to query smart routing metrics: "+err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query smart routing metrics",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func smartRoutingMetricsWindow(c *gin.Context, now time.Time) (int64, int64, error) {
	endTimestamp := now.Unix()
	if rawEnd := c.Query("end_timestamp"); rawEnd != "" {
		parsed, err := strconv.ParseInt(rawEnd, 10, 64)
		if err != nil || parsed <= 0 {
			return 0, 0, fmt.Errorf("end_timestamp must be a positive Unix timestamp")
		}
		endTimestamp = parsed
	}
	if endTimestamp > now.Unix() {
		return 0, 0, fmt.Errorf("end_timestamp cannot be in the future")
	}

	startTimestamp := endTimestamp - int64(service.SmartRoutingMetricsDefaultWindow/time.Second)
	if rawStart := c.Query("start_timestamp"); rawStart != "" {
		parsed, err := strconv.ParseInt(rawStart, 10, 64)
		if err != nil || parsed <= 0 {
			return 0, 0, fmt.Errorf("start_timestamp must be a positive Unix timestamp")
		}
		startTimestamp = parsed
	}
	if startTimestamp >= endTimestamp {
		return 0, 0, fmt.Errorf("start_timestamp must be earlier than end_timestamp")
	}
	if endTimestamp-startTimestamp > int64(service.SmartRoutingMetricsMaximumWindow/time.Second) {
		return 0, 0, fmt.Errorf("smart routing metrics time range cannot exceed 7 days")
	}
	return startTimestamp, endTimestamp, nil
}
