package contextconsensus

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const chatRewriteBody = `{
  "model":"gpt-5",
  "messages":[
    {"role":"system","content":"system invariant"},
    {"role":"developer","content":"developer invariant"},
    {"role":"user","content":"old user"},
    {"role":"assistant","content":"old assistant"},
    {"role":"user","content":"recent user"},
    {"role":"assistant","content":"recent assistant"},
    {"role":"user","content":"current user"}
  ],
  "response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"}}},
  "max_completion_tokens":0
}`

const responsesRewriteBody = `{
  "model":"gpt-5",
  "instructions":"developer invariant",
  "input":[
    {"type":"message","role":"user","content":[{"type":"input_text","text":"old user"}]},
    {"type":"message","role":"assistant","content":[{"type":"output_text","text":"old assistant"}]},
    {"type":"message","role":"user","content":[{"type":"input_text","text":"recent user"}]},
    {"type":"message","role":"assistant","content":[{"type":"output_text","text":"recent assistant"}]},
    {"type":"message","role":"user","content":[{"type":"input_text","text":"current user"}]}
  ],
  "text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"}}},
  "max_output_tokens":0
}`

const claudeRewriteBody = `{
  "model":"claude-sonnet-4",
  "system":"system invariant",
  "messages":[
    {"role":"user","content":"old user"},
    {"role":"assistant","content":"old assistant"},
    {"role":"user","content":"recent user"},
    {"role":"assistant","content":"recent assistant"},
    {"role":"user","content":"current user"}
  ],
  "output_format":{"type":"json_schema","schema":{"type":"object"}},
  "max_tokens":0
}`

const geminiRewriteBody = `{
  "systemInstruction":{"role":"system","parts":[{"text":"system invariant"}]},
  "contents":[
    {"role":"user","parts":[{"text":"old user"}]},
    {"role":"model","parts":[{"text":"old assistant"}]},
    {"role":"user","parts":[{"text":"recent user"}]},
    {"role":"model","parts":[{"text":"recent assistant"}]},
    {"role":"user","parts":[{"text":"current user"}]}
  ],
  "generationConfig":{"responseJsonSchema":{"type":"object"},"maxOutputTokens":0}
}`

func TestRewriteRequestWithConsensusPreservesProtocolInvariants(t *testing.T) {
	tests := []struct {
		name     string
		protocol types.RelayFormat
		body     string
		verify   func(*testing.T, []byte)
	}{
		{name: "Chat Completions", protocol: types.RelayFormatOpenAI, body: chatRewriteBody, verify: verifyRewrittenChat},
		{name: "Responses", protocol: types.RelayFormatOpenAIResponses, body: responsesRewriteBody, verify: verifyRewrittenResponses},
		{name: "Claude", protocol: types.RelayFormatClaude, body: claudeRewriteBody, verify: verifyRewrittenClaude},
		{name: "Gemini", protocol: types.RelayFormatGemini, body: geminiRewriteBody, verify: verifyRewrittenGemini},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(test.body)
			plan := testCompactionPlan(t, test.protocol, test.body)
			summaryBody, err := common.Marshal(validConsensusSummary(t, plan))
			require.NoError(t, err)

			rewritten, err := RewriteRequestWithConsensus(RewriteCompactedRequest{
				Protocol:    test.protocol,
				Body:        body,
				Plan:        plan,
				SummaryBody: summaryBody,
			})
			require.NoError(t, err)
			assert.NotContains(t, string(rewritten), "old user")
			assert.NotContains(t, string(rewritten), "old assistant")
			assert.Contains(t, string(rewritten), "current user")
			test.verify(t, rewritten)
		})
	}
}

func TestRewriteRequestWithConsensusRejectsTamperedPlan(t *testing.T) {
	body := []byte(chatRewriteBody)
	plan := testCompactionPlan(t, types.RelayFormatOpenAI, chatRewriteBody)
	plan.CoveredSegments = append([]ContextSegment(nil), plan.ImmutableSegments...)
	plan.CoveredRanges = []SummarySourceRange{summarySourceRange(plan.CoveredSegments)}
	plan.SummaryInsertBefore = plan.CoveredSegments[0].Sequence

	summary := validConsensusSummary(t, plan)
	summary.TaskGoal = []ConsensusFact{}
	summaryBody, err := common.Marshal(summary)
	require.NoError(t, err)

	_, err = RewriteRequestWithConsensus(RewriteCompactedRequest{
		Protocol:    types.RelayFormatOpenAI,
		Body:        body,
		Plan:        plan,
		SummaryBody: summaryBody,
	})
	require.ErrorContains(t, err, "integrity check failed")
}

