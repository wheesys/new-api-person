package oairesponses

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

const (
	responsesInputTypeFunctionCall       = "function_call"
	responsesInputTypeFunctionCallOutput = "function_call_output"
	responsesInputTypeCustomToolCall     = "custom_tool_call"
	responsesInputTypeCustomToolOutput   = "custom_tool_call_output"
	responsesInputTypeReasoning          = "reasoning"
)

const (
	ResponsesInputTypeFunctionCall       = responsesInputTypeFunctionCall
	ResponsesInputTypeFunctionCallOutput = responsesInputTypeFunctionCallOutput
	ResponsesInputTypeCustomToolCall     = responsesInputTypeCustomToolCall
	ResponsesInputTypeCustomToolOutput   = responsesInputTypeCustomToolOutput
)

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	return ResponsesRequestToChatCompletionsRequestWithMeta(req, nil)
}

// ResponsesRequestToChatCompletionsRequestWithMeta converts a Responses request
// to Chat Completions. When meta indicates a Responses→Chat downgrade
// (CodexOptions.ResponsesChatFallback), freeform custom tools and namespaces
// are adapted so Chat-completions-only upstreams (GLM, DeepSeek, etc.) can
// represent them, and the mapping is recorded for reverse conversion.
func ResponsesRequestToChatCompletionsRequestWithMeta(req *dto.OpenAIResponsesRequest, meta convmeta.Meta) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if err := validateResponsesRequestChatUnsupportedFields(req); err != nil {
		return nil, err
	}

	messages, err := responsesRequestMessagesToChat(req, meta)
	if err != nil {
		return nil, err
	}

	// Codex (Responses API) injects its tool catalog into input items of type
	// "additional_tools" rather than the top-level "tools" field. Merge those
	// tool definitions into the top-level tools so the downstream conversion
	// (including codex tool adaptation) sees the full tool set.
	mergedTools, err := mergeAdditionalToolsFromInput(req.Tools, req.Input)
	if err != nil {
		return nil, err
	}

	tools, err := responsesRequestToolsToChat(mergedTools, meta)
	if err != nil {
		return nil, err
	}

	toolChoice, err := responsesRequestToolChoiceToChat(req.ToolChoice)
	if err != nil {
		return nil, err
	}

	responseFormat, err := responsesRequestTextToChatResponseFormat(req.Text)
	if err != nil {
		return nil, err
	}

	out := &dto.GeneralOpenAIRequest{
		Model:                req.Model,
		Messages:             messages,
		Stream:               req.Stream,
		StreamOptions:        req.StreamOptions,
		MaxCompletionTokens:  req.MaxOutputTokens,
		Temperature:          req.Temperature,
		TopP:                 req.TopP,
		TopLogProbs:          req.TopLogProbs,
		ResponseFormat:       responseFormat,
		Tools:                tools,
		ToolChoice:           toolChoice,
		User:                 req.User,
		Store:                req.Store,
		Metadata:             req.Metadata,
		SafetyIdentifier:     req.SafetyIdentifier,
		PromptCacheRetention: req.PromptCacheRetention,
		EnableThinking:       req.EnableThinking,
		ThinkingBudget:       req.ThinkingBudget,
	}

	out.FrequencyPenalty, err = responsesRawFloat(req.FrequencyPenalty)
	if err != nil {
		return nil, fmt.Errorf("invalid frequency_penalty: %w", err)
	}
	out.PresencePenalty, err = responsesRawFloat(req.PresencePenalty)
	if err != nil {
		return nil, fmt.Errorf("invalid presence_penalty: %w", err)
	}

	if req.Reasoning != nil {
		out.ReasoningEffort = req.Reasoning.Effort
	}
	if req.ServiceTier != "" {
		out.ServiceTier, _ = kitutil.Marshal(req.ServiceTier)
	}
	if len(req.ParallelToolCalls) > 0 && kitutil.GetJsonType(req.ParallelToolCalls) == "boolean" {
		var parallelToolCalls bool
		if err := kitutil.Unmarshal(req.ParallelToolCalls, &parallelToolCalls); err == nil {
			out.ParallelTooCalls = &parallelToolCalls
		}
	}
	if len(req.PromptCacheKey) > 0 && kitutil.GetJsonType(req.PromptCacheKey) == "string" {
		var promptCacheKey string
		if err := kitutil.Unmarshal(req.PromptCacheKey, &promptCacheKey); err == nil {
			out.PromptCacheKey = promptCacheKey
		}
	}

	return out, nil
}

func validateResponsesRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	unsupported := make([]string, 0, 4)
	if rawJSONPresent(req.Conversation) {
		unsupported = append(unsupported, "conversation")
	}
	if strings.TrimSpace(req.PreviousResponseID) != "" {
		unsupported = append(unsupported, "previous_response_id")
	}
	if rawJSONPresent(req.Prompt) {
		unsupported = append(unsupported, "prompt")
	}
	if rawJSONPresent(req.ContextManagement) {
		unsupported = append(unsupported, "context_management")
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("responses to chat conversion does not support stateful fields: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

func ValidateRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	return validateResponsesRequestChatUnsupportedFields(req)
}

func responsesRequestMessagesToChat(req *dto.OpenAIResponsesRequest, meta convmeta.Meta) ([]dto.Message, error) {
	messages := make([]dto.Message, 0)
	if rawJSONPresent(req.Instructions) {
		instructions, err := responsesJSONString(req.Instructions)
		if err != nil {
			return nil, fmt.Errorf("invalid instructions: %w", err)
		}
		if strings.TrimSpace(instructions) != "" {
			messages = append(messages, dto.Message{Role: "system", Content: instructions})
		}
	}

	if !rawJSONPresent(req.Input) {
		return messages, nil
	}

	switch kitutil.GetJsonType(req.Input) {
	case "string":
		input, err := responsesJSONString(req.Input)
		if err != nil {
			return nil, fmt.Errorf("invalid input string: %w", err)
		}
		messages = append(messages, dto.Message{Role: "user", Content: input})
		return messages, nil
	case "array":
		var items []map[string]any
		if err := kitutil.Unmarshal(req.Input, &items); err != nil {
			return nil, fmt.Errorf("invalid input array: %w", err)
		}
		for _, item := range items {
			nextMessages, err := responsesInputItemToChatMessages(item, messages, meta)
			if err != nil {
				return nil, err
			}
			messages = nextMessages
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported responses input type %q", kitutil.GetJsonType(req.Input))
	}
}

func responsesInputItemToChatMessages(item map[string]any, messages []dto.Message, meta convmeta.Meta) ([]dto.Message, error) {
	itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
	switch itemType {
	case responsesInputTypeFunctionCall:
		toolCall, err := responsesFunctionCallItemToChatToolCall(item)
		if err != nil {
			return nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil
	case responsesInputTypeCustomToolCall:
		toolCall, err := responsesCustomToolCallItemToChatToolCall(item, meta)
		if err != nil {
			return nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil
	case responsesInputTypeFunctionCallOutput:
		callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
		content := responseToolOutputToChatContent(item["output"])
		return append(messages, dto.Message{Role: "tool", ToolCallId: callID, Content: content}), nil
	case responsesInputTypeCustomToolOutput:
		callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
		content := responseToolOutputToChatContent(item["output"])
		return append(messages, dto.Message{Role: "tool", ToolCallId: callID, Content: content}), nil
	case responsesInputTypeReasoning:
		// Reasoning summaries from a prior turn are not chat messages; skip
		// them so they do not become empty user messages (which make the
		// upstream report a missing prompt).
		return messages, nil
	case additionalToolsInputType:
		// Tool catalog carried in the input stream; already merged into
		// top-level tools by mergeAdditionalToolsFromInput, so skip it here.
		return messages, nil
	}

	role := strings.TrimSpace(kitutil.Interface2String(item["role"]))
	if role == "" {
		role = "user"
	}
	content, err := responsesInputContentToChatContent(item["content"])
	if err != nil {
		return nil, err
	}
	return append(messages, dto.Message{Role: role, Content: content}), nil
}

func responsesInputContentToChatContent(content any) (any, error) {
	if content == nil {
		return "", nil
	}

	switch value := content.(type) {
	case string:
		return value, nil
	case []any:
		return responsesContentPartsToChatContent(value)
	case []map[string]any:
		parts := make([]any, 0, len(value))
		for _, part := range value {
			parts = append(parts, part)
		}
		return responsesContentPartsToChatContent(parts)
	default:
		return content, nil
	}
}

func responsesContentPartsToChatContent(parts []any) (any, error) {
	chatParts := make([]any, 0, len(parts))
	var textOnly strings.Builder
	onlyText := true

	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			onlyText = false
			chatParts = append(chatParts, rawPart)
			continue
		}

		partType := strings.TrimSpace(kitutil.Interface2String(part["type"]))
		switch partType {
		case "input_text", "output_text", "text":
			text := kitutil.Interface2String(part["text"])
			textOnly.WriteString(text)
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeText,
				"text": text,
			})
		case "input_image":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeImageURL,
				"image_url": responsesImagePartToChatImageURL(part),
			})
		case "input_file":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeFile,
				"file": responsesFilePartToChatFile(part),
			})
		case "input_audio":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":        dto.ContentTypeInputAudio,
				"input_audio": responsesPartPayload(part, "input_audio"),
			})
		case "input_video":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeVideoUrl,
				"video_url": responsesVideoPartToChatVideoURL(part),
			})
		default:
			onlyText = false
			chatParts = append(chatParts, part)
		}
	}

	if onlyText {
		return textOnly.String(), nil
	}
	return chatParts, nil
}

func responsesFunctionCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	name := strings.TrimSpace(kitutil.Interface2String(item["name"]))
	if name == "" {
		return dto.ToolCallRequest{}, errors.New("function_call item is missing name")
	}
	return dto.ToolCallRequest{
		ID:   responsesCallID(item),
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      name,
			Arguments: responsesArgumentsString(item["arguments"]),
		},
	}, nil
}

// codexToolMappingEnabled reports whether the current conversion should apply
// codex-style tool adaptation (custom→function wrapping + namespace flattening).
func codexToolMappingEnabled(meta convmeta.Meta) bool {
	if meta == nil {
		return false
	}
	return convmeta.OptionsOf(meta).Codex.ResponsesChatFallback
}

func responsesCustomToolCallItemToChatToolCall(item map[string]any, meta convmeta.Meta) (dto.ToolCallRequest, error) {
	name := strings.TrimSpace(kitutil.Interface2String(item["name"]))
	if codexToolMappingEnabled(meta) {
		ctx := meta.EnsureCodexToolContext()
		// The original custom tool was sent upstream as a flattened function;
		// mirror its name so the wrapped input round-trips under the same name.
		chatName := ctx.FlattenNamespaceName("", name)
		input := responsesArgumentsString(item["input"])
		arguments, err := kitutil.Marshal(map[string]any{convmeta.ChatToolCustomInputField: input})
		if err != nil {
			return dto.ToolCallRequest{}, err
		}
		return dto.ToolCallRequest{
			ID:   responsesCallID(item),
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      chatName,
				Arguments: string(arguments),
			},
		}, nil
	}

	raw, err := kitutil.Marshal(item)
	if err != nil {
		return dto.ToolCallRequest{}, err
	}
	return dto.ToolCallRequest{
		ID:     responsesCallID(item),
		Type:   dto.CustomType,
		Custom: raw,
		Function: dto.FunctionRequest{
			Name:      name,
			Arguments: responsesArgumentsString(item["input"]),
		},
	}, nil
}

func appendToolCallToLastAssistant(messages []dto.Message, toolCall dto.ToolCallRequest) []dto.Message {
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		messages = append(messages, dto.Message{Role: "assistant"})
	}

	idx := len(messages) - 1
	toolCalls := messages[idx].ParseToolCalls()
	toolCalls = append(toolCalls, toolCall)
	toolCallsRaw, _ := kitutil.Marshal(toolCalls)
	messages[idx].ToolCalls = toolCallsRaw
	return messages
}

// additionalToolsInputType is the Responses input item type Codex uses to carry
// its tool catalog (functions/custom tools) inside the request input array.
const additionalToolsInputType = "additional_tools"

