# ContextConsensus 阶段 D-3c.1 实施记录

日期：2026-07-31

## 结论

阶段 D-3c.1 已完成默认关闭的托管 OpenAI provider file 删除闭环：后台系统任务按主数据库 outbox 领取有限 lease，在网络调用前持久化派发边界，只接受严格 `deleted=true` 回执作为成功，并以有限重试、明确未知终态、失败告警和摘要事件链记录结果。

本阶段未调用真实 OpenAI、未执行生产配置或数据库数据修改，也未开放客户端 DELETE 接口。

## 删除与恢复合同

- OpenAI DELETE 固定为官方 `https://api.openai.com/v1/files/{id}`，单次调用且禁止重定向；响应正文上限为 64 KiB。
- 只有 HTTP 200、`object=file`、回执 ID 精确一致且 `deleted=true` 才进入 `deleted`。404 保持 `provider_not_found_unverified` 失败，不能伪装成成功。
- 429 使用 15 秒起步、最大 15 分钟的有限指数退避，并严格受 outbox `max_attempts` 限制。
- 超时、传输失败、5xx、响应超限或畸形均进入 `delete_outcome_unknown`，禁止自动重发。
- lease 领取后、网络发送前，worker 在同一事务内写入 `deletion_dispatched` 事件和 `dispatched_at`。进程若在派发后失去结果，恢复 worker 直接进入未知终态，不会再次 DELETE。
- 成功终态清除加密 provider reference；未知或失败终态保留加密引用，便于管理员排查。

## 生命周期与调度

- `verification_failed` 在保存已知 provider reference 的同一事务中创建立即到期的删除 outbox，不再留下已确认上传但无清理任务的文件。
- 后台 `provider_file_deletion` 系统任务每 15 秒检查一次到期 outbox，批次隔离单条异常；缺失 lifecycle 的孤立 outbox 通过 version CAS 进入 `lifecycle_missing` 终态并告警，即使批次大小为 1 也不会永久阻断后续正常删除。
- 删除恢复不依赖上传开关或渠道启用状态，但必须重新校验冻结的渠道类型、官方 origin、单 Key、credential、Organization 和 scope fingerprint。
- 在 lifecycle 非终态期间，渠道 Key、Base URL、Organization、参数覆盖、请求头覆盖、其他设置、多 Key 形态及渠道删除均受事务内保护；读取和变更统一按 channel row -> lifecycle binding 的锁顺序执行。
- intent 创建后、上传派发前再次读取并比对持久化目标快照，渠道发生变化时以 `target_changed_before_dispatch` 失败关闭。

## 安全处理

- DELETE 客户端及 worker 错误只暴露固定有限代码，不记录 file ID、opaque handle、文件名、URL、凭据、认证头或上游正文。
- 告警只包含数值 outbox/lifecycle ID 和有限结果码。

## 验证

- `go test ./... -count=1`：通过。
- `go vet ./relay/channel/openai ./model ./service/providerfile ./setting/model_setting ./controller`：通过。
- `git diff --check`：通过。

测试覆盖严格删除回执、404/429/5xx、超时、传输错误、重定向、畸形和超限响应、派发前完成拒绝、派发后 lease 恢复不重发、429 最大尝试终止、verification failure 清理、渠道停用后的恢复删除、坏 outbox 批次隔离及敏感信息不泄露。

## 完成状态

上传、查询、Responses 文件引用和到期自动删除均已完成。额外启用限制的删除见 `doc/openai-provider-file-simplification-2026-08-01.md`。
