package relay

import (
	"errors"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextRelayExecutionResultExposesUsageAndSettlementFailures(t *testing.T) {
	usage := &dto.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}
	settlementErr := errors.New("settlement failed")
	logErr := errors.New("log failed")
	consumeErr := errors.Join(settlementErr, logErr)
	result := settleTextRelayUsageWith(nil, nil, usage, func(
		context *gin.Context,
		info *relaycommon.RelayInfo,
		gotUsage *dto.Usage,
		extraContent []string,
	) (service.TextConsumeResult, error) {
		require.Same(t, usage, gotUsage)
		assert.Nil(t, extraContent)
		return service.TextConsumeResult{
			PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, ActualQuota: 13,
			SettlementError: settlementErr, LogError: logErr,
		}, consumeErr
	})

	assert.Same(t, usage, result.Usage)
	assert.Equal(t, 13, result.ConsumeResult.ActualQuota)
	assert.ErrorIs(t, result.SettlementError, settlementErr)
	assert.ErrorIs(t, result.ConsumptionError, settlementErr)
	assert.ErrorIs(t, result.ConsumptionError, logErr)
}
