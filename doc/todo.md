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
- [x] 完成 `ContextConsensus` 阶段 A：四协议 `ContextEnvelope`、provider-bound 状态检测、工具图验证、精确预算接口和 `validate_only` 安全审计，见 `doc/auto-smart-routing-context-consensus-stage-a-implementation-2026-07-24.md`。
- [x] 完成 `ContextConsensus` 阶段 B-1：三重授权快照、压缩配置边界、完整 turn 纯计划、严格共识摘要校验、四协议安全重写和网关请求头隔离，见 `doc/auto-smart-routing-context-consensus-stage-b1-implementation-2026-07-24.md`。
- [x] 完成 `ContextConsensus` 阶段 B-2a：统一最终请求不可变快照，覆盖四协议、pass-through 和 Chat-to-Responses，隔离跨候选重试元数据并保留显式零输出上限，见 `doc/auto-smart-routing-context-consensus-stage-b2a-implementation-2026-07-24.md`。
- [x] 完成 `ContextConsensus` 阶段 B-2b1：建立无 Gin/网络/数据库依赖的压缩子请求执行契约，提供独立 request ID、一次性状态机、结构化计费结果、失败退款语义和父子审计，见 `doc/auto-smart-routing-context-consensus-stage-b2b1-implementation-2026-07-24.md`。
- [x] 完成 `ContextConsensus` 阶段 B-2b2：在 controller 层实现隔离的真实非流式压缩子请求执行器，接入独立渠道选择、RelayInfo、BillingSession、计费快照、请求输入、响应校验和父子消费日志，见 `doc/auto-smart-routing-context-consensus-stage-b2b2-implementation-2026-07-24.md`。
- [x] 完成 `ContextConsensus` 阶段 B-2c1：建立绑定模型、渠道、协议和正文摘要的权威 token/上下文上限证据契约，新增压缩渠道白名单、严格上限配置和安全压缩提示构造器，见 `doc/auto-smart-routing-context-consensus-stage-b2c1-implementation-2026-07-24.md`。
- [x] 参考 OmniRoute 智能路由思路补充请求复杂度、任务匹配、上下文/缓存亲和、重置窗口、评分数据有效性、运行时健康隔离和会话稳定性；未引入外部配置体系，见 `doc/auto-smart-routing-omniroute-alignment-implementation-2026-07-28.md`。
- [x] 将智能路由延迟评分改为逐上游尝试的有效样本：流式使用首个上游响应数据 TTFT，非流式使用完整尝试耗时，并隔离重试、忽略无首包流式延迟，见 `doc/auto-smart-routing-omniroute-alignment-implementation-2026-07-28.md`。
- [x] 将智能路由吞吐评分改为按“渠道 + 模型”采集的权威流式文本 output token/s，过滤本地估算和非文本请求，达到有效样本门槛后在候选集合内相对归一化，见 `doc/auto-smart-routing-omniroute-alignment-implementation-2026-07-28.md`。
- [x] 新增智能路由成功消费日志指标聚合与管理员只读接口，包含日志版本、有效性校验、回退/评分/模型/渠道/健康状态和小时趋势，并限制为最大 7 天、250000 条匹配日志的有界查询；未增加外部配置或后台页面，见 `doc/auto-smart-routing-omniroute-alignment-implementation-2026-07-28.md`。

## 待办

### 上游同步重启接续

