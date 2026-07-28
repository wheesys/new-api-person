package middleware

import (
	"fmt"
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
	contextIDPresent := strings.TrimSpace(c.Request.Header.Get(contextIDHeader)) != ""
	revisionPresent := strings.TrimSpace(c.Request.Header.Get(contextRevisionHeader)) != ""

	c.Request.Header.Del(contextIDHeader)
	c.Request.Header.Del(contextModeHeader)
	c.Request.Header.Del(contextRevisionHeader)

	if contextIDPresent || revisionPresent || mode == "managed" {
		return fmt.Errorf("managed context is not supported")
	}
	if mode != "" && mode != "full" && mode != "auto_compact" {
		return fmt.Errorf("unsupported context mode")
	}

	settings := model_setting.GetSmartRoutingSettings()
	policy := contextconsensus.CompactionPolicy{
		SystemEnabled:        settings.ContextConsensusEnabled && settings.AutoCompactionEnabled,
		PolicyVersion:        "context-consensus-v1",
		PreservedRecentTurns: settings.PreservedRecentTurns,
		TargetInputTokens:    settings.MaxCompactionInputTokens,
		MaxSummaryTokens:     settings.MaxSummaryTokens,
	}
	snapshot := policy.Snapshot(
		common.GetContextKeyBool(c, constant.ContextKeyTokenContextCompaction),
		mode == "auto_compact",
	)
	common.SetContextKey(c, constant.ContextKeyContextConsensusPolicy, snapshot)
	return nil
}
