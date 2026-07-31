package contextconsensus

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"unicode"
)

const managedProviderFileHandlePrefix = "file-managed-"

type ManagedProviderFileReferencePayload struct {
	ProviderFileID string `json:"provider_file_id"`
	Filename       string `json:"filename"`
}

func (payload ManagedProviderFileReferencePayload) Validate() error {
	if strings.TrimSpace(payload.ProviderFileID) != payload.ProviderFileID || !strings.HasPrefix(payload.ProviderFileID, "file-") ||
		len(payload.ProviderFileID) <= len("file-") || len(payload.ProviderFileID) > 512 ||
		strings.IndexFunc(strings.TrimPrefix(payload.ProviderFileID, "file-"), func(character rune) bool {
			return !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
				!(character >= '0' && character <= '9') && character != '-' && character != '_'
		}) >= 0 {
		return fmt.Errorf("managed provider file reference is invalid")
	}
	if strings.TrimSpace(payload.Filename) == "" || strings.TrimSpace(payload.Filename) != payload.Filename || len(payload.Filename) > 1024 ||
		strings.IndexFunc(payload.Filename, unicode.IsControl) >= 0 {
		return fmt.Errorf("managed provider file filename is invalid")
	}
	return nil
}

func (payload ManagedProviderFileReferencePayload) String() string {
	return "ManagedProviderFileReferencePayload{ProviderFileID:***masked***,Filename:***masked***}"
}

func (payload ManagedProviderFileReferencePayload) GoString() string {
	return payload.String()
}

func GenerateManagedProviderFileHandle() (string, error) {
	return generateManagedProviderFileHandle(rand.Reader)
}

func generateManagedProviderFileHandle(random io.Reader) (string, error) {
	if random == nil {
		return "", fmt.Errorf("managed provider file handle random source is required")
	}
	randomBytes := make([]byte, 32)
	if _, err := io.ReadFull(random, randomBytes); err != nil {
		return "", fmt.Errorf("generate managed provider file handle: %w", err)
	}
	return managedProviderFileHandlePrefix + base64.RawURLEncoding.EncodeToString(randomBytes), nil
}
