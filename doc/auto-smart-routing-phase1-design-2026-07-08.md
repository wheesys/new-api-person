# 自动智能路由阶段一设计

日期：2026-07-08

## 范围

阶段一只落地显式虚拟模型的主动路由基础能力，不默认替换用户请求的具体模型，不做服务端会话摘要，不新增后台配置页。

客户端必须显式选择 `auto:*` 或 `smart:*` 才允许跨模型切换。客户端明确指定普通模型名时，智能路由只能在同一模型的不同渠道之间评分选择，不能把请求模型改成其他模型。

本阶段目标是建立可测试、可解释、可审计的后端核心结构：

- 虚拟模型：`auto:cheap`、`auto:balanced`、`auto:quality`、`auto:fast`、`auto:reasoning`，兼容 `smart:*` 前缀。
- 请求分析：将任务复杂度和上下文需求拆成两个独立维度。
- 候选评分：对“模型 + 渠道”组合执行硬约束过滤和软评分排序。
- 普通模型渠道评分：对同一真实模型的可用渠道按成本、延迟、权重等因素排序，不改变模型名。
- 日志审计：消费日志 `other.smart_routing` 记录决策摘要，不记录请求正文、API Key、token、secret。

## 核心数据结构

新增包：`service/smartrouting`。

核心类型：

- `SmartRouteRequest`：路由输入，包含原始模型、端点类型、分组、流式、工具、JSON schema、多模态、推理、预估 token 和 `TokenCountMeta`。
- `VirtualModelProfile`：虚拟模型策略模板，包含策略和偏好的模型质量层级。
- `SmartRouteAnalysis`：输出任务复杂度、上下文需求、分数和理由。
- `SmartRouteCandidate`：候选“模型 + 渠道”组合，包含能力画像、估算成本和运行评分。
- `ChannelSnapshot`：从现有渠道读取路由所需字段，避免智能路由核心直接依赖数据库状态。
- `ModelCapabilities`：根据模型名和端点类型推导的保守能力画像，后续后台显式配置可覆盖。
- `Decision`：日志审计结构，只暴露可解释字段。

质量层级：

- `economy`
- `standard`
- `premium`
- `reasoning`

策略：

- `cost_first`
- `balanced`
- `quality_first`
- `latency_first`
- `reliability_first`

## 请求分析规则

任务复杂度和上下文需求必须分开判断。

任务复杂度主要看当前请求本身：

- 翻译、改写、润色、格式调整降低任务分。
- 工具调用、JSON schema、视觉、音频、文件、推理、代码/调试/迁移/架构等关键词提高任务分。
- 高可靠请求直接进入 `critical`。

上下文需求主要看输入规模和引用范围：

- `EstimatedPromptTokens + MaxOutputTokens` 决定基础上下文等级。
- `MessagesCount`、全量历史引用、文件上下文继续加权。
- “上面那句话 / 上一条”只表示最近上下文引用，不等同于复杂任务。

这样可以避免“对话越长就自动选更强模型”的错误行为。长对话只影响上下文窗口要求；模型能力强度由任务复杂度决定。

## 候选过滤和评分

硬约束过滤：

- 上下文 token 不能超过候选模型窗口。
- 流式请求要求候选支持 stream。
- 工具请求要求候选支持 tools。
- JSON schema 请求要求候选支持 JSON schema。
- 图像、音频、文件、推理请求要求候选支持对应能力。
- 运行时健康状态为 `open` 的候选始终隔离；多 Key 渠道全部 Key 不可用时始终拒绝。冷却到期后只有取得半开单探针租约的请求可以试探恢复。
- 普通模型请求只能保留候选模型名等于请求模型名，或等于请求模型规范化匹配名的候选；最终 `selected_model` 仍记录为客户端请求的模型名。

软评分：

```text
final_score =
  cost_weight * cost_score
  + reliability_weight * reliability_score
  + latency_weight * latency_score
  + throughput_weight * throughput_score
  + quality_weight * quality_score
  + task_match_weight * task_match_score
  + context_weight * context_score
  + cache_weight * cache_score
  + reset_window_weight * reset_window_score
  + affinity_weight * affinity_score
```

不同策略只调整权重。`cost_first` 可以让便宜的可用模型优先；`quality_first` 可以让高质量和高可靠模型优先。

渠道评分中，渠道倍率通过 `EstimatedQuota` 影响 `cost_score`；渠道表中的一次性测试 `ResponseTime` 不再直接参与智能调度，只有至少 3 次有效真实请求延迟产生的按“渠道 + 模型”延迟 EWMA 才影响 `latency_score`，样本不足时使用中性分。每次实际上游尝试独立计时：流式请求使用首次上游响应数据的 TTFT，非流式请求和异步任务提交使用单次尝试完整耗时；流式成功但没有有效首包时不写入延迟样本。原始 HTTP 首包只标记当前尝试，请求级首包仍需有效流事件确认，避免失败重试的错误正文污染最终 TTFT。渠道 `Weight` 不再作为吞吐量；`throughput_score` 只采集权威上游 usage 的流式文本 output token/s，以首包到 usage 到达作为生成时长，明确排除结算/日志耗时和图像 SSE 等非文本端点，按“渠道 + 模型”维护 EWMA，至少 3 个有效样本且候选集合至少有两个有效观测时才相对归一化，其余保持中性分。`reliability_score` 来自同维度的运行时成功 EWMA，并结合多 Key 渠道可用 Key 比例。任务匹配、上下文余量、缓存亲和、重置窗口和会话亲和分别保留独立审计因子。

