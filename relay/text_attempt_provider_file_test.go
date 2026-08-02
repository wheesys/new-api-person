package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type providerFileLifecycleTestAdaptor struct {
	*preparedTextTestAdaptor
	capabilities contextconsensus.ProviderFileLifecycleCapabilities
	callCount    int
}

func (adaptor *providerFileLifecycleTestAdaptor) ProviderFileLifecycleCapabilities(_ *relaycommon.RelayInfo, _ contextconsensus.ProviderFileState) contextconsensus.ProviderFileLifecycleCapabilities {
	adaptor.callCount++
	return adaptor.capabilities
}

func newProviderFileLifecycleTestAttempt(t *testing.T, body []byte, protocol types.RelayFormat, adaptor relaychannel.Adaptor) *PreparedTextRelayAttempt {
	t.Helper()
	preparedRequest, err := relaycommon.PrepareJSONRelayRequest(body, relaycommon.PreparedRelayRequestMetadata{
		Model:    "provider-file-model",
		Protocol: protocol,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, preparedRequest.Close()) })
	return &PreparedTextRelayAttempt{
		info: &relaycommon.RelayInfo{
			RelayFormat:             protocol,
			RequestConversionChain:  []types.RelayFormat{protocol},
			FinalRequestRelayFormat: protocol,
			ChannelMeta:             &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		},
		adaptor:         adaptor,
		preparedRequest: preparedRequest,
	}
}

func TestValidateProviderFileLifecycleRequestUsesFrozenFinalBody(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":[{"type":"file","file":{"file_id":"file-frozen-secret"}}]}]}`)
	adaptor := &providerFileLifecycleTestAdaptor{
		preparedTextTestAdaptor: &preparedTextTestAdaptor{},
		capabilities: contextconsensus.ProviderFileLifecycleCapabilities{
			AuthoritativeOwnership:  true,
			AuthoritativeExpiration: true,
			AuthoritativeDeletion:   true,
		},
	}
	attempt := newProviderFileLifecycleTestAttempt(t, body, types.RelayFormatOpenAI, adaptor)
	for index := range body {
		body[index] = 'x'
	}

	state, err := attempt.ValidateProviderFileLifecycleRequest()
	require.NoError(t, err)
	require.Len(t, state.References, 1)
	assert.Equal(t, contextconsensus.ProviderFileReferenceProviderID, state.References[0].Kind)
	assert.NotEmpty(t, state.References[0].ReferenceHMAC)
	assert.Equal(t, 1, adaptor.callCount)
	assert.Zero(t, adaptor.doRequestCount)
	assert.Zero(t, adaptor.doResponseCount)
}

func TestValidateProviderFileLifecycleRequestFailsClosed(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-provider-secret"}]}]}`)
	completeCapabilities := contextconsensus.ProviderFileLifecycleCapabilities{
		AuthoritativeOwnership:  true,
		AuthoritativeExpiration: true,
		AuthoritativeDeletion:   true,
	}

	t.Run("existing adaptor has no lifecycle capability", func(t *testing.T) {
		adaptor := GetAdaptor(constant.APITypeOpenAI)
		require.NotNil(t, adaptor)
		_, implementsLifecycle := adaptor.(relaychannel.ProviderFileLifecycleAdaptor)
		assert.False(t, implementsLifecycle)
		attempt := newProviderFileLifecycleTestAttempt(t, body, types.RelayFormatOpenAIResponses, adaptor)
		_, err := attempt.ValidateProviderFileLifecycleRequest()
		require.ErrorContains(t, err, "does not declare")
	})

	t.Run("incomplete capabilities", func(t *testing.T) {
		adaptor := &providerFileLifecycleTestAdaptor{preparedTextTestAdaptor: &preparedTextTestAdaptor{}}
		attempt := newProviderFileLifecycleTestAttempt(t, body, types.RelayFormatOpenAIResponses, adaptor)
		_, err := attempt.ValidateProviderFileLifecycleRequest()
		require.ErrorContains(t, err, "complete authoritative")
	})

	t.Run("protocol conversion", func(t *testing.T) {
		adaptor := &providerFileLifecycleTestAdaptor{preparedTextTestAdaptor: &preparedTextTestAdaptor{}, capabilities: completeCapabilities}
		attempt := newProviderFileLifecycleTestAttempt(t, body, types.RelayFormatOpenAIResponses, adaptor)
		attempt.info.RelayFormat = types.RelayFormatOpenAI
		attempt.info.RequestConversionChain = []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}
		_, err := attempt.ValidateProviderFileLifecycleRequest()
		require.ErrorContains(t, err, "protocol conversion")
		assert.Zero(t, adaptor.callCount)
	})

	t.Run("pass-through", func(t *testing.T) {
		adaptor := &providerFileLifecycleTestAdaptor{preparedTextTestAdaptor: &preparedTextTestAdaptor{}, capabilities: completeCapabilities}
		attempt := newProviderFileLifecycleTestAttempt(t, body, types.RelayFormatOpenAIResponses, adaptor)
		attempt.passThrough = true
		_, err := attempt.ValidateProviderFileLifecycleRequest()
		require.ErrorContains(t, err, "pass-through")
		assert.Zero(t, adaptor.callCount)
	})

	t.Run("credential header override", func(t *testing.T) {
		adaptor := &providerFileLifecycleTestAdaptor{preparedTextTestAdaptor: &preparedTextTestAdaptor{}, capabilities: completeCapabilities}
		attempt := newProviderFileLifecycleTestAttempt(t, body, types.RelayFormatOpenAIResponses, adaptor)
		attempt.info.UseRuntimeHeadersOverride = true
		attempt.info.RuntimeHeadersOverride = map[string]interface{}{"Authorization": "must-not-be-read"}
		_, err := attempt.ValidateProviderFileLifecycleRequest()
		require.ErrorContains(t, err, "credential header")
		assert.Zero(t, adaptor.callCount)
	})
}

func TestValidateProviderFileLifecycleRequestDoesNotInferSignedURLLifecycle(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://storage.example.invalid/object?X-Amz-Signature=signed-secret"}}]}]}`)
	attempt := newProviderFileLifecycleTestAttempt(t, body, types.RelayFormatOpenAI, &preparedTextTestAdaptor{})

	_, err := attempt.ValidateProviderFileLifecycleRequest()
	require.ErrorContains(t, err, "signed provider file URLs")
}
