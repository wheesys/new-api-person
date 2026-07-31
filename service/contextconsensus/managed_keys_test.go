package contextconsensus

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedConsensusKeyDeriverIsStableAndOwnerIsolated(t *testing.T) {
	deriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("h", 32)), "hmac-v1")
	require.NoError(t, err)
	owner := ManagedConsensusOwner{UserID: 11, TokenID: 29, EndpointFamily: "Responses"}

	first, err := deriver.DeriveConversationStorageKey(owner, "customer-context-secret")
	require.NoError(t, err)
	second, err := deriver.DeriveConversationStorageKey(owner, "customer-context-secret")
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.True(t, strings.HasPrefix(first.RepositoryKey, managedConsensusRepositoryKeyPrefix+":"))
	assert.NotContains(t, first.RepositoryKey, "customer-context-secret")
	assert.NotContains(t, first.RepositoryKey, "11")
	assert.NotEmpty(t, first.OwnerHMAC)
	assert.NotEmpty(t, first.ConversationHMAC)

	otherOwnerKey, err := deriver.DeriveConversationStorageKey(
		ManagedConsensusOwner{UserID: 12, TokenID: 29, EndpointFamily: "responses"},
		"customer-context-secret",
	)
	require.NoError(t, err)
	assert.NotEqual(t, first.RepositoryKey, otherOwnerKey.RepositoryKey)

	otherContextKey, err := deriver.DeriveConversationStorageKey(owner, "another-context-secret")
	require.NoError(t, err)
	assert.NotEqual(t, first.RepositoryKey, otherContextKey.RepositoryKey)
}

func TestManagedConsensusKeyDeriverSeparatesProviderStateAndCredentials(t *testing.T) {
	deriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("k", 32)), "hmac-v2")
	require.NoError(t, err)
	owner := ManagedConsensusOwner{UserID: 7, TokenID: 19, EndpointFamily: "chat"}

	stateKey, err := deriver.DeriveProviderStateStorageKey(owner, "provider-response-secret-id")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(stateKey.RepositoryKey, managedProviderStateRepositoryPrefix+":"))
	assert.NotContains(t, stateKey.RepositoryKey, "provider-response-secret-id")
	assert.NotEmpty(t, stateKey.StateReferenceHMAC)

	fingerprint, err := deriver.DeriveCredentialFingerprint(41, 2, "provider-api-secret")
	require.NoError(t, err)
	assert.NotContains(t, fingerprint, "provider-api-secret")
	otherFingerprint, err := deriver.DeriveCredentialFingerprint(41, 3, "provider-api-secret")
	require.NoError(t, err)
	assert.NotEqual(t, fingerprint, otherFingerprint)
}

func TestManagedConsensusKeyDeriverRejectsIncompleteIdentity(t *testing.T) {
	_, err := NewManagedConsensusKeyDeriver([]byte("short"), "v1")
	require.ErrorContains(t, err, "at least 32 bytes")

	deriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("h", 32)), "v1")
	require.NoError(t, err)
	_, err = deriver.DeriveConversationStorageKey(ManagedConsensusOwner{TokenID: 1, EndpointFamily: "chat"}, "context")
	require.ErrorContains(t, err, "user ID")
	_, err = deriver.DeriveConversationStorageKey(ManagedConsensusOwner{UserID: 1, TokenID: 1, EndpointFamily: "chat"}, " ")
	require.ErrorContains(t, err, "context ID")
	_, err = deriver.DeriveProviderStateStorageKey(ManagedConsensusOwner{UserID: 1, TokenID: 1, EndpointFamily: "chat"}, "")
	require.ErrorContains(t, err, "state reference")
}

func TestManagedConsensusKeyDeriverSeparatesProviderFileDomains(t *testing.T) {
	deriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("p", 32)), "hmac-v3")
	require.NoError(t, err)
	owner := ManagedConsensusOwner{UserID: 17, TokenID: 23, EndpointFamily: "openai_provider_file"}

	storageKey, err := deriver.DeriveProviderFileStorageKey(owner, "file-managed-opaque-handle", "upload-idempotency-secret")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(storageKey.RepositoryKey, managedProviderFileRepositoryPrefix+":"))
	assert.NotContains(t, storageKey.RepositoryKey, "opaque-handle")
	assert.NotContains(t, storageKey.RepositoryKey, "idempotency-secret")
	assert.NotEqual(t, storageKey.HandleHMAC, storageKey.UploadIntentHMAC)
	assert.Len(t, storageKey.OwnerHMAC, 64)
	assert.Len(t, storageKey.HandleHMAC, 64)

	referenceHMAC, err := deriver.DeriveProviderFileReferenceHMAC(owner, "file-provider-secret")
	require.NoError(t, err)
	deletionHMAC, err := deriver.DeriveProviderFileDeletionOperationHMAC(owner, "file-managed-opaque-handle")
	require.NoError(t, err)
	assert.NotEqual(t, storageKey.HandleHMAC, referenceHMAC)
	assert.NotEqual(t, storageKey.HandleHMAC, deletionHMAC)
	previousDeriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("p", 32)), "hmac-v2")
	require.NoError(t, err)
	previousStorageKey, err := previousDeriver.DeriveProviderFileStorageKey(owner, "file-managed-opaque-handle", "upload-idempotency-secret")
	require.NoError(t, err)
	assert.NotEqual(t, storageKey.HandleHMAC, previousStorageKey.HandleHMAC)
	assert.NotEqual(t, storageKey.UploadIntentHMAC, previousStorageKey.UploadIntentHMAC)

	targetFingerprint, err := deriver.DeriveProviderFileTargetFingerprint(ManagedProviderFileTargetIdentity{
		ChannelID: 41, ChannelType: 1, Endpoint: "https://api.openai.com", Project: "project-a",
	})
	require.NoError(t, err)
	otherTargetFingerprint, err := deriver.DeriveProviderFileTargetFingerprint(ManagedProviderFileTargetIdentity{
		ChannelID: 41, ChannelType: 1, Endpoint: "https://api.openai.com", Project: "project-b",
	})
	require.NoError(t, err)
	assert.NotEqual(t, targetFingerprint, otherTargetFingerprint)
	credentialFingerprint, err := deriver.DeriveProviderFileCredentialFingerprint(41, 0, "provider-credential-secret")
	require.NoError(t, err)
	assert.Len(t, credentialFingerprint, 64)
	assert.NotContains(t, credentialFingerprint, "credential-secret")

	eventHMAC, err := deriver.DeriveProviderFileEventHMAC(ManagedProviderFileEventIdentity{
		LifecycleID: 3, Sequence: 1, EventType: "intent_created", ToState: "intent",
		EvidenceDigest: strings.Repeat("a", 64), CreatedAtUnix: 100,
	})
	require.NoError(t, err)
	assert.Len(t, eventHMAC, 64)
	_, err = deriver.DeriveProviderFileEventHMAC(ManagedProviderFileEventIdentity{
		LifecycleID: 3, Sequence: 2, EventType: "activated", FromState: "intent", ToState: "active",
		EvidenceDigest: strings.Repeat("a", 64), CreatedAtUnix: 101,
	})
	require.ErrorContains(t, err, "event identity")
}
