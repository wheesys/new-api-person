package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gin-gonic/gin"
)

func TestGenerateTextOtherInfoIncludesSmartRoutingDecision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	common.SetContextKey(ctx, constant.ContextKeySmartRoutingDecision, smartrouting.Decision{
		Enabled:            true,
		Policy:             smartrouting.PolicyQualityFirst,
		TaskComplexity:     smartrouting.TaskComplex,
		ContextRequirement: smartrouting.ContextLong,
		OriginalModel:      "auto:quality",
		SelectedModel:      "premium-reasoning",
		SelectedChannelID:  12,
		CandidateCount:     4,
		FallbackIndex:      0,
		ScoreFactors: smartrouting.ScoreFactors{
			Cost:        0.20,
			Reliability: 0.98,
			Latency:     0.55,
			Quality:     0.97,
		},
		DecisionReasons: []string{"tools_required", "json_schema_required"},
		ContextConsensus: smartrouting.ContextConsensusLog{
			Mode:                    "stateless_full_context",
			Version:                 1,
			ValidationMode:          "validate_only",
			ValidationResult:        "would_block",
			Protocol:                "openai_responses",
			Compacted:               false,
			PreservedRecentMessages: 6,
			PreservedSegmentCount:   4,
			ToolExchangeCount:       2,
			InputTokensBefore:       2048,
			BindingLevel:            "credential",
			BindingReasonCodes:      []string{"responses_previous_response_id"},
			SwitchAllowed:           false,
			WouldBlock:              true,
		},
	})

	relayInfo := &relaycommon.RelayInfo{
		StartTime:         time.Unix(100, 0),
		FirstResponseTime: time.Unix(101, 0),
		ChannelMeta:       &relaycommon.ChannelMeta{},
	}

	other := GenerateTextOtherInfo(ctx, relayInfo, 1, 1, 1, 0, 0, 0, 1)

	rawSmartRouting, ok := other["smart_routing"]
	require.True(t, ok)
	smartRouting, ok := rawSmartRouting.(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, true, smartRouting["enabled"])
	assert.Equal(t, "quality_first", smartRouting["policy"])
	assert.Equal(t, "complex", smartRouting["complexity"])
	assert.Equal(t, "long", smartRouting["context_requirement"])
	assert.Equal(t, "auto:quality", smartRouting["original_model"])
	assert.Equal(t, "premium-reasoning", smartRouting["selected_model"])
	assert.Equal(t, 12, smartRouting["selected_channel_id"])
	assert.NotContains(t, smartRouting, "request_body")
	assert.NotContains(t, smartRouting, "api_key")

	contextConsensus, ok := smartRouting["context_consensus"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "validate_only", contextConsensus["validation_mode"])
	assert.Equal(t, "would_block", contextConsensus["validation_result"])
	assert.Equal(t, "credential", contextConsensus["binding_level"])
	assert.Equal(t, true, contextConsensus["would_block"])
	encodedOther, err := common.Marshal(other)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedOther), "resp-sensitive-reference")
	assert.NotContains(t, string(encodedOther), "tool-sensitive-result")
}