// mergeAdditionalToolsFromInput extracts tool definitions carried in
// `input[].type == "additional_tools"` items and merges them into the top-level
// tools raw JSON. Codex 0.147+ places its tool catalog there instead of the
// Responses `tools` parameter, so without this the upstream sees no tools at
// all and never issues tool calls.
func mergeAdditionalToolsFromInput(toolsRaw, inputRaw json.RawMessage) (json.RawMessage, error) {
	if !rawJSONPresent(inputRaw) || kitutil.GetJsonType(inputRaw) != "array" {
		return toolsRaw, nil
	}
	var items []map[string]any
	if err := kitutil.Unmarshal(inputRaw, &items); err != nil {
		return nil, fmt.Errorf("invalid input array: %w", err)
	}

	var additional []map[string]any
	for _, item := range items {
		if strings.TrimSpace(kitutil.Interface2String(item["type"])) != additionalToolsInputType {
			continue
		}
		rawTools, ok := item["tools"].([]any)
		if !ok {
			continue
		}
		for _, rawTool := range rawTools {
			if tool, ok := rawTool.(map[string]any); ok {
				additional = append(additional, tool)
			}
		}
	}
	if len(additional) == 0 {
		return toolsRaw, nil
	}

	var existing []map[string]any
	if rawJSONPresent(toolsRaw) {
		if err := kitutil.Unmarshal(toolsRaw, &existing); err != nil {
			return nil, fmt.Errorf("invalid tools: %w", err)
		}
	}
	merged := append(existing, additional...)
	out, err := kitutil.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged tools: %w", err)
	}
	return out, nil
}

func responsesRequestToolsToChat(raw json.RawMessage, meta convmeta.Meta) ([]dto.ToolCallRequest, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var tools []map[string]any
	if err := kitutil.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("invalid tools: %w", err)
	}

	if codexToolMappingEnabled(meta) {
		var strip func(string) bool
		if opts := convmeta.OptionsOf(meta); opts != nil {
			strip = opts.Codex.StripBuiltInTool
		}
		return codexToolsToChat(tools, meta.EnsureCodexToolContext(), strip)
	}
	return responsesToolsToChatNative(tools)
}

// responsesToolsToChatNative converts Responses tools without codex-style
// adaptation: ordinary function tools are passed through, everything else
// (custom, tool_search, namespaces) is preserved verbatim as a custom tool.
func responsesToolsToChatNative(tools []map[string]any) ([]dto.ToolCallRequest, error) {
	out := make([]dto.ToolCallRequest, 0, len(tools))
	for _, tool := range tools {
		toolType := strings.TrimSpace(kitutil.Interface2String(tool["type"]))
		if toolType == "function" {
			out = append(out, dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        strings.TrimSpace(kitutil.Interface2String(tool["name"])),
					Description: kitutil.Interface2String(tool["description"]),
					Parameters:  tool["parameters"],
				},
			})
			continue
		}

		rawTool, err := kitutil.Marshal(tool)
		if err != nil {
			return nil, err
		}
		out = append(out, dto.ToolCallRequest{
			Type:   toolType,
			Custom: rawTool,
		})
	}
	return out, nil
}

// codexToolsToChat adapts Responses tools for a Chat-completions-only upstream:
// freeform custom tools (apply_patch) are wrapped as single-argument function
// tools, namespaces are flattened, tool_search is expressed as a function, and
// built-in tools flagged by strip are dropped. Every chat-facing name is
// recorded in ctx so a model tool call can be resolved back to its Responses
// identity during response conversion.
func codexToolsToChat(tools []map[string]any, ctx *convmeta.CodexToolContext, strip func(string) bool) ([]dto.ToolCallRequest, error) {
	out := make([]dto.ToolCallRequest, 0, len(tools))
	for _, tool := range tools {
		toolType := strings.TrimSpace(kitutil.Interface2String(tool["type"]))
		name := strings.TrimSpace(kitutil.Interface2String(tool["name"]))
		// Built-in tools may carry no name; key the strip decision on the type
		// in that case so e.g. web_search declarations can be dropped.
		stripKey := name
		if stripKey == "" {
			stripKey = toolType
		}
		if strip != nil && stripKey != "" && strip(stripKey) {
			continue
		}

		switch toolType {
		case "function":
			ns := strings.TrimSpace(kitutil.Interface2String(tool["namespace"]))
			chatName := ctx.FlattenNamespaceName(ns, name)
			ctx.Record(chatName, convmeta.CodexToolSpec{Kind: convmeta.CodexToolFunction, Name: name, Namespace: ns})
			out = append(out, dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        chatName,
					Description: kitutil.Interface2String(tool["description"]),
					Parameters:  tool["parameters"],
				},
			})
		case "namespace":
			ns := strings.TrimSpace(kitutil.Interface2String(tool["namespace"]))
			if ns == "" {
				ns = strings.TrimSpace(kitutil.Interface2String(tool["name"]))
			}
			if err := expandCodexNamespaceToChat(ns, tool, ctx, strip, &out); err != nil {
				return nil, err
			}
		case "custom":
			chatName := ctx.FlattenNamespaceName("", name)
			ctx.Record(chatName, convmeta.CodexToolSpec{Kind: convmeta.CodexToolCustom, Name: name})
			out = append(out, customToolToFunctionTool(chatName, tool))
		case "tool_search":
			chatName := ctx.FlattenNamespaceName("", "tool_search")
			ctx.Record(chatName, convmeta.CodexToolSpec{Kind: convmeta.CodexToolToolSearch, Name: "tool_search"})
			out = append(out, dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        chatName,
					Description: "Search the web.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{"type": "string"},
							"limit": map[string]any{"type": "integer"},
						},
						"required": []string{"query"},
					},
				},
			})
		default:
			rawTool, err := kitutil.Marshal(tool)
			if err != nil {
				return nil, err
			}
			out = append(out, dto.ToolCallRequest{
				Type:   toolType,
				Custom: rawTool,
			})
		}
	}
	return out, nil
}

