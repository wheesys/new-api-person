package gemini

import (
	"context"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovertOpenAI2GeminiPreservesExplicitZeroMaxCompletionTokens(t *testing.T) {
	zero := uint(0)
	legacy := uint(7)
	request, err := relayconvert.OpenAIChatRequestToGeminiGenerateContent(context.Background(), dto.GeneralOpenAIRequest{
		Model:               "gemini-2.5-pro",
		MaxTokens:           &legacy,
		MaxCompletionTokens: &zero,
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro"},
	})
	require.NoError(t, err)
	require.NotNil(t, request.GenerationConfig.MaxOutputTokens)
	assert.Zero(t, *request.GenerationConfig.MaxOutputTokens)
}
