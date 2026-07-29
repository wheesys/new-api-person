package middleware

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareManagedConsensusRequestLoadsInjectsReplacesAndReleasesBeforeRouting(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	originalClient, originalEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = originalClient, originalEnabled
		_ = client.Close()
	})
	t.Setenv("CONTEXT_CONSENSUS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32))))
	t.Setenv("CONTEXT_CONSENSUS_ENCRYPTION_KEY_VERSION", "v1")

	runtime, err := contextconsensus.NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.NoError(t, err)
	owner := contextconsensus.ManagedConsensusOwner{UserID: 7, TokenID: 11, EndpointFamily: "chat"}
	seed, err := contextconsensus.BeginManagedConsensusSession(context.Background(), runtime, contextconsensus.BeginManagedConsensusSessionRequest{
		Owner: owner, ExternalContextID: "opaque", ExpectedRevision: 0, HolderID: "seed", LeaseTTL: time.Minute,
	})
	require.NoError(t, err)
	_, err = seed.Commit(context.Background(), managedMiddlewareState(), time.Hour)
	require.NoError(t, err)

	body := `{"model":"gpt","messages":[{"role":"system","content":"safe"},{"role":"user","content":"current"}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	contextValue, _ := gin.CreateTestContext(httptest.NewRecorder())
	contextValue.Request = request
	contextValue.Set(common.RequestIdKey, "request")
	common.SetContextKey(contextValue, constant.ContextKeyUserId, 7)
	common.SetContextKey(contextValue, constant.ContextKeyTokenId, 11)
	common.SetContextKey(contextValue, constant.ContextKeyManagedContextRequest, contextconsensus.ManagedContextRequest{
		ExternalContextID: "opaque", ExpectedRevision: 1,
	})

	lifecycle, status, err := prepareManagedConsensusRequest(contextValue)
	require.NoError(t, err)
	assert.Zero(t, status)
	require.NotNil(t, lifecycle)
	storage, err := common.GetBodyStorage(contextValue)
	require.NoError(t, err)
	rewritten, err := storage.Bytes()
	require.NoError(t, err)
	assert.Contains(t, gjson.GetBytes(rewritten, "messages.1.content").String(), "Untrusted historical context summary")
	assert.Equal(t, "current", gjson.GetBytes(rewritten, "messages.2.content").String())
	assert.True(t, gjson.GetBytes(rewritten, "tools").Exists())
	_, sessionStored := common.GetContextKeyType[*contextconsensus.ManagedConsensusSession](contextValue, constant.ContextKeyManagedContextSession)
	assert.True(t, sessionStored)
	managedRequest, found := common.GetContextKeyType[contextconsensus.ManagedContextRequest](contextValue, constant.ContextKeyManagedContextRequest)
	require.True(t, found)
	assert.Equal(t, types.RelayFormatOpenAI, managedRequest.Protocol)
	assert.NotEmpty(t, managedRequest.IncrementalSourceDigest)
	assert.NotContains(t, managedRequest.IncrementalSourceDigest, "current")
	require.NoError(t, lifecycle.Close(contextValue.Request.Context()))

	second, err := contextconsensus.BeginManagedConsensusSession(context.Background(), runtime, contextconsensus.BeginManagedConsensusSessionRequest{
		Owner: owner, ExternalContextID: "opaque", ExpectedRevision: 1, HolderID: "next", LeaseTTL: time.Minute,
	})
	require.NoError(t, err, "request completion must release the lease")
	require.NoError(t, second.Close(context.Background()))
}

func TestPrepareManagedConsensusRequestMapsRevisionAndRuntimeFailures(t *testing.T) {
	contextValue, _ := gin.CreateTestContext(httptest.NewRecorder())
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"current"}]}`))
	contextValue.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(contextValue, constant.ContextKeyUserId, 1)
	common.SetContextKey(contextValue, constant.ContextKeyTokenId, 2)
	common.SetContextKey(contextValue, constant.ContextKeyManagedContextRequest, contextconsensus.ManagedContextRequest{ExternalContextID: "opaque", ExpectedRevision: 1})

	originalClient, originalEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = nil, false
	t.Cleanup(func() { common.RDB, common.RedisEnabled = originalClient, originalEnabled })
	_, status, err := prepareManagedConsensusRequest(contextValue)
	require.ErrorContains(t, err, "Redis is unavailable")
	assert.Equal(t, http.StatusServiceUnavailable, status)

	assert.Equal(t, http.StatusConflict, managedConsensusBeginStatus(contextconsensus.ErrManagedConsensusRevisionConflict))
	assert.Equal(t, http.StatusConflict, managedConsensusBeginStatus(contextconsensus.ErrManagedConsensusLeaseHeld))
	assert.Equal(t, "managed context state is unavailable", managedConsensusBeginError(assert.AnError).Error())
}

func TestPrepareManagedConsensusRequestRejectsGeminiStreamingBeforeRedis(t *testing.T) {
	contextValue, _ := gin.CreateTestContext(httptest.NewRecorder())
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:streamGenerateContent?alt=sse", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"current"}]}]}`))
	contextValue.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(contextValue, constant.ContextKeyManagedContextRequest, contextconsensus.ManagedContextRequest{ExternalContextID: "opaque"})

	originalClient, originalEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = nil, false
	t.Cleanup(func() { common.RDB, common.RedisEnabled = originalClient, originalEnabled })
	_, status, err := prepareManagedConsensusRequest(contextValue)
	require.ErrorContains(t, err, "does not support streaming")
	assert.Equal(t, http.StatusBadRequest, status)
}

func managedMiddlewareState() contextconsensus.ManagedConsensusState {
	now := time.Unix(1_800_000_000, 0).Unix()
	summary := contextconsensus.ConsensusSummary{
		Version:             contextconsensus.ConsensusSummaryVersion,
		TaskGoal:            []contextconsensus.ConsensusFact{},
		Decisions:           []contextconsensus.ConsensusFact{},
		MustPreserve:        []contextconsensus.ConsensusFact{},
		OpenQuestions:       []contextconsensus.ConsensusFact{},
		UserPreferences:     []contextconsensus.ConsensusFact{},
		DomainTerms:         map[string]contextconsensus.ConsensusFact{},
		CompletedSteps:      []contextconsensus.ConsensusFact{},
		PendingSteps:        []contextconsensus.ConsensusFact{},
		ArtifactRefs:        []contextconsensus.ConsensusFact{},
		ToolResultSummaries: []contextconsensus.ConsensusFact{},
		SourceRanges:        []contextconsensus.SummarySourceRange{},
		SourceDigest:        "source",
	}
	return contextconsensus.ManagedConsensusState{
		Version: contextconsensus.ManagedConsensusStateVersion, Revision: 1, Mode: "managed_consensus",
		TaskConsensus: summary, SourceDigest: "source", PolicyVersion: "context-consensus-v1",
		CreatedAtUnix: now, UpdatedAtUnix: now,
	}
}
