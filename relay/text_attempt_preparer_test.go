package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	appcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type preparedTextTestAdaptor struct {
	channel.Adaptor

	mutex                        sync.Mutex
	initCount                    int
	convertOpenAIRequestCount    int
	convertResponsesRequestCount int
	convertClaudeRequestCount    int
	convertGeminiRequestCount    int
	getRequestURLCount           int
	doRequestCount               int
	doResponseCount              int
	sentBody                     []byte
	modeDuringResponsesConvert   int
	pathDuringResponsesConvert   string
	modeDuringRequest            int
	pathDuringRequest            string
	requestStarted               chan struct{}
	releaseRequest               chan struct{}
	responseBody                 string
	responseContentType          string
	convertedGeminiRequest       any
}

func (adaptor *preparedTextTestAdaptor) Init(_ *relaycommon.RelayInfo) {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.initCount++
}

func (adaptor *preparedTextTestAdaptor) ConvertOpenAIRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.convertOpenAIRequestCount++
	return request, nil
}

func (adaptor *preparedTextTestAdaptor) ConvertOpenAIResponsesRequest(_ *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.convertResponsesRequestCount++
	adaptor.modeDuringResponsesConvert = info.RelayMode
	adaptor.pathDuringResponsesConvert = info.RequestURLPath
	return request, nil
}

func (adaptor *preparedTextTestAdaptor) ConvertClaudeRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.convertClaudeRequestCount++
	return request, nil
}

func (adaptor *preparedTextTestAdaptor) ConvertGeminiRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.convertGeminiRequestCount++
	if adaptor.convertedGeminiRequest != nil {
		return adaptor.convertedGeminiRequest, nil
	}
	return request, nil
}

func (adaptor *preparedTextTestAdaptor) GetRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.getRequestURLCount++
	return "https://example.com", nil
}

func (adaptor *preparedTextTestAdaptor) DoRequest(_ *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}

	adaptor.mutex.Lock()
	adaptor.doRequestCount++
	adaptor.sentBody = append([]byte(nil), body...)
	adaptor.modeDuringRequest = info.RelayMode
	adaptor.pathDuringRequest = info.RequestURLPath
	requestStarted := adaptor.requestStarted
	releaseRequest := adaptor.releaseRequest
	responseBody := adaptor.responseBody
	responseContentType := adaptor.responseContentType
	adaptor.mutex.Unlock()

	if requestStarted != nil {
		close(requestStarted)
	}
	if releaseRequest != nil {
		<-releaseRequest
	}
	if responseContentType == "" {
		responseContentType = "application/json"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{responseContentType}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}, nil
}

