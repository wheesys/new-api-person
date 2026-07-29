package relay

import (
	"net/http"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	attempt, newAPIError := PrepareTextRelayAttempt(c, info)
	if newAPIError != nil {
		return newAPIError
	}
	defer attempt.Close()

	if info.RelayMode == relayconstant.RelayModeResponsesCompact {
		usageDto, executeError := ExecutePreparedTextRelayAttempt(c, info, attempt)
		if executeError != nil {
			return executeError
		}
		originModelName := info.OriginModelName
		originPriceData := info.PriceData

		_, err := helper.ModelPriceHelper(c, info, info.GetEstimatePromptTokens(), &types.TokenCountMeta{})
		if err != nil {
			info.OriginModelName = originModelName
			info.PriceData = originPriceData
			return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry(), types.ErrOptionWithStatusCode(http.StatusBadRequest))
		}
		_ = SettleTextRelayUsage(c, info, usageDto)

		info.OriginModelName = originModelName
		info.PriceData = originPriceData
		return nil
	}

	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		usageDto, executeError := ExecutePreparedTextRelayAttempt(c, info, attempt)
		if executeError != nil {
			return executeError
		}
		service.PostAudioConsumeQuota(c, info, usageDto, "")
		return nil
	}
	_, newAPIError = ExecutePreparedTextAttemptWithSettlement(c, info, attempt)
	return newAPIError
}
