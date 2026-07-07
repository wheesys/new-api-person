# 自动智能路由调研报告

调研日期：2026-07-07

## 背景

当前项目已经具备基础路由能力：

- `Ability` 维护“分组 + 模型 -> 可用渠道”的候选关系。
- `Channel.Priority` / `Ability.Priority` 控制重试优先级。
- `Channel.Weight` / `Ability.Weight` 控制同优先级内随机权重。
- 渠道状态、自动禁用、多 Key、渠道亲和、模型映射、渠道倍率和计费日志已经接入运行链路。
- 请求进入 `middleware.Distribute()` 后会在 `SetupContextForSelectedChannel()` 中固定本次使用的渠道上下文。

这说明“智能路由”不应推翻现有能力路由，而应作为候选渠道排序和候选模型扩展的策略层，挂在现有 `service.CacheGetRandomSatisfiedChannel()` 之前或内部。

## OpenRouter 可参考点

OpenRouter 的智能路由主要分成三类：

1. Provider routing：对同一个模型的多个 provider 做过滤、排序、负载均衡和回退。
2. Model fallback：主模型不可用、限流、宕机、内容过滤或上下文长度错误时，按模型列表尝试备用模型。
3. Tool-call provider optimization：针对工具调用请求主动重排 provider，优先选择工具调用成功率和吞吐表现更好的 provider。

可借鉴点：

- Provider 过滤条件包括指定顺序、是否允许 fallback、是否要求 provider 支持请求里的所有参数、数据收集策略、只允许/忽略指定 provider、量化等级和最高价格。
- Provider 排序可以按价格、吞吐和延迟排序；默认策略会优先最近没有明显故障的 provider，再在低成本候选中按价格偏好做负载均衡，剩余 provider 作为 fallback。
- 性能阈值适合做“软偏好”而不是硬排除：例如吞吐或延迟不达标的 endpoint 可以排到后面，而不是直接不可用。
- 多模型 fallback 需要在响应中记录最终实际使用的模型，并按最终模型计费。
- Auto Exacto 针对工具调用请求自动重排 provider，信号包括实时吞吐、工具调用成功率和评测数据；工具调用成功率来自对返回 tool call 的 JSON、函数名和 schema 的验证。

本项目要实现的重点不应停留在“失败后换模型”，而是类似 Auto Exacto 的思路：在请求发出前就根据任务复杂度、上下文需求、能力要求、成本和可靠性生成候选序列，第一候选就是主动选择结果；失败回退只是沿用同一候选序列的兜底。

参考来源：

- OpenRouter Provider Routing：https://openrouter.ai/docs/guides/routing/provider-selection
- OpenRouter Model Fallbacks：https://openrouter.ai/docs/guides/routing/model-fallbacks
- OpenRouter Auto Exacto：https://openrouter.ai/docs/guides/routing/auto-exacto

## 当前项目适配结论

建议按“主动路由”为主线分三阶段落地：

1. 阶段一：增加 `auto:*` / `smart:*` 虚拟模型，用户请求这类模型时，系统主动按任务复杂度和上下文需求选择具体模型和渠道。
2. 阶段二：对管理员允许的具体模型启用“可替换模型池”，例如请求 `gpt-5-mini` 时，简单请求可主动降级到更便宜模型，复杂请求可主动升级到更强模型。
3. 阶段三：加入工具调用、结构化输出、长上下文和多模态的专用评分器，按不同任务类型主动切换到最合适的模型/渠道组合。

主动换模型必须受策略约束：管理员定义哪些模型可以互相替换、哪些 token / 用户允许自动升级或降级、最高可接受成本是多少。不同模型的行为、上下文长度、工具调用格式、结构化输出、图像/音频能力和价格都可能不同；所以自动切换必须可解释、可回放、可审计。

## 推荐架构

### 1. 路由输入抽象

