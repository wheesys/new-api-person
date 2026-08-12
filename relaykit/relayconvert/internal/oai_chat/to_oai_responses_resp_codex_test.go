package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatToolCallToResponsesOutput_codexCustomRestored(t *testing.T) {
	meta := &convmeta.Values{}
	meta.Options = &convmeta.Options{Codex: convmeta.CodexOptions{ResponsesChatFallback: true}}
	meta.EnsureCodexToolContext().Record("apply_patch", convmeta.CodexToolSpec{Kind: convmeta.CodexToolCustom, Name: "apply_patch"})

	toolCall := dto.ToolCallRequest{
		ID:   "call_1",
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      "apply_patch",
			Arguments: `{"input":"{\"old_string\":\"a\",\"new_string\":\"b\"}"}`,
		},
	}

	out, err := chatToolCallToResponsesOutput(toolCall, "resp_x", 0, "completed", meta)
	require.NoError(t, err)
	assert.Equal(t, "custom_tool_call", out.Type)
	assert.Equal(t, "apply_patch", out.Name)
	// The wrapped input must be extracted back out of the arguments JSON and
	// carried in the "input" field, not "arguments".
	assert.Equal(t, `"{\"old_string\":\"a\",\"new_string\":\"b\"}"`, string(out.Input))
	assert.Empty(t, out.Arguments)
}

func TestChatToolCallToResponsesOutput_namespaceNameRestored(t *testing.T) {
	meta := &convmeta.Values{}
	meta.Options = &convmeta.Options{Codex: convmeta.CodexOptions{ResponsesChatFallback: true}}
	meta.EnsureCodexToolContext().Record("codex__get", convmeta.CodexToolSpec{
		Kind: convmeta.CodexToolFunction, Name: "get", Namespace: "codex",
	})

	toolCall := dto.ToolCallRequest{
		ID:       "call_1",
		Type:     "function",
		Function: dto.FunctionRequest{Name: "codex__get", Arguments: `{}`},
	}

	out, err := chatToolCallToResponsesOutput(toolCall, "resp_x", 0, "completed", meta)
	require.NoError(t, err)
	assert.Equal(t, "function_call", out.Type)
	assert.Equal(t, "get", out.Name)
}

func TestChatToolCallToResponsesOutput_noCodexKeepsFunctionCall(t *testing.T) {
	// Without codex adaptation a plain function tool call stays a function_call.
	meta := &convmeta.Values{}
	toolCall := dto.ToolCallRequest{
		ID:       "call_1",
		Type:     "function",
		Function: dto.FunctionRequest{Name: "get", Arguments: `{}`},
	}

	out, err := chatToolCallToResponsesOutput(toolCall, "resp_x", 0, "completed", meta)
	require.NoError(t, err)
	assert.Equal(t, "function_call", out.Type)
	assert.Equal(t, "get", out.Name)
}
