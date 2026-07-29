package contextconsensus

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type InjectManagedConsensusRequest struct {
	Protocol types.RelayFormat
	Body     []byte
	State    ManagedConsensusState
}

// InjectManagedConsensus adds an authenticated managed L2/L3 summary as
// untrusted user-level data. It never removes or rewrites client-provided
// instructions, turns, tools, schemas, media, or provider state.
func InjectManagedConsensus(request InjectManagedConsensusRequest) ([]byte, error) {
	if err := request.State.Validate(); err != nil {
		return nil, err
	}
	summaryJSON, err := common.Marshal(request.State.TaskConsensus)
	if err != nil {
		return nil, fmt.Errorf("encode managed consensus summary: %w", err)
	}
	summaryText := consensusSummaryPreamble + string(summaryJSON)

	switch request.Protocol {
	case types.RelayFormatOpenAI:
		return injectManagedChat(request.Body, summaryText)
	case types.RelayFormatOpenAIResponses:
		return injectManagedResponses(request.Body, summaryText)
	case types.RelayFormatClaude:
		return injectManagedClaude(request.Body, summaryText)
	case types.RelayFormatGemini:
		return injectManagedGemini(request.Body, summaryText)
	default:
		return nil, fmt.Errorf("unsupported managed consensus protocol %q", request.Protocol)
	}
}

func injectManagedChat(body []byte, summaryText string) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode managed Chat request: %w", err)
	}
	var messages []map[string]json.RawMessage
	if err := common.Unmarshal(request["messages"], &messages); err != nil {
		return nil, fmt.Errorf("decode managed Chat messages: %w", err)
	}
	userIndex := firstManagedUserIndex(len(messages), func(index int) string {
		role, _ := managedRawString(messages[index]["role"])
		return role
	})
	if userIndex < 0 {
		return nil, fmt.Errorf("managed Chat request requires a user message")
	}
	summaryMessage, err := common.Marshal(map[string]any{"role": "user", "content": summaryText})
	if err != nil {
		return nil, fmt.Errorf("encode managed Chat summary: %w", err)
	}
	var encodedSummary map[string]json.RawMessage
	if err := common.Unmarshal(summaryMessage, &encodedSummary); err != nil {
		return nil, fmt.Errorf("decode managed Chat summary: %w", err)
	}
	rewritten := make([]map[string]json.RawMessage, 0, len(messages)+1)
	rewritten = append(rewritten, messages[:userIndex]...)
	rewritten = append(rewritten, encodedSummary)
	rewritten = append(rewritten, messages[userIndex:]...)
	encodedMessages, err := common.Marshal(rewritten)
	if err != nil {
		return nil, fmt.Errorf("encode managed Chat messages: %w", err)
	}
	request["messages"] = encodedMessages
	return marshalManagedRequest(request)
}

func injectManagedResponses(body []byte, summaryText string) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode managed Responses request: %w", err)
	}
	summaryItem := map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": summaryText}},
	}
	switch common.GetJsonType(request["input"]) {
	case "string":
		var currentInput string
		if err := common.Unmarshal(request["input"], &currentInput); err != nil {
			return nil, fmt.Errorf("decode managed Responses string input: %w", err)
		}
		input, err := common.Marshal([]any{
			summaryItem,
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": currentInput}}},
		})
		if err != nil {
			return nil, fmt.Errorf("encode managed Responses input: %w", err)
		}
		request["input"] = input
	case "array":
		var inputItems []json.RawMessage
		if err := common.Unmarshal(request["input"], &inputItems); err != nil {
			return nil, fmt.Errorf("decode managed Responses input items: %w", err)
		}
		userIndex := firstManagedUserIndex(len(inputItems), func(index int) string {
			var item map[string]json.RawMessage
			if err := common.Unmarshal(inputItems[index], &item); err != nil {
				return ""
			}
			role, _ := managedRawString(item["role"])
			return role
		})
		if userIndex < 0 {
			return nil, fmt.Errorf("managed Responses request requires a user input item")
		}
		encodedSummary, err := common.Marshal(summaryItem)
		if err != nil {
			return nil, fmt.Errorf("encode managed Responses summary item: %w", err)
		}
		rewritten := make([]json.RawMessage, 0, len(inputItems)+1)
		rewritten = append(rewritten, inputItems[:userIndex]...)
		rewritten = append(rewritten, encodedSummary)
		rewritten = append(rewritten, inputItems[userIndex:]...)
		input, err := common.Marshal(rewritten)
		if err != nil {
			return nil, fmt.Errorf("encode managed Responses input items: %w", err)
		}
		request["input"] = input
	default:
		return nil, fmt.Errorf("managed Responses request requires string or array input")
	}
	return marshalManagedRequest(request)
}

