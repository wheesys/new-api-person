# 项目待办事项

更新时间：2026-07-04

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

## 待办

- [ ] 如需继续推进，补充“渠道、上游适配器、模型、能力”业务关系图。
- [ ] 如需继续推进，补充 API Key 额度预扣、补扣和退款的计费链路时序图。
- [ ] 如需继续推进，检查后台页面是否需要增加渠道能力预览说明。
