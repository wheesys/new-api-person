# ContextConsensus 阶段 C-3c 实施记录

日期：2026-07-30

## 结论

阶段 C-3c 已完成 adaptor 真实 provider state report、成功响应后的 binding 登记、请求前绑定解析、精确目标固定和 Redis 原子提交。首批仅支持原生、非流式 OpenAI Responses 的 `id -> previous_response_id`；阶段 C-3 与阶段 C 至此完成。

本批未增加管理员接口、管理页面、数据库表或外部配置。`managed_context_enabled` 继续默认关闭；Claude/Gemini opaque state、OpenAI `conversation`、`context_management`、provider file 和非消息 item reference 继续失败关闭。

## Provider State Report

- OpenAI adaptor 仅在 source/final protocol 都是原生 Responses、渠道类型为 OpenAI、无协议转换且请求非流式时声明 report 能力。
- 只有 HTTP 2xx 且响应 `object=response`、`status=completed`、无 `error`、无 `incomplete_details` 时，才提取合法 `resp_` 响应 ID。
- report 的 JSON、`String` 和 `GoString` 均不暴露原始 state reference；原始 ID 只存在于 AEAD 保护的提交 checkpoint 中。
- 缺 ID、失败或 incomplete 响应、Chat-to-Responses、协议转换和其他 adaptor 均拒绝生成 binding。

## 精确绑定与请求门禁

binding 固定以下权威目标：

- 客户端授权后的原始模型和最终上游模型。
- 最终协议、渠道 ID、渠道类型。
- 单/多凭据模式、精确凭据槽索引。
- 基于渠道、槽位和凭据派生的版本化 credential HMAC fingerprint。

后续请求在渠道选择前按 user、API Key Token、endpoint family 和 state reference HMAC 解析 binding。Token 专属渠道同样必须命中绑定渠道；API Key 模型权限使用绑定的原始模型重新校验。选渠后固定同一凭据槽，并在最终不可变请求快照上再次校验模型、协议、渠道、槽位和 fingerprint。

最终发送前门禁检查参数覆盖后的正文和有效请求头：

- `store` 只能缺省或为 `true`。
- `previous_response_id` 必须缺省，或与已认证 binding 精确匹配。
- 拒绝 `conversation`、`context_management`、`prompt`、非消息 item reference 和 `input_file.file_id`。
- 拒绝静态或运行时覆盖 `Authorization`、`api-key`、`x-api-key` 等凭据头。
- managed 请求禁用请求级 debug body 输出，避免 provider state 或认证材料进入日志。

## 原子提交与恢复

- 成功响应生成的 provider commit 与下一 revision、L2/L3 和 provider link 形成同一提交计划；provider binding payload 使用独立 AES-256-GCM purpose 加密。
- Redis Lua 在同一次 CAS 中写入 consensus revision 和 provider binding；旧 namespace 冲突、revision 冲突或 lease/fencing 失效时两者都不写入。
- previous key namespace 的 consensus 迁移与 provider binding 写入同样保持原子；active/previous provider namespace 双命中失败关闭。
- consensus TTL 不得长于 provider binding TTL，避免 revision 引用已经过期的 state mapping。
- Redis 返回不确定结果时，恢复逻辑必须同时确认精确 next state、binding digest 和密文 envelope；跨请求 `settled_pending_commit` 恢复也遵守同一确认规则，不能只凭 revision 推断成功。
- 已持久化的旧密钥 provider commit 可在 previous key 读取窗口内完成和解析；旧密钥提前退役时安全失败，不会改用不匹配的 active namespace。

## 安全与运维边界

- Redis key 只包含版本化 owner/state HMAC；binding 与原始 state reference 不以明文持久化或记录日志。
- 渠道 ID、类型、模型、凭据模式/槽位和 fingerprint 均参与校验。凭据变化会失败关闭；仅原地修改同一渠道的 endpoint 且保持凭据不变不在 fingerprint 覆盖范围内，因此发布流程不得复用渠道身份承载另一个上游账号或 endpoint。
- 本阶段只闭合 Responses `previous_response_id`。其他 provider-owned 字段不因阶段 C 完成而自动获得支持。

## 验证

- report 成功/失败/incomplete/协议转换矩阵，原始 state reference 与凭据不出现在序列化记录中。
- owner、原始模型、上游模型、协议、渠道、单/多凭据模式、槽位和 credential fingerprint 冲突矩阵。
- 参数覆盖后的 `store`、未绑定 `previous_response_id`、provider-owned 字段、item/file reference 和凭据头覆盖门禁。
- Token 专属渠道及 API Key 模型权限集成测试。
- Redis 原子 CAS、旧 namespace 冲突零写入、原子迁移、TTL 和不确定提交恢复。
- `go test ./service/contextconsensus ./middleware ./relay/channel/openai ./relay ./controller -count=1`。
- `go test -race ./service/contextconsensus ./middleware ./relay/channel ./relay/common ./relay ./controller -count=1`。
- `go vet ./service/contextconsensus ./middleware ./relay/channel ./relay/common ./relay ./controller`。
- `go test ./... -count=1`。
- `git diff --check`。

本地自动化使用 SQLite 和 miniredis；本轮未运行真实 MySQL/PostgreSQL、真实 Redis 集群或真实 OpenAI 上游故障注入。对应真实环境验证继续作为启用 `managed_context_enabled` 前的发布门禁。

## 后续待办

- 进入阶段 D 前单独评审工具结果脱敏、串并行工具图和 provider file 生命周期，不复用本阶段的纯文本 state 结论。
- 在真实 Redis 和 OpenAI 测试账号上补充网络中断、Redis 超时、进程退出和旧密钥退役故障注入。
