package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileSourceIdentifierDoesNotExposeRawData(t *testing.T) {
	urlSource := NewURLFileSource("https://user:password@example.invalid/private/file.pdf?token=secret-token")
	base64Source := NewBase64FileSource("sensitive-base64-prefix-and-payload", "application/pdf")

	assert.Equal(t, "remote-url", urlSource.GetIdentifier())
	assert.NotContains(t, urlSource.GetIdentifier(), "password")
	assert.NotContains(t, urlSource.GetIdentifier(), "secret-token")
	assert.Equal(t, "inline-base64", base64Source.GetIdentifier())
	assert.NotContains(t, base64Source.GetIdentifier(), "sensitive-base64-prefix")
}