新增一个内部 `SmartRouteRequest`，由现有请求解析结果生成，不直接替代 DTO：

```text
SmartRouteRequest
  original_model
  endpoint_type
  using_group
  token_id / user_id
  stream
  estimated_prompt_tokens
  max_output_tokens
  has_tools
  tool_count
  requires_json_schema
  has_images / audio / files
  reasoning_requested
  context_length_required
  user_policy
```

这些字段大多已经能从现有链路拿到：

- `dto.GeneralOpenAIRequest`、`dto.OpenAIResponsesRequest`、`dto.ClaudeRequest` 已能抽取 tools、stream、max tokens、reasoning 等信息。
- `types.TokenCountMeta` 已记录工具数量、消息数量、文件信息、最大输出长度。
- `service.EstimateRequestToken()` 已有 prompt token 估算。
- `RelayInfo` 已有请求格式、原始模型、流式状态、预估 prompt tokens 等运行信息。

### 2. 请求复杂度与上下文需求分级

建议先使用确定性规则，不引入 LLM 分类器：

复杂度解析不能把“对话越来越长”直接等价为“任务越来越难”。应该拆成两个独立维度：

```text
task_complexity      # 当前这一轮任务本身需要多强模型
context_requirement  # 当前这一轮需要多少上下文窗口和上下文处理
```

例如用户问“把上面那句话翻译成英文”，即使历史对话很长，当前任务仍然是 `simple`；它只是可能需要从最近上下文中找到“上面那句话”。这种请求不应该直接升到高强度模型，而应该走低成本长上下文模型，或只保留最近引用范围。

任务复杂度分级：

```text
simple:
  改写、翻译、简单问答、格式调整、短摘要、低 max_tokens

standard:
  普通问答、常规摘要、轻量结构化输出、少量上下文推理

complex:
  多步骤推理、代码生成/调试、多工具、强 schema、图像理解、方案设计

critical:
  高可靠工具调用、复杂结构化输出、生产关键任务、用户明确指定高质量/高推理
```

上下文需求分级：

```text
short:
  只需要当前消息或最近 1-3 轮

medium:
  需要最近若干轮上下文，或需要引用少量历史约束

long:
  需要较长历史、文件片段、工具结果或多轮任务状态

huge:
  需要整段会话、长文件、批量工具结果或必须先压缩才能放入上下文
```

任务复杂度分数可以由规则加权：

```text
task_score =
  tool_score
  + schema_score
  + multimodal_score
  + reasoning_score
  + code_or_math_score
  + planning_score
  + output_strictness_score
```

上下文需求分数单独计算：

```text
context_score =
  estimated_prompt_tokens_score
  + message_count_score
  + explicit_reference_scope_score
  + file_or_tool_state_score
  + conversation_state_score
```

当前轮引用范围是关键判断项：

```text
只引用最近 1-3 轮：
  不需要全量 history

明确说“总结整段对话 / 根据之前所有内容 / 延续上面的完整方案”：
  需要长上下文或会话共识摘要

带文件、代码、工具结果引用：
  按引用对象决定上下文，而不是按全部 history 决定
```

这比调用一个“分类模型”更稳：无额外成本、无循环依赖、可解释、可以在日志里记录每个因子。后续可以用真实日志微调权重，但初始版本不应依赖额外模型判断。

任务复杂度和上下文需求判断不是失败后的补救，而是请求发出前的第一步。一次请求的主动路由流程应为：

```text
解析原始请求
  -> 提取能力需求和 token 估算
  -> 计算 task_complexity 和 context_requirement
  -> 根据两维结果选择 route_profile
  -> 根据 original_model / policy / route_profile 选择模型池
  -> 过滤不满足能力、上下文长度和成本上限的模型
  -> 展开为“模型 + 渠道”候选
  -> 按成本、可靠性、延迟、质量、亲和度评分
  -> 选择第一候选并改写上游请求模型
  -> 记录 original_model / selected_model / decision_reason
```

