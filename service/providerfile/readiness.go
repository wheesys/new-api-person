package providerfile

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

var ErrReadinessEvidenceUnavailable = errors.New("managed provider file readiness evidence is unavailable")

type ReadinessArtifactEvidence struct {
	ProjectEvidenceHMAC        string
	SandboxEvidenceHMAC        string
	ImmutableAuditEvidenceHMAC string
	DatabaseMatrixEvidenceHMAC string
}

func VerifyDeletionReadiness(ctx context.Context, settings *model_setting.SmartRoutingSettings, runtime *contextconsensus.ManagedConsensusRuntime, httpClient *http.Client, now time.Time) error {
	if _, err := loadMaintenanceTarget(ctx, settings, runtime, httpClient, now); err != nil {
		return ErrReadinessEvidenceUnavailable
	}
	return nil
}

func BuildReadinessEvidence(runtime *contextconsensus.ManagedConsensusRuntime, target *Target, artifacts ReadinessArtifactEvidence, attestedAt, expiresAt time.Time) (*model.ManagedProviderFileReadinessEvidence, error) {
	if runtime == nil || runtime.KeyDeriver == nil || target == nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	attestedAt = attestedAt.UTC().Truncate(time.Second)
	expiresAt = expiresAt.UTC().Truncate(time.Second)
	targetFingerprint, err := runtime.KeyDeriver.DeriveProviderFileTargetFingerprint(target.identity())
	if err != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	scopeFingerprint, err := runtime.KeyDeriver.DeriveProviderFileScopeFingerprint(target.Organization, target.Project)
	if err != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	credentialFingerprint, err := runtime.KeyDeriver.DeriveProviderFileCredentialFingerprint(target.ChannelID, 0, target.credential)
	if err != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	evidence := &model.ManagedProviderFileReadinessEvidence{
		TargetFingerprint: targetFingerprint, ScopeFingerprint: scopeFingerprint, CredentialFingerprint: credentialFingerprint,
		ProjectEvidenceHMAC: artifacts.ProjectEvidenceHMAC, SandboxEvidenceHMAC: artifacts.SandboxEvidenceHMAC,
		ImmutableAuditEvidenceHMAC: artifacts.ImmutableAuditEvidenceHMAC, DatabaseMatrixEvidenceHMAC: artifacts.DatabaseMatrixEvidenceHMAC,
		ProjectAttestationVersion: model.ManagedProviderFileProjectAttestationVersion,
		SandboxContractVersion:    model.ManagedProviderFileSandboxContractVersion,
		ImmutableAuditVersion:     model.ManagedProviderFileImmutableAuditVersion,
		DatabaseMatrixVersion:     model.ManagedProviderFileDatabaseMatrixVersion,
		KeyVersion:                runtime.KeyDeriver.KeyVersion(), AttestedAt: attestedAt, ExpiresAt: expiresAt,
	}
	evidenceHMAC, err := runtime.KeyDeriver.DeriveProviderFileReadinessEvidenceHMAC(readinessEvidenceIdentity(evidence))
	if err != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	evidence.EvidenceHMAC = evidenceHMAC
	if err := evidence.Validate(); err != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	return evidence, nil
}

func VerifyReadinessEvidence(ctx context.Context, settings *model_setting.SmartRoutingSettings, runtime *contextconsensus.ManagedConsensusRuntime, target *Target, now time.Time) error {
	_, err := GetVerifiedReadinessEvidence(ctx, settings, runtime, target, now)
	return err
}

func GetVerifiedReadinessEvidence(ctx context.Context, settings *model_setting.SmartRoutingSettings, runtime *contextconsensus.ManagedConsensusRuntime, target *Target, now time.Time) (*model.ManagedProviderFileReadinessEvidence, error) {
	if ctx == nil || runtime == nil || runtime.KeyDeriver == nil || target == nil || now.IsZero() ||
		model_setting.ValidateProviderFileDeletionReadiness(settings) != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	targetFingerprint, err := runtime.KeyDeriver.DeriveProviderFileTargetFingerprint(target.identity())
	if err != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	scopeFingerprint, err := runtime.KeyDeriver.DeriveProviderFileScopeFingerprint(target.Organization, target.Project)
	if err != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	credentialFingerprint, err := runtime.KeyDeriver.DeriveProviderFileCredentialFingerprint(target.ChannelID, 0, target.credential)
	if err != nil {
		return nil, ErrReadinessEvidenceUnavailable
	}
	evidence, err := model.GetManagedProviderFileReadinessEvidence(ctx, targetFingerprint, scopeFingerprint, credentialFingerprint, runtime.KeyDeriver.KeyVersion(), now.UTC())
	if err != nil || evidence == nil || evidence.AttestedAt.After(now.UTC()) {
		return nil, ErrReadinessEvidenceUnavailable
	}
	expectedHMAC, err := runtime.KeyDeriver.DeriveProviderFileReadinessEvidenceHMAC(readinessEvidenceIdentity(evidence))
	if err != nil || !hmac.Equal([]byte(expectedHMAC), []byte(evidence.EvidenceHMAC)) {
		return nil, ErrReadinessEvidenceUnavailable
	}
	return evidence, nil
}

func readinessEvidenceIdentity(evidence *model.ManagedProviderFileReadinessEvidence) contextconsensus.ManagedProviderFileReadinessEvidenceIdentity {
	if evidence == nil {
		return contextconsensus.ManagedProviderFileReadinessEvidenceIdentity{}
	}
	return contextconsensus.ManagedProviderFileReadinessEvidenceIdentity{
		TargetFingerprint: evidence.TargetFingerprint, ScopeFingerprint: evidence.ScopeFingerprint, CredentialFingerprint: evidence.CredentialFingerprint,
		ProjectEvidenceHMAC: evidence.ProjectEvidenceHMAC, SandboxEvidenceHMAC: evidence.SandboxEvidenceHMAC,
		ImmutableAuditEvidenceHMAC: evidence.ImmutableAuditEvidenceHMAC, DatabaseMatrixEvidenceHMAC: evidence.DatabaseMatrixEvidenceHMAC,
		ProjectAttestationVersion: evidence.ProjectAttestationVersion, SandboxContractVersion: evidence.SandboxContractVersion,
		ImmutableAuditVersion: evidence.ImmutableAuditVersion, DatabaseMatrixVersion: evidence.DatabaseMatrixVersion,
		AttestedAtUnix: evidence.AttestedAt.UTC().Unix(), ExpiresAtUnix: evidence.ExpiresAt.UTC().Unix(),
	}
}
