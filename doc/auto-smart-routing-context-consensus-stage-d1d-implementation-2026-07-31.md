# ContextConsensus 阶段 D-1d 实施记录

日期：2026-07-31

## 结论

阶段 D-1d 已完成有限 reason code 聚合诊断闭环。新增 `tool_compaction_diagnostic` 只持久化版本、结构资格状态和有限 reason code；管理员可通过只读 API 与模型管理中的 `Context Diagnostics` 页面查看成功智能路由消费日志的有界聚合结果。

新增 diagnostic 对象不复制原始 call ID、函数名、参数、结果、schema、digest、文件引用、摘要正文、会话内容或凭据，聚合 API 也不返回这些内容。结构就绪只表示工具上下文具备进入脱敏阶段的结构条件，不代表已登记脱敏策略或已经发生压缩。

## 窄日志契约

- `tool_compaction_diagnostic` 固定为 schema v1，只允许 `not_applicable`、`ready_for_sanitization`、`blocked` 三种状态。
- `blocked` 只接受 D-1a 已定义的 13 个有限 reason code，并拒绝未知值和重复值；其他状态不得携带 reason code。
- 中间件从已解析的 `ContextEnvelope` 生成诊断，不复制结构证据、digest 或原始工具节点。
- 诊断只进入既有 `other.smart_routing.context_consensus` 审计对象，不新增会话状态、数据库表或动态配置。

## 聚合 API

- 新增管理员只读接口 `GET /api/smart-routing/context-consensus/diagnostics`，复用 `AdminAuth`、关键接口限流和现有时间窗口校验。
- 默认查询 24 小时，最大 7 天；复用成功智能路由消费日志迭代器，最多处理 250000 条匹配日志。
- 单条 `other` 最大解析 1 MiB。超限、JSON 无效、协议未知、schema/status/reason 非法分别计入数据质量，不向客户端透传原始字段。
- 返回总体资格率、阻断原因计数、协议计数、小时趋势，以及 valid/invalid/legacy/oversized 数据质量统计；数组排序稳定且空集合返回空数组。
- API 错误只返回固定消息；服务端错误日志不包含消费日志正文。

## 管理员页面

- 在模型管理新增 `Context Diagnostics` 分区，沿用模型路由的管理员权限门禁。
- 支持 24 小时、3 天、7 天窗口和手动刷新，展示资格摘要、小时趋势、有限阻断原因和协议聚合。
- 前端在进入 React Query 缓存前按响应白名单投影；reason code 和 protocol 均使用固定映射，未知值只显示通用标签，不渲染原值。
- 页面文案明确数据范围仅为已持久化的成功 Smart Routing 消费日志，并区分结构资格与实际脱敏/压缩行为。
- 模型标签导航在移动端保持单行滚动，当前标签会在路由切换和视口变化时完整进入可视区域。
- 新增文案已通过项目 i18n 同步工具补齐 en、zh、fr、ja、ru、vi 六种语言。

## 安全边界

- 本阶段没有启用新的工具脱敏策略，也没有改变 `allow_tool_result_compaction` 默认关闭行为。
- 页面和 API 不提供单请求钻取、会话浏览器、原始日志导出或 reason code 之外的工具详情。
- 仅聚合成功消费日志；它不是全部请求、失败请求或实时执行状态的完整统计。
- D-2 并行工具组、D-3 provider file 生命周期和生产静态脱敏策略登记均不在本阶段范围内。

## 验证

- `go test ./... -count=1`。
- `go test -race ./service/contextconsensus ./service/smartrouting ./middleware ./service ./controller -run 'ToolCompaction|ContextConsensusDiagnostics|ContextConsensus' -count=1`。
- `go vet ./service/contextconsensus ./service/smartrouting ./middleware ./service ./controller ./router`。
- `bun run typecheck`、目标文件 `oxlint`、`bun run i18n:sync` 与 `bun run build`。
- Playwright 在 1440x900 和 390x844 视口验证初始加载、7 天切换、刷新、图表 canvas、无敏感哨兵内容、无控制台错误及移动端无横向溢出。

## 后续拆分

- D-2：统一并行 group 语义后评估稳定 ID 协议的并行工具组；Gemini 同名并行继续失败关闭。
- D-3：待 adaptor 提供权威所有权、到期和删除能力后实现 provider file 生命周期。
- 生产静态脱敏策略仍需针对服务端实际拥有的精确工具/schema 单独评审并通过代码登记。
