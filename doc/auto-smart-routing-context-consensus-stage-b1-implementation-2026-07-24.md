# 智能路由 ContextConsensus 阶段 B-1 实施报告

更新时间：2026-07-24

## 1. 实施结果

本轮完成阶段 B 的安全基础层 B-1：建立请求内压缩的授权快照、配置边界、纯压缩计划、结构化共识摘要校验和四协议安全重写器。所有能力默认关闭，当前请求链路不会调用压缩模型、不会改写主请求正文，也不会新增计费。

阶段 B 尚未完成。最终目标模型和最终转换协议的精确 token 终检、压缩子请求执行器、独立计费与父子审计留在 B-2。

## 2. 授权与配置

请求内自动压缩必须同时满足：

1. 系统配置 `context_consensus_enabled` 与 `auto_compaction_enabled` 同时开启。
2. 当前 API Key 的 `allow_context_compaction` 为 `true`。
3. 当前请求显式发送 `X-New-Api-Context-Mode: auto_compact`。

任一条件不满足时，纯策略判定均为不允许。`full` 或未提供 mode 保持完整上下文语义。

新增的压缩模型池只接受显式真实模型名，拒绝 `auto`、`smart`、`auto:*` 和 `smart:*`，防止压缩子请求递归进入智能路由。配置快照对 map 与 slice 做深拷贝，避免调用方修改全局状态。

阶段 C 才支持托管上下文。因此 B-1 对 `managed`、Context ID 和 revision 明确返回错误，不接受表面成功但实际未生效的参数。

## 3. 纯核心能力

`service/contextconsensus` 新增以下不依赖 HTTP、数据库和计费状态的能力：

- 三重授权策略及不可变快照。
- Chat Completions、Responses、Claude 和 Gemini 的早期完整 turn 切分。
- 仅允许连续、纯文本、已闭合的旧 user/assistant turn 进入压缩计划。
- system/developer、当前 user、最近 N 个完整 turn 和输出 schema 始终保留。
- 工具定义/调用/结果、媒体和 provider-bound 状态首版全部失败关闭。
- 严格的 `ConsensusSummary v1` JSON schema、字段类型、来源范围、摘要 digest 和 provenance 校验。
- 递归拒绝 `analysis`、`reasoning`、`thoughts` 等隐藏推理字段。
- 压缩计划携带包内策略状态和完整性摘要；重写前从原请求重新提取 Envelope、重建计划并做全量一致性校验，拒绝伪造或篡改计划。
- 四协议 DTO 重写，摘要始终作为不可信 user-level 数据注入，不提升为 system/developer 权限。
- 保留可选输出 token 字段的缺失与显式 `0` 语义。

Claude 与 Gemini 将摘要作为首个保留 user 消息中的独立文本块/part 注入，保留原最近 turn 内容并避免生成连续同角色消息。

## 4. 请求头隔离

以下头只供网关消费，禁止发送到上游：

- `X-New-Api-Context-Id`
- `X-New-Api-Context-Mode`
- `X-New-Api-Context-Revision`

隔离覆盖通配符透传、正则透传、显式 header override、`client_header` 别名读取，以及最终 HTTP、Form 和 WebSocket 请求清理。策略快照完成后立即从客户端请求头中删除这些字段。

## 5. 阶段边界

B-1 不把现有按请求内容估算的 token 数量包装成精确终检。阶段 A 的 `TokenCounter` 目前只有接口和预算不变量，生产链路尚未提供基于最终真实模型、最终协议正文的实现。若此时放行自动压缩，会导致主请求先预扣、重试重复压缩、父子请求上下文污染和分层计费读取错误。

B-2 必须先完成：

- 抽取统一的最终上游请求准备边界，覆盖四协议、pass-through 和 Chat-to-Responses 路径。
- 实现生产级精确 `TokenCounter`，在最终模型、最终协议、字段过滤和参数覆盖之后重新计数。
- 增加不向客户端写响应的进程内压缩执行器，并为子请求创建独立 request ID、RelayInfo 和 BillingSession。
- 压缩模型只使用白名单请求头，禁用 stream、tools、MCP、自动压缩和智能路由。
- 主请求预扣发生在压缩成功及最终预算复核之后；压缩与主调用分别结算、退款和审计。
- 单请求最多压缩一次；超时、非法摘要、压缩后仍超限或任一安全校验失败时，不发送主上游请求。

## 6. 验证范围

回归测试覆盖三重授权真值、旧 API Key 默认拒绝、API Key 策略落库、配置快照隔离、压缩模型池校验、四协议完整 turn 切分、摘要 schema/provenance、防隐藏推理、四协议重写、显式零值，以及五类请求头绕过路径。

验证命令：

```bash
GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp go test ./service/contextconsensus ./middleware ./setting/model_setting ./relay/channel ./controller ./model -count=1
GOCACHE=/private/tmp/new-api-race-cache GOTMPDIR=/private/tmp go test -race ./service/contextconsensus ./middleware ./relay/channel -count=1
GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp go test ./... -count=1
```
