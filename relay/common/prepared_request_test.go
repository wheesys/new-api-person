package common

import (
	"encoding/hex"
	"io"
	"testing"

	appcommon "github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareJSONRelayRequestMetadataAndReaderConsistency(t *testing.T) {
	tests := []struct {
		name             string
		protocol         types.RelayFormat
		body             string
		fallbackModel    string
		expectedModel    string
		expectedMax      uint
		expectedMaxIsSet bool
	}{
		{name: "chat explicit zero", protocol: types.RelayFormatOpenAI, body: `{"model":"gpt-5","messages":[],"max_completion_tokens":0}`, expectedModel: "gpt-5", expectedMaxIsSet: true},
		{name: "responses explicit zero", protocol: types.RelayFormatOpenAIResponses, body: `{"model":"gpt-5","input":"hello","max_output_tokens":0}`, expectedModel: "gpt-5", expectedMaxIsSet: true},
		{name: "claude explicit zero", protocol: types.RelayFormatClaude, body: `{"model":"claude-sonnet-4","messages":[],"max_tokens":0}`, expectedModel: "claude-sonnet-4", expectedMaxIsSet: true},
		{name: "gemini explicit zero", protocol: types.RelayFormatGemini, body: `{"contents":[],"generationConfig":{"maxOutputTokens":0}}`, fallbackModel: "gemini-2.5-pro", expectedModel: "gemini-2.5-pro", expectedMaxIsSet: true},
		{name: "absent max output", protocol: types.RelayFormatOpenAI, body: `{"model":"gpt-4.1","messages":[]}`, expectedModel: "gpt-4.1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalBody := []byte(test.body)
			prepared, err := PrepareJSONRelayRequest(originalBody, PreparedRelayRequestMetadata{
				Model:    test.fallbackModel,
				Protocol: test.protocol,
			})
			require.NoError(t, err)
			defer prepared.Close()

			originalBody[0] = 'x'
			assert.Equal(t, test.expectedModel, prepared.Model())
			assert.Equal(t, test.protocol, prepared.Protocol())
			assert.Equal(t, int64(len(test.body)), prepared.Size())
			assert.Equal(t, hex.EncodeToString(appcommon.Sha256Raw([]byte(test.body))), prepared.BodyDigest())
			if test.expectedMaxIsSet {
				require.NotNil(t, prepared.RequestedMaxOutput())
				assert.Equal(t, test.expectedMax, *prepared.RequestedMaxOutput())
			} else {
				assert.Nil(t, prepared.RequestedMaxOutput())
			}

			for retry := 0; retry < 2; retry++ {
				reader, readerErr := prepared.Reader()
				require.NoError(t, readerErr)
				actualBody, readErr := io.ReadAll(reader)
				require.NoError(t, readErr)
				assert.Equal(t, test.body, string(actualBody))
			}
		})
	}
}

func TestPreparedRelayRequestReturnsImmutableCopies(t *testing.T) {
	prepared, err := PrepareJSONRelayRequest(
		[]byte(`{"model":"gpt-5","messages":[],"max_tokens":7}`),
		PreparedRelayRequestMetadata{Protocol: types.RelayFormatOpenAI},
	)
	require.NoError(t, err)
	defer prepared.Close()

	body, err := prepared.Body()
	require.NoError(t, err)
	body[0] = 'x'
	maxOutput := prepared.RequestedMaxOutput()
	require.NotNil(t, maxOutput)
	*maxOutput = 99

	reader, err := prepared.Reader()
	require.NoError(t, err)
	actualBody, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, `{"model":"gpt-5","messages":[],"max_tokens":7}`, string(actualBody))
	assert.Equal(t, uint(7), *prepared.RequestedMaxOutput())
}

