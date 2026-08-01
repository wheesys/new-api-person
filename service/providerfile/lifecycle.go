package providerfile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/model"
	openai "github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"gorm.io/gorm"
)

const managedProviderFileEndpointFamily = "openai_provider_file"

var (
	ErrLifecycleConflict    = errors.New("managed provider file lifecycle conflicts with an existing request")
	ErrLifecycleUnavailable = errors.New("managed provider file lifecycle is unavailable")
	ErrUploadOutcomeUnknown = errors.New("managed provider file upload outcome is unknown")
)

type File struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
}

func NewOwner(userID, tokenID int) contextconsensus.ManagedConsensusOwner {
	return contextconsensus.ManagedConsensusOwner{UserID: userID, TokenID: tokenID, EndpointFamily: managedProviderFileEndpointFamily}
}

type UploadRequest struct {
	Owner          contextconsensus.ManagedConsensusOwner
	IdempotencyKey string
	Body           *UploadBody
	Settings       *model_setting.SmartRoutingSettings
	Runtime        *contextconsensus.ManagedConsensusRuntime
	Target         *Target
}

type RetrieveRequest struct {
	Owner   contextconsensus.ManagedConsensusOwner
	Handle  string
	Runtime *contextconsensus.ManagedConsensusRuntime
	Target  *Target
}

func Retrieve(ctx context.Context, request RetrieveRequest) (File, error) {
	_, payload, metadata, err := resolveActiveReference(ctx, request)
	if err != nil {
		return File{}, err
	}
	return publicFile(payload.GatewayHandle, metadata), nil
}

func resolveActiveReference(ctx context.Context, request RetrieveRequest) (*model.ManagedProviderFileLifecycle, contextconsensus.ManagedProviderFileReferencePayload, openai.ProviderFileMetadata, error) {
	if ctx == nil || request.Runtime == nil || request.Target == nil || request.Owner.EndpointFamily != managedProviderFileEndpointFamily ||
		contextconsensus.ValidateManagedProviderFileHandle(request.Handle) != nil {
		return nil, contextconsensus.ManagedProviderFileReferencePayload{}, openai.ProviderFileMetadata{}, ErrLifecycleConflict
	}
	handleKeys, err := request.Runtime.ProviderFileHandleKeys(request.Owner, request.Handle)
	if err != nil {
		return nil, contextconsensus.ManagedProviderFileReferencePayload{}, openai.ProviderFileMetadata{}, ErrLifecycleConflict
	}
	lookupCandidates := make([]model.ManagedProviderFileLifecycleLookupCandidate, 0, len(handleKeys))
	for _, key := range handleKeys {
		lookupCandidates = append(lookupCandidates, model.ManagedProviderFileLifecycleLookupCandidate{
			HandleLookupHMAC: key.HandleHMAC, OwnerHMAC: key.OwnerHMAC, KeyVersion: key.KeyVersion,
		})
	}
	lifecycle, err := model.FindManagedProviderFileLifecycle(ctx, lookupCandidates)
	if err != nil || lifecycle.State != model.ManagedProviderFileLifecycleStateActive || lifecycle.ExpiresAt == nil || !lifecycle.ExpiresAt.After(time.Now()) {
		return nil, contextconsensus.ManagedProviderFileReferencePayload{}, openai.ProviderFileMetadata{}, ErrLifecycleConflict
	}
	targetBindings, err := request.Runtime.ProviderFileTargetBindings(request.Target.identity(), request.Target.credential)
	if err != nil || !matchesLifecycleTarget(lifecycle, request.Target, targetBindings) {
		return nil, contextconsensus.ManagedProviderFileReferencePayload{}, openai.ProviderFileMetadata{}, ErrLifecycleConflict
	}
	repositoryKey, err := contextconsensus.ManagedProviderFileRepositoryKey(lifecycle.OwnerHMAC, lifecycle.UploadIntentHMAC)
	if err != nil {
		return nil, contextconsensus.ManagedProviderFileReferencePayload{}, openai.ProviderFileMetadata{}, ErrLifecycleUnavailable
	}
	payload, err := request.Runtime.DecryptProviderFileReference(ctx, repositoryKey, lifecycle.ProviderPayload)
	if err != nil || payload.GatewayHandle != request.Handle || lifecycle.ProviderCreatedAt == nil {
		return nil, contextconsensus.ManagedProviderFileReferencePayload{}, openai.ProviderFileMetadata{}, ErrLifecycleUnavailable
	}
	metadata, err := request.Target.client.Retrieve(ctx, payload.ProviderFileID)
	if err != nil {
		return nil, contextconsensus.ManagedProviderFileReferencePayload{}, openai.ProviderFileMetadata{}, ErrLifecycleUnavailable
	}
	storedMetadata := openai.ProviderFileMetadata{
		ProviderFileID: payload.ProviderFileID, Filename: payload.Filename, Object: "file", Purpose: lifecycle.Purpose,
		Bytes: lifecycle.ProviderBytes, CreatedAtUnix: lifecycle.ProviderCreatedAt.Unix(), ExpiresAtUnix: lifecycle.ExpiresAt.Unix(),
	}
	if !sameProviderFileMetadata(storedMetadata, metadata) {
		return nil, contextconsensus.ManagedProviderFileReferencePayload{}, openai.ProviderFileMetadata{}, ErrLifecycleUnavailable
	}
	return lifecycle, payload, metadata, nil
}

