# ContextConsensus 阶段 D-3c.2 本地实现与外部门禁记录

日期：2026-08-01

## 结论

D-3c.2 的本地代码闭环已完成：OpenAI Project 成为渠道一等敏感字段，普通配置布尔值不再能够单独开放托管 Files 上传或删除；系统必须同时读到绑定精确渠道、Organization、Project、credential fingerprint 和密钥版本的短期签名 readiness 证据。内部孤儿对账只执行固定范围的只读分页扫描，完整扫描后才写入加密候选记录，扫描器本身不授权也不执行删除。

生产能力仍保持关闭。当前环境没有 OpenAI sandbox 凭据、独占 Project 证明或外部 WORM 落点，因而没有执行任何真实 OpenAI Files 请求，也没有执行任何生产变更。D-3c.2 总任务继续保持未完成，直到外部门禁全部取证并在临执行前完成独立生产高风险确认。

## 一等 Project 与精确目标绑定

- `Channel.OpenAIProject` 使用带字段注释的独立数据库列，不从 `HeaderOverride` 或客户端透传值推导。
- 渠道新增、编辑和敏感权限判断覆盖 Project；非终态托管文件存在时，Project 与 Organization、Key、Base URL 等目标字段同样禁止变更。
- 普通 OpenAI 请求与专用 Files POST、LIST、GET、DELETE 均从相同渠道字段发送 `OpenAI-Project`。
- 每次渠道选择或重试都显式覆盖 Organization 和 Project；托管文件 Resolution 在最终不可变请求发出前同时复核 endpoint、Organization、Project、渠道、固定凭据槽和精确凭据。
- Project/Organization 在进入请求头前拒绝首尾空白、控制字符、非 ASCII 和超长值；RelayInfo 格式化输出不再打印 Organization 或 Project 原文。
- 默认前端渠道表单增加 Project 字段、敏感编辑保护和六语言文案，创建、更新及显式清空负载均已覆盖。

## 版本化 readiness 证据

新增短期、不可直接更新或删除的 `ManagedProviderFileReadinessEvidence`：

- HMAC 绑定精确 target fingerprint、scope fingerprint、credential fingerprint、删除维护策略、上传生命周期策略、密钥版本、声明时间和硬过期时间；上传策略变化不会阻断已有文件的删除维护，但会阻断新上传。
- 独立绑定 Project 独占证明、真实 sandbox 契约、外部不可变审计和三数据库矩阵的证据摘要及版本。
- 单条声明有效期最多 24 小时；证据缺失、过期、被篡改、目标变化或密钥轮换时立即失败关闭。
- `provider_file_exclusive_project_attested` 与 `provider_file_sandbox_contract_verified` 仅保留为配置意图，上传入口、删除 worker 和对账任务均必须再通过签名证据校验。
- 项目没有增加可由普通管理 API 写入 readiness 证据的入口，避免把外部验收再次降级为可随意设置的布尔值。

## 有界孤儿对账

内部扫描固定使用 `purpose=user_data`、`order=asc` 和 `limit=100`：

- 每轮最多 100 页、10000 个对象、8 MiB 响应和 2 分钟；重复 file ID、游标循环、无进展页、上游错误、超时或达到上限时整轮标记 `incomplete`。
- 扫描开始时间减 5 分钟作为可见性保护 cutoff；晚于 cutoff 的新对象不进入本轮候选。
- 只有完整扫描才推进候选观察次数；不完整扫描不会写入或更新候选，避免把部分列表误当成完整项目视图。
- 原始 file ID 和 filename 仅保存于独立 AES-256-GCM 载荷；分页 cursor 仅留 HMAC，普通列、JSON、任务结果和错误信息均不包含原值。
- target-scoped provider reference HMAC 允许在未知 owner 的前提下与已有 lifecycle 精确匹配，不使用文件名或相似元数据猜测所有权。
- 候选状态首期仅允许 `managed`、`excluded_pre_attestation`、`quarantined`、`await_expiry` 和 `ambiguous`；隔离期至少 24 小时且要求至少两次完整稳定扫描。扫描器不会进入删除授权状态。
- 每小时系统任务仅在 reconciliation 配置开启且签名 readiness 有效时运行；默认配置和当前生产门禁均为关闭状态。
- 关闭新上传不会停止删除 worker 或只读恢复对账；删除 worker 在 claim 前按每条 lifecycle 的冻结渠道校验 target/scope/credential 和实际 batch/timeout 签名策略，不依赖当前新上传渠道；最大重试次数属于上传时冻结策略，不会因后续配置变化阻断旧 outbox。

