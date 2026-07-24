# 智能路由 ContextConsensus 阶段 B-2b2 实施报告

更新时间：2026-07-24

## 1. 实施结果

本轮完成 B-2b2：在 controller 层实现可真实选择渠道并执行上游非流式 Chat Completions 请求的内部压缩子执行器。执行器复用现有渠道适配、最终请求准备、用量归一化和计费逻辑，但不递归调用 `controller.Relay`，也不会向父请求的客户端响应 writer 写入数据。

该执行器目前只提供给后续 B-2c 编排调用，尚未接入现有主请求流程。自动压缩继续默认关闭，现有请求行为不变。

## 2. 隔离边界

内部请求使用全新的 Gin context、HTTP request、有界响应 writer、request ID、`RelayInfo`、`BillingSession`、分层计费快照和 `BillingRequestInput`。

子上下文只复制用户和 Token 计费所需的白名单字段。以下父请求状态不会继承：

- 客户端 `Authorization`、Cookie 和其他请求头。
- 父渠道 ID、渠道密钥、BaseURL 和多 Key 槽位。
- smart-routing 决策、候选和重试状态。
- 父请求正文存储、上游 request ID 和响应 writer。

内部 HTTP 请求只设置 `Content-Type: application/json` 和 `Accept: application/json`。TokenKey 仅保留在子计费上下文中，不写入上游请求头、消费日志或父子审计字段。

## 3. 请求与渠道约束

- 每个执行器只接受一个显式真实模型，并确认该模型属于构造时冻结的压缩模型池。
- 调用方必须提供非空的冻结渠道 ID 授权集合；随机选择结果不在集合内时立即拒绝，避免历史上下文被发送到未授权 provider、区域或渠道。
- 继续执行 Token 模型访问限制，不允许使用 `auto`、`smart` 或池外模型。
- 子调用重新选择渠道，只执行一次渠道尝试，不复用父请求渠道。
- 全局或渠道开启原始正文透传时拒绝执行，避免内部 OpenAI Chat 请求绕过协议转换。
- 摘要请求被重建为纯文本 system/user 消息，固定 `stream=false` 和最大摘要 token，不携带 tools、MCP、媒体、客户端 metadata 或其他透传字段。
- 上游归一化响应写入独立的有界内存 writer，随后解析唯一 assistant choice，并通过 `ParseAndValidateConsensusSummaryV1` 校验摘要结构和来源范围。
- 子请求设置请求级 DEBUG 抑制标记，所有普通 relay/adaptor 的 DEBUG 日志均跳过该调用，防止压缩输入和完整摘要响应进入调试日志。

## 4. 计费与失败语义

`relay.ExecuteTextAttemptWithoutQuota` 只执行协议转换、上游调用和响应归一化，不在 runner 阶段结算。原 `TextHelper` 保持兼容包装，正常主请求行为不变。

内部压缩调用采用以下独立生命周期：

- 免费模型跳过预扣，但仍按真实 usage 生成消费记录。
- 付费模型创建独立 `BillingSession` 并预扣额度。
- `MaxQuota` 必须为正数；执行前的最大输出 token 估价超过该硬上限时 fail closed，不允许发起上游调用。上游已产生实际费用后不再因事后阈值丢弃已付费摘要。
- 网络或协议执行失败且没有可结算 usage 时，同步退款子调用，不影响父请求计费。
- 上游已经产生 usage、但摘要 JSON 或共识校验失败时，标记为可计费执行失败，按实际 usage 结算且不退款。
- 结算失败由 `BillingSession.NeedsRefund` 的最终状态决定是否退款，避免对已提交资金重复退款。
- 结算成功后的消费日志失败返回 `audit_failed`，但不退款。

`PostTextConsumeQuotaResult` 和日志 result 接口提供实际额度、结算错误和日志错误；原有无返回值入口继续保留，未改变其他调用方契约。`BillingSession.RefundSync` 为内部子请求提供同步、幂等且可报告错误的退款路径，并分别记录资金源与 Token 退款完成状态，仅重试失败步骤。

## 5. 审计与安全

消费日志使用 child request ID，并通过 `RelayInfo` 增加：

- `request_purpose=context_compaction`
- `parent_request_id`
- `policy_version`

失败审计只记录 result code、模型、渠道、token 数量、额度和各阶段 digest。请求正文、摘要正文、API Key、TokenKey、Cookie、认证头及 provider state 均不进入审计字段。

准备阶段在渠道选择前就建立最小子运行时，确保早期失败也能写父子错误审计。错误日志或消费日志关闭、写入失败或明确返回未记录时，执行结果为 `audit_failed`，不会把未持久化的日志误报为 `AuditRecorded=true`。

## 6. 验证范围

测试覆盖：

- 父子 Gin context、HTTP request、writer、请求头、渠道凭据和路由状态隔离。
- 显式模型池、冻结渠道 ID、正数费用上限、非流式纯文本请求和独立 `BillingRequestInput`。
- 成功结算、普通上游失败同步退款、无效摘要可计费结算、消费日志失败不退款。
- 早期渠道授权拒绝仍写失败审计，消费日志未记录时不得报告审计成功。
- 请求级 DEBUG 日志抑制，以及退款失败后只重试未完成步骤。
- 有界响应 writer 拒绝超限内容。
- B-2b1 可计费错误状态机、同步退款和父子日志字段。

通过的验证命令：

```bash
go test ./controller -run 'TestInternalCompaction|TestBoundedInternal'
go test -race ./controller -run 'TestInternalCompaction|TestBoundedInternal'
go test -race ./service/contextconsensus
go test -race ./service -run 'TestBillingSessionRefundSync'
go test ./model ./relay ./service
```

扩大到整个 `service` 包的竞态测试时，仍会命中待办中已记录的既有异步视频任务竞态，路径位于 `logger/logger.go:112` 和 `model/task.go:126`，本轮未修改这些代码。

## 7. 后续边界

B-2c 需要把该执行器接入请求内压缩编排，并补齐权威 tokenizer、模型上下文上限、单请求最多一次压缩、压缩后逐候选终检，以及主请求 DTO 与正文的原子提交。完成这些门禁前，不允许开启自动压缩。
