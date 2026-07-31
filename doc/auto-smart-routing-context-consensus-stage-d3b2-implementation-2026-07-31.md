# ContextConsensus 阶段 D-3b.2 实施记录

日期：2026-07-31

## 结论

阶段 D-3b.2 已完成默认关闭的 OpenAI provider file 托管上传、查询和原生 Responses 精确引用闭环。客户端只接触 owner-bound opaque handle；网关以专用官方 OpenAI 单 Key 渠道上传文件，立即读取并核对权威元数据，随后才把生命周期激活。Responses 请求在最终上游调用前把类型化句柄改写为原始 provider file ID，并再次校验冻结正文和精确渠道目标。

本阶段未调用真实 OpenAI、未执行生产配置或数据库数据修改。列表、内容和删除接口仍返回未实现，删除 worker、不可变审计、真实 sandbox 契约和生产 readiness 属于 D-3c，功能继续默认关闭。

## 接口与上传边界

- `POST /v1/files` 使用现有 Token 认证和模型请求限速，但不进入通用渠道分发；必须提交唯一 `X-New-Api-File-Idempotency-Key`，字符范围与托管共识幂等键一致。
- multipart 只接受一个 `purpose=user_data` 和一个 `file`，拒绝额外字段及客户端自定义到期策略；正文和单文件上限为 50 MiB，文件部分以流式方式转发。
- `GET /v1/files/{opaque-handle}` 按用户、API Token 和固定 endpoint family 定位生命周期，并使用原上传目标重新读取元数据。
- OpenAI client 固定使用 `https://api.openai.com`、`purpose=user_data` 和服务端 `expires_after[anchor]=created_at` / `expires_after[seconds]`；禁止重定向，响应上限为 64 KiB，错误不包含凭据、文件值、URL 或上游正文。
- 上传和查询响应保持 OpenAI file 对象形状，但 `id` 始终为网关 opaque handle，绝不返回原始 provider file ID。

## 生命周期与故障恢复

上传先持久化 intent 和 `upload_dispatched` 事件，再发出网络请求：

```text
intent -> upload_dispatched -> active
upload_dispatched -> upload_unknown
upload_dispatched -> verification_failed
```

- POST 结果未知时进入 `upload_unknown`，相同幂等请求不会盲目重新上传。
- POST 成功但立即 GET 失败或元数据不一致时进入 `verification_failed`；加密保存必要 provider reference，供 D-3c 后续清理和对账，不允许用于 Responses。
- 激活事务保存经 GET 核验的 bytes、purpose、created_at、expires_at 和 encrypted reference，并创建执行时间为 `expires_at - deletion_lead` 的删除 outbox。
- 相同 owner、幂等键、文件摘要、目标和到期策略的重放解密既有 handle 并返回同一结果，不产生第二次上传；冲突 intent 失败关闭。

## Responses 精确绑定

- 仅解析原生 `POST /v1/responses` JSON 中顶层或 message content 内的类型化 `input_file.file_id`，最多 128 个引用。
- 输入必须是合法网关 handle；原始 `file-*` provider ID、`file_data` / `file_url` 混用、未知句柄和非类型化引用均被拒绝。
- 在任何 provider GET 前先校验请求模型、用户分组和管理员配置的专用渠道；随后以相同凭据读取每个文件并核对持久化元数据。
- 改写后的 provider ID 只存在于本次请求内存和最终上游正文。参数覆盖完成后再次比对有序引用证据，并复核渠道 ID、类型、单 Key 槽 0、credential、endpoint 和 scope fingerprint。
- provider file 请求不参与智能路由、协议转换、失败切换、重试、亲和记录或 ContextConsensus 自动压缩；与 managed `previous_response_id` 同时使用的请求在当前阶段失败关闭。

## 密钥与审计

- 上传 intent、handle、repository key、target、credential、endpoint 和 scope 使用独立版本化 HMAC 域；provider reference 使用独立 AEAD purpose。
- encrypted reference 同时保存原始 provider file ID、文件名和网关 handle，支持 active/previous key 读取窗口与幂等重放。
- 生命周期事件身份改为稳定 upload-intent HMAC，避免依赖数据库分配前尚不存在的自增 ID；事件和错误只记录有限状态及 digest，不记录敏感原文。
- 应用内事件摘要链仍不等价于生产不可变审计，D-3c.2 前不得据此开放生产能力。

## 验证

- `go test ./model ./service/contextconsensus ./service/providerfile ./relay/channel/openai ./relay ./middleware ./controller ./router -count=1`：通过。
- `go vet ./model ./service/contextconsensus ./service/providerfile ./relay/channel/openai ./relay ./middleware ./controller ./router`：通过。
- `git diff --check`：通过。

测试覆盖 multipart 形状和大小限制、OpenAI 上传/查询 wire contract、重定向与错误脱敏、上传状态转换、元数据核验、幂等重放、active/previous key、owner 隔离、专用目标限制、Responses 引用解析改写、最终正文和目标门禁以及路由隔离。

## 后续

- D-3c.1：实现实际 DELETE client、CAS lease worker、未知结果终态、有限告警、渠道变更保护和删除终态审计，继续保持生产关闭。
- D-3c.2：在真实 sandbox 验证首次/重复删除、404、超时、到期和项目隔离语义，完成独占 Project、孤儿对账及生产门禁。
