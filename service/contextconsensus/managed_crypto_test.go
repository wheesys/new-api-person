package contextconsensus

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type managedCipherTestPayload struct {
	TaskGoal string `json:"task_goal"`
	Secret   string `json:"secret"`
}

func TestManagedConsensusCipherRoundTripBindsRepositoryAndRevision(t *testing.T) {
	managedCipher, err := NewManagedConsensusCipher([]byte(strings.Repeat("e", 32)), "encryption-v1")
	require.NoError(t, err)
	encryptionContext := ManagedEncryptionContext{
		RepositoryKey: "new-api:context_consensus:v1:owner:conversation",
		Purpose:       ManagedEncryptionPurposeConsensusState,
		Revision:      3,
	}
	payload := managedCipherTestPayload{TaskGoal: "finish the migration", Secret: "must-not-appear"}

	envelope, err := managedCipher.EncryptJSON(context.Background(), encryptionContext, payload)
	require.NoError(t, err)
	assert.Equal(t, managedEncryptedEnvelopeVersion, envelope.Version)
	assert.Equal(t, "AES-256-GCM", envelope.Algorithm)
	assert.Equal(t, encryptionContext.Purpose, envelope.Purpose)
	assert.Equal(t, encryptionContext.Revision, envelope.Revision)
	assert.NotEmpty(t, envelope.Nonce)
	assert.NotEmpty(t, envelope.Ciphertext)

	encodedEnvelope, err := common.Marshal(envelope)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedEnvelope), payload.TaskGoal)
	assert.NotContains(t, string(encodedEnvelope), payload.Secret)

	var decrypted managedCipherTestPayload
	require.NoError(t, managedCipher.DecryptJSON(context.Background(), encryptionContext, envelope, &decrypted))
	assert.Equal(t, payload, decrypted)

	wrongRepositoryContext := encryptionContext
	wrongRepositoryContext.RepositoryKey += ":other"
	require.ErrorContains(t, managedCipher.DecryptJSON(context.Background(), wrongRepositoryContext, envelope, &decrypted), "authenticate")

	wrongRevisionContext := encryptionContext
	wrongRevisionContext.Revision++
	require.ErrorContains(t, managedCipher.DecryptJSON(context.Background(), wrongRevisionContext, envelope, &decrypted), "revision does not match")
}

func TestManagedConsensusCipherRejectsTamperingAndWrongKeyVersion(t *testing.T) {
	key := []byte(strings.Repeat("q", 32))
	managedCipher, err := NewManagedConsensusCipher(key, "encryption-v1")
	require.NoError(t, err)
	encryptionContext := ManagedEncryptionContext{
		RepositoryKey: "provider-state-key",
		Purpose:       ManagedEncryptionPurposeProviderState,
		Revision:      1,
	}
	envelope, err := managedCipher.EncryptJSON(context.Background(), encryptionContext, managedCipherTestPayload{Secret: "opaque-state"})
	require.NoError(t, err)

	tampered := envelope
	require.NotEmpty(t, tampered.Ciphertext)
	if tampered.Ciphertext[0] == 'A' {
		tampered.Ciphertext = "B" + tampered.Ciphertext[1:]
	} else {
		tampered.Ciphertext = "A" + tampered.Ciphertext[1:]
	}
	var destination managedCipherTestPayload
	require.Error(t, managedCipher.DecryptJSON(context.Background(), encryptionContext, tampered, &destination))

	otherVersionCipher, err := NewManagedConsensusCipher(key, "encryption-v2")
	require.NoError(t, err)
	require.ErrorContains(t, otherVersionCipher.DecryptJSON(context.Background(), encryptionContext, envelope, &destination), "key version")
}

func TestManagedConsensusCipherFailsClosedWithoutStableConfiguration(t *testing.T) {
	_, err := NewManagedConsensusCipher([]byte("short"), "v1")
	require.Error(t, err)
	_, err = NewManagedConsensusCipher([]byte(strings.Repeat("a", 32)), " ")
	require.ErrorContains(t, err, "key version")

	managedCipher, err := NewManagedConsensusCipher([]byte(strings.Repeat("a", 32)), "v1")
	require.NoError(t, err)
	_, err = managedCipher.EncryptJSON(context.Background(), ManagedEncryptionContext{
		RepositoryKey: "key",
		Purpose:       ManagedEncryptionPurposeConsensusState,
	}, managedCipherTestPayload{})
	require.ErrorContains(t, err, "revision")

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = managedCipher.EncryptJSON(canceledContext, ManagedEncryptionContext{
		RepositoryKey: "key",
		Purpose:       ManagedEncryptionPurposeConsensusState,
		Revision:      1,
	}, managedCipherTestPayload{})
	require.ErrorIs(t, err, context.Canceled)
}
