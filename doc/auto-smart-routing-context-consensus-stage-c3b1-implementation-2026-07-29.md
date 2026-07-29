# ContextConsensus 阶段 C-3b1 实施记录

日期：2026-07-29

## 结论

阶段 C-3b1 已完成托管主调用与摘要子调用的持久化计费 operation。每个 operation 以 owner、conversation、expected revision 和调用用途为业务身份，在主数据库事务中原子完成 API Key 额度预占/补扣/退款、用户与渠道统计、冻结结算结果及消费日志 outbox，从而使同一计费 operation 的重试不会重复扣费或重复统计。

阶段 C-3b 和阶段 C 仍未完成。稳定客户端幂等键、revision intent、`settled_pending_commit`/`committed` outcome、已提交响应回放及上游调用 exactly-once 不属于本批；这些边界继续归入 C-3b2。provider state report、登记、绑定和精确凭据槽固定继续归入 C-3c。

本批未增加管理员接口、管理页面或外部路由配置。新增数据库对象由既有自动迁移创建；`managed_context_enabled` 继续默认关闭，provider-owned state 继续失败关闭。

## 已完成范围

### 持久化计费身份与状态

- 新增 `billing_operations`，使用整数自增主键；业务唯一性由版本化 lookup HMAC 表达，不保存原始 conversation ID、请求正文、认证信息或密钥。
- 主调用与摘要子调用使用独立 purpose，因此同一 revision 的两类消费互不覆盖。
- 请求指纹绑定 expected revision、purpose、协议、源计划摘要、策略版本、原始/最终模型、渠道及计费请求输入摘要；身份或请求语义不一致时返回冲突，不复用既有结算。
- operation 状态限定为 `reserved`、`settled`、`refunded`。重复预占返回持久化快照，重复结算返回冻结结果，重复退款不再次增加额度。
- active/previous key lookup 同时命中时失败关闭；只命中 previous key 时在主库事务内迁移至 active lookup，并保持稳定数字 operation ID。

### 原子额度与结算

- 托管请求在上游调用前必须已创建并预占 operation；禁止在上游完成后补做惰性预占。
- 固定价格、阶梯表达式和免费请求统一进入 durable session。价格设置、阶梯快照、预占额度及最终结算结果写入 operation，配置热更新后的重试继续使用首次冻结值。
- 结算事务同时处理 API Key 额度差额、Token 已用额度、用户请求/用量统计、渠道请求/用量统计、operation 终态和日志 outbox。
- 即使全局批量更新开关启用，durable session 仍使用同步数据库事务，不依赖内存批处理推断计费结果。
- 结算写入响应丢失时，调用方按 operation ID 读取冻结 receipt；只有数据库中的终态 receipt 可以恢复成功。

### 消费日志 outbox

- 新增 `billing_operation_log_outboxes`，在结算主事务中冻结日志载荷，每个 operation 仅允许一个 outbox。
- `logs.billing_operation_id` 为可空唯一字段：普通请求保持原日志路径，托管 durable operation 通过 operation ID 去重。
- 主库与 SQL 日志库分离时，先提交主库结算和 outbox，再幂等投递日志库；投递确认丢失时重放不会新增第二条消费日志。
- 当前 ClickHouse 日志路径不能提供本契约要求的唯一约束，因此托管 durable billing 在 ClickHouse 模式下失败关闭；普通非托管日志不受影响。

### 故障边界

- 预占事务失败会回滚额度；并发创建由唯一 lookup 收敛为同一 operation。
- 已结算 operation 不允许退款；已退款 operation 不允许结算或重新预占。
- owner、Token、渠道、playground 身份或请求指纹不一致时失败关闭。
- durable operation 不宣称上游调用 exactly-once，也不根据 Redis phase 猜测数据库扣费结果。

## 数据库兼容

- 主表、outbox 和日志字段通过 GORM/既有迁移路径支持 SQLite、MySQL 与 PostgreSQL。
- 新表使用整数主键、无外键；所有字段均包含数据库注释。
- SQL 日志库支持与主库分离部署。ClickHouse 仅增加兼容的可空 operation ID 列，但托管 durable billing 在唯一投递能力补齐前保持失败关闭。

## 验证

- 固定价格、阶梯表达式、免费请求及批量更新开启时的同步 durable 结算。
- 并发预占、并发退款、重复结算、已结算后退款、事务回滚和结算确认丢失恢复。
- 价格热更新后的冻结快照复用、active/previous key 迁移及双 lookup 冲突。
- 主库与独立 SQL 日志库投递、日志确认丢失重放及 ClickHouse 失败关闭。
- `go test ./... -count=1`。
- `go test -race ./model ./service ./service/contextconsensus ./middleware ./relay ./controller -count=1`。
- `go vet ./model ./service ./service/contextconsensus ./middleware ./relay ./controller`。
- `git diff --check`。

## 后续待办

- C-3b2：定义稳定客户端幂等键及作用域，持久化 revision intent、`settled_pending_commit`/`committed` outcome 与可回放响应，恢复上游成功但 revision 提交或客户端响应失败后的跨请求重试。
- C-3b2：明确 operation、outcome 与托管 state 的联合保留期、清理机制及 key rotation 退役窗口。
- C-3c：接入 adaptor 真实 provider state report；首批仅闭环原生 OpenAI Responses `id -> previous_response_id`，并固定最终模型、协议、渠道、精确凭据槽和 credential fingerprint。
