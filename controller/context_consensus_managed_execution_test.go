package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteManagedPreparedTextAttemptKeepsSuccessfulResponseBuffered(t *testing.T) {
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{}
	usage := &dto.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}

	result, newAPIError := executeManagedPreparedTextAttempt(
		contextValue,
		info,
		nil,
		types.RelayFormatOpenAI,
		4096,
		managedMainExecutionDependencies{execute: func(contextValue *gin.Context, _ *relaycommon.RelayInfo, _ *relay.PreparedTextRelayAttempt) (relay.TextRelayExecutionResult, *types.NewAPIError) {
			contextValue.Header("Content-Type", "application/json")
			_, err := contextValue.Writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
			require.NoError(t, err)
			return relay.TextRelayExecutionResult{
				Usage: usage,
				ConsumeResult: service.TextConsumeResult{
					PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7, LogRecorded: true,
				},
			}, nil
		}},
	)

	require.Nil(t, newAPIError)
	assert.Equal(t, "done", result.Output.Text)
	assert.NotEmpty(t, result.Output.ResponseDigest)
	assert.Same(t, usage, result.RelayResult.Usage)
	assert.Empty(t, recorder.Body.Bytes(), "C-2b must not write before the C-2c commit barrier")
	assert.False(t, recorder.Flushed)
	assert.NotNil(t, result.buffer)
}

func TestExecuteManagedPreparedTextAttemptFailsClosedOnSettlementError(t *testing.T) {
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	settlementError := errors.New("settlement failed")

	result, newAPIError := executeManagedPreparedTextAttempt(
		contextValue,
		&relaycommon.RelayInfo{},
		nil,
		types.RelayFormatOpenAI,
		4096,
		managedMainExecutionDependencies{execute: func(contextValue *gin.Context, _ *relaycommon.RelayInfo, _ *relay.PreparedTextRelayAttempt) (relay.TextRelayExecutionResult, *types.NewAPIError) {
			_, err := contextValue.Writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
			require.NoError(t, err)
			return relay.TextRelayExecutionResult{
				Usage:           &dto.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
				ConsumeResult:   service.TextConsumeResult{SettlementError: settlementError, LogRecorded: true},
				SettlementError: settlementError,
			}, nil
		}},
	)

	assert.Nil(t, result.buffer)
	require.NotNil(t, newAPIError)
	assert.Contains(t, newAPIError.Error(), "settlement failed")
	assert.Empty(t, recorder.Body.Bytes())
}
