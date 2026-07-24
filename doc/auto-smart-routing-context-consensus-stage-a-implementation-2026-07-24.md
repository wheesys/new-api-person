# 智能路由 ContextConsensus 阶段 A 实施报告

更新时间：2026-07-24

## 1. 实施结果

本轮完成 `ContextConsensus` 阶段 A，在不调用压缩模型、不改写上下文正文、不新增持久化状态的前提下，为智能路由增加四协议上下文解析、provider-bound 状态检测、工具调用图验证、精确预算接口和安全审计。

实现范围覆盖：

- OpenAI Chat Completions
- OpenAI Responses
- Claude Messages
- Google Gemini

## 2. 核心实现

新增 `service/contextconsensus` 包，负责把协议请求提取为统一 `ContextEnvelope`。Envelope 仅保存路由所需的结构信息、计数和 SHA-256 摘要，不进入候选评分器，也不持久化请求正文。

主要能力包括：

- 区分不可变、可压缩和必须保留的上下文段。
- 保留可选输出 token 字段的缺失与显式 `0` 语义。
- 检测 Responses 会话引用、Claude container/MCP/thinking signature、Gemini cached content/thought signature/file URI，以及各协议的 provider file 引用。
- 构建工具调用与工具结果关系图，拒绝未闭合调用、孤立结果、重复结果和不明确的 Gemini 并行同名调用。
- 提供注入式 `TokenCounter` 精确预算接口，由后续最终目标模型和协议转换链路提供实际计数器。

智能路由通过 `ContextRoutingConstraint` 消费解析结果，不读取协议正文或工具载荷。

## 3. 路由行为

| 请求场景 | 阶段 A 行为 |
| --- | --- |
| 无 provider-bound 状态的 `auto:*` / `smart:*` | 沿用现有候选模型和渠道评分 |
| 含 provider-bound 状态的 `auto:*` / `smart:*` | 以 HTTP 409 拒绝不可信的跨模型或跨凭据切换 |
| 含 provider-bound 状态的显式模型 | 记录 `would_block`，禁用智能重试候选序列；初始渠道仍沿用现有选择逻辑 |
| 工具图不合法 | 以 HTTP 400 拒绝请求，并只返回静态原因码和位置 |

当前 `validate_only` 表示只验证、检测和审计，不执行压缩或状态改写。虚拟模型的 provider-bound 冲突会强制失败关闭；显式模型尚无法在初始请求前绑定可信渠道与凭据，该映射留待阶段 C 完成。

## 4. 审计与安全

消费日志 `other.smart_routing.context_consensus` 新增以下白名单字段：

- 版本、协议、验证模式和验证结果
- 有效输入 token、保留段数量和工具交换数量
- provider binding 级别和静态原因码
- 是否允许切换、是否会阻断、是否需要压缩

日志和错误响应不包含请求正文、provider state 原值、工具调用 ID、工具参数、工具结果或其摘要。API key、token、secret 等认证信息不参与提取或日志输出。

## 5. 阶段边界

本轮未实现：

- 调用压缩模型或生成结构化共识摘要
- 对最终上游请求执行协议安全重写
- 接入最终目标模型/协议转换后的精确 token 终检
- 压缩任务独立计费
- Redis 加密存储、revision/CAS、lease、TTL
- provider state 到渠道/账号凭据的可信绑定映射

因此，阶段 B 需要把已落地的 `TokenCounter` 接口接入最终协议转换边界，并实现请求内压缩；阶段 C 再完成可信状态绑定和托管共识。

## 6. 验证结果

以下验证全部通过：

```bash
GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp go test ./service/contextconsensus ./service/smartrouting ./middleware ./service -count=1
GOCACHE=/private/tmp/new-api-race-cache GOTMPDIR=/private/tmp go test -race ./service/contextconsensus ./service/smartrouting -count=1
GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp go test ./... -count=1
```

同时通过 `gofmt` 和 `git diff --check`。安全回归用例确认日志与错误响应不会泄露 provider state、工具调用 ID 或工具正文。
