package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type authoritativePreparedTextAdaptor struct {
	*preparedTextTestAdaptor
	capabilities relaychannel.TextRelayPreparationCapabilities
	resolveCount int
	lastInput    relaychannel.TextRelayTargetInput
}

func (adaptor *authoritativePreparedTextAdaptor) TextRelayPreparationCapabilities(input relaychannel.TextRelayTargetInput) relaychannel.TextRelayPreparationCapabilities {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.lastInput = input
	return adaptor.capabilities
}

func (adaptor *authoritativePreparedTextAdaptor) ResolveTextRelayTarget(input relaychannel.TextRelayTargetInput) (relaychannel.TextRelayTarget, error) {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.resolveCount++
	adaptor.lastInput = input
	return relaychannel.TextRelayTarget{
		Model:          input.UpstreamModel,
		Protocol:       input.FinalProtocol,
		RelayMode:      input.RelayMode,
		RequestURLPath: input.RequestURLPath,
	}, nil
}

type driftingAuthoritativeAdaptor struct {
	*authoritativePreparedTextAdaptor
}

func (adaptor *driftingAuthoritativeAdaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	adaptor.mutex.Lock()
	adaptor.getRequestURLCount++
	adaptor.mutex.Unlock()
	info.UpstreamModelName = "model-drifted-during-url-resolution"
	return "https://example.com/v1/chat/completions", nil
}

type websocketURLTestAdaptor struct {
	relaychannel.Adaptor
	requestURL string
}

func (adaptor *websocketURLTestAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) {
	return adaptor.requestURL, nil
}

func (adaptor *websocketURLTestAdaptor) SetupRequestHeader(*gin.Context, *http.Header, *relaycommon.RelayInfo) error {
	return nil
}

func newAuthorityOpenAITextTestInfo() (*gin.Context, *relaycommon.RelayInfo) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		Request: &dto.GeneralOpenAIRequest{
			Model:    "gpt-authoritative",
			Messages: []dto.Message{{Role: "user", Content: "hello"}},
		},
		OriginModelName: "gpt-authoritative",
		RelayMode:       relayconstant.RelayModeChatCompletions,
		RequestURLPath:  "/v1/chat/completions",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-authoritative",
		},
	}
	info.InitRequestConversionChain()
	return context, info
}

func TestAuthoritativeTextPreparationFailsClosedBeforeConversion(t *testing.T) {
	fullCapabilities := relaychannel.TextRelayPreparationCapabilities{OfflineConversion: true, PureTargetResolution: true}
	tests := []struct {
		name       string
		adaptor    relaychannel.Adaptor
		configure  func(*relaycommon.RelayInfo)
		compatible bool
	}{
		{name: "missing capability interface", adaptor: &preparedTextTestAdaptor{}},
		{name: "capability false", adaptor: &authoritativePreparedTextAdaptor{preparedTextTestAdaptor: &preparedTextTestAdaptor{}}},
		{
			name:    "pass-through",
			adaptor: &authoritativePreparedTextAdaptor{preparedTextTestAdaptor: &preparedTextTestAdaptor{}, capabilities: fullCapabilities},
			configure: func(info *relaycommon.RelayInfo) {
				info.ChannelSetting.PassThroughBodyEnabled = true
			},
		},
		{name: "compatible entry remains open", adaptor: &preparedTextTestAdaptor{}, compatible: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, info := newAuthorityOpenAITextTestInfo()
			if test.configure != nil {
				test.configure(info)
			}
			policy := authoritativeTextAttemptPolicy
			if test.compatible {
				policy = compatibleTextAttemptPolicy
			}
			attempt, newAPIError := prepareOpenAITextAttemptWithAdaptorPolicy(context, info, test.adaptor, policy)
			if test.compatible {
				require.Nil(t, newAPIError)
				require.NotNil(t, attempt)
				assert.NoError(t, attempt.Close())
			} else {
				assert.Nil(t, attempt)
				require.NotNil(t, newAPIError)
			}

			var conversionCount int
			switch adaptor := test.adaptor.(type) {
			case *preparedTextTestAdaptor:
				adaptor.mutex.Lock()
				conversionCount = adaptor.convertOpenAIRequestCount
				adaptor.mutex.Unlock()
			case *authoritativePreparedTextAdaptor:
				adaptor.mutex.Lock()
				conversionCount = adaptor.convertOpenAIRequestCount
				assert.Zero(t, adaptor.resolveCount)
				adaptor.mutex.Unlock()
			}
			if test.compatible {
				assert.Equal(t, 1, conversionCount)
			} else {
				assert.Zero(t, conversionCount)
			}
		})
	}
}

func TestVertexAdaptorDoesNotOptInToAuthoritativeTextPreparation(t *testing.T) {
	adaptor := GetAdaptor(constant.APITypeVertexAi)
	require.NotNil(t, adaptor)

	_, authoritative := adaptor.(relaychannel.AuthoritativeTextRelayAdaptor)
	assert.False(t, authoritative)
}