func Upload(ctx context.Context, request UploadRequest) (File, error) {
	if ctx == nil || request.Body == nil || request.Settings == nil || request.Runtime == nil || request.Target == nil ||
		request.Owner.EndpointFamily != managedProviderFileEndpointFamily {
		return File{}, ErrLifecycleUnavailable
	}
	bindings, err := request.Runtime.ProviderFileUploadBindings(
		request.Owner, request.IdempotencyKey, request.Body.ContentDigest, request.Target.identity(), request.Target.credential,
		model.ManagedProviderFilePurposeUserData, request.Settings.ProviderFileExpirationSeconds,
	)
	if err != nil || len(bindings) == 0 {
		return File{}, ErrLifecycleUnavailable
	}
	lookupCandidates := make([]model.ManagedProviderFileUploadIntentLookupCandidate, 0, len(bindings))
	for _, binding := range bindings {
		lookupCandidates = append(lookupCandidates, model.ManagedProviderFileUploadIntentLookupCandidate{
			UploadIntentHMAC: binding.IntentKey.UploadIntentHMAC, OwnerHMAC: binding.IntentKey.OwnerHMAC, KeyVersion: binding.IntentKey.KeyVersion,
		})
	}
	existing, lookupErr := model.FindManagedProviderFileLifecycleByUploadIntent(ctx, lookupCandidates)
	if lookupErr == nil {
		return replayUpload(ctx, request, existing, bindings)
	}
	if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		return File{}, ErrLifecycleConflict
	}

	activeBinding := bindings[0]
	handle, err := contextconsensus.GenerateManagedProviderFileHandle()
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	handleKey, err := request.Runtime.KeyDeriver.DeriveProviderFileHandleKey(request.Owner, handle)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	now := time.Now().UTC().Truncate(time.Second)
	intentEvent, err := providerFileEvent(request.Runtime, activeBinding.IntentKey.UploadIntentHMAC, 0, 1, "", "",
		model.ManagedProviderFileLifecycleEventIntentCreated, "", model.ManagedProviderFileLifecycleStateIntent, "", activeBinding.RequestFingerprint, now)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	lifecycleIntent := model.ManagedProviderFileLifecycle{
		UploadIntentHMAC: activeBinding.IntentKey.UploadIntentHMAC, HandleLookupHMAC: handleKey.HandleHMAC,
		OwnerHMAC: activeBinding.IntentKey.OwnerHMAC, LookupKeyVersion: activeBinding.IntentKey.KeyVersion,
		RequestFingerprint: activeBinding.RequestFingerprint, Provider: model.ManagedProviderFileProviderOpenAI,
		State: model.ManagedProviderFileLifecycleStateIntent, ChannelId: request.Target.ChannelID, ChannelType: request.Target.ChannelType,
		ChannelMultiKeyIndex: 0, CredentialFingerprint: activeBinding.CredentialFingerprint,
		CredentialFingerprintKeyVersion: activeBinding.IntentKey.KeyVersion, EndpointFingerprint: activeBinding.EndpointFingerprint,
		ProviderScopeFingerprint: activeBinding.ScopeFingerprint, PayloadKeyVersion: activeBinding.IntentKey.KeyVersion,
		Purpose: model.ManagedProviderFilePurposeUserData,
	}
	lifecycle, created, err := model.CreateManagedProviderFileLifecycleIntent(ctx, lifecycleIntent, intentEvent)
	if err != nil {
		return File{}, ErrLifecycleConflict
	}
	if !created {
		return replayUpload(ctx, request, lifecycle, bindings)
	}
	currentTarget, targetErr := LoadTarget(request.Settings, request.Runtime, nil)
	targetChanged := targetErr == nil && (currentTarget.ChannelID != request.Target.ChannelID || currentTarget.ChannelType != request.Target.ChannelType ||
		currentTarget.Endpoint != request.Target.Endpoint || currentTarget.Organization != request.Target.Organization ||
		currentTarget.Project != request.Target.Project || currentTarget.credential != request.Target.credential)
	if targetErr != nil || targetChanged {
		failedAt := time.Now().UTC().Truncate(time.Second)
		failedEvent, eventErr := providerFileEvent(request.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id, 2, lifecycle.LastEventHMAC,
			model.ManagedProviderFileLifecycleStateIntent, model.ManagedProviderFileLifecycleEventUploadFailed,
			model.ManagedProviderFileLifecycleStateIntent, model.ManagedProviderFileLifecycleStateUploadFailed,
			"target_changed_before_dispatch", activeBinding.RequestFingerprint, failedAt)
		if eventErr == nil {
			_ = model.AdvanceManagedProviderFileUploadState(ctx, model.ManagedProviderFileUploadTransition{
				LifecycleId: lifecycle.Id, ExpectedVersion: lifecycle.Version, RequestFingerprint: activeBinding.RequestFingerprint,
				ExpectedState: model.ManagedProviderFileLifecycleStateIntent, NextState: model.ManagedProviderFileLifecycleStateUploadFailed,
				ReasonCode: "target_changed_before_dispatch", Event: failedEvent,
			})
		}
		if targetErr != nil {
			return File{}, fmt.Errorf("%w: target snapshot unavailable before dispatch", ErrLifecycleUnavailable)
		}
		return File{}, fmt.Errorf("%w: target snapshot changed before dispatch", ErrLifecycleUnavailable)
	}

	dispatchedAt := time.Now().UTC().Truncate(time.Second)
	dispatchedEvent, err := providerFileEvent(request.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id, 2, lifecycle.LastEventHMAC,
		model.ManagedProviderFileLifecycleStateIntent, model.ManagedProviderFileLifecycleEventUploadDispatched,
		model.ManagedProviderFileLifecycleStateIntent, model.ManagedProviderFileLifecycleStateUploadDispatched, "", activeBinding.RequestFingerprint, dispatchedAt)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	if err := model.AdvanceManagedProviderFileUploadState(ctx, model.ManagedProviderFileUploadTransition{
		LifecycleId: lifecycle.Id, ExpectedVersion: lifecycle.Version, RequestFingerprint: activeBinding.RequestFingerprint,
		ExpectedState: model.ManagedProviderFileLifecycleStateIntent, NextState: model.ManagedProviderFileLifecycleStateUploadDispatched, Event: dispatchedEvent,
	}); err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	lifecycle.Version++
	lifecycle.State = model.ManagedProviderFileLifecycleStateUploadDispatched
	lifecycle.LastEventSequence = 2
	lifecycle.LastEventHMAC = dispatchedEvent.EventHMAC

	content, err := request.Body.Open()
	if err != nil {
		markUploadUnknown(ctx, request.Runtime, lifecycle, activeBinding.RequestFingerprint, "upload_body_unavailable")
		return File{}, ErrUploadOutcomeUnknown
	}
	uploadMetadata, uploadErr := request.Target.client.Upload(ctx, openai.ProviderFileUploadRequest{
		Filename: request.Body.Filename, Content: content, SizeBytes: request.Body.SizeBytes,
		ExpiresAfterSeconds: request.Settings.ProviderFileExpirationSeconds,
	})
	_ = content.Close()
	if uploadErr != nil {
		markUploadUnknown(ctx, request.Runtime, lifecycle, activeBinding.RequestFingerprint, "provider_upload_unknown")
		return File{}, ErrUploadOutcomeUnknown
	}
	if uploadMetadata.Bytes != request.Body.SizeBytes {
		return recordVerificationFailure(ctx, request, lifecycle, activeBinding, handle, uploadMetadata, "upload_metadata_mismatch")
	}

	providerLookupHMAC, targetProviderLookupHMAC, encryptedPayload, payloadKeyVersion, err := providerReferenceEvidence(ctx, request, activeBinding, handle, uploadMetadata)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	retrievedMetadata, retrieveErr := request.Target.client.Retrieve(ctx, uploadMetadata.ProviderFileID)
	if retrieveErr != nil || !sameProviderFileMetadata(uploadMetadata, retrievedMetadata) {
		return recordEncryptedVerificationFailure(ctx, request, lifecycle, activeBinding, handle, uploadMetadata, providerLookupHMAC, targetProviderLookupHMAC, encryptedPayload, "metadata_unverified")
	}
	deletionOperationHMAC, err := request.Runtime.KeyDeriver.DeriveProviderFileDeletionOperationHMAC(request.Owner, handle)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	verifiedAt := time.Now().UTC().Truncate(time.Second)
	activationEvent, err := providerFileEvent(request.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id, 3, lifecycle.LastEventHMAC,
		model.ManagedProviderFileLifecycleStateUploadDispatched, model.ManagedProviderFileLifecycleEventActivated,
		model.ManagedProviderFileLifecycleStateUploadDispatched, model.ManagedProviderFileLifecycleStateActive, "", activeBinding.RequestFingerprint, verifiedAt)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	createdAt := time.Unix(retrievedMetadata.CreatedAtUnix, 0).UTC()
	expiresAt := time.Unix(retrievedMetadata.ExpiresAtUnix, 0).UTC()
	deletionAt := expiresAt.Add(-time.Duration(request.Settings.ProviderFileDeletionLeadSeconds) * time.Second)
	activated, _, _, err := model.ActivateManagedProviderFileLifecycle(ctx, model.ManagedProviderFileLifecycleActivation{
		LifecycleId: lifecycle.Id, ExpectedVersion: lifecycle.Version, RequestFingerprint: activeBinding.RequestFingerprint,
		ProviderLookupHMAC: providerLookupHMAC, TargetProviderLookupHMAC: targetProviderLookupHMAC, ProviderPayload: encryptedPayload, ProviderBytes: retrievedMetadata.Bytes,
		ProviderCreatedAt: createdAt, MetadataVerifiedAt: verifiedAt, ExpiresAt: expiresAt,
		DeletionOperationHMAC: deletionOperationHMAC, DeletionNextAttemptAt: deletionAt,
		MaxDeletionAttempts: request.Settings.ProviderFileDeletionMaxAttempts, Event: activationEvent,
	})
	if err != nil || activated.PayloadKeyVersion != payloadKeyVersion {
		return File{}, ErrLifecycleUnavailable
	}
	return publicFile(handle, retrievedMetadata), nil
}

