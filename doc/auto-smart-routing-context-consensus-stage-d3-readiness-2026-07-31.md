# ContextConsensus 阶段 D-3 就绪性核查

日期：2026-07-31

## 结论

阶段 D-3 首个 provider file 生命周期仅选择 OpenAI 官方 Files API，并将范围限制为原生 OpenAI Responses 的 `input_file.file_id`。上传目标由管理员配置的专用渠道确定，客户端不能指定渠道，也不能提交或获得原始 provider file ID。

本地可以继续实现默认关闭的持久化模型、加密绑定、上传与元数据核验、删除 outbox、审计和测试。生产开放仍依赖专用 OpenAI Project 的独占声明和真实 sandbox 契约验证；这些外部门禁不阻塞默认关闭代码的实现。

## 首期固定范围

- 仅允许 `ChannelTypeOpenAI` 和规范化后的官方 origin `https://api.openai.com`。
- 仅允许原生 OpenAI Responses，不允许协议转换、pass-through、兼容代理、自定义 Base URL 或认证头覆盖。
- 仅允许管理员显式配置的专用单 Key 渠道，凭据槽固定为 `0`；多 Key 后续需先具备稳定槽位标识。
- 上传强制使用 `purpose=user_data` 和服务端有限 `expires_after`，每个文件及单请求文件合计均不得超过 50 MB。
- 客户端只使用 owner-bound 不透明网关句柄；原始 file ID 和文件名只允许出现在 AEAD 加密载荷及上游调用内存中。
- `/v1/files` 上传、单文件查询和删除使用独立分发流程；列表与文件内容接口首期继续返回未实现。
- 功能默认关闭。关闭新上传和新使用时，已有文件的删除 worker 与恢复流程不能随之停止。

## 权威所有权与目标绑定

所有权不能由“当前凭据可以读取文件”推断。只有网关先持久化上传 intent，并从同一精确目标收到合法上传响应，再立即读取并核对权威元数据后，才能登记为 active。

持久化绑定至少包含：

- owner：用户、API Token 和固定 endpoint family 的版本化 HMAC。
- lookup：网关句柄、客户端上传幂等键和 provider reference 的 owner-bound 版本化 HMAC。
- target：协议、渠道 ID/类型、单 Key 槽位、credential fingerprint，以及 Base URL、Organization、Project 和相关头身份的 target fingerprint。
- metadata：provider 返回的 bytes、created_at、expires_at、purpose 和最后核验时间。
- secret payload：AEAD 加密的原始 provider file ID 和必要恢复信息。

使用文件前必须按 owner 解析句柄，锁定相同渠道和凭据槽，必要时用同一目标重新读取元数据，再将类型化 `input_file.file_id` 改写为原始 ID。最终冻结请求仍由 D-3a 门禁复核引用集合和精确目标，禁止负载均衡或失败切换到其他渠道。

## 删除与恢复边界

- 删除使用主数据库持久化 outbox、有限批次、跨数据库 CAS lease、有限退避和明确终态。
- 仅已验证的 `deleted=true` 响应可以直接进入 deleted；404 是否等价于 not-found 必须等待真实 sandbox 验证。
- DELETE 发出后响应丢失属于 `delete_unknown`，在 provider 重试语义未验证前不能盲目重发。
- 上传成功但数据库未保存 file ID 的崩溃窗口无法靠本地幂等键消除。只有专用 Project 被声明为网关独占后，才能通过有界分页和隔离期对未知 `user_data` 文件执行 reconciliation。
- 审计记录禁止包含原始句柄、file ID、文件名、正文、URL、凭据或上游错误正文。应用内摘要链只能提供篡改检测；生产不可变性仍需部署侧不可变审计存储和权限证明。
- 存在非终态文件时必须阻止修改或删除绑定渠道的 Key、Base URL、Organization、Project、相关头及多 Key 配置。

## 外部门禁

生产启用前必须完成：

1. 管理员指定一个由网关独占的 OpenAI Project、渠道和专用服务凭据，并确认该 Project 内文件没有其他创建者。
2. 使用真实 sandbox 验证 `user_data + expires_after`、上传后立即读取、实际 `expires_at`、错误项目和错误凭据、首次与重复 DELETE、删除后 GET、429/5xx/超时以及实际到期边界。
3. 明确接受独占 Project 下的有界 orphan reconciliation，以及存在非终态文件时的渠道和凭据变更限制。
4. 核验生产不可变审计、告警、停止阈值、备份恢复证据和数据库迁移矩阵。任何真实生产变更仍需单独的临执行前二次确认。

## 实施拆分

- D-3b.1：已完成领域契约、版本化 HMAC/AEAD、跨数据库持久化模型和默认关闭配置，见 `doc/auto-smart-routing-context-consensus-stage-d3b1-implementation-2026-07-31.md`。
- D-3b.2：已完成专用上传/查询 API、原生 OpenAI 单 Key 客户端、权威元数据核验、owner-bound 句柄和 Responses 精确目标绑定，见 `doc/auto-smart-routing-context-consensus-stage-d3b2-implementation-2026-07-31.md`。
- D-3c.1：已完成严格 DELETE client、持久化派发边界、CAS lease worker、有限重试、未知终态、审计链、告警和渠道变更保护，生产硬门禁继续关闭，见 `doc/auto-smart-routing-context-consensus-stage-d3c1-implementation-2026-07-31.md`。
- D-3c.2：真实 sandbox 契约测试、独占 Project 声明、孤儿对账和生产 readiness 门禁。

## 依据

- OpenAI Files create：`https://developers.openai.com/api/reference/resources/files/methods/create`
- OpenAI Files retrieve：`https://developers.openai.com/api/reference/resources/files/methods/retrieve`
- OpenAI Files delete：`https://developers.openai.com/api/reference/resources/files/methods/delete`
- OpenAI file inputs：`https://developers.openai.com/api/docs/guides/file-inputs`