// expandCodexNamespaceToChat flattens a Codex catalog namespace into flat chat
// function tools. Codex namespaces carry their children under either `tools`
// (catalog format, e.g. from an "additional_tools" input item) or `children`
// (Responses top-level tools format), and may nest. Function children are
// flattened with a namespace prefix; custom children (e.g. the `exec` freeform
// tool) are wrapped as single-argument function tools. Namespaces may also
// contain further namespaces, handled recursively.
func expandCodexNamespaceToChat(ns string, namespace map[string]any, ctx *convmeta.CodexToolContext, strip func(string) bool, out *[]dto.ToolCallRequest) error {
	children, _ := namespace["children"].([]any)
	if len(children) == 0 {
		children, _ = namespace["tools"].([]any)
	}
	for _, rawChild := range children {
		child, ok := rawChild.(map[string]any)
		if !ok {
			continue
		}
		childType := strings.TrimSpace(kitutil.Interface2String(child["type"]))
		cname := strings.TrimSpace(kitutil.Interface2String(child["name"]))
		stripKey := cname
		if stripKey == "" {
			stripKey = childType
		}
		if strip != nil && stripKey != "" && strip(stripKey) {
			continue
		}
		switch childType {
		case "function":
			chatName := ctx.FlattenNamespaceName(ns, cname)
			ctx.Record(chatName, convmeta.CodexToolSpec{Kind: convmeta.CodexToolFunction, Name: cname, Namespace: ns})
			*out = append(*out, dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        chatName,
					Description: kitutil.Interface2String(child["description"]),
					Parameters:  child["parameters"],
				},
			})
		case "custom":
			chatName := ctx.FlattenNamespaceName(ns, cname)
			ctx.Record(chatName, convmeta.CodexToolSpec{Kind: convmeta.CodexToolCustom, Name: cname, Namespace: ns})
			*out = append(*out, customToolToFunctionTool(chatName, child))
		case "namespace":
			childNs := strings.TrimSpace(kitutil.Interface2String(child["namespace"]))
			if childNs == "" {
				childNs = strings.TrimSpace(kitutil.Interface2String(child["name"]))
			}
			childNs = ctx.FlattenNamespaceName(ns, childNs)
			if err := expandCodexNamespaceToChat(childNs, child, ctx, strip, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// customToolToFunctionTool wraps a freeform custom tool as a function tool whose
// only parameter is a string named by convmeta.ChatToolCustomInputField, so a
// Chat-completions-only upstream can represent apply_patch-style tools.
func customToolToFunctionTool(chatName string, tool map[string]any) dto.ToolCallRequest {
	description := kitutil.Interface2String(tool["description"])
	return dto.ToolCallRequest{
		Type: "function",
		Function: dto.FunctionRequest{
			Name:        chatName,
			Description: description,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					convmeta.ChatToolCustomInputField: map[string]any{
						"type":        "string",
						"description": description,
					},
				},
				"required": []string{convmeta.ChatToolCustomInputField},
			},
		},
	}
}