## 日志审计

消费日志新增 `other.smart_routing`，字段包括：

```json
{
  "enabled": true,
  "policy": "quality_first",
  "complexity": "complex",
  "task_type": "coding",
  "recommended_tier": "premium",
  "context_requirement": "long",
  "original_model": "auto:quality",
  "selected_model": "premium-reasoning",
  "selected_channel_id": 12,
  "selected_health": "healthy",
  "candidate_count": 4,
  "fallback_index": 0,
  "score_factors": {
    "cost": 0.2,
    "reliability": 0.98,
    "latency": 0.55,
    "throughput": 0.5,
    "quality": 0.97,
    "task_match": 0.95,
    "context": 0.75,
    "cache": 1,
    "reset_window": 1,
    "affinity": 0
  },
  "decision_reasons": ["tools_required", "json_schema_required"],
  "context_consensus": {
    "mode": "stateless_full_context",
    "compacted": false,
    "preserved_recent_messages": 6
  }
}
```

禁止写入完整请求体、API Key、上游 key、token、secret。

## 当前已完成

- 新增 `service/smartrouting` 纯规则核心。
- 新增虚拟模型解析、复杂度分析、候选过滤和评分排序。
- 新增从 `Pricing` 与渠道快照生成 `SmartRouteCandidate` 的最小实现。
- 新增模型名与端点类型的保守能力推导，覆盖质量层级、上下文窗口、tools、JSON schema、vision、audio、reasoning、stream。
- 新增 `ContextKeySmartRoutingDecision`。
- 消费日志生成支持追加 `smart_routing` 决策字段。
- 补充单元测试覆盖核心规则和日志字段安全边界。
- `middleware.Distribute()` 已识别 `auto:*` / `smart:*` 虚拟模型，在渠道选择前生成真实模型和渠道组合。
- 分发阶段已将选中的真实模型写入 `ContextKeyOriginalModel`，并将 JSON 请求体中的 `model` 字段同步改为真实模型，避免 pass-through 渠道把虚拟模型发送给上游。
- 客户端明确指定普通模型时，分发阶段不会更换模型，只会在同模型候选渠道中按综合分数选择渠道，并在审计决策中记录 `original_model == selected_model`。
- 智能路由决策已写入 `ContextKeySmartRoutingDecision`，后续消费日志会落到 `other.smart_routing`。
- 失败重试会优先消费本次智能路由生成的同一真实模型候选渠道序列；候选耗尽后不会回落到普通随机渠道，避免重试阶段跨模型或绕过排序结果。
- `auto` 已作为 `auto:balanced` 的别名；客户端只传 `model: "auto"` 时默认使用均衡策略。
- 新增模型路由画像缓存，画像维度包含综合质量、编码、推理、速度、成本、上下文、偏好和可靠性；候选评分优先读取画像缓存，缓存缺失时回退保守模型名推断。
- 新增智能路由画像系统任务：外部榜单数据默认 10 天刷新一次，内部模型画像默认 1 天重算一次。首版支持 Aider 榜单 JSON/CSV/Markdown 表格解析，并将编码通过率、成本和耗时归一化为多维评分。
- 新增 SWE-bench、Artificial Analysis 和 Arena 榜单适配器，统一输出 `ExternalBenchmarkRecord`；外部榜单刷新任务支持按环境变量配置多来源地址并合并缓存。
- 模型画像生成改为先按来源归一化，再按模型名聚合，避免 Arena Elo、Artificial Analysis 指数和 Aider/SWE-bench 通过率的量纲互相污染；`Sources` 与 `SourceScores` 会保留来源审计信息。
- 参考 OmniRoute 智能路由思路补充任务类型与推荐层级，新增任务匹配、上下文、缓存亲和、重置窗口和会话亲和评分，修正可靠性、延迟和吞吐评分数据来源。
- 新增按“渠道 + 模型”隔离的运行时健康状态、EWMA 可靠性、连续失败熔断、指数冷却、半开恢复和多 Key 健康检查；现有渠道禁用仍作为全局硬状态。
- 延迟 EWMA 改为逐上游尝试采样：流式使用首个上游响应数据 TTFT，非流式使用完整尝试耗时，重试和无首包流式成功不会污染延迟数据。
- 吞吐评分改为真实流式文本 output token/s：只采权威上游 usage，以首包到 usage 到达作为生成时长，过滤本地估算及图像 SSE 等非文本请求，并按候选集合相对归一化。
- 复用既有渠道亲和缓存，并在已经选定的真实模型内部优先亲和渠道，保持会话稳定性。完整实施说明见 `doc/auto-smart-routing-omniroute-alignment-implementation-2026-07-28.md`。

## 下一步

1. 按 `doc/auto-smart-routing-configurable-model-pool-2026-07-09.md` 实现虚拟模型的管理员可配置模型池。
2. 设计跨模型 `ContextConsensus` 会话共识、自动压缩和工具状态保持方案。
3. 设计智能路由日志指标聚合和后台配置页面。
