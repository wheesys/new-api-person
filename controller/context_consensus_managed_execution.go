package controller

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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
	execute        func(*gin.Context, *relaycommon.RelayInfo, *relay.PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError)
	settle         func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage) relay.TextRelayExecutionResult
	beforeDispatch func(*gin.Context) error
	beforeSettle   func(*gin.Context, *relaycommon.RelayInfo, *managedResponseBuffer, contextconsensus.ManagedAssistantOutput) error
}

// executeManagedPreparedTextAttempt creates the C-2b execution boundary. It
// validates a complete non-streaming upstream response before settlement while
// the client response remains buffered. C-2c owns state generation, CAS, and
// the eventual flush.
func executeManagedPreparedTextAttempt(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	attempt *relay.PreparedTextRelayAttempt,
	clientProtocol types.RelayFormat,
	maximumResponseBytes int,
	dependencies managedMainExecutionDependencies,
) (managedMainExecutionResult, *types.NewAPIError) {
	if c == nil || c.Writer == nil || info == nil || dependencies.execute == nil || dependencies.settle == nil {
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
	if dependencies.beforeDispatch != nil {
		if err := dependencies.beforeDispatch(c); err != nil {
			c.Writer = parentWriter
			return managedMainExecutionResult{}, managedExecutionError(err)
		}
	}
	usage, newAPIError := dependencies.execute(c, info, attempt)
	c.Writer = parentWriter
	body, err := buffer.Body()
	if err != nil {
		if errors.Is(err, errManagedResponseBufferOverflow) {
			return managedMainExecutionResult{}, types.NewErrorWithStatusCode(
				err, types.ErrorCodeManagedContextResponseTooLarge, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry(),
			)
		}
		return managedMainExecutionResult{}, managedExecutionError(err)
	}
	if newAPIError != nil {
		return managedMainExecutionResult{}, newAPIError
	}
	if buffer.Status() < http.StatusOK || buffer.Status() >= http.StatusMultipleChoices {
		return managedMainExecutionResult{}, managedExecutionError(fmt.Errorf("managed upstream response status is not successful"))
	}
	output, err := contextconsensus.NormalizeManagedAssistantOutput(clientProtocol, body)
	if err != nil {
		return managedMainExecutionResult{}, managedExecutionError(err)
	}
	if err := validateManagedRelayUsage(usage); err != nil {
		return managedMainExecutionResult{}, managedExecutionError(err)
	}
	if dependencies.beforeSettle != nil {
		if err := dependencies.beforeSettle(c, info, buffer, output); err != nil {
			return managedMainExecutionResult{}, managedExecutionError(err)
		}
	}
	relayResult := dependencies.settle(c, info, usage)
	if err := validateManagedRelayResult(relayResult); err != nil {
		return managedMainExecutionResult{}, managedExecutionError(err)
	}
	return managedMainExecutionResult{RelayResult: relayResult, Output: output, buffer: buffer}, nil
}

func validateManagedRelayResult(result relay.TextRelayExecutionResult) error {
	if err := validateManagedRelayUsage(result.Usage); err != nil {
		return err
	}
	if result.SettlementError != nil || result.ConsumeResult.SettlementError != nil {
		return fmt.Errorf("managed relay settlement failed")
	}
	if result.ConsumptionError != nil || result.ConsumeResult.LogError != nil || !result.ConsumeResult.LogRecorded {
		return fmt.Errorf("managed relay consumption record failed")
	}
	return nil
}

func validateManagedRelayUsage(usage *dto.Usage) error {
	if usage == nil || usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens <= 0 {
		return fmt.Errorf("managed relay usage is unavailable")
	}
	return nil
}

func managedExecutionError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeManagedContextRevisionFailed,
		http.StatusServiceUnavailable,
		types.ErrOptionWithSkipRetry(),
	)
}
