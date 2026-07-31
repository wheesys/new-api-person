# ContextConsensus 阶段 D-2 实施记录

日期：2026-07-31

## 结论

阶段 D-2 已完成稳定 ID 并行工具组的安全压缩闭环。首批运行时仅开放 OpenAI Chat Completions 中恰好一个、包含 1 至 8 个调用的已闭合工具组；单工具继续兼容 D-1，2 至 8 个调用按并行组处理。

并行结果允许按不同于调用的顺序返回，但结构证据、脱敏投影和 Summary v2 工具事实始终按调用数组顺序冻结。调用消息、全部结果消息和最终 assistant 回复只能整体压缩或整体保留。

## 统一工具组语义

- `ToolGraph` 新增 call-derived group；`ToolExchange` 通过安全的 `GroupIndex` 关联所属组。
- group 记录协议、调用容器序号、调用/结果序号边界、按调用顺序排列的 exchange 索引、闭合状态和身份模式。
- result 只能通过稳定 call ID 或协议既有的名称匹配继承调用组；结果容器混合关闭不同调用组时失败关闭。
- 原始 call ID、函数名和歧义函数名不参与 JSON 序列化，图中只保留结构元数据和 digest。
- OpenAI Responses 的相邻 function call item 不被推断为并行组，每个 call item 保持独立 singleton group。
- Claude 可形成稳定 ID 图组，但本阶段不开放运行时压缩；Gemini 无稳定 call ID，同名并行不再按 FIFO 猜配并继续失败关闭。

## 整组结构证据与诊断

- 新增带进程内完整性证明的 `ToolCompactionGroupEvidence`，只包含协议、组序号、因果序号、有序 exchange 证据和 digest。
- 结构评估要求恰好一个完整组、稳定且唯一的 call ID、连续结果区间、无 media、opaque/provider-bound 状态，并设置最多 8 个 exchange 的硬上限。
- D-1 单串行评估接口保留为兼容包装；并行组不会被误判为单工具。
- 工具压缩诊断升级为 schema v2，使合法并行组可报告 `ready_for_sanitization`；聚合读取同时接受历史 v1 和当前 v2 日志。
- 诊断仍只持久化 schema、资格状态和 13 个有限 reason code，不包含结构证据或任何 digest。

## 原子脱敏与 Summary v2

- 组内结果按 call ID 重新映射到调用顺序，再逐项调用既有版本化白名单脱敏策略。
- 任一成员缺少登记策略时，covered prefix 整体缩到工具组之前；其他脱敏错误使整次计划失败，禁止部分投影和部分工具事实。
- 所有投影的编码总量最多 16 KiB，工具事实总数最多 32；既有单策略输入、深度和字段限制继续生效。
- 多工具事实使用 `tool_01.<field>`、`tool_02.<field>` 等安全序号命名，避免字段碰撞且不暴露工具名或 call ID。
- Prompt 隐藏调用消息、全部结果消息和最终 assistant 回复，只向摘要模型发送可见普通历史与脱敏事实。
- Summary v2 必须逐项、按序精确返回预期工具事实；遗漏、重复、换序、来源范围或投影篡改均失败关闭。
- rewrite 继续通过请求重建和 plan 深比较验证，并原子删除完整工具 turn；当前 tools/schema 原样保留。

## 安全边界

- 运行时仍只支持最终协议为 OpenAI Chat Completions；Responses、Claude、Gemini 不因图层增强而获得压缩权限。
- `allow_tool_result_compaction` 继续默认关闭，生产编译期静态脱敏策略表继续为空，本阶段没有登记任何业务工具。
- 未新增数据库表、配置项、管理员写接口或生产状态变更。
- provider file 生命周期仍不在本阶段范围内，文件、媒体、签名和 provider-owned state 继续失败关闭。

## 验证

- `go test ./service/contextconsensus -count=1`。
- `go test ./service/contextconsensus ./controller ./middleware -run 'Tool|Compaction|ContextConsensus' -count=1`。
- `go test -race ./service/contextconsensus ./controller ./middleware -run 'Tool|Compaction|ContextConsensus' -count=1`。
- `go vet ./service/contextconsensus ./controller ./middleware`。
- `go test ./... -count=1`。
- `git diff --check`。

测试覆盖稳定 ID 反序结果、同字段投影的确定顺序、完整原子范围、Prompt/重写隔离、缺一策略整组回退、投影重排篡改、组上限、Responses 不推断相邻并行组、Claude 图组和 Gemini 同名歧义失败关闭。

## 后续拆分

- D-3：待 adaptor 提供权威所有权、到期和删除能力后实现 provider file 生命周期。
- 生产静态脱敏策略仍需针对服务端实际拥有的精确工具/schema 单独评审并通过代码登记。
