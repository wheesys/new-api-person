package model

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestManagedProviderFileSchemaMySQL(t *testing.T) {
	runManagedProviderFileDatabaseMatrix(t, "mysql", os.Getenv("TEST_PROVIDER_FILE_MYSQL_DSN"))
}

func TestManagedProviderFileSchemaPostgreSQL(t *testing.T) {
	runManagedProviderFileDatabaseMatrix(t, "postgres", os.Getenv("TEST_PROVIDER_FILE_POSTGRES_DSN"))
}

func TestManagedProviderFileSchemaSQLite(t *testing.T) {
	runManagedProviderFileDatabaseMatrix(t, "sqlite", "file:provider_file_matrix?mode=memory&cache=shared")
}

func runManagedProviderFileDatabaseMatrix(t *testing.T, dialect, dsn string) {
	t.Helper()
	if dsn == "" {
		t.Skipf("set the %s provider file test DSN to run this database test", dialect)
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
		&ManagedProviderFileLifecycleEvent{},
		&ManagedProviderFileDeletionOutbox{},
		&ManagedProviderFileLifecycle{},
		&Channel{},
	}
	for _, modelValue := range models {
		if database.Migrator().HasTable(modelValue) {
			t.Fatalf("refusing to run provider file tests against a non-empty %s schema", dialect)
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

	require.NoError(t, database.AutoMigrate(models...))
	for _, modelValue := range models {
		assert.True(t, database.Migrator().HasTable(modelValue))
	}
	assert.True(t, database.Migrator().HasColumn(&Channel{}, "OpenAIProject"))
	assert.True(t, database.Migrator().HasColumn(&ManagedProviderFileLifecycle{}, "EndpointFingerprint"))
}
