package providerfile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasManagedReferencesRejectsRawAndConflictingFileSources(t *testing.T) {
	for _, body := range []string{
		`{"input":[{"type":"input_file","file_id":"file-raw-provider-id"}]}`,
		`{"input":[{"type":"input_file","file_id":"file-managed-invalid"}]}`,
		`{"input":[{"type":"input_file","file_id":"file-managed-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","file_data":"inline"}]}`,
		`{"input":[{"type":"message","content":[{"type":"input_file","file_url":"https://example.test/file"}]}]}`,
	} {
		_, err := HasManagedReferences([]byte(body))
		assert.ErrorIs(t, err, ErrInvalidReference)
	}
}

func TestHasManagedReferencesIgnoresUntypedGenericFileID(t *testing.T) {
	hasReferences, err := HasManagedReferences([]byte(`{"input":[{"type":"custom","file_id":"file-unrelated"}]}`))
	require.NoError(t, err)
	assert.False(t, hasReferences)
}
