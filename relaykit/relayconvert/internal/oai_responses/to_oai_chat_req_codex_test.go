package oairesponses

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codexMeta returns a Values-based meta with Responses→Chat codex adaptation
// enabled, mirroring what a downgraded request carries on RelayInfo.
func codexMeta() *convmeta.Values {
	m := &convmeta.Values{}
	m.Options = &convmeta.Options{Codex: convmeta.CodexOptions{ResponsesChatFallback: true}}
	return m
}

func TestResponsesRequestToChatCompletionsRequestWithMeta_codexCustomWrapped(t *testing.T) {
	meta := codexMeta()
	req := &dto.OpenAIResponsesRequest{
		Model: "glm-4",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`),
		Tools: json.RawMessage(`[{"type":"custom","name":"apply_patch","description":"Apply a patch.","input_schema":{}}]`),
	}

	chat, err := ResponsesRequestToChatCompletionsRequestWithMeta(req, meta)
	require.NoError(t, err)
	require.Len(t, chat.Tools, 1)

	// The freeform custom tool must be wrapped as a function tool carrying only
	// an "input" string parameter.
	tool := chat.Tools[0]
	assert.Equal(t, "function", tool.Type)
	assert.Equal(t, "apply_patch", tool.Function.Name)
	params, ok := tool.Function.Parameters.(map[string]any)
	require.True(t, ok)
	properties, ok := params["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, properties, convmeta.ChatToolCustomInputField)

	// The mapping must be recorded for reverse resolution.
	spec, ok := meta.EnsureCodexToolContext().Lookup("apply_patch")
	require.True(t, ok)
	assert.Equal(t, convmeta.CodexToolCustom, spec.Kind)
	assert.Equal(t, "apply_patch", spec.Name)
}

func TestResponsesRequestToChatCompletionsRequestWithMeta_namespaceFlattened(t *testing.T) {
	meta := codexMeta()
	req := &dto.OpenAIResponsesRequest{
		Model: "glm-4",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`),
		Tools: json.RawMessage(`[{"type":"namespace","namespace":"codex","name":"","children":[{"type":"function","name":"get","description":"get","parameters":{"type":"object"}}]}]`),
	}

	chat, err := ResponsesRequestToChatCompletionsRequestWithMeta(req, meta)
	require.NoError(t, err)
	require.Len(t, chat.Tools, 1)

	assert.Equal(t, "function", chat.Tools[0].Type)
	assert.Equal(t, "codex__get", chat.Tools[0].Function.Name)

	spec, ok := meta.EnsureCodexToolContext().Lookup("codex__get")
	require.True(t, ok)
	assert.Equal(t, convmeta.CodexToolFunction, spec.Kind)
	assert.Equal(t, "get", spec.Name)
	assert.Equal(t, "codex", spec.Namespace)
}

func TestResponsesRequestToChatCompletionsRequestWithMeta_stripBuiltInTool(t *testing.T) {
	meta := &convmeta.Values{}
	meta.Options = &convmeta.Options{Codex: convmeta.CodexOptions{
		ResponsesChatFallback: true,
		StripBuiltInTool:      func(name string) bool { return name == "web_search" },
	}}
	req := &dto.OpenAIResponsesRequest{
		Model: "glm-4",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`),
		Tools: json.RawMessage(`[
			{"type":"web_search"},
			{"type":"custom","name":"apply_patch","input_schema":{}}
		]`),
	}

	chat, err := ResponsesRequestToChatCompletionsRequestWithMeta(req, meta)
	require.NoError(t, err)
	// web_search (keyed by type, no name) must be dropped; apply_patch kept.
	require.Len(t, chat.Tools, 1)
	assert.Equal(t, "apply_patch", chat.Tools[0].Function.Name)
}

func TestResponsesRequestToChatCompletionsRequestWithMeta_noCodexKeepsCustom(t *testing.T) {
	// Without codex adaptation the custom tool is preserved verbatim.
	req := &dto.OpenAIResponsesRequest{
		Model: "glm-4",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`),
		Tools: json.RawMessage(`[{"type":"custom","name":"apply_patch","input_schema":{}}]`),
	}

	chat, err := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chat.Tools, 1)
	assert.Equal(t, "custom", chat.Tools[0].Type)
}

func TestResponsesRequestToChatCompletionsRequestWithMeta_additionalToolsMerged(t *testing.T) {
	// Codex carries its tool catalog in an "additional_tools" input item, not
	// the top-level "tools" field. They must be extracted and merged so the
	// upstream sees the tools and can issue tool calls.
	meta := codexMeta()
	req := &dto.OpenAIResponsesRequest{
		Model: "glm-4",
		Input: json.RawMessage(`[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"custom","name":"exec","description":"Run JS","input_schema":{"type":"object"}},
				{"type":"namespace","name":"functions","tools":[
					{"type":"function","name":"get","description":"get","parameters":{"type":"object"}}
				]}
			]},
			{"type":"message","role":"user","content":"hi"}
		]`),
	}

	chat, err := ResponsesRequestToChatCompletionsRequestWithMeta(req, meta)
	require.NoError(t, err)

	// exec custom tool -> wrapped function, functions namespace -> flattened.
	names := make([]string, 0, len(chat.Tools))
	for _, tool := range chat.Tools {
		names = append(names, tool.Function.Name)
	}
	assert.Contains(t, names, "exec")
	assert.Contains(t, names, "functions__get")

	// The additional_tools item itself must not leak as a message.
	gotUser := false
	for _, msg := range chat.Messages {
		if msg.Role == "user" && strings.Contains(fmt.Sprint(msg.Content), "additional_tools") {
			gotUser = true
		}
	}
	assert.False(t, gotUser, "additional_tools item leaked into chat messages")
}

func TestResponsesRequestToChatCompletionsRequestWithMeta_additionalToolsEmptyKeepsTools(t *testing.T) {
	// When input has no additional_tools, the top-level tools are preserved.
	req := &dto.OpenAIResponsesRequest{
		Model: "glm-4",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`),
		Tools: json.RawMessage(`[{"type":"custom","name":"apply_patch","input_schema":{}}]`),
	}
	chat, err := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chat.Tools, 1)
	assert.Equal(t, "custom", chat.Tools[0].Type)
}

func TestResponsesRequestToChatCompletionsRequestWithMeta_additionalToolsNestedNamespace(t *testing.T) {
	// Namespaces may nest; children carried under the codex catalog "tools" key.
	meta := codexMeta()
	req := &dto.OpenAIResponsesRequest{
		Model: "glm-4",
		Input: json.RawMessage(`[{"type":"additional_tools","role":"developer","tools":[
			{"type":"namespace","name":"outer","tools":[
				{"type":"namespace","name":"inner","tools":[
					{"type":"custom","name":"exec","input_schema":{"type":"object"}}
				]}
			]}
		]}]`),
	}
	chat, err := ResponsesRequestToChatCompletionsRequestWithMeta(req, meta)
	require.NoError(t, err)
	require.Len(t, chat.Tools, 1)
	assert.Equal(t, "function", chat.Tools[0].Type)
	assert.Equal(t, "outer__inner__exec", chat.Tools[0].Function.Name)
}
