package deepseek

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 纯文本透传：每次输出安全前缀（保留尾部防跨 chunk start），Flush 输出剩余。
func TestDsmlStreamAccumulator_PlainTextPassesThrough(t *testing.T) {
	var acc DsmlStreamAccumulator
	long := strings.Repeat("a", 100) // 远长于 dsmlToolCallsStart(23 字节)
	text, calls := acc.Feed(long)

	require.Nil(t, calls)
	safe := 100 - len(dsmlToolCallsStart) + 1 // = 78
	assert.Equal(t, long[:safe], text)

	text2, calls2 := acc.Flush()
	require.Nil(t, calls2)
	assert.Equal(t, long[safe:], text2)
}

// 完整 DSML 单 chunk 到达。
func TestDsmlStreamAccumulator_SingleChunkCompleteBlock(t *testing.T) {
	var acc DsmlStreamAccumulator
	block := dsmlBlock(dsmlInvoke("exec_command", dsmlParam("cmd", "ls")))

	text, calls := acc.Feed(block)
	require.Len(t, calls, 1)
	assert.Equal(t, "exec_command", calls[0].Name)
	assert.Equal(t, "ls", calls[0].Arguments["cmd"])
	assert.Equal(t, "", text)

	text2, calls2 := acc.Flush()
	assert.Equal(t, "", text2)
	require.Nil(t, calls2)
}

// start 标记被拆分到两个 chunk。
func TestDsmlStreamAccumulator_StartSplitAcrossChunks(t *testing.T) {
	var acc DsmlStreamAccumulator
	full := dsmlBlock(dsmlInvoke("exec_command", dsmlParam("cmd", "ls")))
	mid := 5 // <｜DS（start 标记前缀）
	delta1, delta2 := full[:mid], full[mid:]

	text1, calls1 := acc.Feed(delta1)
	require.Nil(t, calls1)

	text2, calls2 := acc.Feed(delta2)
	require.Len(t, calls2, 1)
	assert.Equal(t, "exec_command", calls2[0].Name)

	assert.Equal(t, "", text1+text2)
}

// end 标记被拆分到两个 chunk。
func TestDsmlStreamAccumulator_EndSplitAcrossChunks(t *testing.T) {
	var acc DsmlStreamAccumulator
	full := dsmlBlock(dsmlInvoke("exec_command", dsmlParam("cmd", "ls")))
	endStart := strings.Index(full, dsmlToolCallsEnd)
	mid := endStart + 3 // 在 end 标记中间拆分
	delta1, delta2 := full[:mid], full[mid:]

	_, calls1 := acc.Feed(delta1)
	require.Nil(t, calls1) // end 未完整，继续缓冲

	_, calls2 := acc.Feed(delta2)
	require.Len(t, calls2, 1)
	assert.Equal(t, "exec_command", calls2[0].Name)
}

// DSML 块前后都有文本，文本实时输出、块闭合时给 toolCalls。
func TestDsmlStreamAccumulator_TextBeforeAndAfterBlock(t *testing.T) {
	var acc DsmlStreamAccumulator
	block := dsmlBlock(dsmlInvoke("exec_command", dsmlParam("cmd", "ls")))
	// 先喂前缀文本（足够长，触发 safe 输出）
	prefix := strings.Repeat("b", 50)
	text1, _ := acc.Feed(prefix)
	// 喂块
	text2, calls := acc.Feed(block)
	// 喂后缀文本
	suffix := strings.Repeat("c", 50)
	text3, _ := acc.Feed(suffix)
	// Flush
	text4, _ := acc.Flush()

	require.Len(t, calls, 1)
	assert.Equal(t, "exec_command", calls[0].Name)
	// 拼回应等于 prefix + suffix（DSML 被剥离）
	assert.Equal(t, prefix+suffix, text1+text2+text3+text4)
}

// 未闭合的 DSML 块在 Flush 时降级为文本。
func TestDsmlStreamAccumulator_UnclosedFlushesAsText(t *testing.T) {
	var acc DsmlStreamAccumulator
	acc.Feed(dsmlToolCallsStart + dsmlInvoke("exec_command", dsmlParam("cmd", "ls")))

	text, calls := acc.Flush()
	require.Nil(t, calls)
	assert.Contains(t, text, "exec_command")
	assert.Contains(t, text, dsmlToolCallsStart) // 残片保留
}
