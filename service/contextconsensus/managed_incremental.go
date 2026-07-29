package contextconsensus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// PrepareManagedIncrementalRequest freezes the first managed client contract:
// one current user turn plus portable instructions, tools, schemas, and media.
// Historical assistant/tool/function state and provider-owned opaque state are
// rejected before any stored consensus is injected.
func PrepareManagedIncrementalRequest(protocol types.RelayFormat, body []byte, state *ManagedConsensusState) ([]byte, error) {
	consensusMarker := strings.TrimSpace(consensusSummaryPreamble)
	if bytes.Contains(body, []byte(consensusMarker)) {
		return nil, fmt.Errorf("managed request contains the reserved consensus marker")
	}
	if err := ValidateManagedIncrementalRequest(protocol, body); err != nil {
		return nil, err
	}
	if state == nil {
		return append([]byte(nil), body...), nil
	}
	rewritten, err := InjectManagedConsensus(InjectManagedConsensusRequest{Protocol: protocol, Body: body, State: *state})
	if err != nil {
		return nil, err
	}
	if bytes.Count(rewritten, []byte(consensusMarker)) != 1 {
		return nil, fmt.Errorf("managed request must contain exactly one authenticated consensus summary")
	}
	return rewritten, nil
}

func ValidateManagedIncrementalRequest(protocol types.RelayFormat, body []byte) error {
	switch protocol {
	case types.RelayFormatOpenAI:
		return validateManagedChatIncrement(body)
	case types.RelayFormatOpenAIResponses:
		return validateManagedResponsesIncrement(body)
	case types.RelayFormatClaude:
		return validateManagedClaudeIncrement(body)
	case types.RelayFormatGemini:
		return validateManagedGeminiIncrement(body)
	default:
		return fmt.Errorf("unsupported managed consensus protocol %q", protocol)
	}
}

func DigestManagedIncrementalRequest(body []byte) string {
	return digestBytes(body)
}

func validateManagedChatIncrement(body []byte) error {
	var request struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := common.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("decode managed Chat request: %w", err)
	}
	userTurns := 0
	seenUser := false
	for _, encodedMessage := range request.Messages {
		var message map[string]json.RawMessage
		if err := common.Unmarshal(encodedMessage, &message); err != nil {
			return fmt.Errorf("decode managed Chat message: %w", err)
		}
		role, err := managedRawString(message["role"])
		if err != nil {
			return fmt.Errorf("managed Chat message role: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "system", "developer":
			if seenUser {
				return fmt.Errorf("managed Chat instructions must precede the current user turn")
			}
		case "user":
			userTurns++
			seenUser = true
			if managedRawPresent(message["tool_calls"]) || managedRawPresent(message["function_call"]) ||
				managedRawPresent(message["tool_call_id"]) || managedRawPresent(message["reasoning"]) ||
				managedRawPresent(message["reasoning_content"]) {
				return fmt.Errorf("managed Chat current user turn contains historical or opaque state")
			}
			if err := validateManagedChatUserContent(message["content"]); err != nil {
				return err
			}
		default:
			return fmt.Errorf("managed Chat request contains historical role %q", role)
		}
	}
	if userTurns != 1 {
		return fmt.Errorf("managed Chat request requires exactly one current user turn")
	}
	return nil
}

func validateManagedChatUserContent(content json.RawMessage) error {
	if common.GetJsonType(content) == "string" {
		text, err := managedRawString(content)
		if err != nil || strings.TrimSpace(text) == "" {
			return fmt.Errorf("managed Chat current user content is empty")
		}
		return nil
	}
	if common.GetJsonType(content) != "array" {
		return fmt.Errorf("managed Chat current user content must be text or a content array")
	}
	var parts []map[string]json.RawMessage
	if err := common.Unmarshal(content, &parts); err != nil {
		return fmt.Errorf("decode managed Chat user content: %w", err)
	}
	if len(parts) == 0 {
		return fmt.Errorf("managed Chat current user content is empty")
	}
	for _, part := range parts {
		partType, err := managedRawString(part["type"])
		if err != nil {
			return fmt.Errorf("managed Chat user content type: %w", err)
		}
		switch partType {
		case "text", "image_url", "input_audio", "file", "video_url":
		default:
			return fmt.Errorf("managed Chat current user content contains unsupported type %q", partType)
		}
		if partType != "file" {
			continue
		}
		var file map[string]json.RawMessage
		if err := common.Unmarshal(part["file"], &file); err != nil {
			return fmt.Errorf("decode managed Chat file content: %w", err)
		}
		if managedRawPresent(file["file_id"]) {
			return fmt.Errorf("managed Chat current user turn contains a provider-owned file ID")
		}
	}
	return nil
}

