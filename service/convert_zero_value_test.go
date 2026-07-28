package service

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiToOpenAIRequestPreservesExplicitZeroMaxOutputTokens(t *testing.T) {
	zero := uint(0)
	request, err := GeminiToOpenAIRequest(&dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{MaxOutputTokens: &zero},
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"},
	})
	require.NoError(t, err)
	require.NotNil(t, request.MaxTokens)
	assert.Zero(t, *request.MaxTokens)
}
