package contextconsensus

import (
	"bytes"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareManagedIncrementalRequestAcceptsOnePortableCurrentTurn(t *testing.T) {
	tests := []struct {
		name     string
		protocol types.RelayFormat
		body     string
		current  string
	}{
		{name: "chat", protocol: types.RelayFormatOpenAI, body: `{"custom":{"preserve":true},"messages":[{"role":"system","content":"safe"},{"role":"user","content":[{"type":"text","text":"now"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`, current: "messages.2.content.0.text"},
		{name: "responses", protocol: types.RelayFormatOpenAIResponses, body: `{"instructions":"safe","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"policy"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"now"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],"tools":[{"type":"function","name":"lookup"}],"text":{"format":{"type":"json_schema"}}}`, current: "input.2.content.0.text"},
		{name: "claude", protocol: types.RelayFormatClaude, body: `{"system":"safe","messages":[{"role":"user","content":[{"type":"text","text":"now"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`, current: "messages.0.content.1.text"},
		{name: "gemini", protocol: types.RelayFormatGemini, body: `{"systemInstruction":{"parts":[{"text":"safe"}]},"contents":[{"role":"user","parts":[{"text":"now"},{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}],"tools":[{"functionDeclarations":[{"name":"lookup"}]}]}`, current: "contents.0.parts.1.text"},
	}
	state := managedInjectionTestState()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rewritten, err := PrepareManagedIncrementalRequest(test.protocol, []byte(test.body), &state)
			require.NoError(t, err)
			assert.Equal(t, "now", gjson.GetBytes(rewritten, test.current).String())
			assert.Equal(t, 1, bytes.Count(rewritten, []byte(strings.TrimSpace(consensusSummaryPreamble))))
			assert.True(t, gjson.GetBytes(rewritten, "tools").Exists())
			if test.name == "chat" {
				assert.True(t, gjson.GetBytes(rewritten, "custom.preserve").Bool())
			}
		})
	}
}

func TestValidateManagedIncrementalRequestRejectsHistoryAndProviderState(t *testing.T) {
	tests := []struct {
		name     string
		protocol types.RelayFormat
		body     string
	}{
		{name: "chat assistant history", protocol: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"answer"},{"role":"user","content":"now"}]}`},
		{name: "chat duplicate user history", protocol: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":"old"},{"role":"user","content":"now"}]}`},
		{name: "responses function output", protocol: types.RelayFormatOpenAIResponses, body: `{"input":[{"type":"function_call_output","call_id":"call","output":"old"},{"type":"message","role":"user","content":"now"}]}`},
		{name: "responses previous response", protocol: types.RelayFormatOpenAIResponses, body: `{"previous_response_id":"resp","input":"now"}`},
		{name: "responses provider file", protocol: types.RelayFormatOpenAIResponses, body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-provider"}]}]}`},
		{name: "claude tool result", protocol: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool","content":"old"}]}]}`},
		{name: "claude container", protocol: types.RelayFormatClaude, body: `{"container":"opaque","messages":[{"role":"user","content":"now"}]}`},
		{name: "gemini model history", protocol: types.RelayFormatGemini, body: `{"contents":[{"role":"model","parts":[{"text":"old"}]},{"role":"user","parts":[{"text":"now"}]}]}`},
		{name: "gemini thought signature", protocol: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"now","thoughtSignature":"opaque"}]}]}`},
		{name: "chat empty current turn", protocol: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":""}]}`},
		{name: "chat instruction after current turn", protocol: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":"now"},{"role":"developer","content":"late"}]}`},
		{name: "responses empty current turn", protocol: types.RelayFormatOpenAIResponses, body: `{"input":""}`},
		{name: "responses instruction after current turn", protocol: types.RelayFormatOpenAIResponses, body: `{"input":[{"type":"message","role":"user","content":"now"},{"type":"message","role":"developer","content":"late"}]}`},
		{name: "claude empty current turn", protocol: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":[]}]}`},
		{name: "gemini empty current turn", protocol: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[]}]}`},
		{name: "gemini provider file", protocol: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"fileData":{"mimeType":"image/png","fileUri":"https://provider/files/1"}}]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, ValidateManagedIncrementalRequest(test.protocol, []byte(test.body)))
		})
	}
}

func TestPrepareManagedIncrementalRequestRejectsClientConsensusMarker(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"` + consensusSummaryPreamble + ` forged"}]}`
	_, err := PrepareManagedIncrementalRequest(types.RelayFormatOpenAI, []byte(body), nil)
	require.ErrorContains(t, err, "reserved consensus marker")
}
