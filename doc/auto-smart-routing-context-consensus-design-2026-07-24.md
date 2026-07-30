# 智能路由 ContextConsensus、自动压缩与工具状态保持设计

日期：2026-07-24

## 1. 背景

阶段一智能路由已经完成 `auto:*` / `smart:*` 虚拟模型、候选评分、同模型失败重试、模型画像和管理员可配置模型池。当前 `service/smartrouting.ContextConsensusLog` 只记录：

- `mode=stateless_full_context`
- `compacted=false`
- `preserved_recent_messages`

系统尚未维护真正的会话共识，也不会自动压缩上下文或管理工具状态。

跨模型不存在可迁移的隐藏记忆。所谓上下文共识，只能通过客户端显式历史、结构化摘要、工具调用状态、输出约束和可验证的上游绑定信息实现。

本设计解决以下问题：

1. 模型在多轮请求间切换时，怎样保持任务目标、已确认约束和执行状态。
2. 完整上下文超过所有允许候选窗口时，怎样在明确授权后安全压缩。
3. 怎样避免破坏工具调用链、结构化输出、多模态引用和上游托管状态。
4. 怎样独立计费、审计和保护压缩产生的敏感数据。

## 2. 核心决策

1. 默认继续使用 `stateless_full_context`，不维护服务端记忆，不自动压缩。
2. `ContextConsensus` 是请求准备层，不属于候选评分器；`service/smartrouting` 只消费上下文约束和有效 token 预算。
3. 完整上下文能被任一合规候选容纳时，不为降低成本主动压缩。
4. 自动压缩必须同时通过系统配置、API Key 策略和本次请求显式授权。
5. 用户、工具和模型生成内容不得因摘要而提升为 `system` / `developer` 权威指令。
6. 工具调用及其结果是不可分割的因果段；未闭合工具段、opaque signature 和 provider-bound 状态不得摘要。
7. `/v1/responses/compact` 是 OpenAI/Codex 上游原生能力，不等同于跨模型可移植的共识摘要。
8. 压缩调用与最终生成调用使用独立请求、独立计费会话和独立消费日志。
9. 首个代码阶段只做协议状态识别、预算校验和切换保护，不调用摘要模型。

## 3. 范围与非目标

### 3.1 本设计范围

- Chat Completions、OpenAI Responses、Claude Messages、Gemini 四类协议。
- 完整历史模式、请求内压缩和可选托管共识模式。
- 工具调用、结构化输出、多模态引用和 provider-bound 状态分类。
- token 预算、压缩计划、状态存储、并发、计费、日志和测试策略。

### 3.2 非目标

- 不迁移或保存模型隐藏推理、chain-of-thought。
- 不在基础 relay 中实现多 agent 编排。
- 不默认替用户删除历史消息。
- 不把渠道亲和 `prompt_cache_key` 当成会话正文或网关会话 ID。
- 不在首个阶段新增数据库表。
- 不承诺所有上游私有文件、缓存对象或推理签名可以跨供应商迁移。

## 4. 上下文分层

```text
L0 原始上下文
  客户端传入的 system/developer/user/assistant/tool 消息、input、contents

L1 路由上下文
  original_model、selected_model、selected_channel、候选评分、决策原因
  只用于路由和审计，不注入模型

L2 任务共识
  任务目标、用户已确认约束、决策、术语、输出契约、未决问题

L3 执行状态
  已完成/待完成步骤、工具状态、schema 状态、artifact 引用、上游绑定
```

L0 始终是事实来源。L2/L3 只在请求明确启用压缩或托管共识时生成。任何摘要都不能覆盖原始 system/developer 约束，也不能把未确认的模型推断标记为用户事实。

## 5. 会话模式与请求契约

### 5.1 模式

| 模式 | 行为 | 持久化 |
| --- | --- | --- |
| `stateless_full_context` | 完整客户端上下文直接路由 | 无 |
| `stateless_compacted` | 只压缩本次请求的旧上下文 | 无 |
| `managed_consensus` | 服务端维护版本化 L2/L3 | Redis，加密、短 TTL |
| `upstream_state_pinned` | 请求依赖上游 opaque state，固定原绑定 | 只保存绑定映射和摘要元数据 |