func TestPrepareNativeTextProtocolsFreezesExactlyWhatExecuteSends(t *testing.T) {
	zero := uint(0)
	tests := []struct {
		name                    string
		relayFormat             types.RelayFormat
		relayMode               int
		request                 dto.Request
		upstreamModel           string
		prepare                 func(*gin.Context, *relaycommon.RelayInfo, channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError)
		expectedProtocol        types.RelayFormat
		expectedConversionCount func(*preparedTextTestAdaptor) int
		expectStreamDetection   bool
		configureAdaptor        func(*preparedTextTestAdaptor)
		assertPreparedBody      func(*testing.T, []byte)
	}{
		{
			name:        "responses",
			relayFormat: types.RelayFormatOpenAIResponses,
			relayMode:   relayconstant.RelayModeResponses,
			request: &dto.OpenAIResponsesRequest{
				Model:           "gpt-upstream",
				Input:           json.RawMessage(`"hello"`),
				MaxOutputTokens: &zero,
			},
			upstreamModel:    "gpt-upstream",
			prepare:          prepareResponsesTextAttemptWithAdaptor,
			expectedProtocol: types.RelayFormatOpenAIResponses,
			expectedConversionCount: func(adaptor *preparedTextTestAdaptor) int {
				return adaptor.convertResponsesRequestCount
			},
		},
		{
			name:        "claude explicit zero",
			relayFormat: types.RelayFormatClaude,
			relayMode:   relayconstant.RelayModeUnknown,
			request: &dto.ClaudeRequest{
				Model:     "claude-upstream",
				Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
				MaxTokens: &zero,
			},
			upstreamModel:    "claude-upstream",
			prepare:          prepareClaudeTextAttemptWithAdaptor,
			expectedProtocol: types.RelayFormatClaude,
			expectedConversionCount: func(adaptor *preparedTextTestAdaptor) int {
				return adaptor.convertClaudeRequestCount
			},
			expectStreamDetection: true,
		},
		{
			name:        "gemini keeps unfiltered field and explicit zero",
			relayFormat: types.RelayFormatGemini,
			relayMode:   relayconstant.RelayModeGemini,
			request: &dto.GeminiChatRequest{
				Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}},
			},
			upstreamModel:    "gemini-upstream",
			prepare:          prepareGeminiTextAttemptWithAdaptor,
			expectedProtocol: types.RelayFormatGemini,
			expectedConversionCount: func(adaptor *preparedTextTestAdaptor) int {
				return adaptor.convertGeminiRequestCount
			},
			expectStreamDetection: true,
			configureAdaptor: func(adaptor *preparedTextTestAdaptor) {
				adaptor.convertedGeminiRequest = map[string]any{
					"contents": []any{},
					"generationConfig": map[string]any{
						"maxOutputTokens": 0,
					},
					"service_tier": "priority",
				}
			},
			assertPreparedBody: func(t *testing.T, body []byte) {
				var decoded map[string]any
				require.NoError(t, appcommon.Unmarshal(body, &decoded))
				assert.Equal(t, "priority", decoded["service_tier"])
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/test", nil)
			adaptor := &preparedTextTestAdaptor{responseContentType: "text/event-stream; charset=utf-8"}
			if test.configureAdaptor != nil {
				test.configureAdaptor(adaptor)
			}
			info := &relaycommon.RelayInfo{
				Request:         test.request,
				OriginModelName: test.upstreamModel,
				RelayMode:       test.relayMode,
				RequestURLPath:  "/v1/test",
				RelayFormat:     test.relayFormat,
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: test.upstreamModel,
				},
			}
			info.InitRequestConversionChain()

			attempt, newAPIError := test.prepare(context, info, adaptor)
			require.Nil(t, newAPIError)
			require.NotNil(t, attempt)
			defer attempt.Close()

			preparedBody, err := attempt.PreparedRequest().Body()
			require.NoError(t, err)
			assert.Equal(t, test.expectedProtocol, attempt.PreparedRequest().Protocol())
			require.NotNil(t, attempt.PreparedRequest().RequestedMaxOutput())
			assert.Zero(t, *attempt.PreparedRequest().RequestedMaxOutput())
			assert.Empty(t, recorder.Body.String())
			assert.Empty(t, recorder.Header())
			if test.assertPreparedBody != nil {
				test.assertPreparedBody(t, preparedBody)
			}
			switch originalRequest := test.request.(type) {
			case *dto.OpenAIResponsesRequest:
				originalRequest.Model = "mutated-after-prepare"
			case *dto.ClaudeRequest:
				originalRequest.Model = "mutated-after-prepare"
			case *dto.GeminiChatRequest:
				originalRequest.Contents[0].Parts[0].Text = "mutated-after-prepare"
			}

			adaptor.mutex.Lock()
			assert.Equal(t, 1, test.expectedConversionCount(adaptor))
			assert.Zero(t, adaptor.getRequestURLCount)
			assert.Zero(t, adaptor.doRequestCount)
			assert.Zero(t, adaptor.doResponseCount)
			adaptor.mutex.Unlock()

			usage, newAPIError := ExecutePreparedTextRelayAttempt(context, info, attempt)
			require.Nil(t, newAPIError)
			require.NotNil(t, usage)
			adaptor.mutex.Lock()
			assert.Equal(t, preparedBody, adaptor.sentBody)
			assert.Equal(t, 1, adaptor.doRequestCount)
			assert.Equal(t, 1, adaptor.doResponseCount)
			assert.Zero(t, adaptor.getRequestURLCount)
			adaptor.mutex.Unlock()
			assert.Equal(t, test.expectStreamDetection, info.IsStream)
		})
	}
}