典型路由组合：

```text
simple + short:
  cheap / fast

simple + long:
  cheap_long_context，或先压缩再走 fast 模型

standard + medium:
  balanced

standard + long:
  balanced_long_context

complex + short:
  reasoning_or_quality

complex + long:
  premium_long_context

critical + any:
  reliability_first + capable_model
```

最终不是“对话越长模型越强”，而是：

```text
对话越长 -> 上下文处理策略更谨慎
任务越复杂 -> 模型能力更强
两者独立评分，再组合路由
```

如果用户请求的是虚拟模型，例如 `auto:balanced`，路由器必须主动选出真实模型；如果用户请求的是具体模型，只有在策略允许时才进入可替换模型池。这样既支持主动切换，又不会让所有历史客户端在不知情的情况下被改模型。

### 3. 模型能力画像

需要补充模型层面的能力画像，建议先放在现有 `models` 表的扩展字段或独立 `model_capabilities` 表。

核心字段：

```text
model_name
quality_tier         # economy / standard / premium / reasoning
max_context_tokens
supports_tools
supports_json_schema
supports_vision
supports_audio
supports_reasoning
supports_stream
endpoint_types
default_complexity_min
default_complexity_max
replacement_pool
replacement_policy     # exact_only / allow_upgrade / allow_downgrade / allow_both
```

来源优先级：

1. 管理员手动配置。
2. 内置模型能力模板。
3. 从渠道/上游模型同步结果补充模型名和上下文长度。
4. 后续再考虑从外部模型目录同步。

### 4. 渠道运行画像

当前 `channels.response_time` 是一次测试时间，不足以支撑智能调度。建议新增滚动指标，或先存入 Redis / 内存，后续再持久化。

核心指标：

```text
channel_id
model_name
group
success_rate_1m / 5m / 30m
error_rate_1m / 5m / 30m
timeout_rate
rate_limit_rate
p50_latency_ms
p90_latency_ms
p99_latency_ms
throughput_tokens_per_second
tool_call_success_rate
json_schema_success_rate
last_failure_at
cooldown_until
```

写入来源：

- 消费日志和错误日志。
- `RelayInfo.FirstResponseTime`、总耗时、stream 状态。
- tool call 返回校验结果。
- JSON/schema 输出校验结果。
- 自动禁用和重试错误原因。

### 5. 成本估算

路由阶段只能估算成本，最终仍以实际用量结算。

估算公式应复用现有计费配置：

```text
estimated_cost =
  estimated_prompt_tokens * input_price
  + estimated_output_tokens * output_price
  + media_cost
  + tool_or_reasoning_surcharge
```

再乘：

```text
group_ratio * channel_price_ratio
```

注意：不要在路由层重新发明价格体系。路由只读取 `ModelPrice`、`ModelRatio`、tiered billing、group ratio、channel ratio 的快照。

### 6. 候选评分

候选对象建议从“模型 + 渠道”组合开始，而不是只对渠道评分：

```text
SmartRouteCandidate
  model_name
  channel_id
  group
  capability_match
  estimated_cost
  reliability_score
  latency_score
  throughput_score
  quality_score
  affinity_score
  final_score
```

推荐默认策略：

```text
final_score =
  policy.cost_weight * cost_score
  + policy.reliability_weight * reliability_score
  + policy.latency_weight * latency_score
  + policy.quality_weight * quality_score
  + policy.affinity_weight * affinity_score
```

策略模板：

- `cost_first`：低成本优先，可靠性兜底。
- `balanced`：成本、可靠性、延迟均衡。
- `quality_first`：复杂请求和工具调用质量优先。
- `latency_first`：交互式低延迟优先。
- `reliability_first`：生产关键请求优先成功率。

主动切换时，候选生成要先过硬约束，再做软评分：

硬约束：

