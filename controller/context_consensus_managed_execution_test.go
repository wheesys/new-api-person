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
		managedMainExecutionDependencies{
			execute: func(contextValue *gin.Context, _ *relaycommon.RelayInfo, _ *relay.PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError) {
				contextValue.Header("Content-Type", "application/json")
				_, err := contextValue.Writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
				require.NoError(t, err)
				return usage, nil
			},
			settle: func(_ *gin.Context, _ *relaycommon.RelayInfo, settledUsage *dto.Usage) relay.TextRelayExecutionResult {
				return relay.TextRelayExecutionResult{
					Usage: settledUsage,
					ConsumeResult: service.TextConsumeResult{
						PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7, LogRecorded: true,
					},
				}
			},
		},
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
		managedMainExecutionDependencies{
			execute: func(contextValue *gin.Context, _ *relaycommon.RelayInfo, _ *relay.PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError) {
				_, err := contextValue.Writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
				require.NoError(t, err)
				return &dto.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}, nil
			},
			settle: func(_ *gin.Context, _ *relaycommon.RelayInfo, settledUsage *dto.Usage) relay.TextRelayExecutionResult {
				return relay.TextRelayExecutionResult{
					Usage:           settledUsage,
					ConsumeResult:   service.TextConsumeResult{SettlementError: settlementError, LogRecorded: true},
					SettlementError: settlementError,
				}
			},
		},
	)

	assert.Nil(t, result.buffer)
	require.NotNil(t, newAPIError)
	assert.Contains(t, newAPIError.Error(), "settlement failed")
	assert.Empty(t, recorder.Body.Bytes())
}

func TestExecuteManagedPreparedTextAttemptValidatesResponseBeforeSettlement(t *testing.T) {
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	settled := false

	result, newAPIError := executeManagedPreparedTextAttempt(
		contextValue,
		&relaycommon.RelayInfo{},
		nil,
		types.RelayFormatOpenAI,
		4096,
		managedMainExecutionDependencies{
			execute: func(contextValue *gin.Context, _ *relaycommon.RelayInfo, _ *relay.PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError) {
				_, err := contextValue.Writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}]}`))
				require.NoError(t, err)
				return &dto.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}, nil
			},
			settle: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) relay.TextRelayExecutionResult {
				settled = true
				return relay.TextRelayExecutionResult{}
			},
		},
	)

	assert.Nil(t, result.buffer)
	require.NotNil(t, newAPIError)
	assert.Contains(t, newAPIError.Error(), "did not finish normally")
	assert.False(t, settled)
	assert.Empty(t, recorder.Body.Bytes())
}

func TestExecuteManagedPreparedTextAttemptValidatesUsageBeforeSettlement(t *testing.T) {
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	settled := false

	result, newAPIError := executeManagedPreparedTextAttempt(
		contextValue,
		&relaycommon.RelayInfo{},
		nil,
		types.RelayFormatOpenAI,
		4096,
		managedMainExecutionDependencies{
			execute: func(contextValue *gin.Context, _ *relaycommon.RelayInfo, _ *relay.PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError) {
				_, err := contextValue.Writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
				require.NoError(t, err)
				return nil, nil
			},
			settle: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) relay.TextRelayExecutionResult {
				settled = true
				return relay.TextRelayExecutionResult{}
			},
		},
	)

	assert.Nil(t, result.buffer)
	require.NotNil(t, newAPIError)
	assert.Contains(t, newAPIError.Error(), "usage is unavailable")
	assert.False(t, settled)
	assert.Empty(t, recorder.Body.Bytes())
}

func TestExecuteManagedPreparedTextAttemptUsesFrozenOverflowContract(t *testing.T) {
	assert.Equal(t, 2*1024*1024, managedMainResponseMaximumBytes)
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	result, newAPIError := executeManagedPreparedTextAttempt(
		contextValue,
		&relaycommon.RelayInfo{},
		nil,
		types.RelayFormatOpenAI,
		16,
		managedMainExecutionDependencies{
			execute: func(contextValue *gin.Context, _ *relaycommon.RelayInfo, _ *relay.PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError) {
				_, writeErr := contextValue.Writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"too large"},"finish_reason":"stop"}]}`))
				require.ErrorIs(t, writeErr, errManagedResponseBufferOverflow)
				return nil, types.NewError(writeErr, types.ErrorCodeBadResponseBody)
			},
			settle: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) relay.TextRelayExecutionResult {
				t.Fatal("overflow response must not settle")
				return relay.TextRelayExecutionResult{}
			},
		},
	)

	assert.Nil(t, result.buffer)
	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeManagedContextResponseTooLarge, newAPIError.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(newAPIError))
	assert.Empty(t, recorder.Body.Bytes())
}
