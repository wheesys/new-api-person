package contextconsensus

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

func digestBytes(value []byte) string {
	return hex.EncodeToString(common.Sha256Raw(value))
}

func digestString(value string) string {
	return digestBytes([]byte(value))
}

func digestValue(value any) string {
	encoded, err := common.Marshal(value)
	if err != nil {
		return ""
	}
	return digestBytes(encoded)
}

func rawJSONPresent(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed != "" && trimmed != "null"
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func bindingRank(level BindingLevel) int {
	switch level {
	case BindingLevelModelFamily:
		return 1
	case BindingLevelProvider:
		return 2
	case BindingLevelChannel:
		return 3
	case BindingLevelCredential:
		return 4
	default:
		return 0
	}
}

func requireBinding(binding *ProtocolBinding, level BindingLevel, reasonCode string, stateReference string) {
	if bindingRank(level) > bindingRank(binding.BindingLevel) {
		binding.BindingLevel = level
	}
	binding.ReasonCodes = appendUnique(binding.ReasonCodes, reasonCode)
	if stateReference != "" {
		binding.StateReferenceHashes = appendUnique(binding.StateReferenceHashes, digestString(stateReference))
	}
}
