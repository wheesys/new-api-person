# 更新日志

## 2.1.0（2026-08-12）

本版本合并了 cc-switch 的两类核心能力：渠道运行时健康追踪与熔断，以及 OpenAI Responses ⇄ Chat Completions 的工具兼容转换。

### 新增

- **渠道熔断健康追踪**：以「渠道 + 模型」为粒度维护运行时健康观测（EWMA 可靠性、连续失败计数、指数冷却），状态机 `healthy → degraded → open → half_open → healthy`，并发安全。
- **正常路由跳过熔断渠道**：普通路由选取落在 circuit-open 渠道时自动重选，候选全熔断时回退到上次选取渠道，避免热循环。
- **渠道列表健康状态列**：channel 列表新增健康状态展示（healthy / degraded / open / half_open），支持 `GET /api/channel/health` 聚合接口，状态文案 i18n 覆盖全部语言。
- **Responses ⇄ Chat Completions 工具兼容**：Codex 客户端经 Chat 上游时工具定义与调用可无损往返（function / custom / tool_search，含 namespace 压平），支持 GLM / DeepSeek / Codex。

### 改进

- Responses 请求降级走 Chat 上游时，上游 Chat 响应可正确转回 Responses 响应，供 Codex 客户端消费。
- 协议「冻结 / 桥接 / 降级」决策集中到 `relay/text_attempt_preparer.go`，执行阶段发送 body 与准备阶段冻结一致。

### 说明

- 熔断为进程内状态，单实例重启即清空；多实例部署下各实例独立判定。
- 合并说明与 feature-gap 对比见 `docs/cc-switch-comparison.md`。

---

许可证：AGPLv3 © 2026 QuantumNous and contributors; modifications © 2026 wheesys and contributors.