### 5.2 网关专用头

建议使用只由网关消费、绝不向上游透传的请求头：

```text
X-New-Api-Context-Id
X-New-Api-Context-Mode
X-New-Api-Context-Revision
```

约束：

- `Context-Mode` 允许 `full`、`auto_compact`、`managed`。
- `managed` 必须同时提供 opaque `Context-Id` 和期望 revision。
- 会话所有权绑定 `user_id + token_id + endpoint_family`。
- 外部 Context ID 只用于计算 HMAC key，不在日志和数据库中保存原值。
- 新增头必须加入禁止上游透传名单，不能依赖渠道 header passthrough 配置。
- OpenAI Responses 的 `conversation`、`previous_response_id` 不能复用为网关 Context ID。

## 6. 核心对象

```text
ContextEnvelope
  protocol
  original_model
  immutable_instructions
  compressible_segments
  preserved_segments
  tool_state
  schema_state
  media_state
  provider_binding
  token_breakdown
  source_digest
```

```text
ContextConsensus
  version
  owner_user_id
  owner_token_id
  conversation_key_hash
  revision
  mode
  task_consensus
  execution_state
  provider_binding
  compaction_history
  source_digest
  created_at
  updated_at
  expires_at
```

```text
ConsensusFact
  field
  value
  provenance          # policy / user_confirmed / assistant_inferred / tool_observed
  source_range
  source_digest
  confidence
```

```text
ToolExchange
  protocol
  sequence
  parallel_group
  call_id
  function_name
  arguments_digest
  result_digest
  status              # pending / completed / failed
  raw_call_present
  raw_result_present
  opaque_state_present
  required_binding
```

```text
ContextRoutingConstraint
  mode
  effective_prompt_tokens
  required_binding
  switch_allowed
  compaction_required
  reason_codes
```

`ContextConsensusLog` 继续作为精简审计对象，不能承载会话正文或完整共识状态。

## 7. 可移植性与绑定矩阵

| 内容/状态 | 可移植性 | 处理规则 |
| --- | --- | --- |
| 原始 system/developer/instructions | 可移植 | 原角色、原顺序、原文保留，不摘要 |
| 普通文本历史 | 可移植 | 仅完整 turn 可被压缩 |
| 当前 tools/schema | 条件可移植 | 原样保留，目标协议必须已有合法转换链 |
| 已闭合工具段 | 条件可移植 | 默认保留；后续才允许摘要旧结果 |
| 未闭合/并行工具段 | 不可压缩 | 整组调用、参数、已有结果和 schema 原样保留 |
| OpenAI `previous_response_id`、`conversation`、`context_management` | provider-bound | 固定原 provider、凭据槽位、渠道、协议和兼容模型 |
| Claude `container`、`context_management`、server tools/MCP | provider-bound | 固定原 Claude 绑定 |
| Claude thinking `signature` | opaque/provider-bound | 原样透传，不摘要、不记录 |
| Gemini `cachedContent`、`thoughtSignature` | opaque/provider-bound | 固定原 Gemini 绑定 |
| provider `file_id`、Gemini File URI、临时签名 URL | provider-bound 或短期有效 | 校验所有权和有效期；不能用文字摘要替代 |
| base64 或稳定可访问媒体 | 条件可移植 | 校验目标能力、MIME、大小和授权范围 |
| `prompt_cache_key` | 非会话状态 | 只作 affinity hint，不用于恢复历史 |

### 7.1 ProtocolBinding

```text
ProtocolBinding
  binding_level       # none / model_family / provider / channel / credential
  relay_format
  channel_id
  channel_type
  upstream_model
  multi_key_index
  credential_fingerprint
  state_reference_hashes
  reason_codes
```

多 Key 渠道不能只绑定 `channel_id`，还必须绑定稳定凭据槽位。不得保存真实渠道密钥；`credential_fingerprint` 使用服务端 HMAC 生成且不写普通日志。

