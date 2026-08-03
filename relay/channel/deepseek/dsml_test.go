package deepseek

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dsmlBlock 拼接一个完整 DSML tool_calls 块（用包内常量，避免手写全角竖线出错）。
func dsmlBlock(invokes ...string) string {
	return dsmlToolCallsStart + strings.Join(invokes, "") + dsmlToolCallsEnd
}

func dsmlInvoke(name string, params ...string) string {
	return dsmlInvokeStart + ` name="` + name + `">` + strings.Join(params, "") + dsmlInvokeEnd
}

func dsmlParam(name, value string) string {
	return dsmlParamStart + ` name="` + name + `" string="true">` + value + dsmlParamEnd
}

func TestParseDsmlToolCalls_SingleInvokeSingleParam(t *testing.T) {
	content := dsmlBlock(dsmlInvoke("exec_command", dsmlParam("cmd", "ls -la")))
	calls, remaining, found := ParseDsmlToolCalls(content)

	require.True(t, found)
	require.Len(t, calls, 1)
	assert.Equal(t, "exec_command", calls[0].Name)
	assert.Equal(t, "ls -la", calls[0].Arguments["cmd"])
	assert.Equal(t, "", remaining)
}

func TestParseDsmlToolCalls_MultiInvokeMultiParam(t *testing.T) {
	content := dsmlBlock(
		dsmlInvoke("write_file", dsmlParam("path", "/tmp/test"), dsmlParam("content", "hello")),
		dsmlInvoke("read_file", dsmlParam("path", "/tmp/out")),
	)
	calls, remaining, found := ParseDsmlToolCalls(content)

	require.True(t, found)
	require.Len(t, calls, 2)
	assert.Equal(t, "write_file", calls[0].Name)
	assert.Equal(t, "/tmp/test", calls[0].Arguments["path"])
	assert.Equal(t, "hello", calls[0].Arguments["content"])
	assert.Equal(t, "read_file", calls[1].Name)
	assert.Equal(t, "/tmp/out", calls[1].Arguments["path"])
	assert.Equal(t, "", remaining)
}

func TestParseDsmlToolCalls_PreservesSurroundingText(t *testing.T) {
	content := "before" + dsmlBlock(dsmlInvoke("exec_command", dsmlParam("cmd", "ls"))) + "after"
	calls, remaining, found := ParseDsmlToolCalls(content)

	require.True(t, found)
	require.Len(t, calls, 1)
	assert.Equal(t, "beforeafter", remaining)
}

func TestParseDsmlToolCalls_ValueWithNewlineAndSpecialChars(t *testing.T) {
	value := "line1\nline2\ttab \"quote\" <tag> &"
	content := dsmlBlock(dsmlInvoke("write_file", dsmlParam("content", value)))
	calls, _, found := ParseDsmlToolCalls(content)

	require.True(t, found)
	require.Len(t, calls, 1)
	assert.Equal(t, value, calls[0].Arguments["content"])
}

func TestParseDsmlToolCalls_NoMarker(t *testing.T) {
	content := "just regular model output, no DSML here"
	calls, remaining, found := ParseDsmlToolCalls(content)

	require.False(t, found)
	assert.Nil(t, calls)
	assert.Equal(t, content, remaining) // 原样返回
}

func TestParseDsmlToolCalls_UnclosedBlock(t *testing.T) {
	// 只有起始标记，无结束标记
	content := dsmlToolCallsStart + dsmlInvoke("exec_command", dsmlParam("cmd", "ls"))
	calls, remaining, found := ParseDsmlToolCalls(content)

	require.False(t, found) // 未闭合降级
	assert.Nil(t, calls)
	assert.Equal(t, content, remaining) // 原样返回
}

func TestParseDsmlToolCalls_MultipleBlocks(t *testing.T) {
	block1 := dsmlBlock(dsmlInvoke("a", dsmlParam("x", "1")))
	block2 := dsmlBlock(dsmlInvoke("b", dsmlParam("y", "2")))
	content := block1 + "middle" + block2
	calls, remaining, found := ParseDsmlToolCalls(content)

	require.True(t, found)
	require.Len(t, calls, 2)
	assert.Equal(t, "a", calls[0].Name)
	assert.Equal(t, "b", calls[1].Name)
	assert.Equal(t, "middle", remaining)
}

func TestHasDsmlMarker(t *testing.T) {
	assert.True(t, HasDsmlMarker("text"+dsmlToolCallsStart+"more"))
	assert.False(t, HasDsmlMarker("plain text without dsml"))
}

func TestDsmlArgumentsToJSON(t *testing.T) {
	out, err := DsmlArgumentsToJSON(map[string]string{"cmd": "ls", "flag": "-la"})
	require.NoError(t, err)
	// Go json.Marshal 对 map 的字符串键按字典序输出
	assert.Equal(t, `{"cmd":"ls","flag":"-la"}`, out)

	out2, err := DsmlArgumentsToJSON(map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "{}", out2)
}
