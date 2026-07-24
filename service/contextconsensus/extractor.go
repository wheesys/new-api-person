package contextconsensus

import (
	"fmt"

	"github.com/QuantumNous/new-api/relaykit/types"
)

func Extract(request ExtractionRequest) (*ContextEnvelope, error) {
	if len(request.Body) == 0 {
		return nil, fmt.Errorf("context envelope request body is empty")
	}

	var envelope *ContextEnvelope
	var err error
	switch request.Protocol {
	case types.RelayFormatOpenAI:
		envelope, err = extractChatCompletions(request)
	case types.RelayFormatOpenAIResponses:
		envelope, err = extractOpenAIResponses(request)
	case types.RelayFormatClaude:
		envelope, err = extractClaudeMessages(request)
	case types.RelayFormatGemini:
		envelope, err = extractGemini(request)
	default:
		return nil, fmt.Errorf("unsupported context envelope protocol %q", request.Protocol)
	}
	if envelope != nil {
		envelope.SourceDigest = digestBytes(request.Body)
		if envelope.OriginalModel == "" {
			envelope.OriginalModel = request.OriginalModel
		}
		if envelope.ProviderBinding.BindingLevel == "" {
			envelope.ProviderBinding.BindingLevel = BindingLevelNone
		}
		envelope.ProviderBinding.RelayFormat = request.Protocol
	}
	return envelope, err
}