成功响应产生的 response/conversation/cache ID 必须登记到所有者隔离的绑定映射。客户端提交未登记、已过期或所有权不匹配的上游状态 ID 时，必须 fail closed。

## 8. 工具状态不变量

1. assistant tool call 与对应的全部 tool result 构成一个原子因果段。
2. 并行调用只有在同组全部结果返回后才算闭合。
3. `call_id` / `tool_call_id` / `tool_use_id`、函数名、顺序、参数和结果不得被自动改名或补造。
4. 当前工具 schema 原样保留并参与能力和 token 预算校验。
5. 非幂等工具调用不得因压缩、重试或状态恢复而重新执行。
6. 缺少结果、ID 冲突、schema digest 不一致时返回明确错误。
7. Claude signature、Gemini thoughtSignature、server tool/MCP 状态不进入摘要模型。
8. 首版不压缩工具结果；未来开启 `allow_tool_result_compaction` 时，只能处理已闭合的旧工具段，并保留 digest、状态和 artifact 引用。

Gemini 同名并行调用如果没有稳定 ID，不能安全转换到依赖 call ID 的协议，应保持 Gemini 绑定或拒绝跨协议切换。

## 9. 状态机与请求链路

```text
stateless
  -> extracted
  -> validated
  -> provider_binding_checked
  -> full_context_ready -----------------------> routed
  -> compaction_required
       -> compaction_authorized
       -> compacting
       -> compacted_validated
       -> token_recounted
       -> rerouted
  -> in_flight
  -> committed | aborted
```

推荐链路：

```text
解析一次协议 DTO
  -> 生成 ContextEnvelope
  -> 校验工具图、schema、媒体和 provider binding
  -> 计算完整上下文精确预算
  -> 用完整上下文过滤候选
  -> 有候选：不压缩，正常排序
  -> 无候选且已授权：生成一次 CompactionPlan
  -> 发起独立压缩子请求并校验结构化摘要
  -> 协议适配器重写请求
  -> 按最终转换格式重新计数、重新过滤和排序
  -> 发起主上游请求
  -> 完整成功后更新状态和绑定 revision
```

当前 `middleware.Distribute()` 在 relay DTO 精确解析之前选模。首版不能直接把摘要调用塞进 middleware；应先提供 `validate_only` 的 ContextEnvelope/绑定检测，后续再逐步抽出统一的请求准备阶段，避免重复解析和计费顺序错误。

## 10. Token 预算与压缩触发

### 10.1 最终预算不变量

```text
immutable instructions
+ consensus wrapper and summary
+ preserved recent turns
+ complete tool segments
+ tools and output schema
+ media token estimate
+ requested max output
+ provider protocol overhead
+ safety margin
<= selected model max context
```

预算必须在最终真实模型和最终转换协议确定后使用对应 tokenizer 重算。现有按请求 JSON 字节数除以 4 的估算只能用于初筛，不能作为压缩与最终放行依据。

缺省 max output 与客户端显式 `0` 必须区分，不能在 DTO 往返时丢失显式零值。

### 10.2 压缩触发顺序

1. 完整上下文能被合规候选容纳：不压缩。
2. 当前候选不够：优先选择允许池中的更长上下文候选。
3. 所有允许候选都不够，且三重授权通过：压缩一次。
4. 压缩后仍超限：明确失败，不二次递归压缩。

### 10.3 硬上限

- 单请求最多一次自动压缩。
- 固定 timeout。
- 固定最大输入 token、最大摘要 token 和最大估算费用。
- 压缩模型必须使用显式真实模型池，禁止再次进入 `auto:*` 智能路由。
- 压缩请求禁用 stream、tools、MCP 和自动压缩。
- 压缩模型上下文不足时直接失败，首版不做递归/分层摘要。

## 11. CompactionPlan 与安全切分

```text
CompactionPlan
  source_digest
  covered_segments
  preserved_segments
  immutable_segments
  open_tool_segments
  media_segments
  target_input_tokens
  max_summary_tokens
  policy_version
```

切分规则：