func TestValidateManagedProviderStateRequestUsesFrozenBodyAndEffectiveHeaders(t *testing.T) {
	newAttempt := func(t *testing.T, body string, staticHeaders, runtimeHeaders map[string]interface{}) *PreparedTextRelayAttempt {
		t.Helper()
		preparedRequest, err := relaycommon.PrepareJSONRelayRequest([]byte(body), relaycommon.PreparedRelayRequestMetadata{
			Model: "gpt-upstream", Protocol: types.RelayFormatOpenAIResponses,
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, preparedRequest.Close()) })
		info := &relaycommon.RelayInfo{
			RelayFormat: types.RelayFormatOpenAIResponses, RelayMode: relayconstant.RelayModeResponses,
			RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAIResponses},
			FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
			ChannelMeta:             &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, HeadersOverride: staticHeaders},
		}
		if runtimeHeaders != nil {
			info.UseRuntimeHeadersOverride = true
			info.RuntimeHeadersOverride = runtimeHeaders
		}
		return &PreparedTextRelayAttempt{info: info, adaptor: GetAdaptor(constant.APITypeOpenAI), preparedRequest: preparedRequest}
	}

	require.NoError(t, newAttempt(t, `{"model":"gpt-upstream","input":"hello"}`, nil, nil).ValidateManagedProviderStateRequest(nil))
	require.ErrorContains(t, newAttempt(t, `{"model":"gpt-upstream","input":"hello","store":false}`, nil, nil).ValidateManagedProviderStateRequest(nil), "storable")
	require.ErrorContains(t, newAttempt(t, `{"model":"gpt-upstream","input":"hello","previous_response_id":"resp_unbound"}`, nil, nil).ValidateManagedProviderStateRequest(nil), "authenticated binding")
	require.ErrorContains(t, newAttempt(t, `{"model":"gpt-upstream","input":"hello","conversation":"conv_injected"}`, nil, nil).ValidateManagedProviderStateRequest(nil), "provider-owned field")
	require.ErrorContains(t, newAttempt(t, `{"model":"gpt-upstream","input":[{"type":"item_reference","id":"item_injected"}]}`, nil, nil).ValidateManagedProviderStateRequest(nil), "provider-owned input type")
	require.ErrorContains(t, newAttempt(t, `{"model":"gpt-upstream","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file_injected"}]}]}`, nil, nil).ValidateManagedProviderStateRequest(nil), "provider-owned file ID")
	require.ErrorContains(t, newAttempt(t, `{"model":"gpt-upstream","input":"hello"}`, map[string]interface{}{"Authorization": "redacted"}, nil).ValidateManagedProviderStateRequest(nil), "credential header")
	require.ErrorContains(t, newAttempt(t, `{"model":"gpt-upstream","input":"hello"}`, nil, map[string]interface{}{"x-api-key": "redacted"}).ValidateManagedProviderStateRequest(nil), "credential header")
}

func TestPrepareResponsesCompactionRejectsUnsupportedAdaptorBeforeConversion(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	adaptor := &preparedTextTestAdaptor{}
	info := &relaycommon.RelayInfo{
		Request:         &dto.OpenAIResponsesCompactionRequest{Model: "gpt-upstream"},
		OriginModelName: "gpt-upstream-compact",
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		RelayFormat:     types.RelayFormatOpenAIResponsesCompaction,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeGemini,
			UpstreamModelName: "gpt-upstream",
		},
	}
	info.InitRequestConversionChain()

	attempt, newAPIError := prepareResponsesTextAttemptWithAdaptor(context, info, adaptor)
	assert.Nil(t, attempt)
	require.NotNil(t, newAPIError)
	adaptor.mutex.Lock()
	assert.Zero(t, adaptor.initCount)
	assert.Zero(t, adaptor.convertResponsesRequestCount)
	assert.Zero(t, adaptor.getRequestURLCount)
	assert.Zero(t, adaptor.doRequestCount)
	assert.Zero(t, adaptor.doResponseCount)
	adaptor.mutex.Unlock()
}

