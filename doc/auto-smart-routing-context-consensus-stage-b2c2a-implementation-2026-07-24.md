# 智能路由 ContextConsensus 阶段 B-2c2a 实施报告

更新时间：2026-07-24

## 1. 实施结果

本轮完成 B-2c2a 的第一段生产兼容性重构：为 OpenAI Chat Completions 及其 Chat-to-Responses 路径增加无网络最终请求预准备边界，并让现有发送链直接消费同一个不可变请求快照。

本阶段没有接入 `controller.Relay`、自动压缩、权威 tokenizer 或主请求计费顺序。自动压缩相关开关继续默认关闭，现有请求仍按原 handler 入口执行。

## 2. 请求准备与发送同源

`relay.PreparedTextRelayAttempt` 现在同时持有：

- 当前候选的 `RelayInfo`。
- 已初始化且后续实际发送复用的同一个 adaptor。
- `PreparedRelayRequest` 不可变正文快照。
- 响应转换模式。
- Chat-to-Responses 的有效 `RelayMode` 和请求路径生命周期。

`PrepareTextRelayAttempt` 当前只接受 `RelayFormatOpenAI`。准备阶段复用原有顺序完成深拷贝、模型映射、流式 usage 选项归一化、协议转换、渠道系统提示、禁用字段过滤、参数覆盖和最终正文冻结，但不会调用 `DoRequest`、`DoResponse` 或计费结算。

`ExecutePreparedTextRelayAttempt` 只从该快照创建 Reader 并发送，不再执行模型映射、协议转换、字段过滤或参数覆盖。因此后续权威 token 计数可以直接读取 `PreparedRequest()`，且实际发送不会重新生成另一份正文。

## 3. Chat-to-Responses 生命周期

Chat-to-Responses 保持既有转换语义：

1. Chat 请求执行禁用字段过滤和参数覆盖。
2. 反序列化为覆盖后的 Chat DTO。
3. 转换为 Responses DTO。
4. 临时切换到 Responses relay mode 和 `/v1/responses`。
5. 由 adaptor 执行 Responses 转换和最终禁用字段过滤。
6. 冻结最终正文并使用 Responses 响应转换器处理结果。

准备成功后，有效 mode 和路径会保持到响应处理结束；attempt 被淘汰、失败或执行完成后由幂等 `Close` 恢复原值。Claude 现有的 Chat-to-Responses 兼容入口继续复用同一准备与执行路径。

## 4. 生命周期保护

attempt 是一次性对象：

- 并发或重复执行只允许一次进入上游请求。
- `Close` 幂等，并等待正在执行的请求完成后再释放正文存储。
- 关闭后的 attempt 明确拒绝执行。
- JSON 转换正文由 attempt 负责关闭；pass-through 继续借用原请求 BodyStorage，不改变原有所有权。

## 5. 当前边界

本阶段不是完整 B-2c2，仍有以下门禁：

- `PrepareTextRelayAttempt` 尚未覆盖原生 Responses、Claude 和 Gemini handler。
- Gemini、Vertex 等 adaptor 仍可能在 `GetRequestURL` 中规范化 `UpstreamModelName`；在纯目标解析接口完成前，准备结果不能被标记为权威最终模型证据。
- adaptor 的离线转换能力尚未逐个显式 opt-in；自动压缩候选终检不能直接假定所有 adaptor 都满足纯准备合同。
- 尚未冻结完整跨模型候选集合，也未实现 DTO、正文、候选和预算证据的原子提交。
- 主请求预扣仍发生在 handler 最终正文准备之前，自动压缩不能进入生产主链。

因此本轮只建立兼容性复用点，不开放生产自动压缩。

## 6. 验证范围

新增测试覆盖：

- 准备阶段不调用 `DoRequest` 或 `DoResponse`。
- 实际发送字节与准备阶段冻结字节逐字节一致。
- 并发重复执行只有一次上游调用和一次响应处理。
- `Close` 幂等，关闭后拒绝执行。
- Chat-to-Responses 在转换、发送和响应处理期间保持 Responses mode/path，并在关闭后恢复。

定向普通测试 `go test ./relay ./relay/common` 已通过；编码角色已独立运行并通过 `go test -race ./relay`。
