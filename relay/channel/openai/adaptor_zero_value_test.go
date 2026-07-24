package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIRequestPreservesExplicitZeroMaxCompletionTokens(t *testing.T) {
	zero := uint(0)
	legacy := uint(7)
	request := &dto.GeneralOpenAIRequest{
		Model:               "gpt-5",
		MaxTokens:           &legacy,
		MaxCompletionTokens: &zero,
	}
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-5",
		},
	}, request)
	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Nil(t, convertedRequest.MaxTokens)
	require.NotNil(t, convertedRequest.MaxCompletionTokens)
	assert.Zero(t, *convertedRequest.MaxCompletionTokens)
}
