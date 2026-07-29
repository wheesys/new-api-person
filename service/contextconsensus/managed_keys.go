package contextconsensus

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	managedConsensusRepositoryKeyPrefix  = "new-api:context_consensus:v1"
	managedProviderStateRepositoryPrefix = "new-api:context_consensus:provider_state:v1"
	managedOutcomeRepositoryKeyPrefix    = "new-api:context_consensus:outcome:v1"
)

type ManagedConsensusOwner struct {
	UserID         int
	TokenID        int
	EndpointFamily string
}

type ManagedConversationStorageKey struct {
	RepositoryKey    string
	OwnerHMAC        string
	ConversationHMAC string
	KeyVersion       string
}

type ManagedProviderStateStorageKey struct {
	RepositoryKey      string
	OwnerHMAC          string
	StateReferenceHMAC string
	KeyVersion         string
}

type ManagedOutcomeStorageKey struct {
	RepositoryKey      string
	LookupHMAC         string
	RevisionIntentHMAC string
	OwnerHMAC          string
	ConversationHMAC   string
	KeyVersion         string
	BillingLookupHMAC  string
}

type ManagedConsensusKeyDeriver struct {
	key        []byte
	keyVersion string
}

func NewManagedConsensusKeyDeriver(key []byte, keyVersion string) (*ManagedConsensusKeyDeriver, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("managed consensus HMAC key must contain at least 32 bytes")
	}
	if strings.TrimSpace(keyVersion) == "" {
		return nil, fmt.Errorf("managed consensus HMAC key version is required")
	}
	return &ManagedConsensusKeyDeriver{
		key:        append([]byte(nil), key...),
		keyVersion: strings.TrimSpace(keyVersion),
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveConversationStorageKey(owner ManagedConsensusOwner, externalContextID string) (ManagedConversationStorageKey, error) {
	ownerHMAC, err := deriver.deriveOwnerHMAC(owner)
	if err != nil {
		return ManagedConversationStorageKey{}, err
	}
	if strings.TrimSpace(externalContextID) == "" {
		return ManagedConversationStorageKey{}, fmt.Errorf("external context ID is required")
	}
	conversationHMAC, err := deriver.calculateHMAC("conversation", struct {
		OwnerHMAC         string `json:"owner_hmac"`
		ExternalContextID string `json:"external_context_id"`
	}{
		OwnerHMAC:         ownerHMAC,
		ExternalContextID: externalContextID,
	})
	if err != nil {
		return ManagedConversationStorageKey{}, err
	}
	return ManagedConversationStorageKey{
		RepositoryKey:    managedConsensusRepositoryKeyPrefix + ":" + ownerHMAC + ":" + conversationHMAC,
		OwnerHMAC:        ownerHMAC,
		ConversationHMAC: conversationHMAC,
		KeyVersion:       deriver.keyVersion,
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderStateStorageKey(owner ManagedConsensusOwner, providerStateReference string) (ManagedProviderStateStorageKey, error) {
	ownerHMAC, err := deriver.deriveOwnerHMAC(owner)
	if err != nil {
		return ManagedProviderStateStorageKey{}, err
	}
	if strings.TrimSpace(providerStateReference) == "" {
		return ManagedProviderStateStorageKey{}, fmt.Errorf("provider state reference is required")
	}
	stateReferenceHMAC, err := deriver.calculateHMAC("provider_state", struct {
		OwnerHMAC              string `json:"owner_hmac"`
		ProviderStateReference string `json:"provider_state_reference"`
	}{
		OwnerHMAC:              ownerHMAC,
		ProviderStateReference: providerStateReference,
	})
	if err != nil {
		return ManagedProviderStateStorageKey{}, err
	}
	return ManagedProviderStateStorageKey{
		RepositoryKey:      managedProviderStateRepositoryPrefix + ":" + ownerHMAC + ":" + stateReferenceHMAC,
		OwnerHMAC:          ownerHMAC,
		StateReferenceHMAC: stateReferenceHMAC,
		KeyVersion:         deriver.keyVersion,
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveOutcomeStorageKey(owner ManagedConsensusOwner, externalContextID, idempotencyKey string, expectedRevision uint64) (ManagedOutcomeStorageKey, error) {
	conversationKey, err := deriver.DeriveConversationStorageKey(owner, externalContextID)
	if err != nil {
		return ManagedOutcomeStorageKey{}, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ManagedOutcomeStorageKey{}, fmt.Errorf("managed context idempotency key is required")
	}
	lookupHMAC, err := deriver.calculateHMAC("idempotency_key", struct {
		OwnerHMAC      string `json:"owner_hmac"`
		IdempotencyKey string `json:"idempotency_key"`
	}{OwnerHMAC: conversationKey.OwnerHMAC, IdempotencyKey: idempotencyKey})
	if err != nil {
		return ManagedOutcomeStorageKey{}, err
	}
	revisionIntentHMAC, err := deriver.calculateHMAC("revision_intent", struct {
		OwnerHMAC        string `json:"owner_hmac"`
		ConversationHMAC string `json:"conversation_hmac"`
		ExpectedRevision uint64 `json:"expected_revision"`
	}{OwnerHMAC: conversationKey.OwnerHMAC, ConversationHMAC: conversationKey.ConversationHMAC, ExpectedRevision: expectedRevision})
	if err != nil {
		return ManagedOutcomeStorageKey{}, err
	}
	return ManagedOutcomeStorageKey{
		RepositoryKey: managedOutcomeRepositoryKeyPrefix + ":" + conversationKey.OwnerHMAC + ":" + lookupHMAC,
		LookupHMAC:    lookupHMAC, RevisionIntentHMAC: revisionIntentHMAC,
		OwnerHMAC: conversationKey.OwnerHMAC, ConversationHMAC: conversationKey.ConversationHMAC,
		KeyVersion:        conversationKey.KeyVersion,
		BillingLookupHMAC: digestBytes([]byte(conversationKey.RepositoryKey)),
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveCredentialFingerprint(channelID int, multiKeyIndex int, credential string) (string, error) {
	if channelID <= 0 {
		return "", fmt.Errorf("credential fingerprint channel ID must be positive")
	}
	if multiKeyIndex < 0 {
		return "", fmt.Errorf("credential fingerprint multi-key index must not be negative")
	}
	if credential == "" {
		return "", fmt.Errorf("credential fingerprint credential is required")
	}
	return deriver.calculateHMAC("credential", struct {
		ChannelID     int    `json:"channel_id"`
		MultiKeyIndex int    `json:"multi_key_index"`
		Credential    string `json:"credential"`
	}{
		ChannelID:     channelID,
		MultiKeyIndex: multiKeyIndex,
		Credential:    credential,
	})
}

func (deriver *ManagedConsensusKeyDeriver) deriveOwnerHMAC(owner ManagedConsensusOwner) (string, error) {
	if deriver == nil || len(deriver.key) == 0 {
		return "", fmt.Errorf("managed consensus key deriver is required")
	}
	if owner.UserID <= 0 {
		return "", fmt.Errorf("managed consensus owner user ID must be positive")
	}
	if owner.TokenID <= 0 {
		return "", fmt.Errorf("managed consensus owner token ID must be positive")
	}
	endpointFamily := strings.ToLower(strings.TrimSpace(owner.EndpointFamily))
	if endpointFamily == "" {
		return "", fmt.Errorf("managed consensus owner endpoint family is required")
	}
	return deriver.calculateHMAC("owner", struct {
		UserID         int    `json:"user_id"`
		TokenID        int    `json:"token_id"`
		EndpointFamily string `json:"endpoint_family"`
	}{
		UserID:         owner.UserID,
		TokenID:        owner.TokenID,
		EndpointFamily: endpointFamily,
	})
}

func (deriver *ManagedConsensusKeyDeriver) calculateHMAC(scope string, value any) (string, error) {
	if deriver == nil || len(deriver.key) == 0 {
		return "", fmt.Errorf("managed consensus key deriver is required")
	}
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode managed consensus %s HMAC input: %w", scope, err)
	}
	messageAuthenticationCode := hmac.New(sha256.New, deriver.key)
	_, _ = messageAuthenticationCode.Write([]byte(deriver.keyVersion))
	_, _ = messageAuthenticationCode.Write([]byte{0})
	_, _ = messageAuthenticationCode.Write([]byte(scope))
	_, _ = messageAuthenticationCode.Write([]byte{0})
	_, _ = messageAuthenticationCode.Write(encoded)
	return base64.RawURLEncoding.EncodeToString(messageAuthenticationCode.Sum(nil)), nil
}
