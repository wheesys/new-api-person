package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthoritativeTextCapabilitiesOnlyOptInNativeOpenAI(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		protocol    types.RelayFormat
		expected    bool
	}{
		{name: "openai chat", channelType: constant.ChannelTypeOpenAI, protocol: types.RelayFormatOpenAI, expected: true},
		{name: "openai responses", channelType: constant.ChannelTypeOpenAI, protocol: types.RelayFormatOpenAIResponses, expected: true},
		{name: "openai claude source", channelType: constant.ChannelTypeOpenAI, protocol: types.RelayFormatClaude, expected: true},
		{name: "openai gemini source", channelType: constant.ChannelTypeOpenAI, protocol: types.RelayFormatGemini, expected: true},
		{name: "azure closed", channelType: constant.ChannelTypeAzure, protocol: types.RelayFormatOpenAI},
		{name: "compaction closed", channelType: constant.ChannelTypeOpenAI, protocol: types.RelayFormatOpenAIResponsesCompaction},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities := (&Adaptor{}).TextRelayPreparationCapabilities(channel.TextRelayTargetInput{
				ChannelType:    test.channelType,
				SourceProtocol: test.protocol,
			})
			assert.Equal(t, test.expected, capabilities.OfflineConversion)
			assert.Equal(t, test.expected, capabilities.PureTargetResolution)
		})
	}
}

func TestResolveTextRelayTargetIsPureValueMapping(t *testing.T) {
	input := channel.TextRelayTargetInput{
		ChannelType:    constant.ChannelTypeOpenAI,
		OriginModel:    "gpt-client",
		UpstreamModel:  "gpt-5",
		SourceProtocol: types.RelayFormatClaude,
		FinalProtocol:  types.RelayFormatOpenAI,
		RelayMode:      relayconstant.RelayModeChatCompletions,
		RequestURLPath: "/v1/messages",
	}
	original := input

	target, err := (&Adaptor{}).ResolveTextRelayTarget(input)
	require.NoError(t, err)
	assert.Equal(t, original, input)
	assert.Equal(t, "gpt-5", target.Model)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), target.Protocol)
	assert.Equal(t, relayconstant.RelayModeChatCompletions, target.RelayMode)
	assert.Equal(t, "/v1/messages", target.RequestURLPath)
}

func TestConvertResponsesReasoningSuffixKeepsInfoAndBodyModelAligned(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5-high"}}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{Model: "gpt-5-high"})
	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, "gpt-5", request.Model)
	assert.Equal(t, "gpt-5", info.UpstreamModelName)
	require.NotNil(t, request.Reasoning)
	assert.Equal(t, "high", request.Reasoning.Effort)
}
