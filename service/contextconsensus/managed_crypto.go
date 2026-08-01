package contextconsensus

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const managedEncryptedEnvelopeVersion = 1

type ManagedEncryptionPurpose string

const (
	ManagedEncryptionPurposeConsensusState             ManagedEncryptionPurpose = "consensus_state"
	ManagedEncryptionPurposeProviderState              ManagedEncryptionPurpose = "provider_state_binding"
	ManagedEncryptionPurposeOutcomeResponse            ManagedEncryptionPurpose = "managed_outcome_response"
	ManagedEncryptionPurposeOutcomeAssistant           ManagedEncryptionPurpose = "managed_outcome_assistant"
	ManagedEncryptionPurposeOutcomeSummaryExecution    ManagedEncryptionPurpose = "managed_outcome_summary_execution"
	ManagedEncryptionPurposeOutcomeSummaryResult       ManagedEncryptionPurpose = "managed_outcome_summary_result"
	ManagedEncryptionPurposeOutcomeNextState           ManagedEncryptionPurpose = "managed_outcome_next_state"
	ManagedEncryptionPurposeProviderFileReference      ManagedEncryptionPurpose = "provider_file_reference"
	ManagedEncryptionPurposeProviderFileReconciliation ManagedEncryptionPurpose = "provider_file_reconciliation"
)

type ManagedEncryptionContext struct {
	RepositoryKey string
	Purpose       ManagedEncryptionPurpose
	Revision      uint64
}

type ManagedEncryptedEnvelope struct {
	Version    int                      `json:"version"`
	Algorithm  string                   `json:"algorithm"`
	KeyVersion string                   `json:"key_version"`
	Purpose    ManagedEncryptionPurpose `json:"purpose"`
	Revision   uint64                   `json:"revision"`
	Nonce      string                   `json:"nonce"`
	Ciphertext string                   `json:"ciphertext"`
}

type ManagedConsensusCipher struct {
	keyVersion string
	aead       cipher.AEAD
	random     io.Reader
}

func NewManagedConsensusCipher(key []byte, keyVersion string) (*ManagedConsensusCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("managed consensus AES-256-GCM key must contain exactly 32 bytes")
	}
	if strings.TrimSpace(keyVersion) == "" {
		return nil, fmt.Errorf("managed consensus encryption key version is required")
	}
	blockCipher, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create managed consensus AES cipher: %w", err)
	}
	authenticatedCipher, err := cipher.NewGCM(blockCipher)
	if err != nil {
		return nil, fmt.Errorf("create managed consensus AES-GCM cipher: %w", err)
	}
	return &ManagedConsensusCipher{
		keyVersion: strings.TrimSpace(keyVersion),
		aead:       authenticatedCipher,
		random:     rand.Reader,
	}, nil
}

func (managedCipher *ManagedConsensusCipher) EncryptJSON(ctx context.Context, encryptionContext ManagedEncryptionContext, value any) (ManagedEncryptedEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return ManagedEncryptedEnvelope{}, err
	}
	if managedCipher == nil || managedCipher.aead == nil || managedCipher.random == nil {
		return ManagedEncryptedEnvelope{}, fmt.Errorf("managed consensus cipher is required")
	}
	if err := validateManagedEncryptionContext(encryptionContext); err != nil {
		return ManagedEncryptedEnvelope{}, err
	}
	if value == nil {
		return ManagedEncryptedEnvelope{}, fmt.Errorf("managed consensus plaintext value is required")
	}

	plaintext, err := common.Marshal(value)
	if err != nil {
		return ManagedEncryptedEnvelope{}, fmt.Errorf("encode managed consensus plaintext: %w", err)
	}
	envelope := ManagedEncryptedEnvelope{
		Version:    managedEncryptedEnvelopeVersion,
		Algorithm:  "AES-256-GCM",
		KeyVersion: managedCipher.keyVersion,
		Purpose:    encryptionContext.Purpose,
		Revision:   encryptionContext.Revision,
	}
	additionalAuthenticatedData, err := managedEncryptionAdditionalData(encryptionContext, envelope)
	if err != nil {
		return ManagedEncryptedEnvelope{}, err
	}
	nonce := make([]byte, managedCipher.aead.NonceSize())
	if _, err := io.ReadFull(managedCipher.random, nonce); err != nil {
		return ManagedEncryptedEnvelope{}, fmt.Errorf("generate managed consensus AES-GCM nonce: %w", err)
	}
	ciphertext := managedCipher.aead.Seal(nil, nonce, plaintext, additionalAuthenticatedData)
	envelope.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	envelope.Ciphertext = base64.RawURLEncoding.EncodeToString(ciphertext)
	return envelope, nil
}

