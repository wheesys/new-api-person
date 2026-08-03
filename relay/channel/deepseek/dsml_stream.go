package deepseek

import (
	"strings"
	"unicode/utf8"
)

// DsmlStreamAccumulator 跨 SSE chunk 缓冲 content，检测并解析跨 chunk
// 边界的 DSML tool_calls 块。非 DSML 文本实时返回输出；DSML 块在闭合时
// 一次性返回解析出的工具调用。
//
// 设计要点：
//   - 非 DSML 段：每次只输出"不可能是 dsmlStart 前缀"的安全前缀（对齐 rune
//     边界，避免截断多字节字符），尾部保留到下次拼接，防止 start 标记被拆分。
//   - DSML 段：进入后缓冲直到 dsmlToolCallsEnd 出现，再整体解析。
//   - 多块、块前后文本均支持；未闭合块在 Flush 时降级为文本。
type DsmlStreamAccumulator struct {
	buffer strings.Builder
	inDsml bool
}

// Feed 处理一个 content delta，返回可立即输出的非 DSML 文本，以及若某个
// DSML 块刚好闭合时解析出的工具调用。
func (a *DsmlStreamAccumulator) Feed(delta string) (textDelta string, toolCalls []DsmlToolCall) {
	a.buffer.WriteString(delta)
	return a.drain()
}

// Flush 在流结束时调用，返回剩余缓冲内容。未闭合的 DSML 块当作文本返回。
func (a *DsmlStreamAccumulator) Flush() (textDelta string, toolCalls []DsmlToolCall) {
	s := a.buffer.String()
	a.buffer.Reset()
	a.inDsml = false
	return s, nil
}

func (a *DsmlStreamAccumulator) drain() (textDelta string, toolCalls []DsmlToolCall) {
	for {
		s := a.buffer.String()
		if !a.inDsml {
			startIdx := strings.Index(s, dsmlToolCallsStart)
			if startIdx < 0 {
				// 无完整 start：输出安全前缀，保留可能是 start 前缀的尾部
				safe := safeTextLen(s, len(dsmlToolCallsStart))
				if safe > 0 {
					textDelta += s[:safe]
					a.buffer.Reset()
					a.buffer.WriteString(s[safe:])
				}
				return textDelta, toolCalls
			}
			// 输出 start 前的文本，进入 DSML 模式
			textDelta += s[:startIdx]
			a.buffer.Reset()
			a.buffer.WriteString(s[startIdx:])
			a.inDsml = true
			continue
		}
		// DSML 模式：找 end
		endIdx := strings.Index(s, dsmlToolCallsEnd)
		if endIdx < 0 {
			return textDelta, toolCalls // 未闭合，继续缓冲
		}
		endAbs := endIdx + len(dsmlToolCallsEnd)
		block := s[:endAbs]
		if calls, _, found := ParseDsmlToolCalls(block); found {
			toolCalls = append(toolCalls, calls...)
		}
		a.buffer.Reset()
		a.buffer.WriteString(s[endAbs:])
		a.inDsml = false
	}
}

// safeTextLen 返回 s 中可安全输出（不可能是 dsmlStart 前缀）的字节数，
// 对齐到 rune 边界，避免截断多字节字符。keepPrefix 是需保留的 dsmlStart 字节长度。
func safeTextLen(s string, keepPrefix int) int {
	max := len(s) - keepPrefix + 1
	if max <= 0 {
		return 0
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return max
}