func injectManagedClaude(body []byte, summaryText string) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode managed Claude request: %w", err)
	}
	var messages []map[string]json.RawMessage
	if err := common.Unmarshal(request["messages"], &messages); err != nil {
		return nil, fmt.Errorf("decode managed Claude messages: %w", err)
	}
	userIndex := firstManagedUserIndex(len(messages), func(index int) string {
		role, _ := managedRawString(messages[index]["role"])
		return role
	})
	if userIndex < 0 {
		return nil, fmt.Errorf("managed Claude request requires a user message")
	}
	message := messages[userIndex]
	summaryBlock := map[string]any{"type": "text", "text": summaryText}
	var contents []any
	if common.GetJsonType(message["content"]) == "string" {
		var currentText string
		if err := common.Unmarshal(message["content"], &currentText); err != nil {
			return nil, fmt.Errorf("decode managed Claude user text: %w", err)
		}
		contents = []any{summaryBlock, map[string]any{"type": "text", "text": currentText}}
	} else if common.GetJsonType(message["content"]) == "array" {
		var originalContents []json.RawMessage
		if err := common.Unmarshal(message["content"], &originalContents); err != nil {
			return nil, fmt.Errorf("decode managed Claude user content: %w", err)
		}
		contents = make([]any, 0, len(originalContents)+1)
		contents = append(contents, summaryBlock)
		for _, content := range originalContents {
			contents = append(contents, content)
		}
	} else {
		return nil, fmt.Errorf("managed Claude user content must be text or an array")
	}
	encodedContents, err := common.Marshal(contents)
	if err != nil {
		return nil, fmt.Errorf("encode managed Claude user content: %w", err)
	}
	message["content"] = encodedContents
	messages[userIndex] = message
	encodedMessages, err := common.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("encode managed Claude messages: %w", err)
	}
	request["messages"] = encodedMessages
	return marshalManagedRequest(request)
}

func injectManagedGemini(body []byte, summaryText string) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode managed Gemini request: %w", err)
	}
	var contents []map[string]json.RawMessage
	if err := common.Unmarshal(request["contents"], &contents); err != nil {
		return nil, fmt.Errorf("decode managed Gemini contents: %w", err)
	}
	userIndex := firstManagedUserIndex(len(contents), func(index int) string {
		role, _ := managedRawString(contents[index]["role"])
		return role
	})
	if userIndex < 0 {
		return nil, fmt.Errorf("managed Gemini request requires user content")
	}
	var parts []json.RawMessage
	if err := common.Unmarshal(contents[userIndex]["parts"], &parts); err != nil {
		return nil, fmt.Errorf("decode managed Gemini parts: %w", err)
	}
	summaryPart, err := common.Marshal(map[string]any{"text": summaryText})
	if err != nil {
		return nil, fmt.Errorf("encode managed Gemini summary: %w", err)
	}
	parts = append([]json.RawMessage{summaryPart}, parts...)
	encodedParts, err := common.Marshal(parts)
	if err != nil {
		return nil, fmt.Errorf("encode managed Gemini parts: %w", err)
	}
	contents[userIndex]["parts"] = encodedParts
	encodedContents, err := common.Marshal(contents)
	if err != nil {
		return nil, fmt.Errorf("encode managed Gemini contents: %w", err)
	}
	request["contents"] = encodedContents
	return marshalManagedRequest(request)
}

func firstManagedUserIndex(length int, roleAt func(int) string) int {
	for index := 0; index < length; index++ {
		if strings.EqualFold(strings.TrimSpace(roleAt(index)), "user") {
			return index
		}
	}
	return -1
}

func marshalManagedRequest(request any) ([]byte, error) {
	rewritten, err := common.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode managed consensus request: %w", err)
	}
	return rewritten, nil
}
