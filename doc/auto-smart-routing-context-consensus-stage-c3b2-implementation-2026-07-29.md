# ContextConsensus 阶段 C-3b2 实施记录

日期：2026-07-29

## 结论

阶段 C-3b2 已完成稳定客户端幂等键、revision intent、跨请求 outcome、结算后 revision 提交恢复和已提交响应回放。阶段 C-3b 至此完成；阶段 C 与 C-3 仍未完成，剩余工作为 C-3c 的真实 provider state report、登记、绑定和精确凭据槽固定。

本批未增加管理员接口、管理页面或外部路由配置。`managed_context_enabled` 继续默认关闭，provider-owned state 继续失败关闭。

## 客户端契约

- managed 非流式请求必须携带 `X-New-Api-Context-Idempotency-Key`，仅允许单值、16-128 字节及字符 `A-Z`、`a-z`、`0-9`、`.`、`_`、`~`、`-`。
- 非 managed 请求携带该头会被拒绝；空白、Unicode、多值、过短、过长或包含分隔符的值均在 Redis、选渠、计费和上游调用前失败。
- 网关捕获后立即删除请求头；完成版本化 HMAC 派生后清空请求上下文中的原值。直接覆盖、通配符、正则、placeholder 和 WebSocket 透传均不能将该头发送给上游。
- 作用域固定为 user、API Key Token 和 endpoint family。同一幂等键只能绑定一个 Context ID、expected revision 和请求指纹；同一 conversation/revision 换用另一个键同样冲突。

## 持久化状态机

新增主库表 `managed_context_outcomes`，使用整数自增主键、无外键、字段注释完整，并由既有 GORM 迁移支持 SQLite、MySQL 和 PostgreSQL。数据库只保存 owner/conversation/idempotency/revision 的版本化 HMAC、请求指纹、阶段、计费 operation 数字 ID、时间边界和 AEAD 密文，不保存原始 Context ID、幂等键、认证头或完整请求正文。

状态按以下顺序推进：

```text
intent
  -> main_dispatched
  -> main_settled
  -> summary_dispatched
  -> settled_pending_commit
  -> committed
```

另保留 `terminal_failed` 和 `expired` 终态。过期记录拒绝重新调用上游，并在读取时清除 response、assistant、摘要执行快照、摘要结果和 next state 密文，只保留冲突判定所需 tombstone。

## 原子结算与恢复

- outcome intent 在 Redis session、渠道选择、预扣费和任一上游调用前创建或读取。
- 主上游发送前先持久化 `main_dispatched`。完整响应、规范化 assistant output 和冻结摘要执行快照使用独立 AEAD purpose 加密，并与主 `BillingOperation` 结算、用户/渠道统计和日志 outbox 在同一主库事务中写为 `main_settled`。
- 摘要上游发送前先持久化 `summary_dispatched`。校验后的摘要结果和精确 next state 与摘要 `BillingOperation` 结算在同一主库事务中写为 `settled_pending_commit`。
- `main_settled` 重试只恢复摘要子调用；`settled_pending_commit` 重试只恢复 Redis revision CAS。Redis 已是完全一致的下一 revision 时按读后确认补写数据库 `committed`。
- Redis CAS 成功后必须确认数据库 `committed`，随后首次响应也从数据库密文解密重建。客户端写入失败不回滚 revision、不退款；相同请求后续直接回放已提交响应。
- `main_dispatched` 或 `summary_dispatched` 且数据库没有完整 checkpoint 时返回 `503 managed_context_outcome_unknown`，禁止自动再次调用上游。

## 回放与安全边界

- 回放只输出 `Content-Type`，重新计算 `Content-Length`，由网关生成 `X-New-Api-Context-Revision`；不会持久化或回放 Cookie、认证头、上游 request ID、连接头或速率头。
- 客户端响应正文上限为 2 MiB。正文、assistant、摘要执行快照、摘要结果和 next state 分别使用 AES-256-GCM purpose，密钥版本由 envelope 固定。
- active key 与最多 4 个 previous key 同时参与 outcome 定位。双记录命中失败关闭；命中旧版本时，在主库事务中重新加密 outcome，并同步迁移同 revision 的主/摘要计费 identity，不延长原过期时间。
- `committed` 数据库状态只会在 Redis revision 已确认后写入，因此已提交响应可以在 Redis 暂时不可用时直接从数据库回放；新请求和未提交恢复仍要求 Redis 可用。

## Exactly-once 边界

本阶段保证同一 managed revision 的计费、统计、消费日志、数据库 checkpoint 和已提交响应不会因客户端重试而重复。它不宣称任意上游具备通用 exactly-once：如果进程在“上游可能已执行”与“完整结果进入主库事务”之间退出，系统只能返回 outcome unknown 并失败关闭，不能安全自动重发。

## 验证

- 幂等头合法性、重复值、managed 专用约束及所有 header override/透传路径隔离。
- 幂等键与 revision intent 冲突、并发 claim、计费与 checkpoint 事务回滚、过期拒绝和密文清理。
- 主/摘要 checkpoint 加密、2 MiB 响应边界、active/previous key 迁移、双 lookup 冲突和精确 next state 确认。
- 安全响应头、已提交字节回放、固定/阶梯/免费 durable operation 及并发结算。
- `go test ./... -count=1`。
- `go test -race ./model ./service ./service/contextconsensus ./middleware ./relay/channel ./relay/common ./controller -count=1`。
- `go vet ./model ./service ./service/contextconsensus ./middleware ./relay/channel ./relay/common ./controller`。
- `git diff --check`。

本地自动化使用 SQLite；本轮未运行真实 MySQL/PostgreSQL 集成环境，也未进行真实上游进程崩溃注入。对应代码仅使用 GORM 和跨方言字段类型，真实数据库与故障注入继续作为发布前验证项。

## 后续待办

- C-3c：接入 adaptor 真实 provider state report，首批仅闭环原生 OpenAI Responses `id -> previous_response_id`。
- C-3c：在成功响应后原子登记 binding，并在下一请求前固定最终模型、协议、渠道、精确凭据槽和 credential fingerprint；其他 provider-owned state 继续失败关闭。
