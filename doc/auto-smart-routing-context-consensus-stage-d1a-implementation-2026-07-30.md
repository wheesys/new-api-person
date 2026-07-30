# ContextConsensus 阶段 D-1a 实施记录

日期：2026-07-30

## 结论

阶段 D-1a 已完成工具图因果证据和单工具串行结构资格评估。它只判断一个 OpenAI Chat Completions 工具交换是否具备进入未来确定性脱敏阶段的结构条件，不授权工具结果压缩，也不改变现有请求、路由、计费或摘要行为。

`BuildCompactionPlan` 继续拒绝所有工具上下文，`ConsensusSummary v1` 继续要求 `tool_result_summaries` 为空并拒绝 `tool_observed`。本批未增加配置、数据库、管理员接口或页面。

## 工具图因果证据

- `ToolExchange` 增加独立的 result sequence；call sequence 与 result sequence 不再混用。
- result 必须与 call 使用同一协议，并且严格发生在 call 之后。
- 协议不匹配或顺序错误的 result 不会关闭 exchange；工具图同时保留未闭合错误，避免错误证据进入后续阶段。
- Gemini 按函数名匹配时，只有通过协议与顺序校验后才消费 pending call，非法 result 不会破坏后续合法匹配。
- 原始 call ID 和函数名继续使用 `json:"-"`，参数与结果只保留 digest。

## 结构资格契约

`AssessSingleSerialToolCompaction` 首批只认可：

- OpenAI Chat Completions。
- 恰好一个已闭合 exchange，call/result 原文存在且因果顺序有效。
- 稳定 call ID、函数名、参数 digest、结果 digest 和工具 schema digest 完整。
- 无 provider binding、媒体、opaque state 或歧义函数名。

评估结果使用固定 reason code 失败关闭。成功证据只包含协议、call/result sequence、完成状态，以及 call identity、tool identity、参数、结果和 schema 的 digest；不包含原始工具 ID、名称、参数、结果、schema 或媒体引用。

`ready_for_sanitization=true` 仅表示结构完整，不表示可向摘要模型发送内容。后续仍必须通过服务端注册的版本化脱敏策略、字段白名单、大小/深度上限、敏感值检测和 Summary v2 来源证明。

## 明确保留的门禁

- 工具结果不会进入现有 compaction prompt。
- OpenAI Responses、Claude、Gemini、并行工具、MCP/hosted tool、opaque signature、provider file、媒体和 managed 工具状态均未开放。
- 不从函数名、客户端 schema 或参数推断幂等性、授权或脱敏安全性。
- 不增加 `allow_tool_result_compaction` 设置；在脱敏和 Summary v2 契约完成前不存在可开启入口。
- 日志和指标尚不消费结构证据，避免在聚合边界冻结前扩大可观察面。

## 验证

- 单工具串行 Chat 生成仅含 digest 的结构证据，并断言序列化结果不包含工具 ID、函数名、参数 secret 或结果 secret。
- provider binding、媒体、缺 schema、歧义、多 exchange、未闭合、opaque、缺身份、缺 digest 和非法 sequence 全部返回固定 reason code。
- 跨协议 result、先于 call 的 result 均保持 exchange 未闭合。
- Gemini 不同函数名反序匹配仍保留精确 result sequence。
- 结构资格通过后，`BuildCompactionPlan` 仍返回 `tool context cannot be compacted`。
- `go test ./service/contextconsensus -run 'Tool|Compaction' -count=1`。
- `go test ./service/contextconsensus -count=1`。
- `go test -race ./service/contextconsensus -count=1`。
- `go vet ./service/contextconsensus`。
- `go test ./... -count=1`。

## 后续拆分

- D-1b：实现服务端注册、版本化、默认拒绝的 JSON Pointer 白名单脱敏策略，只输出有界结构化投影。
- D-1c：新增 Summary v2 和单个旧串行 Chat 工具原子段的 plan/prompt/rewrite/runtime 闭环；工具调用、结果和最终 assistant 回复必须整体压缩或整体保留。
- D-1d：基于有限 reason code 增加聚合诊断 API 和后台页面，禁止会话状态浏览器或原始工具数据展示。
- D-2：在稳定 ID、统一 group 语义和完整闭合证明后评估并行工具组。
- D-3：待 adaptor 提供权威所有权、到期和删除能力后，再实现 provider file 生命周期。
