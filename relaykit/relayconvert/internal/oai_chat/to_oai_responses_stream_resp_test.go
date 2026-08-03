package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamReasoningAddedCarriesItemID guards the fix where the
// response.output_item.added event for a reasoning item must carry the top-level
// item_id. Codex reports "ReasoningSummaryDelta without active item" when the
// reasoning item's added event omits item_id while the subsequent
// reasoning_summary_text.delta carries one, so the client cannot bind the delta.
func TestStreamReasoningAddedCarriesItemID(t *testing.T) {
	state := NewChatToResponsesStreamState("chatcmpl_1", "gpt-test")
	chunk := &dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: loP("some reasoning")}},
		},
	}

	events, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
	require.NoError(t, err)

	// First call also emits response.created; locate the output_item.added.
	require.GreaterOrEqual(t, len(events), 3)
	require.Equal(t, responsesEventCreated, events[0].Type)

	added := events[1]
	require.Equal(t, responsesEventOutputItemAdded, added.Type)
	assert.Equal(t, state.reasoningID(), added.Payload.ItemID, "reasoning output_item.added must carry top-level item_id")
	require.NotNil(t, added.Payload.Item)
	assert.Equal(t, responsesOutputTypeReasoning, added.Payload.Item.Type)
	assert.Equal(t, state.reasoningID(), added.Payload.Item.ID)

	delta := events[2]
	assert.Equal(t, responsesEventReasoningSummaryDelta, delta.Type)
	assert.Equal(t, state.reasoningID(), delta.Payload.ItemID)
}

// TestStreamTextAddedCarriesItemID guards the same contract for the message
// (output_text) item, which must also expose the top-level item_id on added.
func TestStreamTextAddedCarriesItemID(t *testing.T) {
	state := NewChatToResponsesStreamState("chatcmpl_1", "gpt-test")
	chunk := &dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: loP("hello")}},
		},
	}

	events, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 3)
	require.Equal(t, responsesEventCreated, events[0].Type)

	added := events[1]
	require.Equal(t, responsesEventOutputItemAdded, added.Type)
	assert.Equal(t, state.messageID(), added.Payload.ItemID, "message output_item.added must carry top-level item_id")
	require.NotNil(t, added.Payload.Item)
	assert.Equal(t, responsesOutputTypeMessage, added.Payload.Item.Type)
	assert.Equal(t, state.messageID(), added.Payload.Item.ID)
}

func loP(s string) *string { return &s }

// TestStreamReasoningClosesBeforeMessage guards the fix where a reasoning item
// must be closed (reasoning_summary_text.done + output_item.done) before a
// message item begins. Upstream chat streams may emit all reasoning deltas then
// switch to message text without a finish_reason; the converter must close the
// reasoning item at the switch, otherwise Codex reports
// "ReasoningSummaryDelta without active item".
func TestStreamReasoningClosesBeforeMessage(t *testing.T) {
	state := NewChatToResponsesStreamState("chatcmpl_1", "gpt-test")

	// Chunk 1: reasoning delta.
	_, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: loP("thinking")}},
		},
	}, state)
	require.NoError(t, err)

	// Chunk 2: message text. reasoning must be closed before message.added.
	events, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: loP("answer")}},
		},
	}, state)
	require.NoError(t, err)

	// Locate the reasoning done and message added events.
	var reasoningDoneIdx, msgAddedIdx = -1, -1
	for i, ev := range events {
		switch ev.Type {
		case responsesEventReasoningSummaryDone:
			reasoningDoneIdx = i
		case responsesEventOutputItemAdded:
			if ev.Payload.Item != nil && ev.Payload.Item.Type == responsesOutputTypeMessage {
				msgAddedIdx = i
			}
		}
	}
	require.GreaterOrEqual(t, reasoningDoneIdx, 0, "reasoning done should be emitted")
	require.GreaterOrEqual(t, msgAddedIdx, 0, "message added should be emitted")
	assert.Less(t, reasoningDoneIdx, msgAddedIdx, "reasoning must close before message begins")

	// The message added must carry its item_id too.
	msgAdded := events[msgAddedIdx]
	assert.Equal(t, state.messageID(), msgAdded.Payload.ItemID)
}

// TestStreamReasoningAndTextInSameChunk guards the boundary where a single
// upstream chunk carries both reasoning and message text. The reasoning item
// must be closed before the message item begins, still preserving order.
func TestStreamReasoningAndTextInSameChunk(t *testing.T) {
	state := NewChatToResponsesStreamState("chatcmpl_1", "gpt-test")
	events, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: loP("think"), Content: loP("answer")}},
		},
	}, state)
	require.NoError(t, err)

	var reasoningDoneIdx, msgAddedIdx = -1, -1
	for i, ev := range events {
		switch ev.Type {
		case responsesEventReasoningSummaryDone:
			reasoningDoneIdx = i
		case responsesEventOutputItemAdded:
			if ev.Payload.Item != nil && ev.Payload.Item.Type == responsesOutputTypeMessage {
				msgAddedIdx = i
			}
		}
	}
	require.GreaterOrEqual(t, reasoningDoneIdx, 0)
	require.GreaterOrEqual(t, msgAddedIdx, 0)
	assert.Less(t, reasoningDoneIdx, msgAddedIdx, "reasoning must close before message begins even in same chunk")
}
