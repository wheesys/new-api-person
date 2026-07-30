package contextconsensus

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const (
	ManagedProviderStateReportVersion  = 1
	managedOpenAIResponseIDMaximumSize = 512
	managedOpenAIResponseStateField    = "previous_response_id"
	managedOpenAIResponseStateReason   = "responses_previous_response_id"
)

// ManagedProviderStateReport is produced only from a complete native upstream
// response. StateReference is deliberately excluded from JSON and formatted
// output because callers must treat the opaque provider identifier as secret.
type ManagedProviderStateReport struct {
	Version        int               `json:"version"`
	SourceProtocol types.RelayFormat `json:"source_protocol"`
	FinalProtocol  types.RelayFormat `json:"final_protocol"`
	RequestField   string            `json:"request_field"`
	ReasonCode     string            `json:"reason_code"`
	StateReference string            `json:"-"`
}

func (report ManagedProviderStateReport) String() string {
	return fmt.Sprintf(
		"ManagedProviderStateReport{Version:%d SourceProtocol:%s FinalProtocol:%s RequestField:%s ReasonCode:%s StateReference:***masked***}",
		report.Version,
		report.SourceProtocol,
		report.FinalProtocol,
		report.RequestField,
		report.ReasonCode,
	)
}

func (report ManagedProviderStateReport) GoString() string {
	return report.String()
}

func (report ManagedProviderStateReport) Validate() error {
	if report.Version != ManagedProviderStateReportVersion ||
		report.SourceProtocol != types.RelayFormatOpenAIResponses ||
		report.FinalProtocol != types.RelayFormatOpenAIResponses ||
		report.RequestField != managedOpenAIResponseStateField ||
		report.ReasonCode != managedOpenAIResponseStateReason {
		return fmt.Errorf("managed provider state report is unsupported")
	}
	return validateManagedOpenAIResponseID(report.StateReference)
}

type ExtractManagedProviderStateReportRequest struct {
	SourceProtocol types.RelayFormat
	FinalProtocol  types.RelayFormat
	HTTPStatus     int
	ResponseBody   []byte
}

// ExtractManagedProviderStateReport accepts only a complete native OpenAI
// Responses success. Converted requests and partial/error responses fail
// closed instead of inferring a provider-owned state reference.
func ExtractManagedProviderStateReport(request ExtractManagedProviderStateReportRequest) (ManagedProviderStateReport, error) {
	if request.SourceProtocol != types.RelayFormatOpenAIResponses || request.FinalProtocol != types.RelayFormatOpenAIResponses {
		return ManagedProviderStateReport{}, fmt.Errorf("managed provider state report supports only native OpenAI Responses")
	}
	if request.HTTPStatus < 200 || request.HTTPStatus >= 300 {
		return ManagedProviderStateReport{}, fmt.Errorf("managed provider state response was not successful")
	}
	if len(request.ResponseBody) == 0 || len(request.ResponseBody) > ManagedOutcomeMaximumResponseBytes {
		return ManagedProviderStateReport{}, fmt.Errorf("managed provider state response body is invalid")
	}
	var response struct {
		ID                string          `json:"id"`
		Object            string          `json:"object"`
		Status            string          `json:"status"`
		Error             json.RawMessage `json:"error"`
		IncompleteDetails json.RawMessage `json:"incomplete_details"`
	}
	if err := common.Unmarshal(request.ResponseBody, &response); err != nil {
		return ManagedProviderStateReport{}, fmt.Errorf("decode managed provider state response: %w", err)
	}
	if response.Object != "response" || response.Status != "completed" || managedRawPresent(response.Error) || managedRawPresent(response.IncompleteDetails) {
		return ManagedProviderStateReport{}, fmt.Errorf("managed provider state response is not complete")
	}
	if err := validateManagedOpenAIResponseID(response.ID); err != nil {
		return ManagedProviderStateReport{}, err
	}
	return ManagedProviderStateReport{
		Version:        ManagedProviderStateReportVersion,
		SourceProtocol: request.SourceProtocol,
		FinalProtocol:  request.FinalProtocol,
		RequestField:   managedOpenAIResponseStateField,
		ReasonCode:     managedOpenAIResponseStateReason,
		StateReference: response.ID,
	}, nil
}

