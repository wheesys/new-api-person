# ContextConsensus 阶段 D-3b.1 实施记录

日期：2026-07-31

## 结论

阶段 D-3b.1 已完成 provider file 生命周期的默认关闭基础设施：首期领域契约被限制为官方 OpenAI、原生 Responses、`user_data`、专用单 Key 渠道；主数据库新增持久化 ownership、删除 outbox 和生命周期事件三张表；版本化 HMAC、独立 AEAD purpose、opaque handle、CAS 状态机和 lease fencing 已闭合。

本阶段没有开放 `/v1/files`，没有调用 OpenAI，没有执行数据库数据修改或生产配置变更。D-3a 的 provider file 门禁仍保持失败关闭。

## 持久化模型

新增三张主库表，均使用整数自增主键、无外键、所有字段带 GORM `COMMENT`，时间字段使用 `time.Time` / `*time.Time`：

- `managed_provider_file_lifecycles`：保存 owner-bound lookup、上传 intent、精确渠道与单 Key 槽、credential/endpoint/scope fingerprint、权威文件元数据、加密 provider payload、CAS version 和事件链 head。
- `managed_provider_file_deletion_outboxes`：保存唯一删除操作、有限尝试次数、下一执行时间、CAS version、HMAC lease fencing token 和有限终态结果。
- `managed_provider_file_lifecycle_events`：只追加有限事件、前序 HMAC、事件 HMAC、证据 digest 和状态迁移，不保存原始句柄、file ID、文件名、凭据或上游正文。

普通迁移和快速迁移均接入三张表，使用同一套跨 SQLite、MySQL 和 PostgreSQL 的 GORM 语义，不依赖外键、JSON/ENUM、partial index、`SKIP LOCKED` 或方言 upsert。

## 状态与事务

上传状态：

```text
intent -> upload_dispatched -> active
intent | upload_dispatched -> upload_failed
upload_dispatched -> upload_unknown
```

删除状态：

```text
active -> deletion_pending -> deleted
deletion_pending -> deletion_pending（有限重试）
deletion_pending -> deletion_failed
```

- 上传发送前必须先提交 `upload_dispatched`；未知上游结果只能进入 `upload_unknown`，不能自动重传并登记所有权。
- 激活事务同时保存权威 metadata、创建到期删除 outbox，并追加审计事件。
- lifecycle 和 outbox 均使用单调 version CAS；删除 claim 还绑定一次性 HMAC lease token、到期时间和 attempt count，迟到 worker 不能提交结果。
- 成功删除在同一事务中终结 lifecycle/outbox、清除加密 provider payload 并追加事件。
- 事件表通过生命周期 head、序号、前序 HMAC 和唯一索引检测缺失、重排及并发写入；应用模型拒绝普通 Update/Delete。数据库内摘要链只提供篡改检测，不替代生产 WORM 或独立不可变审计。

## 密钥与敏感数据

- provider file 使用现有 ContextConsensus active/previous keyring，但采用独立 HMAC 域和 64 字符小写十六进制编码，不改变既有托管会话的 Base64URL 键格式。
- owner、opaque handle、上传幂等键、provider reference、精确凭据、目标、删除操作和事件分别使用独立 HMAC scope。
- provider file reference 使用独立 `provider_file_reference` AES-256-GCM purpose，并把 repository key、purpose、revision 和 key version 绑定为 AAD。
- opaque handle 使用 256 bit 随机数，只包含网关标识，不包含 provider file ID、文件名、用户或渠道信息。
- provider file ID 只允许 OpenAI `file-` 形式的受限字符；原始 file ID 和文件名只存在于加密 payload 或短生命周期内存中，`String` / `GoString` 固定脱敏。

## 默认关闭配置

新增 provider file lifecycle 配置快照，默认 `provider_file_lifecycle_enabled=false`，专用渠道、有限到期和独占 Project 声明均无默认值。readiness 仅在以下条件全部满足时通过：

- 显式开启功能并配置正数专用 OpenAI 渠道 ID。
- 到期时间为 60 秒到 30 天，metadata verify TTL 和 deletion lead 均小于到期时间。
- 删除 batch、最大尝试和 timeout 在硬上限内。
- 管理员显式声明 OpenAI Project 为网关独占。

reconciliation 配置同样默认关闭。D-3b.2/D-3c 尚未完成前，即使配置字段存在，也没有上传路由或真实删除 worker 会被开放。

## 验证

- `go test ./model -run 'TestManagedProviderFile' -count=1`：通过。
- `go test ./model -count=1`：通过。
- `go test ./service/contextconsensus -run 'TestManagedConsensusKeyDeriver|TestManagedProviderFileReferencePayload|TestGenerateManagedProviderFileHandle' -count=1`：通过。
- `go test ./service/contextconsensus ./setting/model_setting ./model -count=1`：允许既有 miniredis 测试监听本地回环地址后通过。
- `go vet ./model`：通过。
- `git diff --check`：通过。

测试覆盖上传 intent 幂等与冲突、owner 隔离、upload dispatched、权威 metadata 激活与幂等回放、active/previous HMAC namespace、opaque handle 熵、AEAD purpose 隔离、敏感值不序列化、stale CAS、lease fencing、事件 append-only/链连续性、删除 payload 清理、重试上限和默认关闭 readiness。

当前自动化数据库测试使用 SQLite。真实 MySQL 5.7.8 和 PostgreSQL 9.6 的迁移、唯一索引及并发 CAS/lease 验收仍属于 D-3c.2 外部门禁。

## 后续

- D-3b.2：实现独立 `/v1/files` 上传/查询、官方 OpenAI 单 Key client、上传后立即 GET 核验、owner-bound handle 解析和 Responses 最终正文精确绑定。
- D-3c.1：接入实际删除 worker、有限退避、告警、渠道变更保护和部署侧不可变审计 sink，继续保持生产关闭。
- D-3c.2：完成独占 Project 声明、真实 OpenAI sandbox 契约测试和有界 orphan reconciliation 后，才能评估生产开放。
