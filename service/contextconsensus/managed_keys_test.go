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