func validateManagedResponsesIncrement(body []byte) error {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("decode managed Responses request: %w", err)
	}
	for _, field := range []string{"conversation", "previous_response_id", "context_management", "prompt"} {
		if managedRawPresent(request[field]) {
			return fmt.Errorf("managed Responses request contains provider-owned field %q", field)
		}
	}
	input := request["input"]
	switch common.GetJsonType(input) {
	case "string":
		inputText, err := managedRawString(input)
		if err != nil || strings.TrimSpace(inputText) == "" {
			return fmt.Errorf("managed Responses current user input is empty")
		}
		return nil
	case "array":
		var items []json.RawMessage
		if err := common.Unmarshal(input, &items); err != nil {
			return fmt.Errorf("decode managed Responses input: %w", err)
		}
		userTurns := 0
		seenUser := false
		for _, encodedItem := range items {
			var item map[string]json.RawMessage
			if err := common.Unmarshal(encodedItem, &item); err != nil {
				return fmt.Errorf("decode managed Responses input item: %w", err)
			}
			itemType, err := managedRawString(item["type"])
			if err != nil {
				return fmt.Errorf("managed Responses input item type: %w", err)
			}
			if itemType != "" && itemType != "message" {
				return fmt.Errorf("managed Responses request contains historical input type %q", itemType)
			}
			role, err := managedRawString(item["role"])
			if err != nil {
				return fmt.Errorf("managed Responses input role: %w", err)
			}
			switch strings.ToLower(strings.TrimSpace(role)) {
			case "system", "developer":
				if seenUser {
					return fmt.Errorf("managed Responses instructions must precede the current user turn")
				}
			case "user":
				userTurns++
				seenUser = true
				if err := validateManagedResponsesUserContent(item["content"]); err != nil {
					return err
				}
			default:
				return fmt.Errorf("managed Responses request contains historical role %q", role)
			}
		}
		if userTurns != 1 {
			return fmt.Errorf("managed Responses request requires exactly one current user turn")
		}
		return nil
	default:
		return fmt.Errorf("managed Responses request requires string or array input")
	}
}

func validateManagedResponsesUserContent(content json.RawMessage) error {
	if common.GetJsonType(content) == "string" {
		text, err := managedRawString(content)
		if err != nil || strings.TrimSpace(text) == "" {
			return fmt.Errorf("managed Responses current user content is empty")
		}
		return nil
	}
	if common.GetJsonType(content) != "array" {
		return fmt.Errorf("managed Responses current user content must be text or an input content array")
	}
	var parts []map[string]json.RawMessage
	if err := common.Unmarshal(content, &parts); err != nil {
		return fmt.Errorf("decode managed Responses user content: %w", err)
	}
	if len(parts) == 0 {
		return fmt.Errorf("managed Responses current user content is empty")
	}
	for _, part := range parts {
		partType, err := managedRawString(part["type"])
		if err != nil {
			return fmt.Errorf("managed Responses user content type: %w", err)
		}
		switch partType {
		case "input_text", "input_image", "input_file", "input_audio":
		default:
			return fmt.Errorf("managed Responses current user turn contains historical content type %q", partType)
		}
		if partType == "input_file" && managedRawPresent(part["file_id"]) {
			return fmt.Errorf("managed Responses current user turn contains a provider-owned file ID")
		}
	}
	return nil
}

func validateManagedClaudeIncrement(body []byte) error {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("decode managed Claude request: %w", err)
	}
	for _, field := range []string{"container", "context_management", "mcp_servers"} {
		if managedRawPresent(request[field]) {
			return fmt.Errorf("managed Claude request contains provider-owned field %q", field)
		}
	}
	var messages []map[string]json.RawMessage
	if err := common.Unmarshal(request["messages"], &messages); err != nil {
		return fmt.Errorf("decode managed Claude messages: %w", err)
	}
	userTurns := 0
	for _, message := range messages {
		role, err := managedRawString(message["role"])
		if err != nil {
			return fmt.Errorf("managed Claude message role: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(role)) != "user" {
			return fmt.Errorf("managed Claude request contains historical role %q", role)
		}
		userTurns++
		if err := validateManagedClaudeUserContent(message["content"]); err != nil {
			return err
		}
	}
	if userTurns != 1 {
		return fmt.Errorf("managed Claude request requires exactly one current user turn")
	}
	return nil
}

