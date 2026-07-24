package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

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
	adaptor.mutex.Unlock()

	if requestStarted != nil {
		close(requestStarted)
	}
	if releaseRequest != nil {
		<-releaseRequest
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}, nil
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
