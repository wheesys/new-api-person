package model

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type managedProviderFileLegacyReadinessEvidence struct {
	Id                         int64     `gorm:"primaryKey;autoIncrement;comment:Legacy readiness evidence numeric identifier"`
	TargetFingerprint          string    `gorm:"type:varchar(64);not null;index:idx_provider_file_readiness_lookup,priority:1;comment:Legacy target fingerprint"`
	ScopeFingerprint           string    `gorm:"type:varchar(64);not null;index:idx_provider_file_readiness_lookup,priority:2;comment:Legacy scope fingerprint"`
	CredentialFingerprint      string    `gorm:"type:varchar(64);not null;index:idx_provider_file_readiness_lookup,priority:3;comment:Legacy credential fingerprint"`
	ProjectEvidenceHMAC        string    `gorm:"type:varchar(64);not null;comment:Legacy project evidence digest"`
	SandboxEvidenceHMAC        string    `gorm:"type:varchar(64);not null;comment:Legacy sandbox evidence digest"`
	ImmutableAuditEvidenceHMAC string    `gorm:"type:varchar(64);not null;comment:Legacy immutable audit evidence digest"`
	DatabaseMatrixEvidenceHMAC string    `gorm:"type:varchar(64);not null;comment:Legacy database matrix evidence digest"`
	ProjectAttestationVersion  string    `gorm:"type:varchar(64);not null;comment:Legacy project attestation version"`
	SandboxContractVersion     string    `gorm:"type:varchar(64);not null;comment:Legacy sandbox contract version"`
	ImmutableAuditVersion      string    `gorm:"type:varchar(64);not null;comment:Legacy immutable audit version"`
	DatabaseMatrixVersion      string    `gorm:"type:varchar(64);not null;comment:Legacy database matrix version"`
	EvidenceHMAC               string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_provider_file_readiness_evidence_hmac;comment:Legacy evidence digest"`
	KeyVersion                 string    `gorm:"type:varchar(64);not null;index:idx_provider_file_readiness_lookup,priority:4;comment:Legacy key version"`
	AttestedAt                 time.Time `gorm:"not null;comment:Legacy attestation timestamp"`
	ExpiresAt                  time.Time `gorm:"not null;index:idx_provider_file_readiness_lookup,priority:5;comment:Legacy expiry timestamp"`
	CreatedAt                  time.Time `gorm:"not null;comment:Legacy creation timestamp"`
}

func (managedProviderFileLegacyReadinessEvidence) TableName() string {
	return "managed_provider_file_readiness_evidences"
}

func TestManagedProviderFileSchemaMySQL(t *testing.T) {
	runManagedProviderFileExternalDatabaseMatrix(t, "mysql", os.Getenv("TEST_PROVIDER_FILE_MYSQL_DSN"))
}

func TestManagedProviderFileSchemaPostgreSQL(t *testing.T) {
	runManagedProviderFileExternalDatabaseMatrix(t, "postgres", os.Getenv("TEST_PROVIDER_FILE_POSTGRES_DSN"))
}

func TestManagedProviderFileSchemaSQLite(t *testing.T) {
	runManagedProviderFileExternalDatabaseMatrix(t, "sqlite", "file:provider_file_matrix?mode=memory&cache=shared")
}

