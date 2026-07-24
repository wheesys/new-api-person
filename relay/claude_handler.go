package relay

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func ClaudeHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	attempt, newAPIError := PrepareTextRelayAttempt(c, info)
	if newAPIError != nil {
		return newAPIError
	}
	defer attempt.Close()

	usage, newAPIError := ExecutePreparedTextRelayAttempt(c, info, attempt)
	if newAPIError != nil {
		return newAPIError
	}
	service.PostTextConsumeQuota(c, info, usage, nil)
	return nil
}