- [x] 创建专用分支 `sync/upstream-full-20260801`，以上游 `upstream/main` 的 `cfaba1dd6754d4238e1360247c198a64a313e96c` 为基线，采用“上游最新版作为底座，再重放本项目定制提交”的方式同步，避免每次完整拉取后反复删除模块。
- [x] 已完成前端目录从 `web/default` 迁移到扁平化 `web/`，删除 classic 前端，并排除 vendor、用户钱包、充值、支付、兑换码和订阅购买模块。
- [x] 已确认并恢复上游渠道账户余额查询；该功能属于渠道运维，不属于用户钱包模块。保留渠道余额列、单渠道查询、批量更新和 Codex 用量弹窗，同时叠加渠道价格倍率列。
- [x] 已重放并验证模型定价、渠道倍率计费、SQLite 数据迁移、累计用量重置和智能路由第一阶段选择。相关前端类型检查、i18n 同步、Compose 配置检查及定向 Go 测试已通过。
- [x] 已完成提交 `51193a87`（智能路由模型画像刷新）的重放，cherry-pick 结果为 `8f51426c`；`controller/relay.go` 导入冲突按上游 `relaykit/types` 迁移和本项目智能路由行为合并，智能路由相关测试已通过。
- [x] 已重放 `6ffd30d7`（可配置模型池）：落地 `smart_routing.virtual_model_pools` 后端配置、`auto:*` / `smart:*` 候选池过滤、前端 `Smart Routing` 入口与六语言 i18n；冲突保留上游 `relaykit/types` 迁移和本项目智能路由行为。
- [x] 已重放上下文共识提交 `d9287816` 至 `69881574` 的完整序列（13785e7f→73d846d1、0cbcabd8→282cc561、9f522b2a→a9d5c5be、7bfb37be→c2f838ae、38ae39f6→5850b24a、549d39ac→069c9577、44fb418b→06ac1bbe、ad105847→c32045ab、69881574→d4de8627）。
- [ ] 正在重放 `f7cbb392`（ContextConsensus managed lifecycle，冲突已解决、待提交），随后继续：`761834e0 c627a5b3 49f3c7cd 6516aa23 d09b95b5 5bae9bd3 3e83b42e eb88752c 732894e3 2d1c6466 86453b91 4b25be32 3a3569cd ba1f0329 c1aee3ae 574b3934 1c0bd4a5 67fe50b5`。
- [x] 重放期间完成上游 `relaykit` 迁移适配：将 `relay/helper/stream_scanner.go` 自动合并产生的双 `wg.Done()` 缺陷修正为独立 `defer wg.Done()`，并把新旧提交残留的根 `dto`/`types` 包引用（`GeneralOpenAIRequest`、`RelayFormat*`、`NewAPIError`、`PriceData` 等）统一迁移到 `relaykit/dto`、`relaykit/types`；`go build ./...`、`go vet ./...` 与受影响包测试均通过。
- [ ] 完成重放后统一审计：不得恢复 classic、vendor、用户钱包、充值、支付、兑换码、订阅购买及上游发布宣传模块；其他上游前后端更新均应保留，特别是由前端改动引发的后端接口和类型调整。
- [ ] 最终运行前端 `bun run i18n:sync`、`bun run typecheck`、`bun run build`，运行相关 Go 测试和 `git diff --check`，归档本次上游同步报告并更新本待办完成状态。
- [ ] 原分支保留为 `research/context-consensus-d3`；`stash@{0}` 是早期选择性同步工作，不要整体弹出，只有确认缺少某项改动时才按文件检查。

- [x] 完成 `ContextConsensus` 阶段 B-2c2a：抽取 OpenAI 与 Chat-to-Responses 无网络最终请求预准备边界，使用同一不可变正文快照执行发送，并增加一次性执行和 mode/path 生命周期保护，见 `doc/auto-smart-routing-context-consensus-stage-b2c2a-implementation-2026-07-24.md`。
- [x] 完成 `ContextConsensus` 阶段 B-2c2b1：将无网络预准备扩展到原生 Responses、Responses Compact、Claude 和 Gemini，保持各协议原有转换顺序并使用同一不可变正文快照发送，见 `doc/auto-smart-routing-context-consensus-stage-b2c2b1-implementation-2026-07-24.md`。
- [x] 完成 `ContextConsensus` 阶段 B-2c2b2：增加 adaptor 纯目标解析与离线转换显式能力门禁，封印最终模型、协议、路径和正文证据，并让未声明能力的 adaptor 在权威终检时失败关闭，见 `doc/auto-smart-routing-context-consensus-stage-b2c2b2-implementation-2026-07-24.md`。
- [x] 完成 `ContextConsensus` 阶段 B-2c2c：冻结完整候选集合，接入严格 tokenizer/上下文上限适配器，并完成单次压缩编排、请求正文与 DTO 原子提交、逐候选终检及主预扣前失败关闭；未支持的最终协议、模型和请求状态继续失败关闭，见 `doc/auto-smart-routing-context-consensus-stage-b2c2c-implementation-2026-07-28.md`。
- [x] 修复全包 `go test -race` 已暴露的既有竞态：收紧流扫描生命周期、消除任务轮询并发日志状态竞争，并隔离 Gin 全局模式与异步任务对象测试读写；同类 AWS、MiniMax 测试一并修复，见 `doc/auto-smart-routing-race-fixes-implementation-2026-07-28.md`。
- [x] 审计并迁移旧渠道输出上限字段：Baidu、Cloudflare、Cohere、Ollama、Xunfei 与 AWS Nova 统一保留字段缺失、显式 `0` 和新旧字段优先级；其余渠道按语义完成核查，见 `doc/auto-smart-routing-output-limit-pointer-migration-2026-07-28.md`。
- [x] 完成 `ContextConsensus` 阶段 C：Redis 加密托管共识、revision/CAS、lease、TTL、事务提交屏障和 provider state 绑定闭环。
  - [x] 完成阶段 C-1：owner/HMAC 隔离、AES-256-GCM、真实 Redis Lua 仓储、revision CAS、lease/fencing、TTL、provider binding 记录契约、网关头解析及失败关闭；未增加管理员接口或页面，见 `doc/auto-smart-routing-context-consensus-stage-c1-implementation-2026-07-28.md`。
  - [x] 完成阶段 C-2：在渠道选择前加载并安全注入托管摘要，接入 lease 续租、非流式响应缓冲、显式结算结果和 commit-before-write revision 提交屏障。
    - [x] 完成阶段 C-2a：实现托管会话 acquire/load/decrypt/renew/commit 状态机、四协议 user-level 安全摘要注入和有界非流式响应缓冲；尚未接入请求生命周期，门禁继续返回 503，见 `doc/auto-smart-routing-context-consensus-stage-c2a-implementation-2026-07-28.md`。
    - [x] 完成阶段 C-2b：冻结 managed 增量 current turn 契约，在渠道选择前接入会话加载、安全摘要注入和 lease 续租，并建立主调用规范化输出、显式结算及非流式缓冲执行边界；门禁继续返回 503，见 `doc/auto-smart-routing-context-consensus-stage-c2b-implementation-2026-07-29.md`。
    - [x] 完成阶段 C-2c：生成下一 revision L2/L3，接入固定 2 MiB 缓冲、同请求 CAS 恢复、commit-before-write 及故障矩阵，并移除非流式 503 门禁，见 `doc/auto-smart-routing-context-consensus-stage-c2c-implementation-2026-07-29.md`。
  - [x] 完成阶段 C-3：冻结稳定客户端幂等键和结算后提交失败的跨请求恢复契约，接入 adaptor 真实 provider state report、成功后登记、请求前绑定校验、精确凭据槽固定及 key rotation/Redis 故障矩阵。
    - [x] 完成阶段 C-3a：为托管会话 state 增加最多 4 个旧版本的 AEAD/HMAC 读取窗口，唯一定位 current/previous namespace，并在下一 revision CAS 时原子迁移到 active namespace；双 namespace 冲突失败关闭，见 `doc/auto-smart-routing-context-consensus-stage-c3a-implementation-2026-07-29.md`。
    - [x] 完成阶段 C-3b：建立托管请求的持久化计费去重与跨请求提交恢复闭环；不得仅靠 Redis phase 推断扣费结果。
      - [x] 完成阶段 C-3b1：为主调用与摘要子调用增加数据库持久化计费 operation，原子处理 API Key 额度、用户/渠道统计、冻结价格结果及消费日志 outbox；支持 active/previous key 定位迁移和独立 SQL 日志库，ClickHouse 托管计费失败关闭，见 `doc/auto-smart-routing-context-consensus-stage-c3b1-implementation-2026-07-29.md`。
      - [x] 完成阶段 C-3b2：增加稳定客户端幂等键、revision intent、`settled_pending_commit`/`committed` outcome 和已提交响应回放，闭合上游成功但客户端重试时的跨请求恢复，见 `doc/auto-smart-routing-context-consensus-stage-c3b2-implementation-2026-07-29.md`。
    - [x] 完成阶段 C-3c：接入 adaptor 真实 provider state report，首批仅闭环原生非流式 OpenAI Responses `id -> previous_response_id`，并原子提交 binding、固定最终模型/协议/渠道/精确凭据槽和 credential fingerprint；其余 provider state 继续失败关闭，见 `doc/auto-smart-routing-context-consensus-stage-c3c-implementation-2026-07-30.md`。