func responsesRequestToolChoiceToChat(raw json.RawMessage) (any, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	if kitutil.GetJsonType(raw) == "string" {
		var choice string
		if err := kitutil.Unmarshal(raw, &choice); err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		return choice, nil
	}

	var choice map[string]any
	if err := kitutil.Unmarshal(raw, &choice); err != nil {
		return nil, fmt.Errorf("invalid tool_choice: %w", err)
	}
	if kitutil.Interface2String(choice["type"]) == "function" {
		name := strings.TrimSpace(kitutil.Interface2String(choice["name"]))
		if name != "" {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}, nil
		}
	}
	return choice, nil
}

func RequestToolChoiceToChat(raw json.RawMessage) (any, error) {
	return responsesRequestToolChoiceToChat(raw)
}

func responsesRequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var textConfig map[string]any
	if err := kitutil.Unmarshal(raw, &textConfig); err != nil {
		return nil, fmt.Errorf("invalid text config: %w", err)
	}
	format, ok := textConfig["format"].(map[string]any)
	if !ok {
		return nil, nil
	}

	formatType := strings.TrimSpace(kitutil.Interface2String(format["type"]))
	if formatType == "" {
		return nil, nil
	}

	out := &dto.ResponseFormat{Type: formatType}
	if formatType == "json_schema" {
		schemaRaw, err := kitutil.Marshal(format)
		if err != nil {
			return nil, err
		}
		out.JsonSchema = schemaRaw
	}
	return out, nil
}

func RequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	return responsesRequestTextToChatResponseFormat(raw)
}

func responsesImagePartToChatImageURL(part map[string]any) any {
	if imageURL, ok := part["image_url"]; ok {
		return imageURL
	}
	imageURL := map[string]any{}
	for _, key := range []string{"url", "file_id", "detail"} {
		if value, ok := part[key]; ok {
			imageURL[key] = value
		}
	}
	if len(imageURL) == 0 {
		return part
	}
	return imageURL
}

func responsesFilePartToChatFile(part map[string]any) any {
	if file, ok := part["file"]; ok {
		return file
	}
	file := map[string]any{}
	for _, key := range []string{"file_id", "file_data", "filename", "file_url"} {
		if value, ok := part[key]; ok {
			file[key] = value
		}
	}
	if len(file) == 0 {
		return part
	}
	return file
}

func responsesVideoPartToChatVideoURL(part map[string]any) any {
	if videoURL, ok := part["video_url"]; ok {
		if videoURLMap, ok := videoURL.(map[string]any); ok {
			if url := kitutil.Interface2String(videoURLMap["url"]); url != "" {
				return url
			}
		}
		return videoURL
	}
	if url := kitutil.Interface2String(part["url"]); url != "" {
		return url
	}
	return responsesPartPayload(part, "video_url")
}

func responsesPartPayload(part map[string]any, key string) any {
	if value, ok := part[key]; ok {
		return value
	}
	payload := make(map[string]any, len(part))
	for k, value := range part {
		if k == "type" {
			continue
		}
		payload[k] = value
	}
	return payload
}

func responsesCallID(item map[string]any) string {
	callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
	if callID != "" {
		return callID
	}
	return strings.TrimSpace(kitutil.Interface2String(item["id"]))
}

func CallID(item map[string]any) string {
	return responsesCallID(item)
}

func responsesArgumentsString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := kitutil.Marshal(v)
		if err != nil {
			return kitutil.Interface2String(v)
		}
		return string(raw)
	}
}

func responseToolOutputToChatContent(value any) any {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := kitutil.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(raw)
	}
}

func responsesRawFloat(raw json.RawMessage) (*float64, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	var value float64
	if err := kitutil.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func responsesJSONString(raw json.RawMessage) (string, error) {
	if kitutil.GetJsonType(raw) != "string" {
		return string(raw), nil
	}
	var value string
	if err := kitutil.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func rawJSONPresent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return kitutil.GetJsonType(raw) != "null"
}

func JSONString(raw json.RawMessage) (string, error) {
	return responsesJSONString(raw)
}

func RawJSONPresent(raw json.RawMessage) bool {
	return rawJSONPresent(raw)
}
