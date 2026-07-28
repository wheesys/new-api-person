package gemini

import (
	"testing"

	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthoritativeTextCapabilitiesOnlyOptInNativeGemini(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		protocol    types.RelayFormat
		expected    bool
	}{
		{name: "openai source closed because conversion may fetch media", channelType: appconstant.ChannelTypeGemini, protocol: types.RelayFormatOpenAI},
		{name: "responses source closed because conversion may fetch media", channelType: appconstant.ChannelTypeGemini, protocol: types.RelayFormatOpenAIResponses},
		{name: "claude source closed because conversion may fetch media", channelType: appconstant.ChannelTypeGemini, protocol: types.RelayFormatClaude},
		{name: "gemini native source", channelType: appconstant.ChannelTypeGemini, protocol: types.RelayFormatGemini, expected: true},
		{name: "vertex closed", channelType: appconstant.ChannelTypeVertexAi, protocol: types.RelayFormatGemini},
		{name: "compaction closed", channelType: appconstant.ChannelTypeGemini, protocol: types.RelayFormatOpenAIResponsesCompaction},
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

func TestResolveTextRelayTargetUsesOnlyFrozenSuffixPolicy(t *testing.T) {
	tests := []struct {
		name                   string
		model                  string
		thinkingAdapterEnabled bool
		preserveThinkingSuffix bool
		expectedModel          string
	}{
		{name: "adapter disabled", model: "gemini-2.5-pro-thinking", expectedModel: "gemini-2.5-pro-thinking"},
		{name: "suffix preserved", model: "gemini-2.5-pro-thinking", thinkingAdapterEnabled: true, preserveThinkingSuffix: true, expectedModel: "gemini-2.5-pro-thinking"},
		{name: "budget suffix", model: "gemini-2.5-pro-thinking-4096", thinkingAdapterEnabled: true, expectedModel: "gemini-2.5-pro"},
		{name: "no-thinking suffix", model: "gemini-2.5-pro-nothinking", thinkingAdapterEnabled: true, expectedModel: "gemini-2.5-pro"},
		{name: "effort suffix", model: "gemini-3-pro-high", thinkingAdapterEnabled: true, expectedModel: "gemini-3-pro"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := channel.TextRelayTargetInput{
				ChannelType:                  appconstant.ChannelTypeGemini,
				OriginModel:                  test.model,
				UpstreamModel:                test.model,
				SourceProtocol:               types.RelayFormatGemini,
				FinalProtocol:                types.RelayFormatGemini,
				RelayMode:                    constant.RelayModeGemini,
				RequestURLPath:               "/v1beta/models/test:generateContent",
				GeminiThinkingAdapterEnabled: test.thinkingAdapterEnabled,
				PreserveThinkingSuffix:       test.preserveThinkingSuffix,
			}
			original := input

			target, err := (&Adaptor{}).ResolveTextRelayTarget(input)
			require.NoError(t, err)
			assert.Equal(t, original, input)
			assert.Equal(t, test.expectedModel, target.Model)
			assert.Equal(t, types.RelayFormat(types.RelayFormatGemini), target.Protocol)
			assert.Equal(t, constant.RelayModeGemini, target.RelayMode)
			assert.Equal(t, input.RequestURLPath, target.RequestURLPath)
		})
	}
}

func TestGetRequestURLKeepsCompatibilityModelNormalization(t *testing.T) {
	geminiSettings := model_setting.GetGeminiSettings()
	originalThinkingAdapterEnabled := geminiSettings.ThinkingAdapterEnabled
	geminiSettings.ThinkingAdapterEnabled = true
	globalSettings := model_setting.GetGlobalSettings()
	originalBlacklist := append([]string(nil), globalSettings.ThinkingModelBlacklist...)
	globalSettings.ThinkingModelBlacklist = nil
	t.Cleanup(func() {
		geminiSettings.ThinkingAdapterEnabled = originalThinkingAdapterEnabled
		globalSettings.ThinkingModelBlacklist = originalBlacklist
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-pro-thinking-4096",
		RelayMode:       constant.RelayModeGemini,
		RelayFormat:     types.RelayFormatGemini,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       appconstant.ChannelTypeGemini,
			ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
			UpstreamModelName: "gemini-2.5-pro-thinking-4096",
		},
	}
	info.InitRequestConversionChain()

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Contains(t, requestURL, "/models/gemini-2.5-pro:generateContent")
	assert.Equal(t, "gemini-2.5-pro", info.UpstreamModelName)
}
