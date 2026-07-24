# 智能路由虚拟模型可配置模型池实施报告

日期：2026-07-10

## 背景

根据 `doc/auto-smart-routing-configurable-model-pool-2026-07-09.md`，阶段一智能路由已具备 `auto:*` / `smart:*` 虚拟模型、候选评分、失败重试候选序列和模型画像缓存。本轮完成管理员可配置虚拟模型真实模型池，避免虚拟模型选到不符合运营策略的真实模型。

## 完成内容

- 新增后端配置模块 `setting/model_setting/smart_routing.go`。
- 新增 Option key：`smart_routing.virtual_model_pools`。
- 复用现有 `Option` / `setting/config` 体系，不新增数据库表。
- 支持以下虚拟模型池配置：
  - `auto:cheap`
  - `auto:balanced`
  - `auto:quality`
  - `auto:fast`
  - `auto:reasoning`
- `smart:*` 会归一到对应 `auto:*` 模型池，例如 `smart:quality` 使用 `auto:quality`。
- 后端保存入口会校验 JSON 结构和受支持的 `auto:*` 配置键，拒绝无效配置落库。
- 配置池拒绝 `null`、空白模型名和非数组值，避免非空限制静默退化为无限制路由。
- 运行时区分“未配置/合法空数组”和“历史无效配置”；历史 `null` 或纯空白池按无候选处理，不会放行池外模型。
- 请求路径通过不可变原子快照读取模型池；配置管理器在加载或热更新后统一刷新快照，避免在线保存与并发路由产生数据竞争。
- 虚拟模型候选排序前按配置池过滤；未配置、缺失 key 或空数组保持原有候选集合。
- 普通模型请求不读取虚拟模型池，仍只在同模型渠道之间评分排序。
- 过滤后无候选时返回包含 `configured model pool` 的清晰错误，不回退到池外模型。
- 只有实际应用非空配置池后候选为空才返回配置池错误；未配置时保留通用无候选错误。
- 默认前端模型设置页新增 `Smart Routing` 配置入口，支持编辑 `smart_routing.virtual_model_pools` JSON。
- 通用设置保存 mutation 在后端返回 `success: false` 时会抛出错误，表单不会把失败保存误记为新基线。
- 补齐默认前端六语言 i18n：`en`、`zh`、`fr`、`ja`、`ru`、`vi`。

## 关键文件

- 后端配置：`setting/model_setting/smart_routing.go`
- 后端配置测试：`setting/model_setting/smart_routing_test.go`
- 分发过滤：`middleware/smart_routing.go`
- 分发回归测试：`middleware/distributor_smart_routing_test.go`
- 前端入口：`web/default/src/features/system-settings/models/smart-routing-settings-section.tsx`
- 前端分区注册：`web/default/src/features/system-settings/models/section-registry.tsx`
- 前端类型与默认值：
  - `web/default/src/features/system-settings/types.ts`
  - `web/default/src/features/system-settings/models/index.tsx`
- 前端 i18n：
  - `web/default/src/i18n/static-keys.ts`
  - `web/default/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json`

## 配置格式

```json
{
  "auto:cheap": ["gpt-5-mini", "gemini-2.5-flash"],
  "auto:balanced": ["gpt-5-mini", "claude-sonnet-4"],
  "auto:quality": ["gpt-5", "claude-opus-4"],
  "auto:fast": ["gemini-2.5-flash"],
  "auto:reasoning": ["gpt-5", "o3"]
}
```

空对象、空数组或缺失虚拟模型 key 表示沿用当前智能路由候选集合。

## 验证记录

- `GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp go test ./... -count=1`：通过。
- `GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp go test -race ./setting/config ./setting/model_setting ./middleware -count=1`：通过。
- `web/default` 下执行 `bun test`：35 项通过。
- `web/default` 下执行 `bun run typecheck`：通过。
- `web/default` 下执行 `bun run build`：通过。
- `web/default` 下执行本次改动文件 targeted `oxlint`：通过。
- `web/default` 下执行 `bun run i18n:sync`：通过，报告中各语言 `missingCount`、`extrasCount`、`untranslatedCount` 均为 0。

## 已知情况

- `web/default` 全仓库 `bun run lint` 仍存在既有无关 lint error，本轮未处理这些历史问题。
- 本轮未新增数据库表，避免触碰当前 `Option` 字符串主键历史结构。

## 后续待办

- 设计跨模型 `ContextConsensus` 会话共识、自动压缩和工具状态保持方案。
- 设计智能路由日志、指标聚合和后台配置页面。
- 补充“渠道、上游适配器、模型、能力”业务关系图。
- 补充 API Key 额度预扣、补扣和退款的计费链路时序图。
- 检查后台页面是否需要增加渠道能力预览说明。
