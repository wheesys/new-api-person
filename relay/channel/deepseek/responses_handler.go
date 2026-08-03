package deepseek

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// dsmlToolCallID 生成一个本地唯一的 tool_call ID（call_ 前缀，符合上游 chat 习惯；
// 后续 Chat→Responses 转换会派生 fc_ 前缀的 item id）。
func dsmlToolCallID(idx int) string {
	return fmt.Sprintf("call_dsml_%d_%d", time.Now().UnixNano(), idx)
}

// injectDsmlToolCallsIntoMessage 把解析出的 DSML 工具调用追加到 chat message.ToolCalls。
func injectDsmlToolCallsIntoMessage(msg *dto.Message, calls []DsmlToolCall) {
	existing := msg.ParseToolCalls()
	for i, call := range calls {
		argsJSON, err := DsmlArgumentsToJSON(call.Arguments)
		if err != nil {
			common.SysError("failed to marshal dsml arguments: " + err.Error())
			continue
		}
		existing = append(existing, dto.ToolCallRequest{
			ID:   dsmlToolCallID(i),
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      call.Name,
				Arguments: argsJSON,
			},
		})
	}
	msg.SetToolCalls(existing)
}

// injectDsmlIntoChatResponse 扫描非流式 chat 响应，把 content 里的 DSML 块
// 解析成 tool_calls 注入 message，content 设为剥离 DSML 后的剩余文本，
// finish_reason 调整为 tool_calls。
func injectDsmlIntoChatResponse(chatResp *dto.OpenAITextResponse) {
	for i := range chatResp.Choices {
		content := chatResp.Choices[i].Message.StringContent()
		if !HasDsmlMarker(content) {
			continue
		}
		calls, remaining, found := ParseDsmlToolCalls(content)
		if !found {
			continue
		}
		injectDsmlToolCallsIntoMessage(&chatResp.Choices[i].Message, calls)
		chatResp.Choices[i].Message.SetStringContent(remaining)
		if chatResp.Choices[i].FinishReason == "stop" || chatResp.Choices[i].FinishReason == "" {
			chatResp.Choices[i].FinishReason = "tool_calls"
		}
	}
}

// DeepSeekChatToResponsesHandler 处理 DeepSeek 非流式 chat 响应：先解析 content 里的
// DSML 工具调用并注入 tool_calls，再委托标准 Chat→Responses 转换。
func DeepSeekChatToResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err == nil {
		injectDsmlIntoChatResponse(&chatResp)
		if modified, mErr := common.Marshal(chatResp); mErr == nil {
			resp.Body = io.NopCloser(bytes.NewReader(modified))
			resp.ContentLength = int64(len(modified))
			resp.Header.Set("Content-Length", strconv.Itoa(len(modified)))
		} else {
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}
	} else {
		// 非 chat 格式，原样委托（由标准 handler 报错或处理）
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}
	return openai.OaiChatToResponsesHandler(c, info, resp)
}

// newDsmlContentStreamChunk 构造一个只含 content delta 的独立 stream chunk。
func newDsmlContentStreamChunk(id, model string, choiceIndex int, content string) *dto.ChatCompletionsStreamResponse {
	c := content
	return &dto.ChatCompletionsStreamResponse{
		Id: id, Model: model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: choiceIndex,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &c},
		}},
	}
}

// newDsmlToolCallStreamChunk 构造一个含完整 tool_call 的独立 stream chunk。
func newDsmlToolCallStreamChunk(id, model string, choiceIndex, toolIdx int, call DsmlToolCall) (*dto.ChatCompletionsStreamResponse, error) {
	argsJSON, err := DsmlArgumentsToJSON(call.Arguments)
	if err != nil {
		return nil, err
	}
	idx := toolIdx
	return &dto.ChatCompletionsStreamResponse{
		Id: id, Model: model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: choiceIndex,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
				ToolCalls: []dto.ToolCallResponse{{
					Index: &idx,
					ID:    dsmlToolCallID(toolIdx),
					Type:  "function",
					Function: dto.FunctionResponse{
						Name:      call.Name,
						Arguments: argsJSON,
					},
				}},
			},
		}},
	}, nil
}