func TestPreparePassThroughRelayRequestDoesNotCloseSharedStorage(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[],"max_tokens":0}`)
	storage, err := appcommon.CreateBodyStorage(body)
	require.NoError(t, err)
	defer storage.Close()

	prepared, err := PreparePassThroughRelayRequest(storage, PreparedRelayRequestMetadata{Protocol: types.RelayFormatOpenAI})
	require.NoError(t, err)
	reader, err := prepared.Reader()
	require.NoError(t, err)
	firstRead, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, body, firstRead)
	require.NoError(t, prepared.Close())

	reader, err = prepared.Reader()
	require.NoError(t, err)
	retryRead, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, body, retryRead)
}

func TestAuthoritativeTextTargetRequiresBodyModelExceptGemini(t *testing.T) {
	openAIRequest, err := PrepareJSONRelayRequest(
		[]byte(`{"messages":[]}`),
		PreparedRelayRequestMetadata{Model: "gpt-5", Protocol: types.RelayFormatOpenAI},
	)
	require.NoError(t, err)
	defer openAIRequest.Close()
	assert.False(t, openAIRequest.ModelFromBody())

	info := &RelayInfo{
		OriginModelName: "gpt-5",
		RelayMode:       relayconstant.RelayModeChatCompletions,
		RequestURLPath:  "/v1/chat/completions",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta:     &ChannelMeta{UpstreamModelName: "gpt-5"},
	}
	info.InitRequestConversionChain()
	info.RecordPreparedRelayRequest(openAIRequest)
	err = info.SealAuthoritativeTextTarget(AuthoritativeTextTarget{
		Model:          "gpt-5",
		Protocol:       types.RelayFormatOpenAI,
		RelayMode:      relayconstant.RelayModeChatCompletions,
		RequestURLPath: "/v1/chat/completions",
	}, openAIRequest)
	require.ErrorContains(t, err, "does not contain")

	geminiRequest, err := PrepareJSONRelayRequest(
		[]byte(`{"contents":[]}`),
		PreparedRelayRequestMetadata{Model: "gemini-2.5-pro", Protocol: types.RelayFormatGemini},
	)
	require.NoError(t, err)
	defer geminiRequest.Close()
	assert.False(t, geminiRequest.ModelFromBody())

	info.ResetPreparedRelayRequest()
	info.RelayMode = relayconstant.RelayModeGemini
	info.RequestURLPath = "/v1beta/models/gemini-2.5-pro:generateContent"
	info.RelayFormat = types.RelayFormatGemini
	info.UpstreamModelName = "gemini-2.5-pro"
	info.InitRequestConversionChain()
	info.RecordPreparedRelayRequest(geminiRequest)
	require.NoError(t, info.SealAuthoritativeTextTarget(AuthoritativeTextTarget{
		Model:          "gemini-2.5-pro",
		Protocol:       types.RelayFormatGemini,
		RelayMode:      relayconstant.RelayModeGemini,
		RequestURLPath: "/v1beta/models/gemini-2.5-pro:generateContent",
	}, geminiRequest))
}

func TestRelayInfoRecordsPreparedMetadataAndConversionChain(t *testing.T) {
	prepared, err := PrepareJSONRelayRequest(
		[]byte(`{"model":"gpt-5","input":"hello","max_output_tokens":0}`),
		PreparedRelayRequestMetadata{Protocol: types.RelayFormatOpenAIResponses},
	)
	require.NoError(t, err)
	defer prepared.Close()

	info := &RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses},
	}
	info.RecordPreparedRelayRequest(prepared)

	assert.Equal(t, "gpt-5", info.FinalRequestModel)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
	assert.Equal(t, prepared.BodyDigest(), info.FinalRequestBodyDigest)
	assert.Equal(t, prepared.Size(), info.FinalRequestBodySize)
	assert.Equal(t, prepared.Size(), info.UpstreamRequestBodySize)
	require.NotNil(t, info.FinalRequestRequestedMaxOutput)
	assert.Zero(t, *info.FinalRequestRequestedMaxOutput)
}

func TestRelayInfoResetPreparedRequestPreventsCrossAttemptProtocolLeak(t *testing.T) {
	zero := uint(0)
	info := &RelayInfo{
		RelayFormat:                    types.RelayFormatOpenAI,
		RequestConversionChain:         []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatGemini},
		FinalRequestRelayFormat:        types.RelayFormatGemini,
		FinalRequestModel:              "gemini-2.5-pro",
		FinalRequestBodyDigest:         "previous-digest",
		FinalRequestBodySize:           128,
		FinalRequestRequestedMaxOutput: &zero,
		UpstreamRequestBodySize:        128,
	}

	info.ResetPreparedRelayRequest()
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAI}, info.RequestConversionChain)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), info.GetFinalRequestRelayFormat())
	assert.Empty(t, info.FinalRequestModel)
	assert.Empty(t, info.FinalRequestBodyDigest)
	assert.Zero(t, info.FinalRequestBodySize)
	assert.Nil(t, info.FinalRequestRequestedMaxOutput)
	assert.Zero(t, info.UpstreamRequestBodySize)

	info.AppendRequestConversion(types.RelayFormatClaude)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude}, info.RequestConversionChain)
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
}
