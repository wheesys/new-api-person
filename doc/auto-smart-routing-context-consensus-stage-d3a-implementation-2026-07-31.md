# ContextConsensus 阶段 D-3a 实施记录

日期：2026-07-31

## 结论

阶段 D-3a 已完成 provider file 生命周期的安全前置闭环：四种协议只在已知文件位置识别 provider file，输出不含原始文件 ID、URI、URL、签名参数或内联数据的有界证据；最终上游请求冻结后再次执行生命周期门禁。现有 adaptor 均未声明完整权威能力，因此 provider-owned 文件在 `ContextConsensus` 中继续失败关闭，没有被误开放。

本阶段没有执行文件查询、删除或其他外部写操作，也没有新增数据库表、管理员写接口或生产配置。

## 文件引用分类

- OpenAI Chat Completions：识别 `file.file_id`、`file.file_data`、`file.file_url`、`image_url`、`video_url` 和内联音频。
- OpenAI Responses：识别 `input_file`、`input_image` 和 `input_audio` 的协议字段。
- Claude Messages：识别 image/document 的 `base64`、`url` 和 `file` source。
- Gemini：识别 `inlineData` / `inline_data` 与 `fileData` / `file_data`。
- 引用分为 provider file ID、provider file URI、内联数据、外部 URL 和签名 URL；不再通过通用递归 `file_id` 猜测 provider 绑定。
- provider ID/URI 只保留进程密钥 HMAC 证据；外部 URL、签名 URL 和内联数据不保留摘要或原文。
- 已识别的联合类型要求来源唯一且字段为非空字符串。数值字段、空值、冲突来源、无 host URL、非 HTTP(S) URL 和空 data URL 均失败关闭。

## 最终请求门禁

生命周期校验读取 `PreparedRelayRequest` 的不可变最终正文，并在权威 token 预算计算和上游发送之前执行：

1. 重新按最终协议提取文件证据，拒绝畸形结构和超出 128 条的请求。
2. 内联数据和普通外部 URL 保持可移植，不要求 provider 生命周期。
3. 签名 URL 不从查询参数推断真实到期时间，直接按不可移植引用拒绝。
4. provider ID/URI 只允许原生协议、无 pass-through、无协议转换且无凭据请求头覆盖的请求。
5. adaptor 必须显式实现 `ProviderFileLifecycleAdaptor`，并同时声明权威所有权、权威到期时间和权威删除能力；任一能力缺失即拒绝。

能力声明是显式 opt-in。现有 adaptor 不会因新增接口而自动获得生命周期权限。

## 证据与删除契约

- `ProviderFileLifecycleMetadata` 只接受合法 HMAC、已确认所有权、正数到期时间和已确认删除支持。
- `ProviderFileDeletionReport` 只接受 `deleted` 或 `not_found` 终态及正数删除时间；失败状态不能伪装为成功报告。
- 契约中不包含原始文件引用，序列化的 `ContextEnvelope` 也不会暴露原始文件 ID、URI、URL、签名或内联数据。
- 本阶段仅冻结契约和门禁，尚未实现 provider 查询或删除执行器。

## 敏感日志保护

- `URLSource.GetIdentifier()` 固定返回 `remote-url`，不再截取 URL、用户信息、路径或查询串。
- `Base64Source.GetIdentifier()` 固定返回 `inline-base64`，不再截取内联数据前缀。
- 文件下载、MIME 探测、重定向拦截和 Replicate 下载错误不再拼接原始外部 URL。
- 现有 token 计数与 Gemini 错误路径继续复用统一文件标识，因此不会间接带出原始引用。

## 验证

- `go test ./... -count=1`：在允许既有 miniredis 测试监听本地回环端口的隔离测试环境中通过。
- `go test ./types ./service/contextconsensus ./relay ./controller -count=1`：通过。
- `go test ./service/contextconsensus ./relay ./controller -run 'ProviderFile|ContextConsensus' -count=1`：通过。
- `go test -race ./service/contextconsensus ./relay ./controller -run 'ProviderFile|ContextConsensus' -count=1`：通过。
- `go vet ./types ./service/contextconsensus ./relay ./controller`：通过。
- `git diff --check`：通过。

测试覆盖四协议分类、原始引用不序列化、通用 `file_id` 不误判、畸形和冲突来源失败关闭、128 条上限、最终冻结正文、协议转换、pass-through、凭据覆盖、缺失/不完整 adaptor 能力以及签名 URL 不推断生命周期。

## 后续拆分

- D-3b：选择确实具有网关托管上传语义的 provider/adaptor，接入只读元数据查询，并以持久化绑定证明文件由当前网关和精确凭据槽创建；仅“当前凭据可读取”不能视为网关所有权。
- D-3c：在 D-3b 基础上实现持久化删除 outbox、幂等删除、到期调度、重试上限、终态审计和失败告警；在具备恢复与审计证据前不得启用真实删除。
