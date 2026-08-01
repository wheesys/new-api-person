package model

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	ManagedProviderFileProjectAttestationVersion = "openai-project-exclusivity-v1"
	ManagedProviderFileSandboxContractVersion    = "openai-files-sandbox-contract-v1"
	ManagedProviderFileImmutableAuditVersion     = "provider-file-worm-audit-v1"
	ManagedProviderFileDatabaseMatrixVersion     = "sqlite-mysql57-postgresql96-v1"
)

var ErrManagedProviderFileReadinessEvidenceImmutable = errors.New("managed provider file readiness evidence is immutable")

// ManagedProviderFileReadinessEvidence is a short-lived, target-bound assertion over external production gates.
type ManagedProviderFileReadinessEvidence struct {
	Id                         int64     `json:"id" gorm:"primaryKey;autoIncrement;comment:Managed provider file readiness evidence numeric identifier"`
	TargetFingerprint          string    `json:"-" gorm:"type:varchar(64);not null;index:idx_provider_file_readiness_lookup,priority:1;comment:HMAC binding the exact dedicated channel target and OpenAI project"`
	ScopeFingerprint           string    `json:"-" gorm:"type:varchar(64);not null;index:idx_provider_file_readiness_lookup,priority:2;comment:HMAC binding the exact OpenAI organization and project scope"`
	CredentialFingerprint      string    `json:"-" gorm:"type:varchar(64);not null;index:idx_provider_file_readiness_lookup,priority:3;comment:HMAC binding the exact dedicated channel credential"`
	ProjectEvidenceHMAC        string    `json:"-" gorm:"type:varchar(64);not null;comment:HMAC digest of the independently retained exclusive project evidence"`
	SandboxEvidenceHMAC        string    `json:"-" gorm:"type:varchar(64);not null;comment:HMAC digest of the independently retained real sandbox contract evidence"`
	ImmutableAuditEvidenceHMAC string    `json:"-" gorm:"type:varchar(64);not null;comment:HMAC digest of the independently retained immutable audit evidence"`
	DatabaseMatrixEvidenceHMAC string    `json:"-" gorm:"type:varchar(64);not null;comment:HMAC digest of the independently retained three database validation evidence"`
	ProjectAttestationVersion  string    `json:"project_attestation_version" gorm:"type:varchar(64);not null;comment:Version of the exclusive OpenAI project attestation contract"`
	SandboxContractVersion     string    `json:"sandbox_contract_version" gorm:"type:varchar(64);not null;comment:Version of the real OpenAI Files sandbox contract matrix"`
	ImmutableAuditVersion      string    `json:"immutable_audit_version" gorm:"type:varchar(64);not null;comment:Version of the external immutable audit contract"`
	DatabaseMatrixVersion      string    `json:"database_matrix_version" gorm:"type:varchar(64);not null;comment:Version of the SQLite MySQL and PostgreSQL validation matrix"`
	EvidenceHMAC               string    `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_provider_file_readiness_evidence_hmac;comment:HMAC over the complete versioned readiness assertion"`
	KeyVersion                 string    `json:"-" gorm:"type:varchar(64);not null;index:idx_provider_file_readiness_lookup,priority:4;comment:Managed consensus key version signing the readiness assertion"`
	AttestedAt                 time.Time `json:"attested_at" gorm:"not null;comment:Timestamp when all referenced external evidence was jointly attested"`
	ExpiresAt                  time.Time `json:"expires_at" gorm:"not null;index:idx_provider_file_readiness_lookup,priority:5;comment:Hard expiry of this short-lived readiness assertion"`
	CreatedAt                  time.Time `json:"created_at" gorm:"not null;comment:Timestamp when the signed readiness assertion was persisted"`
}

func (evidence ManagedProviderFileReadinessEvidence) Validate() error {
	if !validManagedProviderFileDigest(evidence.TargetFingerprint) || !validManagedProviderFileDigest(evidence.ScopeFingerprint) || !validManagedProviderFileDigest(evidence.CredentialFingerprint) ||
		!validManagedProviderFileDigest(evidence.ProjectEvidenceHMAC) || !validManagedProviderFileDigest(evidence.SandboxEvidenceHMAC) ||
		!validManagedProviderFileDigest(evidence.ImmutableAuditEvidenceHMAC) || !validManagedProviderFileDigest(evidence.DatabaseMatrixEvidenceHMAC) ||
		!validManagedProviderFileDigest(evidence.EvidenceHMAC) || strings.TrimSpace(evidence.KeyVersion) == "" || strings.TrimSpace(evidence.KeyVersion) != evidence.KeyVersion ||
		len(evidence.KeyVersion) > 64 || evidence.ProjectAttestationVersion != ManagedProviderFileProjectAttestationVersion ||
		evidence.SandboxContractVersion != ManagedProviderFileSandboxContractVersion || evidence.ImmutableAuditVersion != ManagedProviderFileImmutableAuditVersion ||
		evidence.DatabaseMatrixVersion != ManagedProviderFileDatabaseMatrixVersion || evidence.AttestedAt.IsZero() || evidence.ExpiresAt.IsZero() ||
		!evidence.ExpiresAt.After(evidence.AttestedAt) || evidence.ExpiresAt.Sub(evidence.AttestedAt) > 24*time.Hour {
		return ErrManagedProviderFileReadinessEvidenceImmutable
	}
	return nil
}

func (evidence *ManagedProviderFileReadinessEvidence) BeforeCreate(_ *gorm.DB) error {
	if evidence == nil || evidence.Id != 0 {
		return ErrManagedProviderFileReadinessEvidenceImmutable
	}
	return evidence.Validate()
}

func (evidence *ManagedProviderFileReadinessEvidence) BeforeUpdate(_ *gorm.DB) error {
	return ErrManagedProviderFileReadinessEvidenceImmutable
}

func (evidence *ManagedProviderFileReadinessEvidence) BeforeDelete(_ *gorm.DB) error {
	return ErrManagedProviderFileReadinessEvidenceImmutable
}

func CreateManagedProviderFileReadinessEvidence(ctx context.Context, evidence *ManagedProviderFileReadinessEvidence) error {
	if ctx == nil || evidence == nil {
		return ErrManagedProviderFileReadinessEvidenceImmutable
	}
	return DB.WithContext(ctx).Create(evidence).Error
}

func GetManagedProviderFileReadinessEvidence(ctx context.Context, targetFingerprint, scopeFingerprint, credentialFingerprint, keyVersion string, now time.Time) (*ManagedProviderFileReadinessEvidence, error) {
	if ctx == nil || !validManagedProviderFileDigest(targetFingerprint) || !validManagedProviderFileDigest(scopeFingerprint) ||
		!validManagedProviderFileDigest(credentialFingerprint) || strings.TrimSpace(keyVersion) == "" || strings.TrimSpace(keyVersion) != keyVersion || now.IsZero() {
		return nil, gorm.ErrRecordNotFound
	}
	var evidence ManagedProviderFileReadinessEvidence
	err := DB.WithContext(ctx).Where(
		"target_fingerprint = ? AND scope_fingerprint = ? AND credential_fingerprint = ? AND key_version = ? AND expires_at > ?",
		targetFingerprint, scopeFingerprint, credentialFingerprint, keyVersion, now.UTC(),
	).Order("id DESC").First(&evidence).Error
	if err != nil {
		return nil, err
	}
	if err := evidence.Validate(); err != nil {
		return nil, err
	}
	return &evidence, nil
}