func (managedCipher *ManagedConsensusCipher) DecryptJSON(ctx context.Context, encryptionContext ManagedEncryptionContext, envelope ManagedEncryptedEnvelope, destination any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if managedCipher == nil || managedCipher.aead == nil {
		return fmt.Errorf("managed consensus cipher is required")
	}
	if err := validateManagedEncryptionContext(encryptionContext); err != nil {
		return err
	}
	if destination == nil {
		return fmt.Errorf("managed consensus plaintext destination is required")
	}
	if envelope.Version != managedEncryptedEnvelopeVersion {
		return fmt.Errorf("unsupported managed consensus encrypted envelope version %d", envelope.Version)
	}
	if envelope.Algorithm != "AES-256-GCM" {
		return fmt.Errorf("unsupported managed consensus encryption algorithm %q", envelope.Algorithm)
	}
	if envelope.KeyVersion != managedCipher.keyVersion {
		return fmt.Errorf("managed consensus encryption key version does not match")
	}
	if envelope.Purpose != encryptionContext.Purpose {
		return fmt.Errorf("managed consensus encryption purpose does not match")
	}
	if envelope.Revision != encryptionContext.Revision {
		return fmt.Errorf("managed consensus encryption revision does not match")
	}

	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return fmt.Errorf("decode managed consensus AES-GCM nonce: %w", err)
	}
	if len(nonce) != managedCipher.aead.NonceSize() {
		return fmt.Errorf("managed consensus AES-GCM nonce has invalid length")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return fmt.Errorf("decode managed consensus ciphertext: %w", err)
	}
	additionalAuthenticatedData, err := managedEncryptionAdditionalData(encryptionContext, envelope)
	if err != nil {
		return err
	}
	plaintext, err := managedCipher.aead.Open(nil, nonce, ciphertext, additionalAuthenticatedData)
	if err != nil {
		return fmt.Errorf("authenticate managed consensus ciphertext: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := common.Unmarshal(plaintext, destination); err != nil {
		return fmt.Errorf("decode managed consensus plaintext: %w", err)
	}
	return nil
}

func validateManagedEncryptionContext(encryptionContext ManagedEncryptionContext) error {
	if strings.TrimSpace(encryptionContext.RepositoryKey) == "" {
		return fmt.Errorf("managed consensus encryption repository key is required")
	}
	if encryptionContext.Purpose != ManagedEncryptionPurposeConsensusState && encryptionContext.Purpose != ManagedEncryptionPurposeProviderState &&
		encryptionContext.Purpose != ManagedEncryptionPurposeOutcomeResponse && encryptionContext.Purpose != ManagedEncryptionPurposeOutcomeAssistant &&
		encryptionContext.Purpose != ManagedEncryptionPurposeOutcomeSummaryExecution && encryptionContext.Purpose != ManagedEncryptionPurposeOutcomeSummaryResult &&
		encryptionContext.Purpose != ManagedEncryptionPurposeOutcomeNextState && encryptionContext.Purpose != ManagedEncryptionPurposeProviderFileReference &&
		encryptionContext.Purpose != ManagedEncryptionPurposeProviderFileReconciliation {
		return fmt.Errorf("unsupported managed consensus encryption purpose %q", encryptionContext.Purpose)
	}
	if encryptionContext.Revision == 0 {
		return fmt.Errorf("managed consensus encryption revision must be positive")
	}
	return nil
}

func managedEncryptionAdditionalData(encryptionContext ManagedEncryptionContext, envelope ManagedEncryptedEnvelope) ([]byte, error) {
	additionalAuthenticatedData, err := common.Marshal(struct {
		EnvelopeVersion int                      `json:"envelope_version"`
		Algorithm       string                   `json:"algorithm"`
		KeyVersion      string                   `json:"key_version"`
		RepositoryKey   string                   `json:"repository_key"`
		Purpose         ManagedEncryptionPurpose `json:"purpose"`
		Revision        uint64                   `json:"revision"`
	}{
		EnvelopeVersion: envelope.Version,
		Algorithm:       envelope.Algorithm,
		KeyVersion:      envelope.KeyVersion,
		RepositoryKey:   encryptionContext.RepositoryKey,
		Purpose:         envelope.Purpose,
		Revision:        envelope.Revision,
	})
	if err != nil {
		return nil, fmt.Errorf("encode managed consensus encryption metadata: %w", err)
	}
	return additionalAuthenticatedData, nil
}