func replayUpload(ctx context.Context, request UploadRequest, lifecycle *model.ManagedProviderFileLifecycle, bindings []contextconsensus.ManagedProviderFileUploadBinding) (File, error) {
	if lifecycle == nil {
		return File{}, ErrLifecycleUnavailable
	}
	var matched *contextconsensus.ManagedProviderFileUploadBinding
	for index := range bindings {
		binding := &bindings[index]
		if lifecycle.LookupKeyVersion == binding.IntentKey.KeyVersion && lifecycle.UploadIntentHMAC == binding.IntentKey.UploadIntentHMAC {
			matched = binding
			break
		}
	}
	if matched == nil || lifecycle.RequestFingerprint != matched.RequestFingerprint || lifecycle.ChannelId != request.Target.ChannelID ||
		lifecycle.ChannelType != request.Target.ChannelType || lifecycle.ChannelIsMultiKey || lifecycle.ChannelMultiKeyIndex != 0 ||
		lifecycle.CredentialFingerprint != matched.CredentialFingerprint || lifecycle.EndpointFingerprint != matched.EndpointFingerprint ||
		lifecycle.ProviderScopeFingerprint != matched.ScopeFingerprint {
		return File{}, ErrLifecycleConflict
	}
	if lifecycle.State != model.ManagedProviderFileLifecycleStateActive || lifecycle.ExpiresAt == nil || !lifecycle.ExpiresAt.After(time.Now()) {
		return File{}, ErrLifecycleConflict
	}
	repositoryKey, err := contextconsensus.ManagedProviderFileRepositoryKey(lifecycle.OwnerHMAC, lifecycle.UploadIntentHMAC)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	payload, err := request.Runtime.DecryptProviderFileReference(ctx, repositoryKey, lifecycle.ProviderPayload)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	handleKeys, err := request.Runtime.ProviderFileHandleKeys(request.Owner, payload.GatewayHandle)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	validHandle := false
	for _, key := range handleKeys {
		if key.KeyVersion == lifecycle.LookupKeyVersion && key.HandleHMAC == lifecycle.HandleLookupHMAC {
			validHandle = true
			break
		}
	}
	if !validHandle || lifecycle.ProviderCreatedAt == nil {
		return File{}, ErrLifecycleUnavailable
	}
	metadata := openai.ProviderFileMetadata{
		ProviderFileID: payload.ProviderFileID, Filename: payload.Filename, Object: "file", Purpose: lifecycle.Purpose,
		Bytes: lifecycle.ProviderBytes, CreatedAtUnix: lifecycle.ProviderCreatedAt.Unix(), ExpiresAtUnix: lifecycle.ExpiresAt.Unix(),
	}
	return publicFile(payload.GatewayHandle, metadata), nil
}

