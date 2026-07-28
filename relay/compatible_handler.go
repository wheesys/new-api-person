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
	usage, newAPIError := ExecuteTextAttemptWithoutQuota(c, info)
	if newAPIError != nil {
		return newAPIError
	}

	containAudioTokens := usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0
	containsAudioRatios := ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)
	if containAudioTokens && containsAudioRatios {
		service.PostAudioConsumeQuota(c, info, usage, "")
	} else {
		service.PostTextConsumeQuota(c, info, usage, nil)
	}
	return nil
}

// ExecutePreparedTextAttemptWithQuota executes an already frozen text attempt
// and keeps the existing text/audio settlement behavior. The caller owns and
// must close the attempt.
func ExecutePreparedTextAttemptWithQuota(c *gin.Context, info *relaycommon.RelayInfo, attempt *PreparedTextRelayAttempt) *types.NewAPIError {
	usage, newAPIError := ExecutePreparedTextRelayAttempt(c, info, attempt)
	if newAPIError != nil {
		return newAPIError
	}
	containAudioTokens := usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0
	containsAudioRatios := ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)
	if containAudioTokens && containsAudioRatios {
		service.PostAudioConsumeQuota(c, info, usage, "")
	} else {
		service.PostTextConsumeQuota(c, info, usage, nil)
	}
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