- token / 用户是否允许使用该模型。
- 模型是否支持请求端点、stream、tools、JSON/schema、vision、audio、reasoning。
- 估算上下文长度是否小于模型上限。
- 估算成本是否小于 token / 用户 / 系统策略上限。
- 渠道是否启用、是否支持请求路径、是否在当前分组能力表中可用。

软评分：

- 简单请求更偏向低成本和低延迟。
- 标准请求在成本、可靠性和延迟之间均衡。
- 复杂请求提高质量、上下文长度、工具成功率和可靠性权重。
- 关键请求优先可靠性和模型能力，成本只作为上限约束。

示例映射：

```text
simple    -> economy / fast 模型池，优先 cost_first 或 latency_first
standard  -> standard 模型池，优先 balanced
complex   -> premium / reasoning / tool 模型池，优先 quality_first
critical  -> long-context / tool-strong / high-reliability 模型池，优先 reliability_first
```

### 7. 主动切换与上下文处理

这里的“切换”首先指请求发出前主动选择不同模型/渠道，不只是失败后重试。

同一次请求内的主动切换：

- 路由阶段必须在第一次上游请求前完成任务复杂度、上下文需求、模型选择和渠道选择。
- 选中模型后，在进入 adaptor 前改写 `RelayInfo.OriginModelName` / `UpstreamModelName` 以及请求 DTO 的 `model` 字段。
- 同时保留 `original_model`，用于日志、计费解释、用户响应和后续排查。
- 主动切换后的计费必须按最终 `selected_model + selected_channel` 的价格快照执行，不能按原始模型收费。
- 如果候选模型需要不同请求格式，只能选择当前 relay/adaptor 已支持的转换链路；不能在路由层临时拼接协议转换。

“上下文切换”还要分清两种场景：

1. 同一次请求内的上游失败重试。
2. 多轮对话中下一轮选择不同模型。

同一次请求内：

- 在收到上游有效响应前，可以沿着已经生成的候选序列切换渠道或模型。
- 一旦开始向客户端输出流式内容，不应再切换模型或渠道，否则会造成响应语义断裂。
- 非流式请求如果上游连接失败、限流、5xx、内容过滤、上下文过长，可以继续尝试下一候选。
- 如果上游已经返回部分内容、工具调用或计费 usage，不应自动换模型继续同一个响应。

多轮对话内：

- 用户完整上下文仍由客户端传入；服务端不应私自丢弃历史消息，也不应把上一轮模型的隐藏状态当作可迁移上下文。
- 如果下一轮切换到上下文更短的模型，必须先检查 estimated tokens + max output tokens 是否超过目标模型限制。
- 如果超限，可以：
  - 选择更长上下文模型；
  - 返回明确错误；
  - 仅在用户/管理员启用“自动压缩上下文”时，调用摘要模型生成可审计摘要。
- 自动摘要不能默认开启，因为它会改变用户上下文语义，且会增加一次额外调用和计费。

子 agent 类比：

- 路由器不直接“扮演子 agent”，而是把模型分成角色池：fast、cheap、reasoning、tool、vision、long-context。
- 每次请求根据复杂度和能力需求主动选择角色池，再映射到具体模型和渠道。
- 如果后续要支持真正多 agent 工作流，应独立设计 orchestrator，不能塞进基础 relay 路由层。

主动路由和多 agent 协作的区别：

- 主动路由处理的是“同一个请求应该交给哪个模型和渠道”。它通常只调用一个最终选中的模型，最多在请求失败前后沿候选序列做重试或回退，不主动拆解任务。
- 多 agent 协作处理的是“一个复杂目标应该如何拆成多个角色和步骤”。它需要 planner、executor、reviewer、researcher 等角色，通常会产生多次模型调用、多份中间产物和最终汇总。
- 主动路由的输入是原始请求、模型能力、渠道状态、价格、可靠性、上下文需求；输出是 `selected_model + selected_channel + decision_reason`。
- 多 agent 的输入是用户目标和可用工具；输出是任务计划、子任务执行结果、合并策略、审查结果和最终答复。
- 主动路由必须保持 API 网关语义：不改变用户请求的任务结构，只选择更合适的执行模型。
- 多 agent 属于更上层的编排能力：可以把一个用户请求拆成多个内部请求，再分别调用主动路由选择每一步的模型和渠道。