// DeepSeekChatToResponsesStreamHandler 处理 DeepSeek 流式 chat 响应：用 DsmlStreamAccumulator
// 实时透传非 DSML 文本，DSML 块闭合时合成 tool_call delta，再走标准 Chat→Responses 流式转换。
func DeepSeekChatToResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	responseID := helper.GetResponseID(c)
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID:    responseID,
		Model: info.UpstreamModelName,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	var acc DsmlStreamAccumulator
	toolCallIdx := 0
	seq := 0
	streamErr := (*types.NewAPIError)(nil)

	sendEvent := func(event relayconvert.ChatToResponsesStreamEvent) bool {
		seq++
		event.Payload.SequenceNumber = seq
		data, err := common.Marshal(event.Payload)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, string(data))
		return true
	}
	convertAndSend := func(chunk *dto.ChatCompletionsStreamResponse) bool {
		results, err := relayconvert.ConvertStreamResponseChunk(c, info, state, chunk)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
		for _, result := range results {
			event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
			if !ok {
				streamErr = types.NewOpenAIError(fmt.Errorf("expected responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
				return false
			}
			if !sendEvent(event) {
				return false
			}
		}
		return true
	}
	flushAccumulated := func() bool {
		textDelta, toolCalls := acc.Flush()
		if textDelta != "" {
			if !convertAndSend(newDsmlContentStreamChunk(responseID, info.UpstreamModelName, 0, textDelta)) {
				return false
			}
		}
		for _, tc := range toolCalls {
			chunk, err := newDsmlToolCallStreamChunk(responseID, info.UpstreamModelName, 0, toolCallIdx, tc)
			if err != nil {
				continue
			}
			toolCallIdx++
			if !convertAndSend(chunk) {
				return false
			}
		}
		return true
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}
		var errorResp dto.OpenAITextResponse
		if err := common.UnmarshalJsonStr(data, &errorResp); err == nil {
			if oaiError := errorResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
				streamErr = types.WithOpenAIError(*oaiError, resp.StatusCode)
				sr.Stop(streamErr)
				return
			}
		}
		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			logger.LogError(c, "failed to unmarshal chat stream response: "+err.Error())
			sr.Error(err)
			return
		}

		// 先处理 content delta：喂累加器，得到实时文本 + 闭合的 DSML 工具调用
		var textDelta string
		var toolCalls []DsmlToolCall
		if len(chunk.Choices) > 0 {
			content := chunk.Choices[0].Delta.GetContentString()
			if content != "" {
				chunk.Choices[0].Delta.SetContentString("") // 清空原 content，避免重复
				textDelta, toolCalls = acc.Feed(content)
			}
		}
		// 先转原 chunk（role/finish_reason/usage 等，content 已清空）
		if !convertAndSend(&chunk) {
			sr.Stop(streamErr)
			return
		}
		// 再发实时文本 delta
		if textDelta != "" {
			if !convertAndSend(newDsmlContentStreamChunk(responseID, info.UpstreamModelName, 0, textDelta)) {
				sr.Stop(streamErr)
				return
			}
		}
		// 再发 DSML 工具调用
		for _, tc := range toolCalls {
			tcChunk, err := newDsmlToolCallStreamChunk(responseID, info.UpstreamModelName, 0, toolCallIdx, tc)
			if err != nil {
				continue
			}
			toolCallIdx++
			if !convertAndSend(tcChunk) {
				sr.Stop(streamErr)
				return
			}
		}
	})

	if streamErr != nil {
		return nil, streamErr
	}

	if !flushAccumulated() {
		return nil, streamErr
	}

	usage := state.Usage()
	if usage == nil || usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		state.SetUsage(usage)
	}
	finalResults, err := relayconvert.FinalizeStreamResponse(c, info, state)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	for _, result := range finalResults {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			return nil, types.NewOpenAIError(fmt.Errorf("expected responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		if !sendEvent(event) {
			return nil, streamErr
		}
	}
	return usage, nil
}
