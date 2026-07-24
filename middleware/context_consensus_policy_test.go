package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureContextConsensusPolicyRequiresAllThreeAuthorizations(t *testing.T) {
	originalSettings := model_setting.GetSmartRoutingSettings()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
			"smart_routing.context_consensus_enabled": boolString(originalSettings.ContextConsensusEnabled),
			"smart_routing.auto_compaction_enabled":   boolString(originalSettings.AutoCompactionEnabled),
			"smart_routing.preserved_recent_turns":    intString(originalSettings.PreservedRecentTurns),
			"smart_routing.max_summary_tokens":        intString(originalSettings.MaxSummaryTokens),
		}))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"smart_routing.context_consensus_enabled": "true",
		"smart_routing.auto_compaction_enabled":   "true",
		"smart_routing.preserved_recent_turns":    "4",
		"smart_routing.max_summary_tokens":        "1024",
	}))

	tests := []struct {
		name       string
		tokenAllow bool
		mode       string
		wantAllow  bool
	}{
		{name: "all authorized", tokenAllow: true, mode: "auto_compact", wantAllow: true},
		{name: "token denied", mode: "auto_compact"},
		{name: "request full", tokenAllow: true, mode: "full"},
		{name: "request omitted", tokenAllow: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			context.Request.Header.Set(contextModeHeader, test.mode)
			common.SetContextKey(context, constant.ContextKeyTokenContextCompaction, test.tokenAllow)

			require.NoError(t, captureContextConsensusPolicy(context))
			snapshot, ok := common.GetContextKeyType[contextconsensus.CompactionPolicySnapshot](context, constant.ContextKeyContextConsensusPolicy)
			require.True(t, ok)
			assert.Equal(t, test.wantAllow, contextconsensus.EvaluateCompactionAuthorization(snapshot).Allowed)
			assert.Equal(t, 4, snapshot.PreservedRecentTurns)
			assert.Equal(t, 1024, snapshot.MaxSummaryTokens)
			assert.Empty(t, context.Request.Header.Get(contextModeHeader))
		})
	}
}

func TestCaptureContextConsensusPolicyRejectsUnsupportedManagedHeaders(t *testing.T) {
	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "unknown mode", header: contextModeHeader, value: "unexpected"},
		{name: "managed mode", header: contextModeHeader, value: "managed"},
		{name: "context id", header: contextIDHeader, value: "opaque-context"},
		{name: "revision", header: contextRevisionHeader, value: "3"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			context.Request.Header.Set(test.header, test.value)

			require.Error(t, captureContextConsensusPolicy(context))
			assert.Empty(t, context.Request.Header.Get(contextIDHeader))
			assert.Empty(t, context.Request.Header.Get(contextModeHeader))
			assert.Empty(t, context.Request.Header.Get(contextRevisionHeader))
		})
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func intString(value int) string {
	return fmt.Sprintf("%d", value)
}
