package relay

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func TextHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	containsAudioRatios := ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)
	if !containsAudioRatios {
		_, newAPIError := ExecuteTextAttemptWithSettlement(c, info)
		return newAPIError
	}
	usage, newAPIError := ExecuteTextAttemptWithoutQuota(c, info)
	if newAPIError != nil {
		return newAPIError
	}

	containAudioTokens := usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0
	if containAudioTokens && containsAudioRatios {
		service.PostAudioConsumeQuota(c, info, usage, "")
	} else {
		_ = SettleTextRelayUsage(c, info, usage)
	}
	return nil
}

type TextRelayExecutionResult struct {
	Usage            *dto.Usage
	ConsumeResult    service.TextConsumeResult
	SettlementError  error
	ConsumptionError error
}

// ExecuteTextAttemptWithSettlement exposes normalized usage and the complete
// text settlement outcome for callers that need a commit-before-write barrier.
func ExecuteTextAttemptWithSettlement(c *gin.Context, info *relaycommon.RelayInfo) (TextRelayExecutionResult, *types.NewAPIError) {
	attempt, newAPIError := PrepareTextRelayAttempt(c, info)
	if newAPIError != nil {
		return TextRelayExecutionResult{}, newAPIError
	}
	defer attempt.Close()
	return ExecutePreparedTextAttemptWithSettlement(c, info, attempt)
}

func ExecutePreparedTextAttemptWithSettlement(c *gin.Context, info *relaycommon.RelayInfo, attempt *PreparedTextRelayAttempt) (TextRelayExecutionResult, *types.NewAPIError) {
	usage, newAPIError := ExecutePreparedTextRelayAttempt(c, info, attempt)
	if newAPIError != nil {
		return TextRelayExecutionResult{}, newAPIError
	}
	return SettleTextRelayUsage(c, info, usage), nil
}

func SettleTextRelayUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) TextRelayExecutionResult {
	return settleTextRelayUsageWith(c, info, usage, service.PostTextConsumeQuotaResult)
}

func settleTextRelayUsageWith(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	usage *dto.Usage,
	settle func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) (service.TextConsumeResult, error),
) TextRelayExecutionResult {
	consumeResult, consumeErr := settle(c, info, usage, nil)
	return TextRelayExecutionResult{
		Usage:            usage,
		ConsumeResult:    consumeResult,
		SettlementError:  consumeResult.SettlementError,
		ConsumptionError: consumeErr,
	}
}

// ExecutePreparedTextAttemptWithQuota executes an already frozen text attempt
// and keeps the existing text/audio settlement behavior. The caller owns and
// must close the attempt.
func ExecutePreparedTextAttemptWithQuota(c *gin.Context, info *relaycommon.RelayInfo, attempt *PreparedTextRelayAttempt) *types.NewAPIError {
	containsAudioRatios := ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)
	if !containsAudioRatios {
		_, newAPIError := ExecutePreparedTextAttemptWithSettlement(c, info, attempt)
		return newAPIError
	}
	usage, newAPIError := ExecutePreparedTextRelayAttempt(c, info, attempt)
	if newAPIError != nil {
		return newAPIError
	}
	containAudioTokens := usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0
	if containAudioTokens {
		service.PostAudioConsumeQuota(c, info, usage, "")
		return nil
	}
	_ = SettleTextRelayUsage(c, info, usage)
	return nil
}

// ExecuteTextAttemptWithoutQuota performs one upstream text attempt and returns
// normalized usage without settling quota or recording a consume log.
func ExecuteTextAttemptWithoutQuota(c *gin.Context, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	attempt, newAPIError := PrepareTextRelayAttempt(c, info)
	if newAPIError != nil {
		return nil, newAPIError
	}
	defer attempt.Close()
	return ExecutePreparedTextRelayAttempt(c, info, attempt)
}
