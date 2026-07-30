package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDistributeRejectsProviderBoundStateForVirtualModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())

	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		c.Next()
	})
	router.POST("/v1/responses", Distribute(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	body := strings.NewReader(`{"model":"auto:quality","previous_response_id":"resp-sensitive-reference","input":"continue"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "responses_previous_response_id")
	assert.NotContains(t, recorder.Body.String(), "resp-sensitive-reference")
}

func TestDistributeRejectsInvalidToolGraphBeforeRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())

	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		c.Next()
	})
	router.POST("/v1/chat/completions", Distribute(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	body := strings.NewReader(`{
  "model":"auto:quality",
  "messages":[
    {"role":"user","content":"lookup"},
    {"role":"assistant","tool_calls":[{"id":"call-sensitive","type":"function","function":{"name":"lookup","arguments":"{\"secret\":\"do-not-log\"}"}}]}
  ]
}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "missing_tool_result")
	assert.NotContains(t, recorder.Body.String(), "call-sensitive")
	assert.NotContains(t, recorder.Body.String(), "do-not-log")
}

func TestBuildSmartRouteRequestAuditsProviderBoundExplicitModelWithoutRetryCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.NewReader(`{
  "model":"gpt-5",
  "messages":[{"role":"user","content":[{"type":"file","file":{"file_id":"file-sensitive-reference"}}]}]
}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request

	routeRequest, err := buildSmartRouteRequest(context, &ModelRequest{Model: "gpt-5"}, "default")
	require.NoError(t, err)
	contextLog := smartRoutingContextConsensusLog(routeRequest)
	assert.True(t, contextLog.WouldBlock)
	assert.False(t, contextLog.SwitchAllowed)
	assert.Equal(t, "credential", contextLog.BindingLevel)
	assert.Contains(t, contextLog.BindingReasonCodes, "provider_file_reference")

	setSmartRoutingRetryCandidates(context, []smartrouting.SmartRouteCandidate{{
		ModelName: "gpt-5",
		ChannelID: 4101,
	}}, "gpt-5", smartRoutingContextSwitchAllowed(routeRequest))
	_, retryCandidatesExist := common.GetContextKey(context, constant.ContextKeySmartRoutingRetryCandidates)
	assert.False(t, retryCandidatesExist)

	storage, err := common.GetBodyStorage(context)
	require.NoError(t, err)
	bodyBytes, err := storage.Bytes()
	require.NoError(t, err)
	assert.Contains(t, string(bodyBytes), "file-sensitive-reference")
}

func TestBuildSmartRouteRequestSuppressesDebugLogsForToolExchange(t *testing.T) {
	body := strings.NewReader(`{
  "model":"gpt-5",
  "messages":[
    {"role":"user","content":"lookup"},
    {"role":"assistant","tool_calls":[{"id":"call-sensitive","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"do-not-log\"}"}}]},
    {"role":"tool","tool_call_id":"call-sensitive","content":"{\"status\":\"ok\"}"},
    {"role":"assistant","content":"done"},
    {"role":"user","content":"continue"}
  ],
  "tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]
}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	_, err := buildSmartRouteRequest(context, &ModelRequest{Model: "gpt-5"}, "default")

	require.NoError(t, err)
	assert.True(t, common.GetContextKeyBool(context, constant.ContextKeySuppressDebugLog))
}

func TestBuildSmartRouteRequestSuppressesDebugLogsForInvalidToolResult(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5","messages":[{"role":"tool","tool_call_id":"orphan-sensitive","content":"do-not-log"}]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	_, err := buildSmartRouteRequest(context, &ModelRequest{Model: "gpt-5"}, "default")

	require.Error(t, err)
	assert.True(t, common.GetContextKeyBool(context, constant.ContextKeySuppressDebugLog))
}

func TestFreezeSmartRoutingCandidatesOnlyWhenSystemPreparationEnabled(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	candidates := []smartrouting.SmartRouteCandidate{{ModelName: "gpt-4o", ChannelID: 17}}

	freezeSmartRoutingCandidates(context, candidates)
	_, exists := common.GetContextKey(context, constant.ContextKeySmartRoutingFrozenCandidates)
	assert.False(t, exists)

	common.SetContextKey(context, constant.ContextKeyContextConsensusPolicy, contextconsensus.CompactionPolicySnapshot{SystemEnabled: true})
	freezeSmartRoutingCandidates(context, candidates)
	frozen, exists := common.GetContextKeyType[[]smartrouting.SmartRouteCandidate](context, constant.ContextKeySmartRoutingFrozenCandidates)
	require.True(t, exists)
	require.Len(t, frozen, 1)

	candidates[0].ChannelID = 99
	assert.Equal(t, 17, frozen[0].ChannelID)
}
