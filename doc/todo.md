# 项目待办事项

更新时间：2026-08-01

## 已完成

- [x] 梳理项目模块、渠道、供应商、模型、计费、日志等核心业务关系，并归档到 `doc/project-module-business-relationships.md`。
- [x] 补充模型基础固定价格 `ModelPrice` 的页面配置入口、默认值来源、保存位置和计费公式说明。
- [x] 补充直接上游 `QuantumNous/new-api` 的 AGPLv3 归属、Section 7 附加条款和 Docker/源码追溯说明。
- [x] 将 README 底部 Star History 统计图和 GitHub issues/releases 链接调整为当前项目仓库。
- [x] 将 README 顶部项目状态徽章调整为当前项目仓库，并将上游宣传徽章标注为上游项目徽章。
- [x] 移除 README 顶部项目状态徽章和上游宣传徽章。
- [x] 移除 README 中残留的 DeepWiki 徽章和 Star History 图表徽章。
- [x] 移除默认前端和经典前端模型资产供应商模块及后端 `/api/vendors/*` 管理入口，保留数据库兼容层。
- [x] 将模型定价入口提升到模型管理顶层 `Pricing / 定价` 页签，并将模型基础固定价改为跟随模型自身 `base_price` 设置。
- [x] 新增渠道价格倍率配置，并接入预扣费、后结算、任务、音频、MJ、违规扣费和日志链路。
- [x] 补齐经典前端渠道新增/编辑页面的渠道倍率配置入口，允许设置为 0。
- [x] 新增渠道时自动补齐模型管理元数据，并将默认 `ModelRatio` / `ModelPrice` 模型初始化到模型管理。
- [x] 模型管理列表改为只展示有启用能力覆盖的模型，并确认模型广场按模型名聚合不重复展示。
- [x] 移除钱包、充值、支付网关、兑换码和订阅购买入口，去除请求链路用户余额检查，保留 API Key 自身限额和用量统计。
- [x] 新增 Docker Hub 公共镜像推送脚本，默认发布到 `docker.io/walllee/new-api-person` 并使用 `Asia/Shanghai` 时区。
- [x] 复测 VChart alias 修复后的数据看板页面，确认 `/console` 不再出现 `createCanvas` / `filter` 控制台错误和页面渲染错误。
- [x] 重新执行管理员登录后的后台点击验证：`/console`、`/console/channel`、`/console/models`、`/pricing`。
- [x] 复测渠道新增弹窗的渠道倍率字段；临时本地库无渠道数据，渠道编辑弹窗无可编辑样本，本轮未额外创建测试渠道。
- [x] 复测模型新增弹窗无供应商字段，模型管理页无 `/api/vendors` 请求。
- [x] 完成最终验证后提交代码，并按仓库规则在 `git commit` 后立即执行 `git push`。
- [x] 删除不再使用的 classic 前端；默认前端渠道列表保留上游渠道账户余额查询、Codex 用量查询和批量更新余额入口。
- [x] 在默认前端渠道列表新增渠道倍率展示列；倍率编辑入口继续保留在渠道高级设置中。
- [x] 将个人资料、仪表盘摘要和用量日志用户信息中的当前余额展示改为已用额度/历史消耗展示。
- [x] 将模型定价和分组管理迁移到模型管理左侧菜单，并关闭系统设置中的相关分组定价入口。
- [x] 将默认前端主题切换为新版 default。
- [x] 记录本地单端口启动和双端口热更新启动方案，并在 `CLAUDE.md` 增加默认本地单端口启动约定。
- [x] 将本地启动 SQLite 持久化路径从 `/private/tmp` 迁移到项目内 `data/local/`，并确认本地数据库文件被 Git 忽略。
- [x] 检查本地请求价格计算日志，确认渠道倍率未进入当前消费计费并归档报告。
- [x] 修复文本请求渠道倍率在 `ChannelMeta` 初始化前未参与计费的问题，并补充回归测试。
- [x] 重启本地服务加载修复后的本地代码，并确认 `/api/setup` 状态接口响应正常。
- [x] 复查新增 `/v1/messages` 消费日志，确认 `channel_ratio=0.25` 且逐条扣费复算差异为 0。
- [x] 评估当前项目 PostgreSQL 与 SQLite 生产使用差异，并归档 SQLite 生产化建议。
- [x] 将 Docker Compose 生产部署切换为 SQLite，并落地 SQLite DSN、连接池和启动日志优化。
- [x] 新增 PostgreSQL Compose override 文件，并补充旧 new-api 数据兼容说明。
- [x] 新增 `options.DataMigrationVersion` 数据迁移版本机制，并补齐旧渠道模型元数据、能力路由和渠道倍率默认值。
- [x] 在用户管理页新增 Root 级清空累计用量入口，并将默认前端用户列表额度列改为展示已用额度。
- [x] 调研自动智能路由：按请求复杂度主动切换模型/渠道，并归档跨模型上下文共识、OpenRouter 差异和多 agent 边界方案。
- [x] 设计并落地智能路由阶段一核心数据结构：`auto:*` 虚拟模型、复杂度评分、上下文需求评分、候选评分和日志决策结构。
- [x] 从现有定价缓存和渠道快照生成智能路由 `SmartRouteCandidate`，并补充保守模型能力推导。
- [x] 将 `middleware.Distribute()` 的虚拟模型请求接入 `service/smartrouting`，在渠道选择前解析真实模型、写入审计决策并重写 JSON 请求体模型字段。
- [x] 收紧智能路由模型切换边界：只有 `auto:*` / `smart:*` 允许跨模型切换，普通模型只在同模型渠道之间按渠道倍率、延迟、权重等分数选择。
- [x] 为智能路由补充失败后的同模型候选序列消费机制，让重试沿用本次路由排序结果且不回落到随机渠道。
- [x] 新增智能路由模型画像缓存、`auto` 默认均衡策略、外部榜单 10 天刷新任务、内部模型评分 1 天重算任务，并将画像评分接入候选排序。
- [x] 接入 SWE-bench、Artificial Analysis 和 Arena 榜单适配器，支持通过环境变量配置外部榜单地址，并按来源归一化后合并到模型画像缓存。
- [x] 完成虚拟模型管理员可配置模型池后端闭环：新增 `smart_routing.virtual_model_pools` 配置、`auto:*` / `smart:*` 候选池过滤和回归测试，并归档到 `doc/auto-smart-routing-configurable-model-pool-implementation-2026-07-10.md`。
- [x] 在默认前端模型设置页新增 `Smart Routing` 配置入口，支持编辑 `smart_routing.virtual_model_pools` 并补齐六语言 i18n，实施报告见 `doc/auto-smart-routing-configurable-model-pool-implementation-2026-07-10.md`。
- [x] 完成跨模型 `ContextConsensus`、自动压缩和工具状态保持技术设计，明确 provider-bound 状态、工具原子段、独立计费、加密存储和分阶段实施方案，见 `doc/auto-smart-routing-context-consensus-design-2026-07-24.md`。

