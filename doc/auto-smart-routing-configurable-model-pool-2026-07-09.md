# 智能路由虚拟模型可配置模型池设计

日期：2026-07-09

## 背景

阶段一智能路由已经支持 `auto:*` / `smart:*` 虚拟模型、候选评分、失败重试候选序列和模型画像缓存。当前虚拟模型的模型选择主要依赖内置策略、保守能力推断和画像评分。下一步需要让管理员显式配置每个虚拟模型允许参与路由的真实模型池，避免 `auto:quality`、`auto:cheap` 等虚拟模型选到不符合运营策略的模型。

## 目标

- 为 `auto:cheap`、`auto:balanced`、`auto:quality`、`auto:fast`、`auto:reasoning` 提供管理员可配置真实模型池。
- `smart:*` 继续兼容映射到对应 `auto:*` 配置，例如 `smart:quality` 使用 `auto:quality` 的模型池。
- 未配置模型池时保持当前行为不变，继续按现有候选评分和保守画像工作。
- 普通模型请求不受该模型池影响，仍然只能在同模型渠道之间排序。
- 配置不记录请求正文、API Key、token、secret，不参与日志敏感信息输出。

## 非目标

- 本轮不新增数据库表。
- 本轮不做跨模型会话共识、上下文压缩或工具状态保持。
- 本轮不强制实现完整后台 UI；后端配置闭环完成后，再在模型设置页增加可视化编辑入口。

## 配置位置

优先复用现有 `Option` / `setting/config` 配置体系，而不是新增业务表。

建议新增后端配置模块：

- 文件：`setting/model_setting/smart_routing.go`
- 注册名：`smart_routing`
- Option key：`smart_routing.virtual_model_pools`

建议 JSON 结构：

```json
{
  "auto:cheap": ["gpt-5-mini", "gemini-2.5-flash"],
  "auto:balanced": ["gpt-5-mini", "claude-sonnet-4"],
  "auto:quality": ["gpt-5", "claude-opus-4"],
  "auto:fast": ["gemini-2.5-flash"],
  "auto:reasoning": ["gpt-5", "o3"]
}
```

空数组或缺失 key 表示该虚拟模型不启用管理员模型池限制，沿用当前候选集合。

## 路由行为

1. `ResolveVirtualModel()` 继续负责 `auto` / `smart` 前缀归一化。
2. 虚拟模型请求进入候选排序前，根据归一化后的虚拟模型名读取模型池。
3. 如果模型池非空，只保留 `candidate.ModelName` 在池内的候选。
4. 如果过滤后没有候选，记录清晰拒绝原因，并按现有错误处理返回无可用渠道，不回退到池外模型。
5. 普通模型请求不读取虚拟模型池。

## 测试计划

- `auto:quality` 配置模型池只包含 `gpt-5` 时，`claude-sonnet-4` 候选被过滤。
- `smart:quality` 使用 `auto:quality` 的同一模型池。
- 缺失配置或空数组时，现有候选排序结果不变。
- 模型池过滤后候选为空时，返回可解释的拒绝原因。
- 普通模型请求不受虚拟模型池影响。

## 后续 UI

后端闭环完成后，在默认前端系统设置的模型设置页增加一个 `Smart Routing` 小节：

- 用 JSON 编辑器或可视化多选编辑 `smart_routing.virtual_model_pools`。
- 模型选项优先来自现有模型管理元数据和渠道可用模型。
- 所有新增前端文本必须使用 i18n。