func validateManagedOpenAIResponseID(responseID string) error {
	if !strings.HasPrefix(responseID, "resp_") || strings.TrimSpace(responseID) != responseID || len(responseID) > managedOpenAIResponseIDMaximumSize || !utf8.ValidString(responseID) {
		return fmt.Errorf("managed OpenAI response ID is invalid")
	}
	for _, character := range responseID {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("managed OpenAI response ID is invalid")
		}
	}
	return nil
}

// ManagedProviderFinalTarget is the immutable target selected for the real
// upstream call. Credential participates only in an HMAC fingerprint and is
// never returned from provider-state APIs or included in JSON/formatted text.
type ManagedProviderFinalTarget struct {
	RelayFormat       types.RelayFormat `json:"relay_format"`
	ChannelID         int               `json:"channel_id"`
	ChannelType       int               `json:"channel_type"`
	OriginModel       string            `json:"origin_model"`
	UpstreamModel     string            `json:"upstream_model"`
	MultiKeyIndex     int               `json:"multi_key_index"`
	ChannelIsMultiKey bool              `json:"channel_is_multi_key"`
	Credential        string            `json:"-"`
}

func (target ManagedProviderFinalTarget) String() string {
	return fmt.Sprintf(
		"ManagedProviderFinalTarget{RelayFormat:%s ChannelID:%d ChannelType:%d OriginModel:%s UpstreamModel:%s MultiKeyIndex:%d Credential:***masked***}",
		target.RelayFormat,
		target.ChannelID,
		target.ChannelType,
		target.OriginModel,
		target.UpstreamModel,
		target.MultiKeyIndex,
	)
}

func (target ManagedProviderFinalTarget) GoString() string {
	return target.String()
}

func (target ManagedProviderFinalTarget) Validate() error {
	if target.RelayFormat != types.RelayFormatOpenAIResponses {
		return fmt.Errorf("managed provider state target must use native OpenAI Responses")
	}
	if target.ChannelID <= 0 || target.ChannelType <= 0 || strings.TrimSpace(target.OriginModel) == "" || strings.TrimSpace(target.UpstreamModel) == "" || target.MultiKeyIndex < 0 || target.Credential == "" {
		return fmt.Errorf("managed provider state target is incomplete")
	}
	if !target.ChannelIsMultiKey && target.MultiKeyIndex != 0 {
		return fmt.Errorf("single-key managed provider target must use credential slot zero")
	}
	return nil
}

// ManagedProviderStateResolution is the owner-isolated, decrypted target pin.
// Its key deriver remains private so callers cannot access fingerprint keys.
type ManagedProviderStateResolution struct {
	binding    ManagedProviderStateBinding
	keyDeriver *ManagedConsensusKeyDeriver
	owner      ManagedConsensusOwner
	expiresAt  time.Time
}

func (resolution *ManagedProviderStateResolution) Binding() ManagedProviderStateBinding {
	if resolution == nil {
		return ManagedProviderStateBinding{}
	}
	binding := resolution.binding
	binding.Target.ReasonCodes = append([]string(nil), resolution.binding.Target.ReasonCodes...)
	return binding
}

func (resolution *ManagedProviderStateResolution) Target() ManagedProviderTargetBinding {
	return resolution.Binding().Target
}

func (resolution *ManagedProviderStateResolution) ValidateStateReference(stateReference string) error {
	if resolution == nil || resolution.keyDeriver == nil {
		return fmt.Errorf("managed provider state resolution is unavailable")
	}
	if err := validateManagedOpenAIResponseID(stateReference); err != nil {
		return ErrProviderStateBindingConflict
	}
	storageKey, err := resolution.keyDeriver.DeriveProviderStateStorageKey(resolution.owner, stateReference)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(storageKey.StateReferenceHMAC), []byte(resolution.binding.StateReferenceHMAC)) {
		return ErrProviderStateBindingConflict
	}
	return nil
}

// ValidateFinalTarget must run after the exact multi-key credential slot is
// selected and before sending the provider-bound request upstream.
func (resolution *ManagedProviderStateResolution) ValidateFinalTarget(target ManagedProviderFinalTarget) error {
	if resolution == nil || resolution.keyDeriver == nil {
		return fmt.Errorf("managed provider state resolution is unavailable")
	}
	if err := target.Validate(); err != nil {
		return err
	}
	expected, err := buildManagedProviderTargetBinding(resolution.keyDeriver, target)
	if err != nil {
		return err
	}
	actual := resolution.binding.Target
	if actual.BindingLevel != expected.BindingLevel || actual.RelayFormat != expected.RelayFormat || actual.ChannelID != expected.ChannelID ||
		actual.ChannelType != expected.ChannelType || actual.OriginModel != expected.OriginModel || actual.UpstreamModel != expected.UpstreamModel || actual.MultiKeyIndex != expected.MultiKeyIndex ||
		actual.ChannelIsMultiKey != expected.ChannelIsMultiKey || actual.FingerprintKeyVersion != expected.FingerprintKeyVersion ||
		!hmac.Equal([]byte(actual.CredentialFingerprint), []byte(expected.CredentialFingerprint)) ||
		len(actual.ReasonCodes) != 1 || actual.ReasonCodes[0] != managedOpenAIResponseStateReason {
		return ErrProviderStateBindingConflict
	}
	return nil
}