func TestPrepareNativeTextProtocolPassThroughBorrowsBodyWithoutConversion(t *testing.T) {
	tests := []struct {
		name        string
		relayFormat types.RelayFormat
		relayMode   int
		request     dto.Request
		prepare     func(*gin.Context, *relaycommon.RelayInfo, channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError)
	}{
		{
			name:        "responses",
			relayFormat: types.RelayFormatOpenAIResponses,
			relayMode:   relayconstant.RelayModeResponses,
			request:     &dto.OpenAIResponsesRequest{Model: "upstream-model", Input: json.RawMessage(`"hello"`)},
			prepare:     prepareResponsesTextAttemptWithAdaptor,
		},
		{
			name:        "claude",
			relayFormat: types.RelayFormatClaude,
			relayMode:   relayconstant.RelayModeUnknown,
			request:     &dto.ClaudeRequest{Model: "upstream-model", Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}},
			prepare:     prepareClaudeTextAttemptWithAdaptor,
		},
		{
			name:        "gemini",
			relayFormat: types.RelayFormatGemini,
			relayMode:   relayconstant.RelayModeGemini,
			request:     &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}}},
			prepare:     prepareGeminiTextAttemptWithAdaptor,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalBody := []byte(`{"model":"client-body-model","input":"unchanged"}`)
			storage, err := appcommon.CreateBodyStorage(originalBody)
			require.NoError(t, err)
			defer storage.Close()

			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/test", nil)
			context.Set(appcommon.KeyBodyStorage, storage)
			adaptor := &preparedTextTestAdaptor{}
			info := &relaycommon.RelayInfo{
				Request:         test.request,
				OriginModelName: "upstream-model",
				RelayMode:       test.relayMode,
				RequestURLPath:  "/v1/test",
				RelayFormat:     test.relayFormat,
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: "upstream-model",
					ChannelSetting: dto.ChannelSettings{
						PassThroughBodyEnabled: true,
					},
				},
			}
			info.InitRequestConversionChain()

			attempt, newAPIError := test.prepare(context, info, adaptor)
			require.Nil(t, newAPIError)
			require.NotNil(t, attempt)
			preparedBody, err := attempt.PreparedRequest().Body()
			require.NoError(t, err)
			assert.Equal(t, originalBody, preparedBody)

			adaptor.mutex.Lock()
			assert.Equal(t, 1, adaptor.initCount)
			assert.Zero(t, adaptor.convertResponsesRequestCount)
			assert.Zero(t, adaptor.convertClaudeRequestCount)
			assert.Zero(t, adaptor.convertGeminiRequestCount)
			assert.Zero(t, adaptor.doRequestCount)
			assert.Zero(t, adaptor.doResponseCount)
			adaptor.mutex.Unlock()

			require.NoError(t, attempt.Close())
			bodyAfterClose, err := storage.Bytes()
			require.NoError(t, err)
			assert.Equal(t, originalBody, bodyAfterClose)
		})
	}
}

func TestPrepareClaudeViaResponsesUsesSingleBridgeConversion(t *testing.T) {
	globalSettings := model_setting.GetGlobalSettings()
	originalPolicy := globalSettings.ChatCompletionsToResponsesPolicy
	globalSettings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{
		Enabled:       true,
		AllChannels:   true,
		ModelPatterns: []string{`^claude-upstream$`},
	}
	t.Cleanup(func() {
		globalSettings.ChatCompletionsToResponsesPolicy = originalPolicy
	})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	adaptor := &preparedTextTestAdaptor{}
	info := &relaycommon.RelayInfo{
		Request: &dto.ClaudeRequest{
			Model:     "claude-upstream",
			Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
			MaxTokens: appcommon.GetPointer[uint](0),
		},
		OriginModelName: "claude-upstream",
		RelayMode:       relayconstant.RelayModeUnknown,
		RequestURLPath:  "/v1/messages",
		RelayFormat:     types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         7,
			UpstreamModelName: "claude-upstream",
		},
	}
	info.InitRequestConversionChain()

	attempt, newAPIError := prepareClaudeTextAttemptWithAdaptor(context, info, adaptor)
	require.Nil(t, newAPIError)
	require.NotNil(t, attempt)
	assert.Equal(t, preparedTextResponseModeChatViaResponses, attempt.responseMode)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), attempt.PreparedRequest().Protocol())
	assert.Equal(t, relayconstant.RelayModeResponses, info.RelayMode)
	assert.Equal(t, "/v1/responses", info.RequestURLPath)
	require.NotNil(t, attempt.PreparedRequest().RequestedMaxOutput())
	assert.Zero(t, *attempt.PreparedRequest().RequestedMaxOutput())

	adaptor.mutex.Lock()
	assert.Equal(t, 1, adaptor.initCount)
	assert.Zero(t, adaptor.convertOpenAIRequestCount)
	assert.Zero(t, adaptor.convertClaudeRequestCount)
	assert.Equal(t, 1, adaptor.convertResponsesRequestCount)
	assert.Zero(t, adaptor.doRequestCount)
	assert.Zero(t, adaptor.doResponseCount)
	adaptor.mutex.Unlock()

	require.NoError(t, attempt.Close())
	assert.Equal(t, relayconstant.RelayModeUnknown, info.RelayMode)
	assert.Equal(t, "/v1/messages", info.RequestURLPath)
}