func testCompactionPlan(t *testing.T, protocol types.RelayFormat, bodyText string) CompactionPlan {
	t.Helper()
	body := []byte(bodyText)
	envelope, err := Extract(ExtractionRequest{Protocol: protocol, OriginalModel: "gemini-2.5-pro", Body: body})
	require.NoError(t, err)
	plan, err := BuildCompactionPlan(CompactionPlanRequest{
		Protocol: protocol,
		Body:     body,
		Envelope: envelope,
		Policy:   enabledCompactionPolicy(1),
	})
	require.NoError(t, err)
	return plan
}

func verifyRewrittenChat(t *testing.T, body []byte) {
	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(body, &request))
	require.Len(t, request.Messages, 6)
	assert.Equal(t, "system", request.Messages[0].Role)
	assert.Equal(t, "system invariant", request.Messages[0].Content)
	assert.Equal(t, "developer", request.Messages[1].Role)
	assert.Equal(t, "developer invariant", request.Messages[1].Content)
	assert.Equal(t, "user", request.Messages[2].Role)
	assert.True(t, strings.HasPrefix(request.Messages[2].Content.(string), consensusSummaryPreamble))
	assert.Equal(t, "current user", request.Messages[5].Content)
	require.NotNil(t, request.ResponseFormat)
	require.NotNil(t, request.MaxCompletionTokens)
	assert.Zero(t, *request.MaxCompletionTokens)
}

func verifyRewrittenResponses(t *testing.T, body []byte) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal(body, &request))
	var inputItems []map[string]any
	require.NoError(t, common.Unmarshal(request.Input, &inputItems))
	require.Len(t, inputItems, 4)
	assert.Equal(t, "user", inputItems[0]["role"])
	content := inputItems[0]["content"].([]any)
	assert.True(t, strings.HasPrefix(content[0].(map[string]any)["text"].(string), consensusSummaryPreamble))
	assert.Equal(t, `"developer invariant"`, string(request.Instructions))
	assert.NotEmpty(t, request.Text)
	require.NotNil(t, request.MaxOutputTokens)
	assert.Zero(t, *request.MaxOutputTokens)
}

func verifyRewrittenClaude(t *testing.T, body []byte) {
	var request dto.ClaudeRequest
	require.NoError(t, common.Unmarshal(body, &request))
	require.Len(t, request.Messages, 3)
	assert.Equal(t, "system invariant", request.System)
	content := request.Messages[0].Content.([]any)
	assert.True(t, strings.HasPrefix(content[0].(map[string]any)["text"].(string), consensusSummaryPreamble))
	assert.Equal(t, "recent user", content[1].(map[string]any)["text"])
	assert.Equal(t, []string{"user", "assistant", "user"}, []string{request.Messages[0].Role, request.Messages[1].Role, request.Messages[2].Role})
	assert.NotEmpty(t, request.OutputFormat)
	require.NotNil(t, request.MaxTokens)
	assert.Zero(t, *request.MaxTokens)
}

func verifyRewrittenGemini(t *testing.T, body []byte) {
	var request dto.GeminiChatRequest
	require.NoError(t, common.Unmarshal(body, &request))
	require.Len(t, request.Contents, 3)
	require.NotNil(t, request.SystemInstructions)
	assert.Equal(t, "system invariant", request.SystemInstructions.Parts[0].Text)
	assert.True(t, strings.HasPrefix(request.Contents[0].Parts[0].Text, consensusSummaryPreamble))
	assert.Equal(t, "recent user", request.Contents[0].Parts[1].Text)
	assert.Equal(t, []string{"user", "model", "user"}, []string{request.Contents[0].Role, request.Contents[1].Role, request.Contents[2].Role})
	assert.NotNil(t, request.GenerationConfig.ResponseJsonSchema)
	require.NotNil(t, request.GenerationConfig.MaxOutputTokens)
	assert.Zero(t, *request.GenerationConfig.MaxOutputTokens)
}