func TestPrepareAuthoritativeTextRelayAttemptWithNativeOpenAI(t *testing.T) {
	context, info := newAuthorityOpenAITextTestInfo()
	context.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeOpenAI)
	context.Set(string(constant.ContextKeyOriginalModel), "gpt-authoritative")

	attempt, newAPIError := PrepareAuthoritativeTextRelayAttempt(context, info)
	require.Nil(t, newAPIError)
	require.NotNil(t, attempt)
	defer attempt.Close()

	target, ok := attempt.AuthoritativeTarget()
	require.True(t, ok)
	assert.Equal(t, "gpt-authoritative", target.Model)
	assert.Equal(t, types.RelayFormatOpenAI, target.Protocol)
	assert.True(t, attempt.PreparedRequest().ModelFromBody())
	assert.NoError(t, info.ValidateAuthoritativeTextTarget())
}

func TestAuthoritativeTargetDoesNotExposeOrIgnoreRequestQuery(t *testing.T) {
	const secret = "query-credential"
	context, info := newAuthorityOpenAITextTestInfo()
	info.RequestURLPath = "/v1/chat/completions?key=" + secret
	adaptor := &authoritativePreparedTextAdaptor{
		preparedTextTestAdaptor: &preparedTextTestAdaptor{},
		capabilities: relaychannel.TextRelayPreparationCapabilities{
			OfflineConversion:    true,
			PureTargetResolution: true,
		},
	}

	attempt, newAPIError := prepareOpenAITextAttemptWithAdaptorPolicy(context, info, adaptor, authoritativeTextAttemptPolicy)
	require.Nil(t, newAPIError)
	require.NotNil(t, attempt)
	defer attempt.Close()

	target, ok := attempt.AuthoritativeTarget()
	require.True(t, ok)
	assert.Equal(t, "/v1/chat/completions", target.RequestURLPath)
	assert.NotContains(t, target.RequestURLPath, secret)
	adaptor.mutex.Lock()
	assert.Equal(t, "/v1/chat/completions", adaptor.lastInput.RequestURLPath)
	adaptor.mutex.Unlock()

	info.RequestURLPath = "/v1/chat/completions?key=changed"
	require.ErrorContains(t, info.ValidateAuthoritativeTextTarget(), "request path changed")
}

func TestAuthoritativeTextPreparationRejectsSnapshotModelMismatch(t *testing.T) {
	context, info := newAuthorityOpenAITextTestInfo()
	info.ParamOverride = map[string]interface{}{"model": "model-changed-by-override"}
	adaptor := &authoritativePreparedTextAdaptor{
		preparedTextTestAdaptor: &preparedTextTestAdaptor{},
		capabilities: relaychannel.TextRelayPreparationCapabilities{
			OfflineConversion:    true,
			PureTargetResolution: true,
		},
	}

	attempt, newAPIError := prepareOpenAITextAttemptWithAdaptorPolicy(context, info, adaptor, authoritativeTextAttemptPolicy)
	assert.Nil(t, attempt)
	require.NotNil(t, newAPIError)
	assert.NoError(t, info.ValidateAuthoritativeTextTarget())
	adaptor.mutex.Lock()
	assert.Equal(t, 1, adaptor.resolveCount)
	assert.Zero(t, adaptor.doRequestCount)
	adaptor.mutex.Unlock()
}

func TestAuthoritativeTargetSealBlocksURLResolutionDriftBeforeNetwork(t *testing.T) {
	context, info := newAuthorityOpenAITextTestInfo()
	adaptor := &driftingAuthoritativeAdaptor{authoritativePreparedTextAdaptor: &authoritativePreparedTextAdaptor{
		preparedTextTestAdaptor: &preparedTextTestAdaptor{},
		capabilities: relaychannel.TextRelayPreparationCapabilities{
			OfflineConversion:    true,
			PureTargetResolution: true,
		},
	}}

	attempt, newAPIError := prepareOpenAITextAttemptWithAdaptorPolicy(context, info, adaptor, authoritativeTextAttemptPolicy)
	require.Nil(t, newAPIError)
	require.NotNil(t, attempt)
	defer attempt.Close()
	target, ok := attempt.AuthoritativeTarget()
	require.True(t, ok)
	assert.Equal(t, "gpt-authoritative", target.Model)

	_, err := relaychannel.DoApiRequest(adaptor, context, info, strings.NewReader(`{"model":"gpt-authoritative"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after request URL resolution")
	adaptor.mutex.Lock()
	assert.Equal(t, 1, adaptor.getRequestURLCount)
	adaptor.mutex.Unlock()
}

func TestWebsocketDialErrorMasksQuerySecret(t *testing.T) {
	const secret = "vertex-query-secret"
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
	adaptor := &websocketURLTestAdaptor{requestURL: "ws://127.0.0.1:0/v1/realtime?key=" + secret}

	connection, err := relaychannel.DoWssRequest(adaptor, context, &relaycommon.RelayInfo{}, strings.NewReader(""))
	if connection != nil {
		_ = connection.Close()
	}
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.Contains(t, err.Error(), "key=***")
}