func (adaptor *preparedTextTestAdaptor) DoResponse(_ *gin.Context, _ *http.Response, _ *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	adaptor.mutex.Lock()
	defer adaptor.mutex.Unlock()
	adaptor.doResponseCount++
	return &dto.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}, nil
}

func newPreparedOpenAITextTest(t *testing.T, adaptor *preparedTextTestAdaptor) (*gin.Context, *relaycommon.RelayInfo, *PreparedTextRelayAttempt) {
	t.Helper()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request := &dto.GeneralOpenAIRequest{
		Model: "gpt-client",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		Request:         request,
		OriginModelName: "gpt-client",
		RelayMode:       relayconstant.RelayModeChatCompletions,
		RequestURLPath:  "/v1/chat/completions",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-upstream",
		},
	}
	info.InitRequestConversionChain()

	attempt, newAPIError := prepareOpenAITextAttemptWithAdaptor(context, info, adaptor)
	require.Nil(t, newAPIError)
	require.NotNil(t, attempt)
	return context, info, attempt
}

func TestPrepareOpenAITextAttemptDoesNotPerformIOAndExecuteUsesFrozenBodyOnce(t *testing.T) {
	adaptor := &preparedTextTestAdaptor{
		requestStarted: make(chan struct{}),
		releaseRequest: make(chan struct{}),
	}
	context, info, attempt := newPreparedOpenAITextTest(t, adaptor)
	defer attempt.Close()

	preparedBody, err := attempt.PreparedRequest().Body()
	require.NoError(t, err)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), attempt.PreparedRequest().Protocol())
	assert.Equal(t, "gpt-upstream", attempt.PreparedRequest().Model())

	adaptor.mutex.Lock()
	assert.Equal(t, 1, adaptor.initCount)
	assert.Equal(t, 1, adaptor.convertOpenAIRequestCount)
	assert.Zero(t, adaptor.doRequestCount)
	assert.Zero(t, adaptor.doResponseCount)
	adaptor.mutex.Unlock()

	type executionResult struct {
		usage       *dto.Usage
		newAPIError *types.NewAPIError
	}
	results := make(chan executionResult, 2)
	go func() {
		usage, newAPIError := ExecutePreparedTextRelayAttempt(context, info, attempt)
		results <- executionResult{usage: usage, newAPIError: newAPIError}
	}()
	<-adaptor.requestStarted
	go func() {
		usage, newAPIError := ExecutePreparedTextRelayAttempt(context, info, attempt)
		results <- executionResult{usage: usage, newAPIError: newAPIError}
	}()
	close(adaptor.releaseRequest)

	firstResult := <-results
	secondResult := <-results
	resultsBySuccess := []executionResult{firstResult, secondResult}
	successCount := 0
	errorCount := 0
	for _, result := range resultsBySuccess {
		if result.newAPIError == nil {
			successCount++
			require.NotNil(t, result.usage)
			assert.Equal(t, 5, result.usage.TotalTokens)
		} else {
			errorCount++
		}
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, errorCount)

	adaptor.mutex.Lock()
	assert.Equal(t, 1, adaptor.doRequestCount)
	assert.Equal(t, 1, adaptor.doResponseCount)
	assert.Equal(t, preparedBody, adaptor.sentBody)
	adaptor.mutex.Unlock()

	require.NoError(t, attempt.Close())
	require.NoError(t, attempt.Close())
	usage, newAPIError := ExecutePreparedTextRelayAttempt(context, info, attempt)
	assert.Nil(t, usage)
	require.NotNil(t, newAPIError)
	adaptor.mutex.Lock()
	assert.Equal(t, 1, adaptor.doRequestCount)
	adaptor.mutex.Unlock()
}

