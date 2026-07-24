package contextconsensus

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractChatCompletionsPreservesToolGraphAndSchema(t *testing.T) {
	body := []byte(`{
  "model":"gpt-5",
  "messages":[
    {"role":"system","content":"Never disclose secrets."},
    {"role":"user","content":"Look up both records."},
    {"role":"assistant","content":"","tool_calls":[
      {"id":"call-a","type":"function","function":{"name":"lookup","arguments":"{\"id\":1}"}},
      {"id":"call-b","type":"function","function":{"name":"lookup","arguments":"{\"id\":2}"}}
    ]},
    {"role":"tool","tool_call_id":"call-a","content":"first"},
    {"role":"tool","tool_call_id":"call-b","content":"second"},
    {"role":"user","content":"Summarize the results."}
  ],
  "tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],
  "response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object"}}},
  "max_completion_tokens":0
}`)

	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)
	require.NotNil(t, envelope)
	assert.Equal(t, "gpt-5", envelope.OriginalModel)
	require.NotNil(t, envelope.RequestedMaxOutput)
	assert.Zero(t, *envelope.RequestedMaxOutput)
	assert.Len(t, envelope.ImmutableInstructions, 1)
	assert.Len(t, envelope.CompressibleSegments, 1)
	assert.Len(t, envelope.ToolState.Exchanges, 2)
	assert.NotEmpty(t, envelope.ToolState.SchemaDigest)
	for _, exchange := range envelope.ToolState.Exchanges {
		assert.Equal(t, ToolExchangeCompleted, exchange.Status)
		assert.True(t, exchange.RawCallPresent)
		assert.True(t, exchange.RawResultPresent)
		assert.NotEmpty(t, exchange.ArgumentsDigest)
		assert.NotEmpty(t, exchange.ResultDigest)
	}
	assert.True(t, envelope.SchemaState.Present)
	assert.True(t, envelope.SchemaState.Strict)
	assert.NotEmpty(t, envelope.SourceDigest)
	assert.NotContains(t, envelope.SourceDigest, "Never disclose secrets")
}

func TestExtractChatCompletionsRejectsOpenToolCall(t *testing.T) {
	body := []byte(`{
  "model":"gpt-5",
  "messages":[
    {"role":"user","content":"Look it up."},
    {"role":"assistant","tool_calls":[{"id":"call-open","type":"function","function":{"name":"lookup","arguments":"{}"}}]}
  ]
}`)

	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NotNil(t, envelope)
	require.Error(t, err)
	var validationErr *ValidationError
	require.True(t, errors.As(err, &validationErr))
	require.Len(t, validationErr.Issues, 1)
	assert.Equal(t, "missing_tool_result", validationErr.Issues[0].Code)
}

func TestExtractResponsesDetectsStateBindingAndToolGraph(t *testing.T) {
	body := []byte(`{
  "model":"gpt-5",
  "instructions":"Keep the output concise.",
  "previous_response_id":"resp-secret-reference",
  "conversation":{"id":"conv-secret-reference"},
	"prompt":{"id":"prompt-secret-reference"},
  "input":[
    {"role":"user","content":[{"type":"input_text","text":"lookup"}]},
    {"type":"function_call","call_id":"call-1","name":"lookup","arguments":"{\"q\":\"x\"}"},
    {"type":"function_call_output","call_id":"call-1","output":{"ok":true}},
    {"role":"user","content":[{"type":"input_file","file_id":"file-secret-reference"}]}
  ],
  "tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
	"text":{"format":{"type":"json_schema","name":"result","strict":true,"schema":{"type":"object"}}},
	"max_output_tokens":0
}`)

	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAIResponses, Body: body})
	require.NoError(t, err)
	assert.Equal(t, BindingLevelCredential, envelope.ProviderBinding.BindingLevel)
	assert.False(t, envelope.RoutingConstraint(100).SwitchAllowed)
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "responses_previous_response_id")
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "responses_conversation")
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "responses_prompt")
	assert.NotContains(t, envelope.ProviderBinding.StateReferenceHashes, "resp-secret-reference")
	assert.Len(t, envelope.ProviderBinding.StateReferenceHashes, 3)
	assert.Len(t, envelope.ToolState.Exchanges, 1)
	assert.Equal(t, ToolExchangeCompleted, envelope.ToolState.Exchanges[0].Status)
	assert.True(t, envelope.SchemaState.Present)
	assert.True(t, envelope.SchemaState.Strict)
	assert.Equal(t, 1, envelope.MediaState.FileCount)
	assert.Equal(t, 1, envelope.MediaState.ProviderBoundCount)
	require.NotNil(t, envelope.RequestedMaxOutput)
	assert.Zero(t, *envelope.RequestedMaxOutput)
}

