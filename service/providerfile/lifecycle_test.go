package providerfile

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	openai "github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type providerFileRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip providerFileRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func prepareProviderFileLifecycleTest(t *testing.T) *contextconsensus.ManagedConsensusRuntime {
	t.Helper()
	originalDatabase := model.DB
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = database
	t.Cleanup(func() { model.DB = originalDatabase })
	require.NoError(t, database.AutoMigrate(
		&model.ManagedProviderFileLifecycle{}, &model.ManagedProviderFileDeletionOutbox{}, &model.ManagedProviderFileLifecycleEvent{},
	))
	t.Setenv("CONTEXT_CONSENSUS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("CONTEXT_CONSENSUS_ENCRYPTION_KEY_VERSION", "provider-file-v1")
	t.Setenv("CONTEXT_CONSENSUS_PREVIOUS_ENCRYPTION_KEYS", "")
	runtime, err := contextconsensus.NewManagedConsensusCryptoRuntimeFromEnvironment()
	require.NoError(t, err)
	return runtime
}

func TestUploadActivatesVerifiedLifecycleAndReplaysOpaqueHandle(t *testing.T) {
	runtime := prepareProviderFileLifecycleTest(t)
	storage, contentType := managedProviderFileMultipart(t, "user_data", "facts.txt", []byte("verified upload content"), "")
	body, err := ParseUploadBody(storage, contentType)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	providerFileID := "file-provider-secret"
	responseBody, err := common.Marshal(map[string]any{
		"id": providerFileID, "object": "file", "bytes": body.SizeBytes, "created_at": now.Add(-time.Minute).Unix(),
		"expires_at": now.Add(time.Hour).Unix(), "filename": body.Filename, "purpose": "user_data",
	})
	require.NoError(t, err)
	requestMethods := make([]string, 0, 2)
	var requestMutex sync.Mutex
	httpClient := &http.Client{Transport: providerFileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestMutex.Lock()
		requestMethods = append(requestMethods, request.Method)
		requestMutex.Unlock()
		if request.Method == http.MethodPost {
			_, readErr := io.Copy(io.Discard, request.Body)
			require.NoError(t, readErr)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(responseBody))), Request: request,
		}, nil
	})}
	client, err := openai.NewProviderFileClient(httpClient, openai.OpenAIProviderFileOrigin, "sk-provider-secret", "org-exclusive")
	require.NoError(t, err)
	target := &Target{
		ChannelID: 41, ChannelType: 1, Endpoint: openai.OpenAIProviderFileOrigin, Organization: "org-exclusive",
		credential: "sk-provider-secret", client: client,
	}
	settings := &model_setting.SmartRoutingSettings{
		ProviderFileLifecycleEnabled: true, ProviderFileOpenAIChannelID: 41, ProviderFileExpirationSeconds: 3600,
		ProviderFileMetadataVerifyTTLSeconds: 0, ProviderFileDeletionLeadSeconds: 60, ProviderFileDeletionBatchSize: 10,
		ProviderFileDeletionMaxAttempts: 5, ProviderFileDeletionTimeoutSeconds: 30, ProviderFileExclusiveProjectAttested: true,
	}
	request := UploadRequest{
		Owner:          contextconsensus.ManagedConsensusOwner{UserID: 7, TokenID: 9, EndpointFamily: managedProviderFileEndpointFamily},
		IdempotencyKey: "upload-idempotency-key", Body: body, Settings: settings, Runtime: runtime, Target: target,
	}
	created, err := Upload(context.Background(), request)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(created.ID, "file-managed-"))
	assert.NotEqual(t, providerFileID, created.ID)
	assert.NotContains(t, string(mustMarshalProviderFile(t, created)), providerFileID)
	replayed, err := Upload(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, created, replayed)
	assert.Equal(t, []string{http.MethodPost, http.MethodGet}, requestMethods)
	responsesBody := []byte(`{"model":"gpt-4o","input":[{"type":"input_file","file_id":"` + created.ID + `"},{"type":"message","role":"user","content":[{"type":"input_file","file_id":"` + created.ID + `"}]}]}`)
	resolution, rewrittenBody, err := PrepareResolution(context.Background(), responsesBody, request.Owner, runtime, target)
	require.NoError(t, err)
	require.NotNil(t, resolution)
	assert.NotContains(t, string(rewrittenBody), created.ID)
	assert.Contains(t, string(rewrittenBody), providerFileID)
	require.NoError(t, resolution.ValidateFinalBody(rewrittenBody))
	assert.Equal(t, []string{http.MethodPost, http.MethodGet, http.MethodGet}, requestMethods)
	tamperedBody := []byte(strings.Replace(string(rewrittenBody), providerFileID, "file-other", 1))
	assert.ErrorIs(t, resolution.ValidateFinalBody(tamperedBody), ErrInvalidReference)

	var lifecycle model.ManagedProviderFileLifecycle
	require.NoError(t, model.DB.First(&lifecycle).Error)
	assert.Equal(t, model.ManagedProviderFileLifecycleStateActive, lifecycle.State)
	assert.NotEmpty(t, lifecycle.ProviderPayload)
	var outbox model.ManagedProviderFileDeletionOutbox
	require.NoError(t, model.DB.First(&outbox, "lifecycle_id = ?", lifecycle.Id).Error)
	require.NotNil(t, lifecycle.ExpiresAt)
	assert.Equal(t, lifecycle.ExpiresAt.Add(-time.Minute), outbox.NextAttemptAt)
}

func mustMarshalProviderFile(t *testing.T, value File) []byte {
	t.Helper()
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	return encoded
}