1. 永久保留原始 system/developer/instructions。
2. 永久保留当前用户 turn 和输出 schema。
3. 至少保留配置数量的最近完整 turn，但不能切断工具原子段。
4. 保留所有未完成工具段、opaque state 和仍被引用的媒体。
5. 只压缩更早的完整普通 turn。
6. 如果没有安全可压缩区间，返回上下文超限错误。

## 12. 共识摘要格式与权限隔离

摘要必须通过版本化 JSON Schema 校验：

```json
{
  "version": 1,
  "task_goal": [],
  "current_phase": "",
  "decisions": [],
  "must_preserve": [],
  "open_questions": [],
  "user_preferences": [],
  "domain_terms": {},
  "completed_steps": [],
  "pending_steps": [],
  "artifact_refs": [],
  "tool_result_summaries": [],
  "source_ranges": [],
  "source_digest": ""
}
```

每个事实项都应携带 provenance 和来源摘要，不能把 `assistant_inferred` 自动升级为 `user_confirmed`。

权限隔离规则：

- 原始 system/developer 内容保持原位。
- 网关可加入不含用户内容的固定 system/developer wrapper，用于声明摘要是“不可信历史数据”。
- 实际摘要载荷必须以 user-level 数据消息注入，不能直接拼入 system/developer 文本。
- 工具输出中的指令仍是工具数据，不得因摘要改变角色优先级。
- 不生成、不保存、不要求模型返回隐藏推理。

## 13. 协议重写规则

| 协议 | 共识载荷位置 | 附加约束 |
| --- | --- | --- |
| Chat Completions | 被压缩旧 turn 所在位置的 user 数据消息 | 原 system/developer 和最近 turn 原样保留 |
| Responses | 无 stateful 字段时使用独立 user input item | 不覆盖 `instructions`；stateful 请求只允许原绑定或原生 compact |
| Claude | 独立 user text block | 不把摘要追加到 `system`；保留 cache_control 和 signature block |
| Gemini | 独立 user content/part | 不修改带 thoughtSignature 的 model part |

所有协议解析和重写必须使用 DTO 与 `common.Marshal` / `common.Unmarshal`，禁止用字符串替换操作结构化消息。

多模态内容只保留可继续访问的原引用或数据；摘要文本不能替代图片、音频、视频或文件本体。

## 14. 流式提交屏障

1. 压缩必须发生在主上游请求和任何下游输出之前。
2. 首个下游字节、SSE event、tool-call delta 或 response item 发出后，禁止切换模型、重新压缩或重放请求。
3. 客户端断开、流不完整或工具参数只返回一部分时，不推进托管会话 revision。
4. 首版 `managed_consensus` 仅支持非流式请求；流式请求保持 request-local/stateless，直到状态提交协议另行设计。
5. 首字节后上游失败只终止当前流并按可用 usage/现有估算结算，绝不自动重放非幂等工具调用。

## 15. 存储、并发与幂等

### 15.1 分阶段存储

- 阶段 A/B：不持久化 L0 正文；请求内对象只存在于内存。
- 阶段 C：Redis 保存加密的 L2/L3、revision、TTL 和绑定映射。
- 主数据库不承载高频 L2/L3 会话正文；只持久化计费 operation、日志 outbox、revision intent、恢复阶段和 AEAD outcome checkpoint，用于跨请求恢复数据库结算事实与已提交响应。

### 15.2 Redis 约束

- 会话 key：`new-api:context_consensus:v1:<owner_hmac>:<conversation_hmac>`。
- 使用 revision CAS、短期 lease、fencing token 和续租。
- 同一会话默认串行；revision 不符返回 409，lease 占用返回 409/429。
- revision intent 绑定 owner、conversation、expected revision、客户端幂等键 HMAC、source digest、协议和 policy version；同一 revision 或幂等键的语义变化必须冲突。
- Redis 不可用时，managed 新请求和未提交恢复必须失败关闭；只有数据库已标记 `committed` 的 outcome 可以直接回放。非 managed 且客户端携带完整历史的请求仍按 stateless 规则处理。
- 托管状态或上游绑定无法恢复时必须 fail closed，不使用进程内缓存伪装多实例一致性。

