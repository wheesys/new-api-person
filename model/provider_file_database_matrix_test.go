package model

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestManagedProviderFileSchemaMySQL(t *testing.T) {
	runManagedProviderFileExternalDatabaseMatrix(t, "mysql", os.Getenv("TEST_PROVIDER_FILE_MYSQL_DSN"))
}

func TestManagedProviderFileSchemaPostgreSQL(t *testing.T) {
	runManagedProviderFileExternalDatabaseMatrix(t, "postgres", os.Getenv("TEST_PROVIDER_FILE_POSTGRES_DSN"))
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

	require.NoError(t, database.AutoMigrate(
		&Channel{}, &ManagedProviderFileLifecycle{}, &ManagedProviderFileDeletionOutbox{}, &ManagedProviderFileLifecycleEvent{},
		&ManagedProviderFileReadinessEvidence{}, &ManagedProviderFileReconciliationScan{}, &ManagedProviderFileReconciliationCandidate{},
	))
	assert.True(t, database.Migrator().HasColumn(&Channel{}, "OpenAIProject"))
	assert.True(t, database.Migrator().HasColumn(&ManagedProviderFileLifecycle{}, "TargetProviderLookupHMAC"))

	now := time.Now().UTC().Truncate(time.Second)
	readiness := &ManagedProviderFileReadinessEvidence{
		TargetFingerprint: managedProviderFileDigest("a"), ScopeFingerprint: managedProviderFileDigest("b"),
		CredentialFingerprint: managedProviderFileDigest("c"), ProjectEvidenceHMAC: managedProviderFileDigest("d"),
		SandboxEvidenceHMAC: managedProviderFileDigest("e"), ImmutableAuditEvidenceHMAC: managedProviderFileDigest("f"),
		DatabaseMatrixEvidenceHMAC: managedProviderFileDigest("9"), ProjectAttestationVersion: ManagedProviderFileProjectAttestationVersion,
		SandboxContractVersion: ManagedProviderFileSandboxContractVersion, ImmutableAuditVersion: ManagedProviderFileImmutableAuditVersion,
		DatabaseMatrixVersion: ManagedProviderFileDatabaseMatrixVersion, EvidenceHMAC: managedProviderFileDigest("8"), KeyVersion: "matrix-v1",
		AttestedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
	require.NoError(t, CreateManagedProviderFileReadinessEvidence(context.Background(), readiness))
	loadedReadiness, err := GetManagedProviderFileReadinessEvidence(context.Background(), readiness.TargetFingerprint,
		readiness.ScopeFingerprint, readiness.CredentialFingerprint, readiness.KeyVersion, now)
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
