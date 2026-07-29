package contextconsensus

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeManagedAssistantOutputSupportsFourPortableTextProtocols(t *testing.T) {
	tests := []struct {
		name     string
		protocol types.RelayFormat
		body     string
		finish   string
	}{
		{name: "chat", protocol: types.RelayFormatOpenAI, body: `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`, finish: "stop"},
		{name: "responses", protocol: types.RelayFormatOpenAIResponses, body: `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}`, finish: "completed"},
		{name: "claude", protocol: types.RelayFormatClaude, body: `{"type":"message","role":"assistant","content":[{"type":"text","text":"hel"},{"type":"text","text":"lo"}],"stop_reason":"end_turn"}`, finish: "end_turn"},
		{name: "gemini", protocol: types.RelayFormatGemini, body: `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`, finish: "STOP"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := NormalizeManagedAssistantOutput(test.protocol, []byte(test.body))
			require.NoError(t, err)
			assert.Equal(t, "hello", output.Text)
			assert.Equal(t, test.finish, output.FinishReason)
			assert.Equal(t, test.protocol, output.Protocol)
			assert.NotEmpty(t, output.ResponseDigest)
		})
	}
}

func TestNormalizeManagedAssistantOutputRejectsNonPortableOrAmbiguousOutput(t *testing.T) {
	tests := []struct {
		name     string
		protocol types.RelayFormat
		body     string
	}{
		{name: "chat tool call", protocol: types.RelayFormatOpenAI, body: `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call"}]},"finish_reason":"tool_calls"}]}`},
		{name: "responses function call", protocol: types.RelayFormatOpenAIResponses, body: `{"status":"completed","output":[{"type":"function_call","call_id":"call","name":"lookup","arguments":"{}"}]}`},
		{name: "claude thinking", protocol: types.RelayFormatClaude, body: `{"type":"message","role":"assistant","content":[{"type":"thinking","thinking":"hidden","signature":"opaque"}]}`},
		{name: "gemini multiple candidates", protocol: types.RelayFormatGemini, body: `{"candidates":[{"content":{"parts":[{"text":"a"}]}},{"content":{"parts":[{"text":"b"}]}}]}`},
		{name: "chat truncated", protocol: types.RelayFormatOpenAI, body: `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}]}`},
		{name: "responses missing status", protocol: types.RelayFormatOpenAIResponses, body: `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}]}`},
		{name: "claude truncated", protocol: types.RelayFormatClaude, body: `{"type":"message","role":"assistant","content":[{"type":"text","text":"partial"}],"stop_reason":"max_tokens"}`},
		{name: "gemini safety stop", protocol: types.RelayFormatGemini, body: `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"SAFETY"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeManagedAssistantOutput(test.protocol, []byte(test.body))
			require.Error(t, err)
		})
	}
}