func validateManagedClaudeUserContent(content json.RawMessage) error {
	if common.GetJsonType(content) == "string" {
		text, err := managedRawString(content)
		if err != nil || strings.TrimSpace(text) == "" {
			return fmt.Errorf("managed Claude current user content is empty")
		}
		return nil
	}
	if common.GetJsonType(content) != "array" {
		return fmt.Errorf("managed Claude current user content must be text or a content array")
	}
	var blocks []map[string]json.RawMessage
	if err := common.Unmarshal(content, &blocks); err != nil {
		return fmt.Errorf("decode managed Claude user content: %w", err)
	}
	if len(blocks) == 0 {
		return fmt.Errorf("managed Claude current user content is empty")
	}
	for _, block := range blocks {
		blockType, err := managedRawString(block["type"])
		if err != nil {
			return fmt.Errorf("managed Claude user block type: %w", err)
		}
		switch blockType {
		case "text", "image", "document":
		default:
			return fmt.Errorf("managed Claude current user turn contains historical or opaque block %q", blockType)
		}
		if managedRawPresent(block["signature"]) || managedRawPresent(block["thinking"]) {
			return fmt.Errorf("managed Claude current user turn contains opaque state")
		}
	}
	return nil
}

func validateManagedGeminiIncrement(body []byte) error {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("decode managed Gemini request: %w", err)
	}
	if managedRawPresent(request["cachedContent"]) || managedRawPresent(request["cached_content"]) {
		return fmt.Errorf("managed Gemini request contains provider-owned cached content")
	}
	var contents []map[string]json.RawMessage
	if err := common.Unmarshal(request["contents"], &contents); err != nil {
		return fmt.Errorf("decode managed Gemini contents: %w", err)
	}
	userTurns := 0
	for _, content := range contents {
		role, err := managedRawString(content["role"])
		if err != nil {
			return fmt.Errorf("managed Gemini content role: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(role)) != "user" {
			return fmt.Errorf("managed Gemini request contains historical role %q", role)
		}
		userTurns++
		var parts []map[string]json.RawMessage
		if err := common.Unmarshal(content["parts"], &parts); err != nil {
			return fmt.Errorf("decode managed Gemini parts: %w", err)
		}
		if len(parts) == 0 {
			return fmt.Errorf("managed Gemini current user content is empty")
		}
		for _, part := range parts {
			for _, field := range []string{"functionCall", "function_call", "functionResponse", "function_response", "thoughtSignature", "thought_signature", "executableCode", "codeExecutionResult"} {
				if managedRawPresent(part[field]) {
					return fmt.Errorf("managed Gemini current user turn contains historical or opaque field %q", field)
				}
			}
			thought, err := managedRawBool(part["thought"])
			if err != nil {
				return fmt.Errorf("managed Gemini thought flag: %w", err)
			}
			if thought {
				return fmt.Errorf("managed Gemini current user turn contains opaque thought state")
			}
			portablePart := managedRawPresent(part["text"]) || managedRawPresent(part["inlineData"]) ||
				managedRawPresent(part["inline_data"]) || managedRawPresent(part["fileData"]) || managedRawPresent(part["file_data"])
			if !portablePart {
				return fmt.Errorf("managed Gemini current user turn contains an unsupported part")
			}
			for _, fileField := range []string{"fileData", "file_data"} {
				if !managedRawPresent(part[fileField]) {
					continue
				}
				var fileData map[string]json.RawMessage
				if err := common.Unmarshal(part[fileField], &fileData); err != nil {
					return fmt.Errorf("decode managed Gemini file data: %w", err)
				}
				if managedRawPresent(fileData["fileUri"]) || managedRawPresent(fileData["file_uri"]) {
					return fmt.Errorf("managed Gemini current user turn contains a provider-owned file URI")
				}
			}
		}
	}
	if userTurns != 1 {
		return fmt.Errorf("managed Gemini request requires exactly one current user turn")
	}
	return nil
}

func managedRawPresent(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && !bytes.Equal(trimmed, []byte(`""`))
}

func managedRawString(value json.RawMessage) (string, error) {
	if !managedRawPresent(value) {
		return "", nil
	}
	var decoded string
	if err := common.Unmarshal(value, &decoded); err != nil {
		return "", err
	}
	return decoded, nil
}

func managedRawBool(value json.RawMessage) (bool, error) {
	if !managedRawPresent(value) {
		return false, nil
	}
	var decoded bool
	if err := common.Unmarshal(value, &decoded); err != nil {
		return false, err
	}
	return decoded, nil
}
