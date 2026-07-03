# 项目待办事项

更新时间：2026-07-03

## 已完成

- [x] 梳理项目模块、渠道、供应商、模型、计费、日志等核心业务关系，并归档到 `doc/project-module-business-relationships.md`。
- [x] 补充模型基础固定价格 `ModelPrice` 的页面配置入口、默认值来源、保存位置和计费公式说明。
- [x] 补充直接上游 `QuantumNous/new-api` 的 AGPLv3 归属、Section 7 附加条款和 Docker/源码追溯说明。
- [x] 将 README 底部 Star History 统计图和 GitHub issues/releases 链接调整为当前项目仓库。
- [x] 将 README 顶部项目状态徽章调整为当前项目仓库，并将上游宣传徽章标注为上游项目徽章。
- [x] 移除 README 顶部项目状态徽章和上游宣传徽章。
- [x] 移除 README 中残留的 DeepWiki 徽章和 Star History 图表徽章。
- [x] 移除默认前端模型资产供应商模块和后端 `/api/vendors/*` 管理入口，保留数据库兼容层。
- [x] 将模型定价入口提升到模型管理顶层 `Pricing / 定价` 页签，并将模型基础固定价改为跟随模型自身 `base_price` 设置。

## 待办

- [ ] 如需继续推进，补充“渠道、上游适配器、模型、能力”业务关系图。
- [ ] 如需继续推进，补充 API 调用计费链路时序图。
- [ ] 如需继续推进，检查后台页面是否需要增加渠道能力预览说明。