func TestChatViaResponsesAttemptKeepsEffectiveProtocolStateUntilClose(t *testing.T) {
	adaptor := &preparedTextTestAdaptor{
		responseBody: `{"id":"resp_1","object":"response","created_at":1710000000,"status":"completed","model":"gpt-upstream","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`,
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request := &dto.GeneralOpenAIRequest{
		Model: "gpt-upstream",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-client",
		RelayMode:       relayconstant.RelayModeChatCompletions,
		RequestURLPath:  "/v1/chat/completions?source=test",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-upstream",
		},
	}
	info.InitRequestConversionChain()

	attempt, newAPIError := prepareChatCompletionsViaResponsesAttempt(context, info, adaptor, request)
	require.Nil(t, newAPIError)
	require.NotNil(t, attempt)

	assert.Equal(t, relayconstant.RelayModeResponses, info.RelayMode)
	assert.Equal(t, "/v1/responses", info.RequestURLPath)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), attempt.PreparedRequest().Protocol())
	adaptor.mutex.Lock()
	assert.Equal(t, relayconstant.RelayModeResponses, adaptor.modeDuringResponsesConvert)
	assert.Equal(t, "/v1/responses", adaptor.pathDuringResponsesConvert)
	assert.Zero(t, adaptor.doRequestCount)
	assert.Zero(t, adaptor.doResponseCount)
	adaptor.mutex.Unlock()

	usage, newAPIError := ExecutePreparedTextRelayAttempt(context, info, attempt)
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Equal(t, relayconstant.RelayModeResponses, info.RelayMode)
	assert.Equal(t, "/v1/responses", info.RequestURLPath)
	adaptor.mutex.Lock()
	assert.Equal(t, 1, adaptor.doRequestCount)
	assert.Zero(t, adaptor.doResponseCount)
	assert.Equal(t, relayconstant.RelayModeResponses, adaptor.modeDuringRequest)
	assert.Equal(t, "/v1/responses", adaptor.pathDuringRequest)
	adaptor.mutex.Unlock()

	require.NoError(t, attempt.Close())
	require.NoError(t, attempt.Close())
	assert.Equal(t, relayconstant.RelayModeChatCompletions, info.RelayMode)
	assert.Equal(t, "/v1/chat/completions?source=test", info.RequestURLPath)
}

// responsesFallbackTestAdaptor reports the Responses API as unsupported so the
// relay layer exercises the Responses→Chat downgrade path.
type responsesFallbackTestAdaptor struct {
	preparedTextTestAdaptor
}

func (a *responsesFallbackTestAdaptor) ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error) {
	return nil, relaycommon.ErrResponsesNotSupported
}

func TestPrepareResponsesFallbackDowngradesToChatAndRestoresProtocol(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	adaptor := &responsesFallbackTestAdaptor{}
	info := &relaycommon.RelayInfo{
		Request:         &dto.OpenAIResponsesRequest{Model: "glm-4", Input: json.RawMessage(`"hi"`)},
		OriginModelName: "glm-4",
		RelayMode:       relayconstant.RelayModeResponses,
		RequestURLPath:  "/v1/responses",
		RelayFormat:     types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeMiniMax,
			ApiType:           5,
			UpstreamModelName: "glm-4",
		},
	}
	info.InitRequestConversionChain()

	attempt, newAPIError := prepareResponsesTextAttemptWithAdaptor(context, info, adaptor)
	require.Nil(t, newAPIError)
	require.NotNil(t, attempt)

	// The downgraded attempt must forward the upstream Chat response back to the
	// Codex client as Responses, and mark the request as downgraded.
	assert.Equal(t, preparedTextResponseModeResponsesViaChat, attempt.responseMode)
	assert.True(t, info.ResponsesChatFallback)

	adaptor.mutex.Lock()
	assert.Equal(t, 1, adaptor.convertOpenAIRequestCount)
	adaptor.mutex.Unlock()

	// Protocol state is restored after the attempt closes.
	require.NoError(t, attempt.Close())
	assert.Equal(t, relayconstant.RelayModeResponses, info.RelayMode)
	assert.Equal(t, "/v1/responses", info.RequestURLPath)
}
