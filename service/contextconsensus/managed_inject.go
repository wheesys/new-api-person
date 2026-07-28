package contextconsensus

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
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
	var request dto.GeneralOpenAIRequest
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode managed Chat request: %w", err)
	}
	userIndex := firstManagedUserIndex(len(request.Messages), func(index int) string { return request.Messages[index].Role })
	if userIndex < 0 {
		return nil, fmt.Errorf("managed Chat request requires a user message")
	}
	rewritten := make([]dto.Message, 0, len(request.Messages)+1)
	rewritten = append(rewritten, request.Messages[:userIndex]...)
	rewritten = append(rewritten, dto.Message{Role: "user", Content: summaryText})
	rewritten = append(rewritten, request.Messages[userIndex:]...)
	request.Messages = rewritten
	return marshalManagedRequest(request)
}

func injectManagedResponses(body []byte, summaryText string) ([]byte, error) {
	var request dto.OpenAIResponsesRequest
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode managed Responses request: %w", err)
	}
	summaryItem := map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": summaryText}},
	}
	switch common.GetJsonType(request.Input) {
	case "string":
		var currentInput string
		if err := common.Unmarshal(request.Input, &currentInput); err != nil {
			return nil, fmt.Errorf("decode managed Responses string input: %w", err)
		}
		input, err := common.Marshal([]any{
			summaryItem,
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": currentInput}}},
		})
		if err != nil {
			return nil, fmt.Errorf("encode managed Responses input: %w", err)
		}
		request.Input = input
	case "array":
		var inputItems []map[string]any
		if err := common.Unmarshal(request.Input, &inputItems); err != nil {
			return nil, fmt.Errorf("decode managed Responses input items: %w", err)
		}
		userIndex := firstManagedUserIndex(len(inputItems), func(index int) string { return fmt.Sprint(inputItems[index]["role"]) })
		if userIndex < 0 {
			return nil, fmt.Errorf("managed Responses request requires a user input item")
		}
		rewritten := make([]map[string]any, 0, len(inputItems)+1)
		rewritten = append(rewritten, inputItems[:userIndex]...)
		rewritten = append(rewritten, summaryItem)
		rewritten = append(rewritten, inputItems[userIndex:]...)
		input, err := common.Marshal(rewritten)
		if err != nil {
			return nil, fmt.Errorf("encode managed Responses input items: %w", err)
		}
		request.Input = input
	default:
		return nil, fmt.Errorf("managed Responses request requires string or array input")
	}
	return marshalManagedRequest(request)
}

func injectManagedClaude(body []byte, summaryText string) ([]byte, error) {
	var request dto.ClaudeRequest
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode managed Claude request: %w", err)
	}
	userIndex := firstManagedUserIndex(len(request.Messages), func(index int) string { return request.Messages[index].Role })
	if userIndex < 0 {
		return nil, fmt.Errorf("managed Claude request requires a user message")
	}
	message := request.Messages[userIndex]
	summaryBlock := dto.ClaudeMediaMessage{Type: "text"}
	summaryBlock.SetText(summaryText)
	contents := []dto.ClaudeMediaMessage{summaryBlock}
	if message.IsStringContent() {
		originalBlock := dto.ClaudeMediaMessage{Type: "text"}
		originalBlock.SetText(message.GetStringContent())
		contents = append(contents, originalBlock)
	} else {
		originalContents, err := message.ParseContent()
		if err != nil {
			return nil, fmt.Errorf("decode managed Claude user content: %w", err)
		}
		contents = append(contents, originalContents...)
	}
	message.Content = contents
	request.Messages[userIndex] = message
	return marshalManagedRequest(request)
}

func injectManagedGemini(body []byte, summaryText string) ([]byte, error) {
	var request dto.GeminiChatRequest
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode managed Gemini request: %w", err)
	}
	userIndex := firstManagedUserIndex(len(request.Contents), func(index int) string { return request.Contents[index].Role })
	if userIndex < 0 {
		return nil, fmt.Errorf("managed Gemini request requires user content")
	}
	request.Contents[userIndex].Parts = append([]dto.GeminiPart{{Text: summaryText}}, request.Contents[userIndex].Parts...)
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
