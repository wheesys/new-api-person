package contextconsensus

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type ManagedAssistantOutput struct {
	Protocol       types.RelayFormat `json:"protocol"`
	Text           string            `json:"text"`
	FinishReason   string            `json:"finish_reason,omitempty"`
	OutputDigest   string            `json:"output_digest"`
	ResponseDigest string            `json:"response_digest"`
}

// NormalizeManagedAssistantOutput is a pure, non-streaming response contract
// for the later commit barrier. The first managed version accepts one portable
// text assistant output and rejects tool calls, reasoning state, and multiple
// alternatives instead of guessing which branch should advance the revision.
func NormalizeManagedAssistantOutput(protocol types.RelayFormat, body []byte) (ManagedAssistantOutput, error) {
	switch protocol {
	case types.RelayFormatOpenAI:
		return normalizeManagedChatOutput(body)
	case types.RelayFormatOpenAIResponses:
		return normalizeManagedResponsesOutput(body)
	case types.RelayFormatClaude:
		return normalizeManagedClaudeOutput(body)
	case types.RelayFormatGemini:
		return normalizeManagedGeminiOutput(body)
	default:
		return ManagedAssistantOutput{}, fmt.Errorf("unsupported managed response protocol %q", protocol)
	}
}

func normalizeManagedChatOutput(body []byte) (ManagedAssistantOutput, error) {
	var response struct {
		Choices []struct {
			Message      map[string]json.RawMessage `json:"message"`
			FinishReason string                     `json:"finish_reason"`
		} `json:"choices"`
		Error json.RawMessage `json:"error"`
	}
	if err := common.Unmarshal(body, &response); err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("decode managed Chat response: %w", err)
	}
	if managedRawPresent(response.Error) {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Chat response contains an error")
	}
	if len(response.Choices) != 1 {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Chat response requires exactly one choice")
	}
	message := response.Choices[0].Message
	role, err := managedRawString(message["role"])
	if err != nil || !strings.EqualFold(strings.TrimSpace(role), "assistant") {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Chat response requires an assistant message")
	}
	for _, field := range []string{"tool_calls", "function_call", "reasoning", "reasoning_content", "audio"} {
		if managedRawPresent(message[field]) {
			return ManagedAssistantOutput{}, fmt.Errorf("managed Chat response contains non-portable field %q", field)
		}
	}
	text, err := managedPortableText(message["content"], map[string]struct{}{"text": {}, "output_text": {}})
	if err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("normalize managed Chat assistant content: %w", err)
	}
	if response.Choices[0].FinishReason != "stop" {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Chat response did not finish normally")
	}
	return managedAssistantTextResult(types.RelayFormatOpenAI, text, response.Choices[0].FinishReason, body)
}

func normalizeManagedResponsesOutput(body []byte) (ManagedAssistantOutput, error) {
	var response struct {
		Status string                       `json:"status"`
		Error  json.RawMessage              `json:"error"`
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := common.Unmarshal(body, &response); err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("decode managed Responses response: %w", err)
	}
	if managedRawPresent(response.Error) {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Responses response contains an error")
	}
	if response.Status != "completed" {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Responses response is not completed")
	}
	if len(response.Output) != 1 {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Responses response requires exactly one output item")
	}
	item := response.Output[0]
	itemType, err := managedRawString(item["type"])
	if err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Responses output type is invalid: %w", err)
	}
	role, err := managedRawString(item["role"])
	if err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Responses output role is invalid: %w", err)
	}
	if itemType != "message" || !strings.EqualFold(strings.TrimSpace(role), "assistant") {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Responses output must be one assistant message")
	}
	text, err := managedPortableText(item["content"], map[string]struct{}{"output_text": {}})
	if err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("normalize managed Responses assistant content: %w", err)
	}
	return managedAssistantTextResult(types.RelayFormatOpenAIResponses, text, response.Status, body)
}

func normalizeManagedClaudeOutput(body []byte) (ManagedAssistantOutput, error) {
	var response struct {
		Type       string                       `json:"type"`
		Role       string                       `json:"role"`
		Content    []map[string]json.RawMessage `json:"content"`
		StopReason string                       `json:"stop_reason"`
		Error      json.RawMessage              `json:"error"`
	}
	if err := common.Unmarshal(body, &response); err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("decode managed Claude response: %w", err)
	}
	if managedRawPresent(response.Error) {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Claude response contains an error")
	}
	if response.Type != "message" || !strings.EqualFold(strings.TrimSpace(response.Role), "assistant") {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Claude response requires an assistant message")
	}
	text, err := managedPortableContentBlocks(response.Content, map[string]struct{}{"text": {}})
	if err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("normalize managed Claude assistant content: %w", err)
	}
	if response.StopReason != "end_turn" && response.StopReason != "stop_sequence" {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Claude response did not finish normally")
	}
	return managedAssistantTextResult(types.RelayFormatClaude, text, response.StopReason, body)
}