推荐边界：

```text
Client Request
  -> Optional Agent Orchestrator
      -> Step 1 Internal Request -> Smart Router -> Model/Channel
      -> Step 2 Internal Request -> Smart Router -> Model/Channel
      -> Review Internal Request  -> Smart Router -> Model/Channel
  -> Final Response
```

也就是说，主动路由是基础设施能力，多 agent 是应用层编排能力。后续如果实现多 agent，不应让 relay 层直接保存 agent state、拆任务或合并产物，而应由独立 orchestrator 负责；relay 层只暴露“给定请求和约束，选出最合适模型/渠道”的能力。

后续技术要点：

- 请求特征提取要稳定、低成本、可解释，优先规则评分和配置化阈值，不默认引入额外 LLM 分类调用。
- 模型能力画像要覆盖上下文窗口、工具调用、结构化输出、多模态、推理能力、成本、延迟、可用渠道和历史成功率。
- 主动替换边界要可配置：虚拟模型 `auto:*` 默认允许自由选择；具体模型只允许在管理员配置的替换池内升级或降级。
- 路由决策必须写入日志，但不能记录 API key、token、secret 或完整敏感请求体。
- 成本预估和最终计费必须分离：路由阶段只做估算和上限判断，结算阶段按最终模型、最终渠道和实际 usage 的价格快照计费。
- 指标聚合要控制写入量，高频成功率、延迟和失败原因优先在内存或 Redis 滑动窗口聚合，再周期性落库。

与 OpenRouter 智能路由的核心差异：

- OpenRouter 更偏平台级 provider routing：在同一模型背后选择 provider，并提供排序、禁用、价格、吞吐、延迟和 fallback 等策略。
- 本项目需要同时处理“模型选择”和“渠道选择”：既要决定是否从一个模型主动切到另一个模型，也要在本项目已有渠道、倍率、分组、能力和日志体系内选择具体渠道。
- OpenRouter 的 fallback 更接近请求失败后的模型候选兜底；本方案把失败回退作为候选序列消费机制，但主线是请求发出前的主动复杂度路由。
- OpenRouter 的 Auto Exacto 偏自动挑选合适模型；本项目还要受用户分组、API Key 限额、渠道倍率、模型倍率、模型管理元数据和旧 new-api 数据兼容约束影响。
- 本项目必须兼容 SQLite、MySQL、PostgreSQL 三类部署，路由指标和日志设计不能依赖单一数据库特性。

### 8. 跨模型上下文共识

不同模型之间没有共享隐藏状态，所谓“保持上下文共识”只能依赖显式上下文协议。当前 OpenAI/Claude/Gemini 兼容 API 本质上是无状态请求：客户端每次把 messages/input 传进来，服务端把它转发到某个上游。因此主动切换模型时，必须保证新模型看到的显式上下文足够表达同一任务、同一约束和同一中间状态。

建议把上下文共识分成四层：

```text
L0 原始用户上下文：客户端传入的 messages/input/content
L1 路由上下文：任务复杂度、上下文需求、选型理由、selected_model、original_model
L2 任务共识上下文：目标、已确认约束、术语、输出格式、禁止事项
L3 执行状态上下文：工具调用结果、文件引用、阶段性结论、压缩摘要
```

L0 必须始终保留；L1 只进日志和调试，不默认注入给模型；L2/L3 只有在启用“服务端会话上下文”或“自动压缩上下文”时才参与请求改写。

主动切换模型时的共识策略：

