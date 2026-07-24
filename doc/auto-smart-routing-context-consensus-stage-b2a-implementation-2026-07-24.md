# 智能路由 ContextConsensus 阶段 B-2a 实施报告

更新时间：2026-07-24

## 1. 实施结果

本轮完成阶段 B-2a：为最终上游请求建立统一、不可变的 `PreparedRelayRequest` 快照，并让实际发送端从同一快照读取正文。该边界覆盖 Chat、Responses、Claude、Gemini、pass-through 和 Chat-to-Responses 路径。

B-2a 仍不调用压缩模型、不改写客户端上下文、不创建压缩子请求计费会话，也不执行生产 token 终检。除本报告列出的显式零值纠错外，现有请求的路由、预扣和响应链路保持不变。

## 2. 最终请求快照

快照在模型映射、协议转换、禁用字段过滤和参数覆盖全部完成后创建，保存：

- 最终上游模型。
- 最终协议。
- SHA-256 正文摘要。
- 正文大小。
- 可选输出 token 上限，并区分字段缺失和显式 `0`。

正文由快照持有的 `BodyStorage` 提供。JSON 转换路径由快照拥有并关闭存储；pass-through 路径借用请求原有存储，不提前关闭，确保同一请求内重试仍可重新读取。

`RelayInfo` 只记录最终模型、协议、摘要、大小和输出上限，不记录请求正文。`UpstreamRequestBodySize` 与快照大小由同一入口更新。

## 3. 重试隔离

每次 handler 执行开始时清理上一候选留下的最终请求元数据，并将协议转换链恢复到原始 `RelayFormat`。当前候选随后重新执行模型映射、协议转换和快照创建，避免上一候选的 `FinalRequestRelayFormat`、正文摘要或输出上限污染重试。

Chat-to-Responses 在当前尝试内继续追加 Responses 转换，最终快照因此记录实际发送的 Responses 协议和正文，而不是转换前的 Chat 请求。

## 4. 显式零值

本轮同步修复以下同类问题：

- Claude 原生入口只在 `max_tokens` 缺失时填充默认值，不再覆盖显式 `0`。
- Chat 转 Claude 和 Chat 转 Gemini 时保留 `max_completion_tokens: 0`，且该字段优先于旧版 `max_tokens`。
- Gemini 转 Chat 时保留 `maxOutputTokens: 0`。
- OpenAI、XAI 推理模型适配和 Chat-to-Responses 转换按字段存在性选择新输出上限字段，不再按数值大小覆盖显式 `0`。

thinking 适配器为满足上游协议强制最小预算而进行的专用规范化保持原有行为；普通请求不再把显式 `0` 当作字段缺失。

## 5. 阶段边界

现有 `service.CountTextToken` 对非 OpenAI 模型仍是估算，智能路由的部分上下文上限也来自模型名推断，因此不能作为权威终检。主请求计费仍在最终渠道转换前预扣，也尚无不写客户端响应的内部压缩执行器。

后续按以下顺序继续：

1. B-2b：提供独立 request ID、`RelayInfo`、`BillingSession` 和结构化返回值的内部子请求执行边界。
2. B-2c：接入权威 tokenizer/上下文上限来源、压缩编排、正文与 DTO 原子提交、逐候选终检及失败关闭。

在 B-2b/B-2c 完成前，自动压缩保持默认关闭。

## 6. 验证范围

测试覆盖：

- 四协议最终模型、协议、正文摘要、大小和显式零输出上限。
- JSON 正文拷贝隔离及重复读取一致性。
- pass-through 借用存储的关闭与重试语义。
- `RelayInfo` 最终元数据记录和跨候选重置。
- Chat 转 Claude/Gemini、Gemini 转 Chat 的显式零值转换。
- OpenAI、XAI 和 Chat-to-Responses 核心路径的显式零值优先语义。
- B-1 授权、计划、摘要和重写能力的回归测试。

全量 `go test ./...` 和本轮相关包、新增用例的定向 `go test -race` 通过。扩大 race 范围到整个 OpenAI、Gemini 与 service 包时发现既有竞态，分别位于流扫描结束信号、测试内并发修改 Gin 全局模式、任务轮询日志时间状态和测试读取异步更新对象；堆栈不经过 B-2a 新增代码，已作为独立待办记录。