func normalizeManagedGeminiOutput(body []byte) (ManagedAssistantOutput, error) {
	var response struct {
		Candidates []struct {
			Content      map[string]json.RawMessage `json:"content"`
			FinishReason string                     `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback map[string]json.RawMessage `json:"promptFeedback"`
	}
	if err := common.Unmarshal(body, &response); err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("decode managed Gemini response: %w", err)
	}
	if len(response.Candidates) != 1 {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Gemini response requires exactly one candidate")
	}
	content := response.Candidates[0].Content
	role, err := managedRawString(content["role"])
	if err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Gemini response role is invalid: %w", err)
	}
	if role != "" && !strings.EqualFold(strings.TrimSpace(role), "model") {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Gemini response requires model content")
	}
	var parts []map[string]json.RawMessage
	if err := common.Unmarshal(content["parts"], &parts); err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("decode managed Gemini response parts: %w", err)
	}
	for _, part := range parts {
		for _, field := range []string{"functionCall", "functionResponse", "thoughtSignature", "executableCode", "codeExecutionResult"} {
			if managedRawPresent(part[field]) {
				return ManagedAssistantOutput{}, fmt.Errorf("managed Gemini response contains non-portable field %q", field)
			}
		}
		thought, err := managedRawBool(part["thought"])
		if err != nil {
			return ManagedAssistantOutput{}, fmt.Errorf("managed Gemini response thought flag is invalid: %w", err)
		}
		if thought {
			return ManagedAssistantOutput{}, fmt.Errorf("managed Gemini response contains opaque thought state")
		}
	}
	text, err := managedPortableContentBlocks(parts, map[string]struct{}{"": {}})
	if err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("normalize managed Gemini assistant content: %w", err)
	}
	if response.Candidates[0].FinishReason != "STOP" {
		return ManagedAssistantOutput{}, fmt.Errorf("managed Gemini response did not finish normally")
	}
	return managedAssistantTextResult(types.RelayFormatGemini, text, response.Candidates[0].FinishReason, body)
}

func managedPortableText(content json.RawMessage, allowedTypes map[string]struct{}) (string, error) {
	if common.GetJsonType(content) == "string" {
		var text string
		if err := common.Unmarshal(content, &text); err != nil {
			return "", err
		}
		return text, nil
	}
	if common.GetJsonType(content) != "array" {
		return "", fmt.Errorf("assistant content must be text or an array")
	}
	var blocks []map[string]json.RawMessage
	if err := common.Unmarshal(content, &blocks); err != nil {
		return "", err
	}
	return managedPortableContentBlocks(blocks, allowedTypes)
}

func managedPortableContentBlocks(blocks []map[string]json.RawMessage, allowedTypes map[string]struct{}) (string, error) {
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		blockType, err := managedRawString(block["type"])
		if err != nil {
			return "", err
		}
		if _, allowed := allowedTypes[blockType]; !allowed {
			return "", fmt.Errorf("assistant content contains non-portable block %q", blockType)
		}
		text, err := managedRawString(block["text"])
		if err != nil {
			return "", err
		}
		if text == "" {
			return "", fmt.Errorf("assistant text block is empty")
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, ""), nil
}

func managedAssistantTextResult(protocol types.RelayFormat, text, finishReason string, body []byte) (ManagedAssistantOutput, error) {
	if strings.TrimSpace(text) == "" {
		return ManagedAssistantOutput{}, fmt.Errorf("managed assistant output text is empty")
	}
	normalizedOutput := struct {
		Version      int               `json:"version"`
		Protocol     types.RelayFormat `json:"protocol"`
		Text         string            `json:"text"`
		FinishReason string            `json:"finish_reason"`
	}{Version: 1, Protocol: protocol, Text: text, FinishReason: finishReason}
	encodedOutput, err := common.Marshal(normalizedOutput)
	if err != nil {
		return ManagedAssistantOutput{}, fmt.Errorf("encode normalized managed assistant output: %w", err)
	}
	return ManagedAssistantOutput{
		Protocol:       protocol,
		Text:           text,
		FinishReason:   finishReason,
		OutputDigest:   digestBytes(append([]byte("new-api:managed-assistant-output:v1\x00"), encodedOutput...)),
		ResponseDigest: digestBytes(body),
	}, nil
}