## 待办

### 上游同步重启接续

- [x] 创建专用分支 `sync/upstream-full-20260801`，以上游 `upstream/main` 的 `cfaba1dd6754d4238e1360247c198a64a313e96c` 为基线，采用“上游最新版作为底座，再重放本项目定制提交”的方式同步，避免每次完整拉取后反复删除模块。
- [x] 已完成前端目录从 `web/default` 迁移到扁平化 `web/`，删除 classic 前端，并排除 vendor、用户钱包、充值、支付、兑换码和订阅购买模块。
- [x] 已确认并恢复上游渠道账户余额查询；该功能属于渠道运维，不属于用户钱包模块。保留渠道余额列、单渠道查询、批量更新和 Codex 用量弹窗，同时叠加渠道价格倍率列。
- [x] 已重放并验证模型定价、渠道倍率计费、SQLite 数据迁移、累计用量重置和智能路由第一阶段选择。相关前端类型检查、i18n 同步、Compose 配置检查及定向 Go 测试已通过。
- [x] 已完成提交 `51193a87`（智能路由模型画像刷新）的重放，cherry-pick 结果为 `8f51426c`；`controller/relay.go` 导入冲突按上游 `relaykit/types` 迁移和本项目智能路由行为合并，智能路由相关测试已通过。
- [x] 已重放 `6ffd30d7`（可配置模型池）：落地 `smart_routing.virtual_model_pools` 后端配置、`auto:*` / `smart:*` 候选池过滤、前端 `Smart Routing` 入口与六语言 i18n；冲突保留上游 `relaykit/types` 迁移和本项目智能路由行为。
- [ ] 随后按顺序继续重放上下文共识和文件能力提交：`d9287816 33f2ccf1 40097959 d48a9f5c 9f56e5da 6a1498b9 0e8bfeac f512e49a 0ede84bf a36df420 13785e7f 0cbcabd8 9f522b2a 7bfb37be 38ae39f6 549d39ac 44fb418b ad105847 69881574 f7cbb392 761834e0 c627a5b3 49f3c7cd 6516aa23 d09b95b5 5bae9bd3 3e83b42e eb88752c 732894e3 2d1c6466 86453b91 4b25be32 3a3569cd ba1f0329 c1aee3ae 574b3934 1c0bd4a5 67fe50b5`。
- [ ] 完成重放后统一审计：不得恢复 classic、vendor、用户钱包、充值、支付、兑换码、订阅购买及上游发布宣传模块；其他上游前后端更新均应保留，特别是由前端改动引发的后端接口和类型调整。
- [ ] 最终运行前端 `bun run i18n:sync`、`bun run typecheck`、`bun run build`，运行相关 Go 测试和 `git diff --check`，归档本次上游同步报告并更新本待办完成状态。
- [ ] 原分支保留为 `research/context-consensus-d3`；`stash@{0}` 是早期选择性同步工作，不要整体弹出，只有确认缺少某项改动时才按文件检查。

- [ ] 按 `doc/auto-smart-routing-context-consensus-design-2026-07-24.md` 实现阶段 A：四协议 `ContextEnvelope`、provider-bound 状态检测、工具图验证、精确预算接口和 `validate_only` 审计，不调用压缩模型。
- [ ] 阶段 A 完成后，实现阶段 B：请求内显式压缩、结构化共识摘要、协议安全重写和独立压缩计费。
- [ ] 阶段 B 完成后，实现阶段 C：Redis 加密托管共识、revision/CAS、lease、TTL 和 provider state 绑定映射。
- [ ] 如需继续推进，设计智能路由日志、指标聚合和后台配置页面。
- [ ] 如需继续推进，补充“渠道、上游适配器、模型、能力”业务关系图。
- [ ] 如需继续推进，补充 API Key 额度预扣、补扣和退款的计费链路时序图。
- [ ] 如需继续推进，检查后台页面是否需要增加渠道能力预览说明。
