# cc-switch 功能合并：渠道熔断健康 + responses⇄chat 工具兼容 实施记录

日期：2026-08-12

## 背景

将 cc-switch 的两类核心能力合并进本仓库：一是**渠道运行时健康追踪与熔断**，二是**OpenAI Responses ⇄ Chat Completions 的工具兼容转换**。合并后，网关能在正常路由时跳过已熔断（circuit-open）的渠道，并在 channel 列表直观展示各渠道/模型的健康状态；同时 Codex 客户端（Responses 协议）经 Chat 上游时工具定义与调用可无损往返。

## 方案

### 1. 渠道运行时健康追踪（熔断）

`service/smartrouting/runtime_health.go` 新增进程内 `RuntimeHealthTracker`，以「渠道 + 模型」为粒度维护健康观测：

- **状态机**：`healthy → degraded → open → half_open → healthy`。连续失败达到阈值 `runtimeHealthFailureThreshold = 3` 时 `open`；open 到期（指数冷却，初始 5s、上限 5min）后转 `half_open` 允许探针放行。
- **EWMA 可靠性**：成功/失败以 α=0.2 的 EWMA 累计，`successEWMA < 0.8` 判为 degraded。
- **探针租约**：half_open 状态同一时间只允许一个并发探针（`TryAcquireProbe`，租约 5s），避免熔断恢复瞬间被流量击穿。
- **指标观测**：`RecordSuccessWithLatency` / `RecordSuccessWithMetrics` 额外累计 latency 与 throughput 的 EWMA（如 tokens/秒），供打分与展示。
- **清理**：条目 TTL 15 分钟，每分钟惰性清理；未观测条目视为 healthy。
- 全链路并发安全（`sync.Mutex`），快照为不可变结构 `RuntimeHealthSnapshot`。

**正常路由跳过 open 渠道**：`service/channel_select.go` 新增 `pickChannelSkippingOpen`，替换普通路由路径（`CacheGetRandomSatisfiedChannel`）里的直接随机选取。选取落在 open 渠道时重选，重选次数受 `openChannelReselectLimit = 5` 上限约束；候选全 open 时回退到最后一次选取的渠道，避免热循环或死锁。`runtimeHealthOpen` 为包级可注入 hook，便于测试打桩。

**健康列接口**：`controller/status.go` 新增 `ChannelHealth` 读接口，聚合 `GetRuntimeHealthSnapshotAll()` 输出每个渠道/模型的 `state / reliability / consecutive_failures / cooldown_seconds / open_until`。路由见 `router/channel-router.go`，前端经 `GET /api/channel/health` 拉取。

**前端健康列**：`web/src/features/channels/{api,types,components/channels-columns}.tsx` 在渠道列表新增健康状态列，映射 `healthy / degraded / open / half_open` 四种状态并 i18n 到全语言（en/zh/zh-TW/fr/ru/ja/vi）。

### 2. responses ⇄ chat 工具兼容

`relaykit/relayconvert/convmeta/codex_tool.go` 提供 `CodexToolContext`：把 Responses 风格工具（`function` / `custom` / `tool_search`，可带 namespace）在发往 Chat 上游时压平为 64 字符内、带 hash 后缀的函数名；回程按压平名还原到原始 namespace/kind，与 cc-switch 的 `CodexToolContext` 语义对齐。

- `relay/chat_completions_via_responses.go` 新增 `executePreparedResponsesViaChat`：Responses 请求降级走 Chat 上游时，把上游 Chat 响应（流式/非流式）转回 Responses 响应，供 Codex 客户端消费——与既有 `executePreparedChatCompletionsViaResponses` 镜像对称。
- `relay/text_attempt_preparer.go` 集中处理协议的「冻结/桥接/降级」决策，保证执行阶段发送的 body 与准备阶段冻结的完全一致（frozen body），并记录最终生效协议状态直到请求关闭。
- 转换映射落点在 `relaykit/relayconvert/internal/oai_chat/to_oai_responses_resp.go`（chat→responses 响应）与 `internal/oai_responses/to_oai_chat_req.go`（responses→chat 请求），配合 `request_registry.go` / `response_registry.go` 注册。

## 关键文件

- `service/smartrouting/runtime_health.go` — 熔断状态机、EWMA、指数冷却、探针租约
- `service/channel_select.go` — `pickChannelSkippingOpen` 跳过 open 渠道
- `controller/status.go` — `ChannelHealth` 聚合接口
- `router/channel-router.go` — 健康接口路由
- `web/src/features/channels/{api,types,components/channels-columns}.tsx` — 前端健康列
- `relaykit/relayconvert/convmeta/codex_tool.go` — Responses 工具 ↔ 压平函数名上下文
- `relay/chat_completions_via_responses.go` — `executePreparedResponsesViaChat` 降级回程
- `relay/text_attempt_preparer.go` — 协议冻结/桥接/降级决策
- `relaykit/relayconvert/internal/oai_chat/to_oai_responses_resp.go`、`internal/oai_responses/to_oai_chat_req.go` — 转换映射

## 验证

- `go build ./...`、`cd relaykit && GOWORK=off go build ./...` 双模块独立构建通过。
- 新增测试全绿：`service/smartrouting/runtime_health_test.go`（状态机、指数冷却、并发安全、吞吐/延迟观测）、`service/channel_select_open_test.go`（跳过 open、全 open 回退）、`relay/text_attempt_preparer_test.go`（frozen body、降级恢复、协议状态）、`relaykit` 下 codex 转换测试（`to_oai_responses_resp_codex_test.go`、`to_oai_chat_req_codex_test.go`）。
- 前端 oxlint 通过、`bun run i18n:sync` 后各语言键一致（missing/extras/untranslated 均为 0）。

## 备注

- 熔断为**进程内**状态：单实例重启即清空；多实例部署下各实例独立判定，健康列展示的是当前进程观测。
- `runtimeHealthOpen` 与 `randomSatisfiedChannel` 为包级变量，仅用于测试注入；生产始终委托 `smartrouting` 与 `model`。
- 本功能来自 cc-switch，合并说明与 feature-gap 对比见 `docs/cc-switch-comparison.md` 与 README。