`pkg/cachex.HybridCache` 当前没有 CAS 语义，不能直接承担托管会话 revision 更新。`common.RedisSet` 在 Debug 模式会打印 value，也不能保存共识 payload。应实现不输出值的专用 repository，并使用 Redis 原子脚本完成 CAS/lease。

### 15.3 加密与删除

- 共识正文使用应用层 AEAD（AES-GCM）加密，配置独立的 `CONTEXT_CONSENSUS_ENCRYPTION_KEY` 和 `key_version`。
- active key 只负责新写入；最多 4 个旧版本通过 `CONTEXT_CONSENSUS_PREVIOUS_ENCRYPTION_KEYS` 提供有界读取窗口，版本不得重复。
- key version 同时参与 AEAD envelope 选择和 HMAC namespace。读取旧 namespace 后，下一次 revision CAS 必须在同一 Redis Lua 中写入 active namespace、删除旧 state，并推进 active fencing counter；禁止持续刷新旧 namespace TTL。
- current/previous namespace 同时存在时视为轮换冲突并失败关闭，不根据 revision 或时间猜测权威副本。旧 key 至少保留一个最大 state TTL 窗口，迁移完成或旧 state 自然过期后再退役。
- 不复用 session secret、渠道 key 或 API Key。
- 明确 TTL、最大保留时间、删除入口和过期销毁验证。
- 未配置稳定加密密钥时，`managed_consensus` 不得启用。

## 16. 敏感数据边界

压缩等于新增一次向模型发送历史，必须明确限制外发 provider 和区域。

以下数据禁止进入压缩请求、托管状态或普通日志：

- API Key、Authorization、Cookie、Token、Secret、认证头。
- MCP 凭据、支付数据、完整个人信息。
- 工具结果中的凭据或完整非结构化敏感输出。
- 完整请求正文、完整摘要正文、opaque signature。
- 原始 conversation/response/file ID 和签名 URL。

压缩请求不能复用主请求的完整 headers，只按白名单构造内部请求。非结构化工具输出无法可靠脱敏时，不允许发送给第二个压缩 provider；应保持原模型/长上下文，或要求客户端提供安全摘要。

媒体状态只记录类型、MIME、大小、内容 hash、授权范围和有效期，不能记录可能暴露 URL/base64 前缀的标识符。

## 17. 计费设计

```text
Parent client request
  -> Child compaction request
       独立 request_id
       独立 RelayInfo
       独立 BillingSession
       独立价格/表达式快照
       独立预扣、结算、退款和消费日志
  -> Main generation request
       压缩后重新估算
       独立 BillingSession
```

约束：

- 压缩成功但主请求失败：压缩按实际 usage 结算，主请求独立退款。
- 压缩失败：只退压缩预扣，不产生主请求预扣。
- 配置中途热更新不改变已冻结的价格和策略快照。
- 路由阶段的简化估价不能作为压缩成本上限的唯一依据；最终必须复用正式 pricing/billing 链路。
- 客户端重试通过幂等键复用已成功且未过期的压缩产物，避免重复收费。

## 18. 日志与审计

`other.smart_routing.context_consensus` 只记录：

```text
mode
version
revision
compacted
source_message_count
preserved_recent_messages
input_tokens_before
input_tokens_after
summary_tokens
summary_digest
policy_version
binding_level
binding_reason_codes
compaction_model
compaction_channel_id
compaction_request_id
compaction_quota
result_code
fallback_reason
```

禁止记录摘要正文、原始会话 ID、工具 ID/参数/结果、schema 正文、媒体 URL、opaque state 和任何凭据。

托管状态审计至少保留 owner 指纹、revision、请求者、执行时间、策略版本、压缩/路由前后 token、绑定变更、CAS 结果和最终状态，但仍不得记录敏感 payload。

## 19. 失败与回退矩阵