- 单次无状态请求：不额外生成共识摘要，直接把完整客户端上下文交给选中的模型；只要目标模型能力和上下文长度满足要求，就能保持共识。
- 同一会话多轮请求：如果客户端每轮都传完整 history，服务端只做长度和能力校验，不替用户维护隐藏记忆。
- 服务端托管会话：需要引入 `conversation_state`，记录任务目标、关键约束、已完成步骤、工具结果和用户偏好；每次切换模型时，把这个状态作为可审计的 system/developer 上下文片段注入。
- 上下文超长或切到短上下文模型：必须先执行显式压缩，生成“上下文共识摘要”，并记录摘要来源、压缩模型、压缩时间、覆盖的消息范围和 token 变化。

共识摘要建议结构化，而不是自然语言大段总结：

```json
{
  "task_goal": "用户要完成的目标",
  "current_phase": "planning | coding | debugging | review | final",
  "decisions": ["已经确定的方案或约束"],
  "open_questions": ["仍未确认的问题"],
  "user_preferences": ["用户明确偏好"],
  "domain_terms": {"术语": "定义"},
  "files_or_artifacts": ["相关文件、任务、工具结果引用"],
  "must_preserve": ["不能改变的事实、格式、接口、业务规则"],
  "last_model": "上一次使用的模型",
  "last_response_summary": "上一轮输出的简要状态"
}
```

这类摘要应作为“共识层”，不是替换原始上下文。只有当原始上下文放不下时，才用摘要替代被压缩的旧消息；最近几轮原文仍应优先保留。

切换策略里的关键规则：

- 低复杂度请求可以在模型间主动切换，但不要注入额外共识层，避免污染普通请求。
- 复杂任务、代码修改、工具调用、多轮推理需要稳定模型或稳定模型池；如果主动切换，必须带上 L2/L3 共识上下文。
- 工具调用链中途不要随意换模型。已发出的 tool call、tool result、函数 schema 必须在下一次模型调用中完整保留，否则新模型可能不知道工具执行到了哪一步。
- 结构化输出任务切换模型时，必须保留 schema、格式要求、已验证失败原因和重试次数。
- 多模态任务切换模型时，必须确认目标模型能访问同样的图片/音频/文件引用；不能只传摘要而丢失媒体输入。

实现上建议新增 `ContextConsensus` 内部对象：

```text
ContextConsensus
  conversation_id
  request_id
  original_model
  selected_model
  preserved_recent_messages
  compressed_message_ranges
  summary
  tool_state
  schema_state
  token_budget
  generated_by_model
  created_at
```

它不必第一阶段持久化。阶段一可以只在单次请求内计算 token budget 和是否允许切换；阶段二再支持 Redis/数据库保存会话共识；阶段三再接入自动压缩。

### 9. 日志与审计

智能路由必须可解释。建议在消费日志 `other` 中记录：

```json
{
  "smart_routing": {
    "enabled": true,
    "policy": "balanced",
    "complexity": "complex",
    "original_model": "auto",
    "selected_model": "gpt-5-mini",
    "selected_channel_id": 12,
    "candidate_count": 8,
    "fallback_index": 0,
    "score_factors": {
      "cost": 0.82,
      "reliability": 0.91,
      "latency": 0.73,
      "quality": 0.88
    },
    "context_consensus": {
      "mode": "stateless_full_context",
      "compacted": false,
      "preserved_recent_messages": 8
    },
    "decision_reasons": [
      "tools_required",
      "p90_latency_within_threshold",
      "lower_estimated_cost"
    ]
  }
}
```

不要记录请求正文、API Key、上游 token、secret。

## 推荐落地步骤

### 阶段一：虚拟模型主动路由

范围：

