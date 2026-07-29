package controller

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/gin-gonic/gin"
)

type managedMainExecutionResult struct {
	RelayResult relay.TextRelayExecutionResult
	Output      contextconsensus.ManagedAssistantOutput
	buffer      *managedResponseBuffer
}

type managedMainExecutionDependencies struct {
	execute func(*gin.Context, *relaycommon.RelayInfo, *relay.PreparedTextRelayAttempt) (relay.TextRelayExecutionResult, *types.NewAPIError)
}

// executeManagedPreparedTextAttempt creates the C-2b execution boundary. It
// settles a complete non-streaming upstream response while the client response
// remains buffered. C-2c owns state generation, CAS, and the eventual flush.
func executeManagedPreparedTextAttempt(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	attempt *relay.PreparedTextRelayAttempt,
	clientProtocol types.RelayFormat,
	maximumResponseBytes int,
	dependencies managedMainExecutionDependencies,
) (managedMainExecutionResult, *types.NewAPIError) {
	if c == nil || c.Writer == nil || info == nil || dependencies.execute == nil {
		return managedMainExecutionResult{}, managedExecutionError(fmt.Errorf("managed main execution is incomplete"))
	}
	if info.IsStream {
		return managedMainExecutionResult{}, managedExecutionError(fmt.Errorf("managed context does not support streaming requests"))
	}
	buffer, err := newManagedResponseBuffer(c.Writer, maximumResponseBytes)
	if err != nil {
		return managedMainExecutionResult{}, managedExecutionError(err)
	}
	parentWriter := c.Writer
	c.Writer = buffer
	relayResult, newAPIError := dependencies.execute(c, info, attempt)
	c.Writer = parentWriter
	if newAPIError != nil {
		return managedMainExecutionResult{}, newAPIError
	}
	body, err := buffer.Body()
	if err != nil {
		return managedMainExecutionResult{}, managedExecutionError(err)
	}
	if buffer.Status() < http.StatusOK || buffer.Status() >= http.StatusMultipleChoices {
		return managedMainExecutionResult{}, managedExecutionError(fmt.Errorf("managed upstream response status is not successful"))
	}
	output, err := contextconsensus.NormalizeManagedAssistantOutput(clientProtocol, body)
	if err != nil {
		return managedMainExecutionResult{}, managedExecutionError(err)
	}
	if err := validateManagedRelayResult(relayResult); err != nil {
		return managedMainExecutionResult{}, managedExecutionError(err)
	}
	return managedMainExecutionResult{RelayResult: relayResult, Output: output, buffer: buffer}, nil
}

func validateManagedRelayResult(result relay.TextRelayExecutionResult) error {
	if result.Usage == nil || result.Usage.PromptTokens < 0 || result.Usage.CompletionTokens < 0 || result.Usage.TotalTokens <= 0 {
		return fmt.Errorf("managed relay usage is unavailable")
	}
	if result.SettlementError != nil || result.ConsumeResult.SettlementError != nil {
		return fmt.Errorf("managed relay settlement failed")
	}
	if result.ConsumptionError != nil || result.ConsumeResult.LogError != nil || !result.ConsumeResult.LogRecorded {
		return fmt.Errorf("managed relay consumption record failed")
	}
	return nil
}

func managedExecutionError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeInvalidRequest,
		http.StatusServiceUnavailable,
		types.ErrOptionWithSkipRetry(),
	)
}