func providerReferenceEvidence(ctx context.Context, request UploadRequest, binding contextconsensus.ManagedProviderFileUploadBinding, handle string, metadata openai.ProviderFileMetadata) (string, string, []byte, string, error) {
	providerLookupHMAC, err := request.Runtime.KeyDeriver.DeriveProviderFileReferenceHMAC(request.Owner, metadata.ProviderFileID)
	if err != nil {
		return "", "", nil, "", err
	}
	targetProviderLookupHMAC, err := request.Runtime.KeyDeriver.DeriveProviderFileTargetReferenceHMAC(binding.TargetFingerprint, metadata.ProviderFileID)
	if err != nil {
		return "", "", nil, "", err
	}
	payload, payloadKeyVersion, err := request.Runtime.EncryptProviderFileReference(ctx, binding.IntentKey, contextconsensus.ManagedProviderFileReferencePayload{
		ProviderFileID: metadata.ProviderFileID, Filename: metadata.Filename, GatewayHandle: handle,
	})
	return providerLookupHMAC, targetProviderLookupHMAC, payload, payloadKeyVersion, err
}

func recordVerificationFailure(ctx context.Context, request UploadRequest, lifecycle *model.ManagedProviderFileLifecycle, binding contextconsensus.ManagedProviderFileUploadBinding, handle string, metadata openai.ProviderFileMetadata, reason string) (File, error) {
	providerLookupHMAC, targetProviderLookupHMAC, encryptedPayload, _, err := providerReferenceEvidence(ctx, request, binding, handle, metadata)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	return recordEncryptedVerificationFailure(ctx, request, lifecycle, binding, handle, metadata, providerLookupHMAC, targetProviderLookupHMAC, encryptedPayload, reason)
}