- 新增 `auto:cheap`、`auto:balanced`、`auto:quality`、`auto:fast`、`auto:reasoning`。
- 用户请求虚拟模型时，系统根据任务复杂度和上下文需求主动选择真实模型和渠道。
- 使用完整客户端上下文，不做自动摘要。
- 记录 `original_model`、`selected_model`、任务复杂度、上下文需求和候选评分。

好处：

- 用户明确授权自动选模型。
- 可以直接验证两维分类和模型池配置。
- 计费和日志可以按最终真实模型落地。

### 阶段二：具体模型主动升级/降级

仅管理员开启后生效：

```text
gpt-5-mini -> simple 可降级到 economy 池
gpt-5-mini -> complex 可升级到 premium/reasoning 池
claude-sonnet-* -> tool-heavy 可切到 tool-strong 池
```

必须配置模型替换池和边界：

- 哪些原始模型允许替换。
- 允许升级、降级还是双向。
- 最高估算成本。
- 是否允许跨供应商。
- 是否必须保持同一模型家族。

### 阶段三：会话共识和自动压缩

面向多轮任务和长上下文：

- 引入可选 `conversation_id`。
- 维护 `ContextConsensus`。
- 支持显式上下文摘要和最近消息保留策略。
- 对工具调用、结构化输出、多模态输入做状态保持。

### 阶段四：同模型智能渠道排序和失败回退增强

这个能力不是主线，但仍然必要：

- 从现有 `Ability` 候选中取出渠道列表。
- 基于成本、状态、近期错误、延迟和渠道亲和排序。
- 保留现有 `RetryParam` 语义。
- 失败时沿用主动路由生成的候选序列继续尝试。

## 需要新增的配置

建议新增系统配置：

```text
SmartRoutingEnabled
SmartRoutingDefaultPolicy
SmartRoutingVirtualModelsEnabled
SmartRoutingConcreteModelSwitchEnabled
SmartRoutingAllowedReplacementPools
SmartRoutingAllowModelFallback
SmartRoutingAllowAutoUpgrade
SmartRoutingAllowAutoDowngrade
SmartRoutingMaxFallbacks
SmartRoutingMaxEstimatedQuota
SmartRoutingMetricsWindowSeconds
SmartRoutingLogDecisionEnabled
SmartRoutingContextConsensusEnabled
SmartRoutingAutoCompactionEnabled
```

建议新增 token / 用户策略：

```text
smart_routing_enabled
smart_routing_policy
allowed_model_pools
allowed_replacement_models
max_estimated_quota
allow_model_fallback
allow_context_compaction
conversation_context_mode
```

## 风险与边界

- 自动换模型会影响输出质量、风格和工具调用格式，默认必须关闭。
- 主动切换必须发生在上游请求发出前；失败回退只是消费候选序列，不是智能路由主逻辑。
- 流式响应开始后不能再切换，否则客户端会收到混合响应。
- 结构化输出和工具调用必须检查候选模型/渠道是否支持对应参数。
- 跨模型没有共享隐藏状态；上下文共识只能通过完整 history、结构化摘要、工具状态和明确约束传递。
- 上下文压缩不是普通路由的一部分，应独立计费、独立日志、默认关闭。
- 任务复杂度和上下文需求分类必须可解释，避免黑盒路由导致账单和质量争议。
- SQLite 生产部署下不要把高频路由指标全部同步写入主库，应优先使用内存/Redis 聚合后批量落库。

## 推荐决策

当前最稳妥方案是：

1. 先提供显式虚拟模型 `auto:*`，建立主动任务复杂度和上下文需求路由闭环。
2. 再对具体模型开放可配置替换池，实现主动升级/降级。
3. 然后引入会话共识和自动压缩，解决跨模型多轮任务的一致性。
4. 同步增强同模型渠道排序和失败回退，让候选序列既能主动选择也能兜底。

这样既能复用本项目现有能力路由、渠道倍率、自动禁用、日志和重试体系，又能逐步接近 OpenRouter 的智能路由能力，并把上下文切换风险控制在可审计范围内。
