package oairesponses

import (
	"encoding/json"
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