| 场景 | 行为 |
| --- | --- |
| 完整上下文可容纳 | 不压缩，正常路由 |
| 当前模型不足但允许池有长上下文候选 | 选择长上下文候选 |
| 所有候选不足且未授权压缩 | 返回明确上下文超限错误 |
| 压缩 timeout、非法 JSON、内容拦截 | 返回失败，不使用半成品摘要 |
| 压缩后仍超限 | 返回失败，不递归压缩 |
| provider state 未登记/过期/跨 owner | 返回 state binding 错误 |
| 工具调用缺结果或并行组不完整 | 保留整组；无法容纳时返回错误 |
| 媒体过期或目标 provider 不可访问 | 保持原绑定或返回错误 |
| Redis 不可用且请求含完整可容纳历史 | 明确降级 stateless 并记录原因 |
| Redis 不可用且依赖托管状态 | 返回 503，不猜测恢复 |
| revision/CAS 冲突 | 在任何上游调用和预扣前返回 409 |
| 首字节后上游失败 | 终止流，不切换、不重放 |
| 压缩成功、主调用失败 | 压缩正常结算，主调用独立退款 |

## 20. 代码边界建议

新增独立包，避免继续膨胀候选评分器：

```text
service/contextconsensus/
  types.go
  extractor.go
  tool_graph.go
  binding.go
  budget.go
  compaction.go
  store.go
  audit.go

service/contextconsensus/protocol/
  chat.go
  responses.go
  claude.go
  gemini.go
```

职责：

- protocol adapters：DTO 提取、验证、可移植性分类和请求重写。
- contextconsensus core：纯计划、预算、不变量和状态机。
- store：加密、CAS、lease、TTL 和幂等。
- middleware/controller integration：编排请求准备、路由和独立计费。
- `service/smartrouting`：只根据 `ContextRoutingConstraint` 过滤与排序候选。

建议先抽取 `PrepareRelayRequest` 边界，使协议 DTO 只解析一次，再依次执行共识计划、精确 token 重算、智能路由、计费和 relay。

## 21. 配置建议

继续使用 `smart_routing` 配置模块：

```text
context_consensus_enabled=false
auto_compaction_enabled=false
managed_context_enabled=false
compaction_model_pool=[]
compaction_channel_ids=[]
authoritative_context_limits={}
context_safety_margin_tokens
preserved_recent_turns
max_summary_tokens
max_compaction_input_tokens
max_compaction_calls_per_request=1
max_compaction_quota
compaction_timeout_seconds
context_state_ttl_seconds
allow_tool_result_compaction=false
```

配置热更新继续使用不可变原子快照。`compaction_model_pool` 必须只包含显式真实模型，不接受 `auto:*` / `smart:*`。

## 22. 分阶段实施

### 阶段 A：协议状态识别与切换保护

- 新增四协议 `ContextEnvelope` extractor。
- 校验工具图、schema、媒体引用和 provider-bound 状态。
- 扩展 `SmartRouteRequest` 的绑定/可切换约束。
- 在候选过滤前阻止 stateful Responses、Claude/Gemini opaque state 跨边界。
- 增加 `validate_only` 审计字段。
- 不持久化、不调用压缩模型、不改写消息历史。

### 阶段 B：请求内显式压缩

- 实现 token budget、完整 turn 切分和结构化摘要 schema。
- 只支持 `stateless_compacted`。
- 首批支持 Chat/Responses 的纯文本完整历史。
- Claude/Gemini 仅在无 opaque state 时接入。
- 工具、多模态、provider state 首批保持不可压缩。
- 压缩子请求接入独立计费和父子审计。

实施拆分：

- B-2a：统一最终请求不可变快照，使最终计数和实际发送能够消费同一模型、协议及正文；覆盖 pass-through 和 Chat-to-Responses，并隔离候选重试状态。
- B-2b1：先建立不依赖 Gin、网络和数据库的内部子请求执行契约，固定独立 request ID、一次性生命周期、结构化计费结果、失败退款语义和父子审计字段。
- B-2b2：在 controller 层实现真实非流式子请求执行器，创建独立 `RelayInfo`、`BillingSession`、`BillingSnapshot` 和 `BillingRequestInput`，并接入渠道选择、上游响应解析及消费日志。
- B-2c1：建立绑定模型、渠道、最终协议和正文摘要的权威 tokenizer/上下文上限证据契约，补齐显式压缩渠道白名单与安全压缩提示构造器。
- B-2c2：抽取无网络的最终请求预准备边界，完成单次压缩编排、正文和 DTO 原子提交、逐候选终检与主预扣前失败关闭。