func runManagedProviderFileExternalDatabaseMatrix(t *testing.T, dialect, dsn string) {
	t.Helper()
	if dsn == "" {
		t.Skipf("set TEST_PROVIDER_FILE_%s_DSN to run the provider file database matrix", dialect)
	}
	var (
		database     *gorm.DB
		databaseType common.DatabaseType
		err          error
	)
	switch dialect {
	case "sqlite":
		databaseType = common.DatabaseTypeSQLite
		database, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	case "mysql":
		databaseType = common.DatabaseTypeMySQL
		database, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	case "postgres":
		databaseType = common.DatabaseTypePostgreSQL
		database, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	default:
		t.Fatalf("unsupported database dialect %q", dialect)
	}
	require.NoError(t, err)

	models := []any{
		&ManagedProviderFileReconciliationCandidate{}, &ManagedProviderFileReconciliationScan{}, &ManagedProviderFileReadinessEvidence{},
		&ManagedProviderFileLifecycleEvent{}, &ManagedProviderFileDeletionOutbox{}, &ManagedProviderFileLifecycle{}, &Channel{},
	}
	for _, modelValue := range models {
		if database.Migrator().HasTable(modelValue) {
			t.Fatalf("refusing to run provider file matrix against non-empty %s schema", dialect)
		}
	}

	originalDatabase := DB
	originalLogDatabase := LOG_DB
	originalMainType := common.MainDatabaseType()
	originalLogType := common.LogDatabaseType()
	DB = database
	LOG_DB = database
	common.SetDatabaseTypes(databaseType, databaseType)
	t.Cleanup(func() {
		for _, modelValue := range models {
			_ = database.Migrator().DropTable(modelValue)
		}
		if sqlDatabase, sqlErr := database.DB(); sqlErr == nil {
			_ = sqlDatabase.Close()
		}
		DB = originalDatabase
		LOG_DB = originalLogDatabase
		common.SetDatabaseTypes(originalMainType, originalLogType)
	})
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, database.AutoMigrate(&managedProviderFileLegacyReadinessEvidence{}))
	legacyReadiness := &managedProviderFileLegacyReadinessEvidence{
		TargetFingerprint: managedProviderFileDigest("a"), ScopeFingerprint: managedProviderFileDigest("b"),
		CredentialFingerprint: managedProviderFileDigest("c"), ProjectEvidenceHMAC: managedProviderFileDigest("d"),
		SandboxEvidenceHMAC: managedProviderFileDigest("e"), ImmutableAuditEvidenceHMAC: managedProviderFileDigest("f"),
		DatabaseMatrixEvidenceHMAC: managedProviderFileDigest("9"), ProjectAttestationVersion: ManagedProviderFileProjectAttestationVersion,
		SandboxContractVersion: ManagedProviderFileSandboxContractVersion, ImmutableAuditVersion: ManagedProviderFileImmutableAuditVersion,
		DatabaseMatrixVersion: ManagedProviderFileDatabaseMatrixVersion, EvidenceHMAC: managedProviderFileDigest("5"), KeyVersion: "legacy-v1",
		AttestedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour),
	}
	require.NoError(t, database.Create(legacyReadiness).Error)

	require.NoError(t, database.AutoMigrate(
		&Channel{}, &ManagedProviderFileLifecycle{}, &ManagedProviderFileDeletionOutbox{}, &ManagedProviderFileLifecycleEvent{},
		&ManagedProviderFileReadinessEvidence{}, &ManagedProviderFileReconciliationScan{}, &ManagedProviderFileReconciliationCandidate{},
	))
	assert.True(t, database.Migrator().HasColumn(&Channel{}, "OpenAIProject"))
	assert.True(t, database.Migrator().HasColumn(&ManagedProviderFileLifecycle{}, "TargetProviderLookupHMAC"))
	var upgradedLegacyReadiness ManagedProviderFileReadinessEvidence
	require.NoError(t, database.First(&upgradedLegacyReadiness, "id = ?", legacyReadiness.Id).Error)
	assert.Empty(t, upgradedLegacyReadiness.MaintenancePolicyFingerprint)
	assert.Empty(t, upgradedLegacyReadiness.UploadPolicyFingerprint)
	assert.Empty(t, upgradedLegacyReadiness.PolicyVersion)
	assert.ErrorIs(t, upgradedLegacyReadiness.Validate(), ErrManagedProviderFileReadinessEvidenceImmutable)

	readiness := &ManagedProviderFileReadinessEvidence{
		TargetFingerprint: managedProviderFileDigest("a"), ScopeFingerprint: managedProviderFileDigest("b"),
		CredentialFingerprint: managedProviderFileDigest("c"), MaintenancePolicyFingerprint: managedProviderFileDigest("7"),
		UploadPolicyFingerprint: managedProviderFileDigest("6"), ProjectEvidenceHMAC: managedProviderFileDigest("d"),
		SandboxEvidenceHMAC: managedProviderFileDigest("e"), ImmutableAuditEvidenceHMAC: managedProviderFileDigest("f"),
		DatabaseMatrixEvidenceHMAC: managedProviderFileDigest("9"), ProjectAttestationVersion: ManagedProviderFileProjectAttestationVersion,
		SandboxContractVersion: ManagedProviderFileSandboxContractVersion, ImmutableAuditVersion: ManagedProviderFileImmutableAuditVersion,
		DatabaseMatrixVersion: ManagedProviderFileDatabaseMatrixVersion, PolicyVersion: ManagedProviderFilePolicyVersion,
		EvidenceHMAC: managedProviderFileDigest("8"), KeyVersion: "matrix-v1",
		AttestedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
	require.NoError(t, CreateManagedProviderFileReadinessEvidence(context.Background(), readiness))
	loadedReadiness, err := GetManagedProviderFileReadinessEvidence(context.Background(), readiness.TargetFingerprint,
		readiness.ScopeFingerprint, readiness.CredentialFingerprint, readiness.MaintenancePolicyFingerprint, readiness.KeyVersion, now)
	require.NoError(t, err)
	assert.Equal(t, readiness.CredentialFingerprint, loadedReadiness.CredentialFingerprint)

	scan := &ManagedProviderFileReconciliationScan{
		TargetFingerprint: managedProviderFileDigest("1"), ScopeFingerprint: managedProviderFileDigest("2"), KeyVersion: "matrix-v1",
		State: ManagedProviderFileReconciliationScanStateScanning, Version: 1, StartedAt: now, CutoffAt: now.Add(-time.Minute),
	}
	require.NoError(t, CreateManagedProviderFileReconciliationScan(context.Background(), scan))
	observation := ManagedProviderFileReconciliationObservation{
		TargetFingerprint: scan.TargetFingerprint, TargetProviderLookupHMAC: managedProviderFileDigest("3"), MetadataHMAC: managedProviderFileDigest("4"),
		ProviderPayload: []byte("encrypted-candidate"), PayloadKeyVersion: "matrix-v1", State: ManagedProviderFileReconciliationCandidateQuarantined,
		ProviderBytes: 1, ProviderCreatedAt: now.Add(-time.Hour), QuarantineUntil: common.GetPointer(now.Add(24 * time.Hour)),
	}
	require.NoError(t, FinishManagedProviderFileReconciliationScan(context.Background(), scan, ManagedProviderFileReconciliationScanStateComplete,
		"", "", 1, 1, []ManagedProviderFileReconciliationObservation{observation}, now.Add(time.Second)))
	assert.ErrorIs(t, FinishManagedProviderFileReconciliationScan(context.Background(), scan, ManagedProviderFileReconciliationScanStateComplete,
		"", "", 1, 1, []ManagedProviderFileReconciliationObservation{observation}, now.Add(2*time.Second)), ErrManagedProviderFileReconciliationConflict)

	var candidate ManagedProviderFileReconciliationCandidate
	require.NoError(t, database.First(&candidate).Error)
	assert.Equal(t, 1, candidate.CompleteObservationCount)
	assert.Equal(t, ManagedProviderFileReconciliationCandidateQuarantined, candidate.State)
}