## Live observation harness

已准备 `openai_files_sandbox` build tag 下的真实 OpenAI Files live observation 测试，覆盖双 Project 正常读写、跨 Project 隔离、缺失 Project 实际状态、首次/重复 DELETE、删除后 GET/LIST、实际到期和有界 cleanup ledger。测试默认不编译、不运行；必须同时设置 isolated sandbox 确认值、双 Project/双凭据、独立证据 HMAC 和不存在的 `0600` 证据路径，且拒绝代理与 HTTP 调试输出。运行手册见 `doc/openai-files-sandbox-live-observation-v1.md`。

## 数据库矩阵

同一迁移与行为测试已在以下本地独立临时数据库通过：

- SQLite：项目现有测试数据库。
- MySQL 5.7.44：满足项目 `MySQL >= 5.7.8` 支持边界。`mysql:5.7.8` 的旧 schema-v1 镜像清单被当前 containerd 运行时拒绝，因此改用同一 5.7 系列最新补丁版。
- PostgreSQL 9.6.24：项目最低支持大版本。

矩阵验证覆盖 AutoMigrate、带旧 readiness 证据行的策略字段升级与旧证据失败关闭、Project 与 target lookup 列、credential-bound readiness 写入和精确查询、整数主键、唯一索引、完整扫描事务、候选写入和 stale CAS 拒绝。两个项目专用临时容器已在测试后删除，仅下载的版本镜像仍保留在本机缓存。

## 验证结果

- `go test ./... -count=1`：通过。
- `go test ./model -run 'TestManagedProviderFileSchema(MySQL|PostgreSQL)$' -count=1 -v`：MySQL 5.7.44 与 PostgreSQL 9.6.24 均通过。
- `bun run typecheck`：通过。
- D-3c.2 前端涉及文件定向 `oxlint`：通过。
- `bun run build`：通过。
- `bun run i18n:sync`：六语言 `missingCount/extrasCount/untranslatedCount` 均为 0。
- 对账回归覆盖前页已积累观察、后页失败时整轮 `incomplete` 且候选零写入，以及 10000 对象硬上限终态可持久化。
- `go test -tags=openai_files_sandbox ./relay/channel/openai -run '^TestOpenAIFilesSandboxContract$' -count=1`：在未提供确认门禁时安全跳过；未执行真实 OpenAI 请求。

## 仍需外部完成

1. 在真实隔离 OpenAI Project 执行并独立复核 live observation，验证正确、缺失和错误 Project，跨 Project 可见性，上传后 LIST/GET 延迟与分页稳定性，首次及重复 DELETE、删除后读取和真实到期；429/5xx/超时仍需受控故障注入补证。
2. 证明 Project 的成员、服务账户、API Key、历史创建者和初始文件清单均满足网关独占要求。
3. 提供应用执行者不可修改或删除的 WORM 审计写入、读取回证、保留锁、权限隔离和销毁策略证据。
4. 提供生产监控阈值、停止窗口、备份新鲜度与完整性、RPO/RTO 和最近恢复演练证据。
5. 外部证据齐备后，只能通过独立生产高风险确认再登记短期 readiness 证据；登记、启用开关或发起真实扫描均属于生产状态变更。
