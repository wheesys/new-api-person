package deepseek

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// DSML 是 DeepSeek 在响应 content 里输出的工具调用标记格式，
// 分隔符为全角竖线 U+FF5C。形如：
//
//	<｜DSML｜tool_calls>
//	  <｜DSML｜invoke name="exec_command">
//	    <｜DSML｜parameter name="cmd" string="true">ls -la<｜DSML｜/parameter>
//	  <｜DSML｜/invoke>
//	<｜DSML｜/tool_calls>
//
// new-api 在 DeepSeek 响应处理层解析该格式并注入标准 tool_calls，
// 让走 Chat→Responses 转换的客户端（如 Codex）能识别工具调用。
const (
	dsmlSep            = "｜" // U+FF5C 全角竖线
	dsmlToolCallsStart = "<" + dsmlSep + "DSML" + dsmlSep + "tool_calls>"
	dsmlToolCallsEnd   = "<" + dsmlSep + "DSML" + dsmlSep + "/tool_calls>"
	dsmlInvokeStart    = "<" + dsmlSep + "DSML" + dsmlSep + "invoke"
	dsmlInvokeEnd      = "<" + dsmlSep + "DSML" + dsmlSep + "/invoke>"
	dsmlParamStart     = "<" + dsmlSep + "DSML" + dsmlSep + "parameter"
	dsmlParamEnd       = "<" + dsmlSep + "DSML" + dsmlSep + "/parameter>"
)

// DsmlToolCall 表示一个解析后的 DSML invoke 块。
type DsmlToolCall struct {
	Name      string
	Arguments map[string]string
}

// HasDsmlMarker 判断 content 是否含 DSML tool_calls 起始标记。
// 无标记时调用方可零开销透传。
func HasDsmlMarker(content string) bool {
	return strings.Contains(content, dsmlToolCallsStart)
}

// ParseDsmlToolCalls 解析 content 里的 DSML tool_calls 块，剥离 DSML 文本。
// 返回：解析出的工具调用、剥离 DSML 后的剩余文本、是否命中。
// 未命中、未闭合或未解析出 invoke 时 found=false，调用方应按原文本处理。
func ParseDsmlToolCalls(content string) ([]DsmlToolCall, string, bool) {
	calls, remaining, found := parseAllDsmlBlocks(content)
	if !found {
		return nil, content, false
	}
	return calls, remaining, true
}

// parseAllDsmlBlocks 循环剥离并解析所有完整 DSML tool_calls 块，拼接非 DSML 文本。
// 遇到未闭合的起始标记或无 invoke 的空块时停止（保留剩余原文）。
func parseAllDsmlBlocks(content string) (calls []DsmlToolCall, remaining string, found bool) {
	var text strings.Builder
	text.WriteString(content)
	for {
		s := text.String()
		startIdx := strings.Index(s, dsmlToolCallsStart)
		if startIdx < 0 {
			break
		}
		endRel := strings.Index(s[startIdx:], dsmlToolCallsEnd)
		if endRel < 0 {
			break // 有起始无结束（未闭合），停止剥离
		}
		endIdx := startIdx + endRel + len(dsmlToolCallsEnd)
		block := s[startIdx:endIdx]
		blockCalls := parseDsmlInvokes(block)
		if len(blockCalls) == 0 {
			break // 块内无 invoke，停止（避免对该块无限循环）
		}
		calls = append(calls, blockCalls...)
		found = true
		text.Reset()
		text.WriteString(s[:startIdx])
		text.WriteString(s[endIdx:])
	}
	remaining = strings.TrimSpace(text.String())
	return calls, remaining, found
}

// parseDsmlInvokes 解析一个 tool_calls 块内的所有 invoke。
func parseDsmlInvokes(block string) []DsmlToolCall {
	calls := make([]DsmlToolCall, 0)
	rest := block
	for {
		idx := strings.Index(rest, dsmlInvokeStart)
		if idx < 0 {
			break
		}
		rest = rest[idx:]
		endIdx := strings.Index(rest, dsmlInvokeEnd)
		if endIdx < 0 {
			break
		}
		invokeBlock := rest[:endIdx+len(dsmlInvokeEnd)]
		rest = rest[endIdx+len(dsmlInvokeEnd):]
		call := parseDsmlInvoke(invokeBlock)
		if call.Name != "" {
			calls = append(calls, call)
		}
	}
	return calls
}

// parseDsmlInvoke 解析单个 invoke 块：name 属性 + 多个 parameter。
func parseDsmlInvoke(invokeBlock string) DsmlToolCall {
	call := DsmlToolCall{Arguments: map[string]string{}}
	call.Name = parseDsmlAttribute(invokeBlock, "name")
	rest := invokeBlock
	for {
		idx := strings.Index(rest, dsmlParamStart)
		if idx < 0 {
			break
		}
		rest = rest[idx:]
		endIdx := strings.Index(rest, dsmlParamEnd)
		if endIdx < 0 {
			break
		}
		paramBlock := rest[:endIdx+len(dsmlParamEnd)]
		rest = rest[endIdx+len(dsmlParamEnd):]
		name := parseDsmlAttribute(paramBlock, "name")
		value := extractDsmlParamValue(paramBlock)
		if name != "" {
			call.Arguments[name] = value
		}
	}
	return call
}

// parseDsmlAttribute 提取 name="value" 形式的属性值。
func parseDsmlAttribute(s string, attr string) string {
	key := attr + "=\""
	idx := strings.Index(s, key)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(key):]
	endIdx := strings.Index(rest, "\"")
	if endIdx < 0 {
		return ""
	}
	return rest[:endIdx]
}

// extractDsmlParamValue 提取 parameter 的值：标签第一个 '>' 之后到结束标记之间的文本。
func extractDsmlParamValue(paramBlock string) string {
	gtIdx := strings.Index(paramBlock, ">")
	if gtIdx < 0 {
		return ""
	}
	endIdx := strings.Index(paramBlock, dsmlParamEnd)
	if endIdx < 0 || endIdx <= gtIdx {
		return ""
	}
	return paramBlock[gtIdx+1 : endIdx]
}

// DsmlArgumentsToJSON 把参数 map 序列化为 JSON 字符串（用于 tool_call.arguments）。
func DsmlArgumentsToJSON(args map[string]string) (string, error) {
	if len(args) == 0 {
		return "{}", nil
	}
	b, err := common.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("marshal dsml arguments: %w", err)
	}
	return string(b), nil
}