func ResolveManagedProviderStateBinding(
	ctx context.Context,
	runtime *ManagedConsensusRuntime,
	owner ManagedConsensusOwner,
	stateReference string,
) (*ManagedProviderStateResolution, error) {
	if runtime == nil || runtime.Cipher == nil || runtime.KeyDeriver == nil || runtime.Repository == nil {
		return nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	if err := validateManagedOpenAIResponseID(stateReference); err != nil {
		return nil, err
	}
	keys, derivers, err := runtime.providerStateStorageKeys(owner, stateReference)
	if err != nil {
		return nil, err
	}
	matchedIndex := -1
	var matchedRecord ManagedProviderStateRecord
	for index, key := range keys {
		record, loadErr := runtime.Repository.LoadProviderStateBinding(ctx, key)
		if errors.Is(loadErr, ErrProviderStateBindingNotFound) {
			continue
		}
		if loadErr != nil {
			return nil, loadErr
		}
		if matchedIndex >= 0 {
			return nil, ErrManagedConsensusKeyConflict
		}
		matchedIndex = index
		matchedRecord = record
	}
	if matchedIndex < 0 {
		return nil, ErrProviderStateBindingNotFound
	}
	key := keys[matchedIndex]
	if matchedRecord.Payload.KeyVersion != key.KeyVersion {
		return nil, ErrProviderStateBindingConflict
	}
	var binding ManagedProviderStateBinding
	if err := runtime.decryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: key.RepositoryKey,
		Purpose:       ManagedEncryptionPurposeProviderState,
		Revision:      1,
	}, matchedRecord.Payload, &binding); err != nil {
		return nil, err
	}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if binding.OwnerHMAC != key.OwnerHMAC || binding.StateReferenceHMAC != key.StateReferenceHMAC || time.Now().Unix() >= binding.ExpiresAtUnix {
		return nil, ErrProviderStateBindingConflict
	}
	encodedBinding, err := common.Marshal(binding)
	if err != nil {
		return nil, fmt.Errorf("encode managed provider state binding: %w", err)
	}
	if !hmac.Equal([]byte(matchedRecord.BindingDigest), []byte(digestBytes(encodedBinding))) {
		return nil, ErrProviderStateBindingConflict
	}
	return &ManagedProviderStateResolution{
		binding:    binding,
		keyDeriver: derivers[matchedIndex],
		owner:      owner,
		expiresAt:  matchedRecord.ExpiresAt,
	}, nil
}

