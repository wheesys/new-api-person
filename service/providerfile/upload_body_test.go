package providerfile

import (
	"bytes"
	"io"
	"mime/multipart"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func managedProviderFileMultipart(t *testing.T, purpose, filename string, content []byte, extraField string) (common.BodyStorage, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	require.NoError(t, writer.WriteField("purpose", purpose))
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	if extraField != "" {
		require.NoError(t, writer.WriteField(extraField, "forbidden"))
	}
	require.NoError(t, writer.Close())
	storage, err := common.CreateBodyStorage(buffer.Bytes())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	return storage, writer.FormDataContentType()
}

func TestParseUploadBodyValidatesAndReopensSingleFile(t *testing.T) {
	storage, contentType := managedProviderFileMultipart(t, "user_data", "facts.txt", []byte("bounded content"), "")
	body, err := ParseUploadBody(storage, contentType)
	require.NoError(t, err)
	assert.Equal(t, "facts.txt", body.Filename)
	assert.Equal(t, int64(len("bounded content")), body.SizeBytes)
	assert.Len(t, body.ContentDigest, 64)
	reader, err := body.Open()
	require.NoError(t, err)
	defer reader.Close()
	readContent, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, []byte("bounded content"), readContent)
}

func TestParseUploadBodyRejectsUnsupportedFieldsAndPurpose(t *testing.T) {
	storage, contentType := managedProviderFileMultipart(t, "assistants", "facts.txt", []byte("content"), "")
	_, err := ParseUploadBody(storage, contentType)
	assert.ErrorIs(t, err, ErrInvalidUploadBody)

	storage, contentType = managedProviderFileMultipart(t, "user_data", "facts.txt", []byte("content"), "expires_after")
	_, err = ParseUploadBody(storage, contentType)
	assert.ErrorIs(t, err, ErrInvalidUploadBody)
}
