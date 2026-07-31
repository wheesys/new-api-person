package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetContextConsensusDiagnostics(c *gin.Context) {
	startTimestamp, endTimestamp, err := smartRoutingMetricsWindow(c, time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	result, err := service.QueryContextConsensusDiagnostics(c.Request.Context(), startTimestamp, endTimestamp)
	if errors.Is(err, service.ErrSmartRoutingMetricsTooManyLogs) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"message": "context consensus diagnostics matched too many logs; use a smaller time range",
		})
		return
	}
	if err != nil {
		logger.LogError(c, "failed to query context consensus diagnostics: "+err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query context consensus diagnostics",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