func recordEncryptedVerificationFailure(ctx context.Context, request UploadRequest, lifecycle *model.ManagedProviderFileLifecycle, binding contextconsensus.ManagedProviderFileUploadBinding, handle string, metadata openai.ProviderFileMetadata, providerLookupHMAC, targetProviderLookupHMAC string, encryptedPayload []byte, reason string) (File, error) {
	deletionOperationHMAC, err := request.Runtime.KeyDeriver.DeriveProviderFileDeletionOperationHMAC(request.Owner, handle)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	now := time.Now().UTC().Truncate(time.Second)
	event, err := providerFileEvent(request.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id, 3, lifecycle.LastEventHMAC,
		model.ManagedProviderFileLifecycleStateUploadDispatched, model.ManagedProviderFileLifecycleEventVerificationFailed,
		model.ManagedProviderFileLifecycleStateUploadDispatched, model.ManagedProviderFileLifecycleStateVerificationFailed, reason, binding.RequestFingerprint, now)
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	err = model.RecordManagedProviderFileVerificationFailure(ctx, model.ManagedProviderFileVerificationFailure{
		LifecycleId: lifecycle.Id, ExpectedVersion: lifecycle.Version, RequestFingerprint: binding.RequestFingerprint,
		ProviderLookupHMAC: providerLookupHMAC, TargetProviderLookupHMAC: targetProviderLookupHMAC, ProviderPayload: encryptedPayload, ProviderBytes: metadata.Bytes,
		ProviderCreatedAt: time.Unix(metadata.CreatedAtUnix, 0), ExpiresAt: time.Unix(metadata.ExpiresAtUnix, 0), ReasonCode: reason,
		DeletionOperationHMAC: deletionOperationHMAC, DeletionNextAttemptAt: now,
		MaxDeletionAttempts: request.Settings.ProviderFileDeletionMaxAttempts, Event: event,
	})
	if err != nil {
		return File{}, ErrLifecycleUnavailable
	}
	return File{}, ErrLifecycleUnavailable
}

