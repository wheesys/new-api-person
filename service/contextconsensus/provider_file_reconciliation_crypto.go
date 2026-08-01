package contextconsensus

import (
	"fmt"
	"strings"
	"unicode"
)

type ManagedProviderFileReconciliationPayload struct {
	ProviderFileID string `json:"provider_file_id"`
	Filename       string `json:"filename"`
}

func (payload ManagedProviderFileReconciliationPayload) Validate() error {
	if strings.TrimSpace(payload.ProviderFileID) != payload.ProviderFileID || !strings.HasPrefix(payload.ProviderFileID, "file-") ||
		len(payload.ProviderFileID) <= len("file-") || len(payload.ProviderFileID) > 512 ||
		strings.IndexFunc(strings.TrimPrefix(payload.ProviderFileID, "file-"), func(character rune) bool {
			return !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
				!(character >= '0' && character <= '9') && character != '-' && character != '_'
		}) >= 0 || strings.TrimSpace(payload.Filename) == "" || strings.TrimSpace(payload.Filename) != payload.Filename ||
		len(payload.Filename) > 255 || strings.IndexFunc(payload.Filename, unicode.IsControl) >= 0 {
		return fmt.Errorf("managed provider file reconciliation payload is invalid")
	}
	return nil
}

func (payload ManagedProviderFileReconciliationPayload) String() string {
	return "ManagedProviderFileReconciliationPayload{ProviderFileID:***masked***,Filename:***masked***}"
}

func (payload ManagedProviderFileReconciliationPayload) GoString() string {
	return payload.String()
}
