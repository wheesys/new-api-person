package contextconsensus

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedProviderFileReferencePayloadIsEncryptedAndMasked(t *testing.T) {
	payload := ManagedProviderFileReferencePayload{
		ProviderFileID: "file-provider-secret-reference",
		Filename:       "private-customer-file.pdf",
	}
	require.NoError(t, payload.Validate())
	assert.NotContains(t, fmt.Sprintf("%s", payload), payload.ProviderFileID)
	assert.NotContains(t, fmt.Sprintf("%#v", payload), payload.Filename)

	managedCipher, err := NewManagedConsensusCipher([]byte(strings.Repeat("e", 32)), "encryption-v1")
	require.NoError(t, err)
	encryptionContext := ManagedEncryptionContext{
		RepositoryKey: "new-api:context_consensus:provider_file:v1:owner:handle",
		Purpose:       ManagedEncryptionPurposeProviderFileReference,
		Revision:      1,
	}
	envelope, err := managedCipher.EncryptJSON(context.Background(), encryptionContext, payload)
	require.NoError(t, err)
	encodedEnvelope, err := common.Marshal(envelope)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedEnvelope), payload.ProviderFileID)
	assert.NotContains(t, string(encodedEnvelope), payload.Filename)

	var decrypted ManagedProviderFileReferencePayload
	require.NoError(t, managedCipher.DecryptJSON(context.Background(), encryptionContext, envelope, &decrypted))
	assert.Equal(t, payload, decrypted)

	wrongPurpose := encryptionContext
	wrongPurpose.Purpose = ManagedEncryptionPurposeProviderState
	require.Error(t, managedCipher.DecryptJSON(context.Background(), wrongPurpose, envelope, &decrypted))
}

func TestGenerateManagedProviderFileHandleUsesOpaqueFixedLengthEntropy(t *testing.T) {
	handle, err := generateManagedProviderFileHandle(bytes.NewReader(bytes.Repeat([]byte{0x2a}, 32)))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(handle, managedProviderFileHandlePrefix))
	assert.Len(t, handle, len(managedProviderFileHandlePrefix)+43)
	assert.NotContains(t, handle, "provider-secret")

	_, err = generateManagedProviderFileHandle(bytes.NewReader([]byte("short")))
	require.Error(t, err)
	_, err = generateManagedProviderFileHandle(nil)
	require.Error(t, err)
}

func TestManagedProviderFileReferencePayloadRejectsAmbiguousValues(t *testing.T) {
	tests := []ManagedProviderFileReferencePayload{
		{},
		{ProviderFileID: "provider-file", Filename: "file.pdf"},
		{ProviderFileID: " file-secret", Filename: "file.pdf"},
		{ProviderFileID: "file-secret?query", Filename: "file.pdf"},
		{ProviderFileID: "file-secret", Filename: " "},
		{ProviderFileID: "file-secret", Filename: " file.pdf"},
		{ProviderFileID: "file-secret", Filename: "file\x00.pdf"},
	}
	for _, payload := range tests {
		require.Error(t, payload.Validate())
	}
}
