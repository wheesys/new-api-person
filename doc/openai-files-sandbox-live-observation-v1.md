# OpenAI Files sandbox live observation v1

日期：2026-08-01

## 用途与边界

仓库新增了一个仅在 `openai_files_sandbox` build tag 下编译的 live observation 测试：
`relay/channel/openai/provider_file_sandbox_contract_integration_test.go`。
它不是应用启动任务、管理 API、生产迁移或 CI 默认步骤；普通 `go test ./...` 不会发送外部请求。

该测试只允许固定官方 origin `https://api.openai.com`，使用显式的双重隔离环境门禁、两个不同 Project 和两套专用凭据。测试会创建并删除 `user_data` 文件，因此执行前必须确认目标是可丢弃的 isolated sandbox；本轮没有执行真实 OpenAI 请求。

## 覆盖的观察项

- 两个 Project 必须先通过空文件清单预检；各自上传后，在正确 Project 中 GET 和有界分页 LIST 可见。
- 两个 Project 必须属于同一 Organization；分别正交观察“正确凭据 + 错误 Project”和“错误凭据 + 正确 Project”，避免把 Organization、Project 与凭据隔离混为一项。缺失 `OpenAI-Project` 的 GET 和受限 LIST 会记录实际状态及目标文件是否可见，不预设 Project-scoped key 的默认解析语义。
- 首次 DELETE、重复 DELETE、DELETE 后 GET/LIST 的实际响应被记录；代码不会预设重复删除必须是 404。
- 固定使用服务端最小 60 秒到期值，并以上传响应的真实 `expires_at` 为起点轮询；删除和到期都必须连续两次观察相同的非认证、非限流 4xx 与 LIST 不可见，实际状态码写入证据，超时会失败关闭。
- 所有已确认或可能已创建的对象都进入有界 cleanup：上传结果未知时利用空基线、唯一试件属性和 60 秒强制到期执行最多 2 分钟的 LIST 对账；DELETE 结果未知时不盲目重发，持续观察到返回到期时间后 5 分钟。证据文件只包含项目/凭据 HMAC、有限操作结果和版本化 HMAC，不含 API key、Project 原文、file ID、filename、URL、cursor、请求 ID 或上游正文。

429、5xx、网络超时和 Project 独占性不能安全地由该测试强制制造或单独证明，因此它们仍需由隔离环境中的受控故障注入、权限清单和独立审计证据补齐。本观察证据不能单独升级为 `provider_file_sandbox_verified` 或生产 readiness。

## 显式运行门禁

测试在读取凭据前检查以下两个精确值：

```text
TEST_OPENAI_FILES_SANDBOX_CONFIRM=CREATE_AND_DELETE_ISOLATED_OPENAI_FILES_V1
TEST_OPENAI_FILES_SANDBOX_ENVIRONMENT=isolated_sandbox
```

还必须提供同一 Organization 下的两个 Project、两套专用 API key、独立的证据 HMAC key、预绑定的 Project HMAC 和一个不存在的绝对证据路径；两个 Organization 变量必须同时为空或值完全相同：

```text
TEST_OPENAI_FILES_SANDBOX_PRIMARY_API_KEY
TEST_OPENAI_FILES_SANDBOX_PRIMARY_ORGANIZATION   # 可选
TEST_OPENAI_FILES_SANDBOX_PRIMARY_PROJECT
TEST_OPENAI_FILES_SANDBOX_SECONDARY_API_KEY
TEST_OPENAI_FILES_SANDBOX_SECONDARY_ORGANIZATION # 可选
TEST_OPENAI_FILES_SANDBOX_SECONDARY_PROJECT
TEST_OPENAI_FILES_SANDBOX_EVIDENCE_HMAC_KEY      # 至少 32 字节，禁止复用 API key
TEST_OPENAI_FILES_SANDBOX_PRIMARY_PROJECT_HMAC
TEST_OPENAI_FILES_SANDBOX_SECONDARY_PROJECT_HMAC
TEST_OPENAI_FILES_SANDBOX_EVIDENCE_PATH
```

Project HMAC 使用独立 key、域分隔字符串 `openai-files-sandbox-live-observation-v1\x00project\x00<project>` 计算 SHA-256 HMAC，并以小写十六进制提供。整份 artifact 的 `evidence_hmac` 校验方式是：先将该字段置为空字符串并按结构字段顺序重新编码 JSON，再对 `openai-files-sandbox-live-observation-v1\x00artifact\x00<json>` 计算同一 key 的 SHA-256 HMAC。证据文件使用 `O_EXCL` 创建、权限 `0600`；父目录不能对组或其他用户可写。测试拒绝代理环境变量和 `GODEBUG=http2debug`，HTTP 客户端固定 TLS 1.2 以上、超时和禁止重定向。证据同时绑定 Go build info 中的源码 revision；工作区有未提交修改或无法取得 revision 时在网络请求前失败关闭。

运行命令：

```bash
go test -tags=openai_files_sandbox ./relay/channel/openai -run '^TestOpenAIFilesSandboxContract$' -count=1 -timeout=9m -v
```

不得把凭据写入命令历史、日志、工单或证据；运行输出只允许显示测试状态。执行后应人工核对证据 HMAC、cleanup ledger、两个 Project 的文件清单和权限/成员证明，再由独立审计流程决定是否继续 D-3c.2 外部门禁。生产配置、readiness 登记、数据库写入或服务发布仍需遵守线上修改二次确认。