### 阶段 C：托管共识

- 增加网关会话头、owner 隔离、revision/CAS、lease、TTL 和加密存储。
- 维护 provider state ID 到 ProtocolBinding 的映射。
- 首批仅支持非流式请求。
- Redis/加密密钥不可用时 fail closed。

实施状态：阶段 C-1 已完成加密、owner/HMAC、Redis Lua 仓储、CAS、lease/fencing、TTL、provider binding 记录契约及请求失败关闭，见 `doc/auto-smart-routing-context-consensus-stage-c1-implementation-2026-07-28.md`；阶段 C-2a 已完成托管会话状态机、四协议安全摘要注入和非流式有界响应缓冲，见 `doc/auto-smart-routing-context-consensus-stage-c2a-implementation-2026-07-28.md`；阶段 C-2b 已冻结增量 current turn 契约，在渠道选择前接入会话加载、摘要注入和 lease 续租，并建立规范化输出/显式结算/缓冲执行边界，见 `doc/auto-smart-routing-context-consensus-stage-c2b-implementation-2026-07-29.md`；阶段 C-2c 已完成下一 revision L2/L3、固定 2 MiB 缓冲、同请求 CAS 恢复和 commit-before-write，非流式统一 503 门禁已移除，见 `doc/auto-smart-routing-context-consensus-stage-c2c-implementation-2026-07-29.md`；阶段 C-3a 已完成托管会话 state 的有界旧密钥读取、双 namespace 冲突隔离和 revision 原子迁移，见 `doc/auto-smart-routing-context-consensus-stage-c3a-implementation-2026-07-29.md`；阶段 C-3b 已完成主调用与摘要子调用的持久化计费 operation、稳定客户端幂等键、revision intent、跨请求 outcome、结算后提交恢复和已提交响应回放，见 `doc/auto-smart-routing-context-consensus-stage-c3b1-implementation-2026-07-29.md`、`doc/auto-smart-routing-context-consensus-stage-c3b2-implementation-2026-07-29.md`；阶段 C-3c 已完成原生非流式 OpenAI Responses `id -> previous_response_id` 的真实 adaptor report、owner 隔离 binding、精确目标固定、Redis 原子提交和恢复确认，见 `doc/auto-smart-routing-context-consensus-stage-c3c-implementation-2026-07-30.md`。阶段 C 已完成，`managed_context_enabled` 仍默认关闭；其他 provider-owned state 继续失败关闭。

### 阶段 D：工具结果压缩和可视化

- 先支持单工具串行，再评估并行工具段。
- 单独评审工具结果脱敏和压缩。
- 增加后台配置、状态诊断、失败原因和指标页面。

实施拆分：

- D-1a：补齐工具图的 call/result 协议与因果序号证据，建立 OpenAI Chat 单工具串行结构资格评估；只输出 digest 和有限 reason code，不开放运行时压缩。
- D-1b：实现服务端注册、版本化、默认拒绝的结构化 JSON Pointer 白名单脱敏策略；未知字段、非 JSON、敏感值和超限结构失败关闭。
- D-1c：新增 Summary v2，闭合单个旧串行 Chat 工具原子段的 plan、prompt、rewrite 和 runtime；call、result、最终 assistant 回复整体压缩或整体保留。
- D-1d：复用现有管理员鉴权和有界日志聚合，增加只读诊断 API 与后台页面；禁止展示原始 call ID、函数名、参数、结果、digest、文件引用或摘要正文。
- D-2：统一并行 group 语义后评估稳定 ID 协议的并行工具组；Gemini 同名并行继续失败关闭。
- D-3：在 adaptor 提供权威所有权、到期和删除能力后实现 provider file 生命周期。