- [ ] 完成 `ContextConsensus` 阶段 D：工具结果安全压缩、provider file 生命周期和聚合可视化。
  - [x] 完成阶段 D-1：首批 OpenAI Chat Completions 单工具串行原子段压缩和聚合诊断。
    - [x] 完成阶段 D-1a：补齐 call/result 协议与因果序号证据，建立只含 digest 的结构资格评估；现有工具压缩硬门禁保持不变，见 `doc/auto-smart-routing-context-consensus-stage-d1a-implementation-2026-07-30.md`。
    - [x] 完成阶段 D-1b：实现服务端构造、sanitizer/policy/tool/schema 版本精确绑定且默认拒绝的 JSON Pointer 白名单脱敏注册表；拒绝未知字段、重复键、敏感值和超限结构，仅输出带进程内完整性证明的有界标量投影，见 `doc/auto-smart-routing-context-consensus-stage-d1b-implementation-2026-07-30.md`。
    - [x] 完成阶段 D-1c：新增 Summary v2，闭合单个旧串行 Chat 工具四消息原子段的 plan、prompt、rewrite、内部执行器版本绑定和最终 OpenAI Chat 协议门禁；开关默认关闭，编译期策略表默认空且未登记工具只压缩其之前的完整普通轮次，见 `doc/auto-smart-routing-context-consensus-stage-d1c-implementation-2026-07-30.md`。
    - [x] 完成阶段 D-1d：增加有限 reason code 聚合诊断 API 和后台页面，只持久化版本、资格状态和有限原因，禁止展示原始工具状态、会话内容、digest 或摘要正文，见 `doc/auto-smart-routing-context-consensus-stage-d1d-implementation-2026-07-31.md`。
  - [ ] 完成阶段 D-2：统一并行 group 语义后评估稳定 ID 协议的并行工具组；Gemini 同名并行继续失败关闭。
  - [ ] 完成阶段 D-3：具备 adaptor 权威所有权、到期和删除能力后，实现 provider file 生命周期。
- [ ] 如需继续推进，补充“渠道、上游适配器、模型、能力”业务关系图。
- [ ] 如需继续推进，补充 API Key 额度预扣、补扣和退款的计费链路时序图。
- [ ] 如需继续推进，检查后台页面是否需要增加渠道能力预览说明。