func (runtime *ManagedConsensusRuntime) providerStateStorageKeys(
	owner ManagedConsensusOwner,
	stateReference string,
) ([]ManagedProviderStateStorageKey, []*ManagedConsensusKeyDeriver, error) {
	if runtime == nil || runtime.KeyDeriver == nil {
		return nil, nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	derivers := runtime.readKeyDerivers
	if len(derivers) == 0 {
		derivers = []*ManagedConsensusKeyDeriver{runtime.KeyDeriver}
	}
	keys := make([]ManagedProviderStateStorageKey, 0, len(derivers))
	for _, deriver := range derivers {
		key, err := deriver.DeriveProviderStateStorageKey(owner, stateReference)
		if err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
	}
	return keys, derivers, nil
}

func (session *ManagedConsensusSession) ResolveBoundProviderState(
	ctx context.Context,
	owner ManagedConsensusOwner,
	stateReference string,
	state ManagedConsensusState,
) (*ManagedProviderStateResolution, error) {
	if session == nil || session.runtime == nil || state.ProviderBinding == nil || state.ProviderState == nil {
		return nil, ErrProviderStateBindingNotFound
	}
	resolution, err := ResolveManagedProviderStateBinding(ctx, session.runtime, owner, stateReference)
	if err != nil {
		return nil, err
	}
	binding := resolution.Binding()
	link := state.ProviderState
	if binding.StateReferenceHMAC != link.StateReferenceHMAC || binding.ProducedRevision != link.ProducedRevision ||
		!reflect.DeepEqual(resolution.binding.Target, *state.ProviderBinding) || resolution.binding.Target.FingerprintKeyVersion != link.KeyVersion {
		return nil, ErrProviderStateBindingConflict
	}
	encodedBinding, err := common.Marshal(binding)
	if err != nil {
		return nil, err
	}
	if digestBytes(encodedBinding) != link.BindingDigest {
		return nil, ErrProviderStateBindingConflict
	}
	conversationMatches := false
	for _, conversationKey := range session.runtime.readConversationStorageKeys(session.storageKey, session.activeStorageKey) {
		if conversationKey.KeyVersion == link.KeyVersion && conversationKey.OwnerHMAC == binding.OwnerHMAC && conversationKey.ConversationHMAC == binding.ConversationHMAC {
			conversationMatches = true
			break
		}
	}
	if !conversationMatches || binding.ProducedRevision != state.Revision {
		return nil, ErrProviderStateBindingConflict
	}
	return resolution, nil
}

func buildManagedProviderTargetBinding(
	deriver *ManagedConsensusKeyDeriver,
	target ManagedProviderFinalTarget,
) (ManagedProviderTargetBinding, error) {
	if err := target.Validate(); err != nil {
		return ManagedProviderTargetBinding{}, err
	}
	fingerprint, err := deriver.DeriveCredentialFingerprint(target.ChannelID, target.MultiKeyIndex, target.Credential)
	if err != nil {
		return ManagedProviderTargetBinding{}, err
	}
	binding := ManagedProviderTargetBinding{
		BindingLevel:          BindingLevelCredential,
		RelayFormat:           target.RelayFormat,
		ChannelID:             target.ChannelID,
		ChannelType:           target.ChannelType,
		OriginModel:           target.OriginModel,
		UpstreamModel:         target.UpstreamModel,
		MultiKeyIndex:         target.MultiKeyIndex,
		ChannelIsMultiKey:     target.ChannelIsMultiKey,
		CredentialFingerprint: fingerprint,
		FingerprintKeyVersion: deriver.keyVersion,
		ReasonCodes:           []string{managedOpenAIResponseStateReason},
	}
	if err := binding.Validate(); err != nil {
		return ManagedProviderTargetBinding{}, err
	}
	return binding, nil
}

// ManagedProviderStateCommit is persisted only inside an AEAD-protected
// managed outcome checkpoint. Its String methods deliberately hide the raw
// provider state reference and encrypted payload.
type ManagedProviderStateCommit struct {
	StateReference      string                           `json:"state_reference"`
	StorageKey          ManagedProviderStateStorageKey   `json:"storage_key"`
	ConflictStorageKeys []ManagedProviderStateStorageKey `json:"conflict_storage_keys,omitempty"`
	Binding             ManagedProviderStateBinding      `json:"binding"`
	BindingDigest       string                           `json:"binding_digest"`
	Payload             ManagedEncryptedEnvelope         `json:"payload"`
	ExpiresAtUnix       int64                            `json:"expires_at_unix"`
}

func (commit ManagedProviderStateCommit) String() string {
	return "ManagedProviderStateCommit{StateReference:***masked*** Payload:***encrypted***}"
}

func (commit ManagedProviderStateCommit) GoString() string { return commit.String() }

func (commit ManagedProviderStateCommit) Link() ManagedProviderStateLink {
	return ManagedProviderStateLink{
		Version: ManagedConsensusStateVersion, StateReferenceHMAC: commit.StorageKey.StateReferenceHMAC,
		BindingDigest: commit.BindingDigest, KeyVersion: commit.StorageKey.KeyVersion,
		ProducedRevision: commit.Binding.ProducedRevision,
	}
}

func (commit ManagedProviderStateCommit) Validate(now time.Time) error {
	if err := validateManagedOpenAIResponseID(commit.StateReference); err != nil {
		return err
	}
	if commit.StorageKey.RepositoryKey == "" || commit.StorageKey.OwnerHMAC == "" || commit.StorageKey.StateReferenceHMAC == "" || commit.StorageKey.KeyVersion == "" ||
		strings.TrimSpace(commit.BindingDigest) == "" || commit.ExpiresAtUnix <= now.Unix() {
		return fmt.Errorf("managed provider state commit metadata is invalid")
	}
	if err := commit.Binding.Validate(); err != nil {
		return err
	}
	if commit.Binding.OwnerHMAC != commit.StorageKey.OwnerHMAC || commit.Binding.StateReferenceHMAC != commit.StorageKey.StateReferenceHMAC ||
		commit.Payload.Purpose != ManagedEncryptionPurposeProviderState || commit.Payload.Revision != 1 || commit.Payload.KeyVersion != commit.StorageKey.KeyVersion {
		return fmt.Errorf("managed provider state commit evidence is inconsistent")
	}
	if len(commit.ConflictStorageKeys) > managedConsensusMaximumPreviousKeys {
		return fmt.Errorf("managed provider state conflict keyring is too large")
	}
	seenKeys := map[string]struct{}{commit.StorageKey.RepositoryKey: {}}
	for _, conflictKey := range commit.ConflictStorageKeys {
		if conflictKey.RepositoryKey == "" || conflictKey.OwnerHMAC == "" || conflictKey.StateReferenceHMAC == "" ||
			conflictKey.KeyVersion == "" || conflictKey.KeyVersion == commit.StorageKey.KeyVersion {
			return fmt.Errorf("managed provider state conflict key is invalid")
		}
		if _, exists := seenKeys[conflictKey.RepositoryKey]; exists {
			return fmt.Errorf("managed provider state conflict key is duplicated")
		}
		seenKeys[conflictKey.RepositoryKey] = struct{}{}
	}
	return nil
}

func (session *ManagedConsensusSession) PrepareProviderStateCommitForOwner(
	ctx context.Context,
	owner ManagedConsensusOwner,
	report ManagedProviderStateReport,
	target ManagedProviderFinalTarget,
	ttl time.Duration,
	now time.Time,
) (ManagedProviderStateCommit, error) {
	if session == nil || session.runtime == nil || session.runtime.Cipher == nil || session.runtime.KeyDeriver == nil || now.IsZero() || ttl <= 0 {
		return ManagedProviderStateCommit{}, fmt.Errorf("managed provider state commit preparation is unavailable")
	}
	if err := report.Validate(); err != nil {
		return ManagedProviderStateCommit{}, err
	}
	providerKey, err := session.runtime.KeyDeriver.DeriveProviderStateStorageKey(owner, report.StateReference)
	if err != nil {
		return ManagedProviderStateCommit{}, err
	}
	if providerKey.OwnerHMAC != session.activeStorageKey.OwnerHMAC {
		return ManagedProviderStateCommit{}, ErrProviderStateBindingConflict
	}
	providerKeys, _, err := session.runtime.providerStateStorageKeys(owner, report.StateReference)
	if err != nil || len(providerKeys) == 0 || providerKeys[0].RepositoryKey != providerKey.RepositoryKey {
		return ManagedProviderStateCommit{}, ErrProviderStateBindingConflict
	}
	targetBinding, err := buildManagedProviderTargetBinding(session.runtime.KeyDeriver, target)
	if err != nil {
		return ManagedProviderStateCommit{}, err
	}
	expiresAt := now.Add(ttl)
	binding := ManagedProviderStateBinding{
		Version: ManagedConsensusStateVersion, OwnerHMAC: providerKey.OwnerHMAC,
		ConversationHMAC: session.activeStorageKey.ConversationHMAC, ProducedRevision: session.expectedRevision + 1,
		StateReferenceHMAC: providerKey.StateReferenceHMAC, Target: targetBinding,
		CreatedAtUnix: now.Unix(), ExpiresAtUnix: expiresAt.Unix(),
	}
	if err := binding.Validate(); err != nil {
		return ManagedProviderStateCommit{}, err
	}
	payload, err := session.runtime.Cipher.EncryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: providerKey.RepositoryKey, Purpose: ManagedEncryptionPurposeProviderState, Revision: 1,
	}, binding)
	if err != nil {
		return ManagedProviderStateCommit{}, err
	}
	encoded, err := common.Marshal(binding)
	if err != nil {
		return ManagedProviderStateCommit{}, err
	}
	commit := ManagedProviderStateCommit{
		StateReference: report.StateReference, StorageKey: providerKey,
		ConflictStorageKeys: append([]ManagedProviderStateStorageKey(nil), providerKeys[1:]...), Binding: binding,
		BindingDigest: digestBytes(encoded), Payload: payload, ExpiresAtUnix: expiresAt.Unix(),
	}
	return commit, commit.Validate(now)
}
