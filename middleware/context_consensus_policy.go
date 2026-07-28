package middleware

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

const (
	contextIDHeader       = "X-New-Api-Context-Id"
	contextModeHeader     = "X-New-Api-Context-Mode"
	contextRevisionHeader = "X-New-Api-Context-Revision"
)

func captureContextConsensusPolicy(c *gin.Context) error {
	if c == nil || c.Request == nil {
		return nil
	}

	mode := strings.ToLower(strings.TrimSpace(c.Request.Header.Get(contextModeHeader)))
	contextID := strings.TrimSpace(c.Request.Header.Get(contextIDHeader))
	revisionValue := strings.TrimSpace(c.Request.Header.Get(contextRevisionHeader))

	c.Request.Header.Del(contextIDHeader)
	c.Request.Header.Del(contextModeHeader)
	c.Request.Header.Del(contextRevisionHeader)

	if mode != "" && mode != "full" && mode != "auto_compact" {
		if mode != "managed" {
			return fmt.Errorf("unsupported context mode")
		}
	}

	settings := model_setting.GetSmartRoutingSettings()
	if mode == "managed" {
		if contextID == "" || revisionValue == "" {
			return fmt.Errorf("managed context requires context ID and revision")
		}
		if !settings.ContextConsensusEnabled || !settings.ManagedContextEnabled {
			return fmt.Errorf("managed context is disabled")
		}
		if !common.GetContextKeyBool(c, constant.ContextKeyTokenContextCompaction) {
			return fmt.Errorf("managed context is not authorized for this token")
		}
		expectedRevision, err := strconv.ParseUint(revisionValue, 10, 64)
		if err != nil {
			return fmt.Errorf("managed context revision must be a non-negative integer")
		}
		common.SetContextKey(c, constant.ContextKeyManagedContextRequest, contextconsensus.ManagedContextRequest{
			ExternalContextID: contextID,
			ExpectedRevision:  expectedRevision,
		})
	} else if contextID != "" || revisionValue != "" {
		return fmt.Errorf("context ID and revision require managed context mode")
	}
	policy := contextconsensus.CompactionPolicy{
		SystemEnabled:        settings.ContextConsensusEnabled && settings.AutoCompactionEnabled,
		PolicyVersion:        "context-consensus-v1",
		PreservedRecentTurns: settings.PreservedRecentTurns,
		TargetInputTokens:    settings.MaxCompactionInputTokens,
		MaxSummaryTokens:     settings.MaxSummaryTokens,
	}
	snapshot := policy.Snapshot(
		common.GetContextKeyBool(c, constant.ContextKeyTokenContextCompaction),
		mode == "auto_compact" || mode == "managed",
	)
	common.SetContextKey(c, constant.ContextKeyContextConsensusPolicy, snapshot)
	return nil
}