func markUploadUnknown(ctx context.Context, runtime *contextconsensus.ManagedConsensusRuntime, lifecycle *model.ManagedProviderFileLifecycle, requestFingerprint, reason string) {
	if lifecycle == nil {
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	event, err := providerFileEvent(runtime, lifecycle.UploadIntentHMAC, lifecycle.Id, lifecycle.LastEventSequence+1, lifecycle.LastEventHMAC,
		lifecycle.State, model.ManagedProviderFileLifecycleEventUploadUnknown, lifecycle.State,
		model.ManagedProviderFileLifecycleStateUploadUnknown, reason, requestFingerprint, now)
	if err != nil {
		return
	}
	_ = model.AdvanceManagedProviderFileUploadState(ctx, model.ManagedProviderFileUploadTransition{
		LifecycleId: lifecycle.Id, ExpectedVersion: lifecycle.Version, RequestFingerprint: requestFingerprint,
		ExpectedState: lifecycle.State, NextState: model.ManagedProviderFileLifecycleStateUploadUnknown, ReasonCode: reason, Event: event,
	})
}

func providerFileEvent(runtime *contextconsensus.ManagedConsensusRuntime, lifecycleHMAC string, lifecycleID, sequence int64, previousHMAC, fromState, eventType, eventFromState, toState, resultCode, evidenceDigest string, createdAt time.Time) (model.ManagedProviderFileLifecycleEvent, error) {
	return providerFileEventWithAttempt(runtime, lifecycleHMAC, lifecycleID, sequence, previousHMAC, fromState, eventType, eventFromState, toState, 0, resultCode, evidenceDigest, createdAt)
}

func providerFileEventWithAttempt(runtime *contextconsensus.ManagedConsensusRuntime, lifecycleHMAC string, lifecycleID, sequence int64, previousHMAC, fromState, eventType, eventFromState, toState string, attemptCount int, resultCode, evidenceDigest string, createdAt time.Time) (model.ManagedProviderFileLifecycleEvent, error) {
	if fromState != eventFromState {
		return model.ManagedProviderFileLifecycleEvent{}, fmt.Errorf("provider file event state is invalid")
	}
	eventHMAC, err := runtime.KeyDeriver.DeriveProviderFileEventHMAC(contextconsensus.ManagedProviderFileEventIdentity{
		LifecycleHMAC: lifecycleHMAC, Sequence: sequence, PreviousEventHMAC: previousHMAC, EventType: eventType,
		FromState: fromState, ToState: toState, AttemptCount: attemptCount, ResultCode: resultCode, EvidenceDigest: evidenceDigest, CreatedAtUnix: createdAt.Unix(),
	})
	if err != nil {
		return model.ManagedProviderFileLifecycleEvent{}, err
	}
	return model.ManagedProviderFileLifecycleEvent{
		LifecycleId: lifecycleID, Sequence: sequence, PreviousEventHMAC: previousHMAC, EventHMAC: eventHMAC,
		EventType: eventType, FromState: fromState, ToState: toState, AttemptCount: attemptCount, ResultCode: resultCode,
		EvidenceDigest: evidenceDigest, KeyVersion: runtime.KeyDeriver.KeyVersion(), CreatedAt: createdAt,
	}, nil
}

func sameProviderFileMetadata(left, right openai.ProviderFileMetadata) bool {
	return left.ProviderFileID == right.ProviderFileID && left.Filename == right.Filename && left.Object == right.Object &&
		left.Purpose == right.Purpose && left.Bytes == right.Bytes && left.CreatedAtUnix == right.CreatedAtUnix && left.ExpiresAtUnix == right.ExpiresAtUnix
}

func matchesLifecycleTarget(lifecycle *model.ManagedProviderFileLifecycle, target *Target, bindings []contextconsensus.ManagedProviderFileTargetBinding) bool {
	if lifecycle == nil || target == nil || lifecycle.ChannelId != target.ChannelID || lifecycle.ChannelType != target.ChannelType ||
		lifecycle.ChannelIsMultiKey || lifecycle.ChannelMultiKeyIndex != 0 {
		return false
	}
	for _, binding := range bindings {
		if lifecycle.LookupKeyVersion == binding.KeyVersion && lifecycle.CredentialFingerprintKeyVersion == binding.KeyVersion &&
			lifecycle.CredentialFingerprint == binding.CredentialFingerprint && lifecycle.EndpointFingerprint == binding.EndpointFingerprint &&
			lifecycle.ProviderScopeFingerprint == binding.ScopeFingerprint {
			return true
		}
	}
	return false
}

func publicFile(handle string, metadata openai.ProviderFileMetadata) File {
	return File{
		ID: handle, Object: "file", Bytes: metadata.Bytes, CreatedAt: metadata.CreatedAtUnix,
		ExpiresAt: metadata.ExpiresAtUnix, Filename: metadata.Filename, Purpose: metadata.Purpose,
	}
}