实施状态：阶段 D-1a 已完成工具结果因果序号、跨协议/逆序 result 拒绝和只含 digest 的单工具串行结构资格评估，见 `doc/auto-smart-routing-context-consensus-stage-d1a-implementation-2026-07-30.md`；阶段 D-1b 已完成服务端构造、版本精确绑定且默认拒绝的 JSON Pointer 白名单脱敏注册表，真实 Chat JSON 字符串结果先绑定原始 digest，再拒绝未知字段、重复键、敏感值和超限结构，仅生成带进程内完整性证明的有界标量投影，见 `doc/auto-smart-routing-context-consensus-stage-d1b-implementation-2026-07-30.md`；阶段 D-1c 已完成 Summary v2、单个旧串行 Chat 工具四消息原子计划、原始工具内容隔离、确定性工具事实校验、原子 rewrite、内部执行器版本绑定和最终 OpenAI Chat 协议门禁，见 `doc/auto-smart-routing-context-consensus-stage-d1c-implementation-2026-07-30.md`。工具压缩开关默认关闭，编译期静态策略表因无内置业务工具而保持为空；未登记工具只允许压缩其之前的完整普通轮次。Summary v1、managed、其他协议、并行工具和 provider file 行为不变。

## 23. 测试矩阵

### 23.1 协议与状态

- Chat / Responses / Claude / Gemini。
- full history / previous_response_id / conversation / container / cachedContent。
- 未登记、过期、跨用户、跨 Token 和跨凭据槽位状态。

### 23.2 工具与 schema

- 单工具、并行工具、缺失结果、非幂等工具、MCP、opaque signature。
- OpenAI strict schema、Claude output format、Gemini response schema。
- schema 原样保留、hash 一致和目标能力校验。

### 23.3 多模态

- URL、base64、provider file ID、音频、视频、过期签名 URL。
- MIME、大小、hash、授权和目标 provider 可访问性。

### 23.4 流与预算

- 首字节前失败、文本中途失败、tool delta 中途失败、客户端断开、缺 usage。
- `limit-1`、`limit`、`limit+1`、max output 缺省、显式 0、摘要后仍超限。

### 23.5 状态与计费

- TTL 过期、Redis 故障、CAS 冲突、重复 request_id、并发 fork。
- active/previous key 读取、未知版本、旧 namespace revision 原子迁移、双 namespace 冲突和旧 key 提前退役。
- 固定价格、倍率、tiered expression、免费模型、额度不足。
- 压缩成功主调用失败、压缩失败、幂等重试。

### 23.6 安全

- 摘要 prompt injection 不得提升权限。
- 请求头、工具结果、MCP 和签名 URL 含 secret 时，断言压缩请求、状态和日志均无明文。
- Context ID 跨 owner 访问必须失败。

## 24. 验收标准

1. 默认关闭时，现有请求 body、计费和路由行为不变。
2. provider-bound 状态在缺少可信映射时不能跨模型、渠道、凭据或协议。
3. 完整上下文可容纳时绝不自动压缩。
4. 压缩不会删除或降级 system/developer、schema、当前 turn、未闭合工具段和有效媒体。
5. 压缩失败或压缩后仍超限时，不向主上游发送半成品请求。
6. 首个下游输出后不发生模型切换、压缩或重放。
7. 压缩和主调用分别计费、分别退款、日志可关联且不会重复收费。
8. 日志、状态和压缩请求均不泄露凭据、完整工具结果、opaque state 或签名 URL。
9. Redis 并发通过 revision/CAS/lease 保证，不发生跨会话覆盖。

## 25. 推荐下一步

下一步完成阶段 D-1d：复用现有管理员鉴权和有界日志聚合，增加有限 reason code 的只读诊断 API 与后台页面。诊断不得展示原始 call ID、函数名、参数、结果、schema、digest、文件引用、摘要正文或任何凭据；生产静态脱敏策略仍需针对服务端实际拥有的精确工具/schema 单独评审后通过代码登记。

发布前应在真实 Redis 和 OpenAI 测试账号上补充网络中断、Redis 超时、进程退出、active/previous key 轮换及旧密钥退役故障注入。`managed_context_enabled` 在这些门禁通过前继续默认关闭。