func TestExtractResponsesPinsHostedToolDefinitions(t *testing.T) {
	body := []byte(`{
  "model":"gpt-5",
  "input":"search",
  "tools":[{"type":"mcp","server_label":"internal","server_url":"https://example.invalid"}]
}`)

	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAIResponses, Body: body})
	require.NoError(t, err)
	assert.Equal(t, BindingLevelProvider, envelope.ProviderBinding.BindingLevel)
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "responses_hosted_tool_definition")
}

func TestExtractClaudeDetectsOpaqueSignatureAndOutputSchema(t *testing.T) {
	body := []byte(`{
  "model":"claude-sonnet-4",
  "system":"Follow the policy.",
  "container":"container-secret-reference",
  "messages":[
    {"role":"user","content":"lookup"},
    {"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"lookup","input":{"q":"x"},"signature":"opaque-secret-signature"}]},
    {"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":{"ok":true}}]},
    {"role":"user","content":"summarize"}
  ],
  "tools":[{"name":"lookup","input_schema":{"type":"object"}}],
  "output_format":{"type":"json_schema","schema":{"type":"object"}},
  "max_tokens":256
}`)

	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatClaude, Body: body})
	require.NoError(t, err)
	assert.Equal(t, BindingLevelCredential, envelope.ProviderBinding.BindingLevel)
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "claude_container")
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "claude_thinking_signature")
	assert.Len(t, envelope.ImmutableInstructions, 1)
	assert.Len(t, envelope.ToolState.Exchanges, 1)
	assert.True(t, envelope.ToolState.Exchanges[0].OpaqueStatePresent)
	assert.True(t, envelope.SchemaState.Present)
	require.NotNil(t, envelope.RequestedMaxOutput)
	assert.Equal(t, uint(256), *envelope.RequestedMaxOutput)
	assert.Equal(t, SegmentKindToolResult, envelope.PreservedSegments[1].Kind)
}

func TestExtractClaudeDoesNotTreatEffortAsOutputSchema(t *testing.T) {
	body := []byte(`{
  "model":"claude-sonnet-4",
  "messages":[{"role":"user","content":"answer"}],
  "output_config":{"effort":"high"},
  "max_tokens":0
}`)

	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatClaude, Body: body})
	require.NoError(t, err)
	assert.False(t, envelope.SchemaState.Present)
	require.NotNil(t, envelope.RequestedMaxOutput)
	assert.Zero(t, *envelope.RequestedMaxOutput)
}

func TestExtractGeminiPinsCachedContentAndAmbiguousParallelCalls(t *testing.T) {
	body := []byte(`{
  "cachedContent":"cached-secret-reference",
  "systemInstruction":{"role":"system","parts":[{"text":"Follow policy"}]},
  "contents":[
    {"role":"user","parts":[{"text":"lookup both"}]},
    {"role":"model","parts":[
      {"functionCall":{"name":"lookup","args":{"id":1}},"thoughtSignature":"opaque-signature-one"},
      {"functionCall":{"name":"lookup","args":{"id":2}},"thoughtSignature":"opaque-signature-two"}
    ]},
    {"role":"user","parts":[
      {"functionResponse":{"name":"lookup","response":{"id":1}}},
      {"functionResponse":{"name":"lookup","response":{"id":2}}}
    ]},
    {"role":"user","parts":[{"fileData":{"mimeType":"application/pdf","fileUri":"files/provider-secret"}}]}
  ],
  "generationConfig":{"maxOutputTokens":0,"responseJsonSchema":{"type":"object"}}
}`)

	envelope, err := Extract(ExtractionRequest{
		Protocol:      types.RelayFormatGemini,
		OriginalModel: "gemini-2.5-pro",
		Body:          body,
	})
	require.NoError(t, err)
	assert.Equal(t, "gemini-2.5-pro", envelope.OriginalModel)
	assert.Equal(t, BindingLevelCredential, envelope.ProviderBinding.BindingLevel)
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "gemini_cached_content")
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "gemini_thought_signature")
	assert.Contains(t, envelope.ProviderBinding.ReasonCodes, "gemini_ambiguous_parallel_function_calls")
	assert.Equal(t, []string{"lookup"}, envelope.ToolState.AmbiguousFunctionNames)
	assert.Len(t, envelope.ToolState.Exchanges, 2)
	for _, exchange := range envelope.ToolState.Exchanges {
		assert.Equal(t, ToolExchangeCompleted, exchange.Status)
	}
	assert.Equal(t, SegmentKindToolResult, envelope.PreservedSegments[1].Kind)
	assert.Equal(t, 1, envelope.MediaState.FileCount)
	assert.Equal(t, 1, envelope.MediaState.ProviderBoundCount)
	assert.True(t, envelope.SchemaState.Present)
	require.NotNil(t, envelope.RequestedMaxOutput)
	assert.Zero(t, *envelope.RequestedMaxOutput)
}

func TestExtractRejectsUnsupportedProtocol(t *testing.T) {
	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormat("unknown"), Body: []byte(`{}`)})
	assert.Nil(t, envelope)
	require.ErrorContains(t, err, "unsupported context envelope protocol")
}
