package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
			"smart_routing.context_consensus_enabled":   boolString(originalSettings.ContextConsensusEnabled),
			"smart_routing.auto_compaction_enabled":     boolString(originalSettings.AutoCompactionEnabled),
			"smart_routing.managed_context_enabled":     boolString(originalSettings.ManagedContextEnabled),
			"smart_routing.preserved_recent_turns":      intString(originalSettings.PreservedRecentTurns),
			"smart_routing.max_summary_tokens":          intString(originalSettings.MaxSummaryTokens),
			"smart_routing.max_compaction_input_tokens": intString(originalSettings.MaxCompactionInputTokens),
			"smart_routing.context_state_ttl_seconds":   intString(originalSettings.ContextStateTTLSeconds),
		}))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"smart_routing.context_consensus_enabled":   "true",
		"smart_routing.auto_compaction_enabled":     "true",
		"smart_routing.preserved_recent_turns":      "4",
		"smart_routing.max_summary_tokens":          "1024",
		"smart_routing.max_compaction_input_tokens": "64000",
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
			assert.Equal(t, 64000, snapshot.TargetInputTokens)
			assert.Empty(t, context.Request.Header.Get(contextModeHeader))
		})
	}
}

func TestCaptureContextConsensusPolicyRejectsInvalidManagedHeaders(t *testing.T) {
	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "unknown mode", header: contextModeHeader, value: "unexpected"},
		{name: "managed mode without contract", header: contextModeHeader, value: "managed"},
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

func TestCaptureContextConsensusPolicyCapturesManagedContract(t *testing.T) {
	originalSettings := model_setting.GetSmartRoutingSettings()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
			"smart_routing.context_consensus_enabled": boolString(originalSettings.ContextConsensusEnabled),
			"smart_routing.managed_context_enabled":   boolString(originalSettings.ManagedContextEnabled),
		}))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"smart_routing.context_consensus_enabled": "true",
		"smart_routing.managed_context_enabled":   "true",
	}))

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	context.Request.Header.Set(contextModeHeader, "managed")
	context.Request.Header.Set(contextIDHeader, "opaque-context")
	context.Request.Header.Set(contextRevisionHeader, "7")
	context.Request.Header.Set(contextIdempotencyHeader, "request-key-1234567890")
	common.SetContextKey(context, constant.ContextKeyTokenContextCompaction, true)

	require.NoError(t, captureContextConsensusPolicy(context))
	managedRequest, ok := common.GetContextKeyType[contextconsensus.ManagedContextRequest](context, constant.ContextKeyManagedContextRequest)
	require.True(t, ok)
	assert.Equal(t, "opaque-context", managedRequest.ExternalContextID)
	assert.Equal(t, uint64(7), managedRequest.ExpectedRevision)
	assert.Equal(t, "request-key-1234567890", managedRequest.IdempotencyKey)
	assert.Empty(t, context.Request.Header.Get(contextIDHeader))
	assert.Empty(t, context.Request.Header.Get(contextModeHeader))
	assert.Empty(t, context.Request.Header.Get(contextRevisionHeader))
	assert.Empty(t, context.Request.Header.Get(contextIdempotencyHeader))
}

func TestManagedContextIdempotencyKeyContract(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "minimum", value: strings.Repeat("a", 16), valid: true},
		{name: "maximum and alphabet", value: strings.Repeat("A0._~-z", 18) + "ab", valid: true},
		{name: "too short", value: strings.Repeat("a", 15)},
		{name: "too long", value: strings.Repeat("a", 129)},
		{name: "space", value: "invalid key value"},
		{name: "leading space", value: " request-key-1234567890"},
		{name: "trailing space", value: "request-key-1234567890 "},
		{name: "non ascii", value: "请求幂等键-1234567890"},
		{name: "separator", value: "invalid,key-123456"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.valid, validManagedContextIdempotencyKey(test.value))
		})
	}
}

func TestCaptureContextConsensusPolicyRejectsDuplicateIdempotencyHeaderAndStripsIt(t *testing.T) {
	contextValue, _ := gin.CreateTestContext(httptest.NewRecorder())
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	contextValue.Request.Header.Set(contextModeHeader, "managed")
	contextValue.Request.Header.Set(contextIDHeader, "opaque-context")
	contextValue.Request.Header.Set(contextRevisionHeader, "0")
	contextValue.Request.Header.Add(contextIdempotencyHeader, "request-key-1234567890")
	contextValue.Request.Header.Add(contextIdempotencyHeader, "request-key-0987654321")

	require.Error(t, captureContextConsensusPolicy(contextValue))
	assert.Empty(t, contextValue.Request.Header.Values(contextIdempotencyHeader))
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
