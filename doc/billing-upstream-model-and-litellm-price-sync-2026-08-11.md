# 计费按实际上游模型 + LiteLLM 社区价格同步实施记录

日期：2026-08-11

## 背景

本项目为个人自用中转站。原有两处问题：

1. **计费口径**：`relay/helper/price.go` 中所有查价都使用 `OriginModelName`（用户请求时声明的模型名）。当渠道配置模型映射后，实际发给上游的是 `UpstreamModelName`，计费仍按映射前的名字计算，导致扣费与实际成本不符。
2. **默认价格偏高且手工维护**：`setting/ratio_setting/model_ratio.go` 中的默认倍率表为手写静态值，定价偏高且不随官方调价更新。

## 方案

### 1. 计费按实际上游模型

新增统一解析函数 `RelayInfo.BillingModelName()`（`relay/common/relay_info.go`）：

- 发生模型映射（`IsModelMapped && UpstreamModelName != ""`）时返回 `UpstreamModelName`；
- 否则返回 `OriginModelName`。未映射时两者相等，行为与旧实现完全一致；
- `ChannelMeta` 缺失（如单测）时视为未映射，回退 `OriginModelName`。

所有查价入口统一改用它，保证预扣、结算、音频二次查价、工具附加费同一口径：

- `relay/helper/price.go`：`ModelPriceHelper`、`ModelPriceHelperPerCall`、`modelPriceHelperTiered` 的全部查价 key（含 tiered 计费审计字段 `BillingSnapshot.ModelName`）。查价流程、37.5 兜底、未配价报错逻辑均保留。
- `service/quota.go`：音频/ws/realtime 结算的二次查价 key（`PostAudioConsumeQuota`、`PostWssConsumeQuota`、`PreWssConsumeQuota`）。
- `relay/common/tool_usage.go`：工具附加费查价 key。

文本主结算路径复用 `PriceData` 快照，无需改动。日志展示不变：`logs.model_name` 仍写 `OriginModelName`（用户按请求名搜索/展示），`other.upstream_model_name` 按现有 `IsModelMapped` 条件写入。

### 2. 模型/定价列表加入渠道映射上游模型

`model/model_sync.go` 新增 `collectChannelMappingTargetModels` / `extractMappingTargets` / `EnsureChannelMappingTargetModels`，收集渠道 `model_mapping` 的映射目标（上游模型）纳入 `models` 表，使其出现在模型管理/定价列表，便于为这些上游模型配置价格。触发时机：

- 启动时 `EnsureDefaultOptionModels`（含 `main.go` 初始化）；
- `controller/channel.go` 的 `AddChannel`、`UpdateChannel` 保存渠道后。

### 3. LiteLLM 社区价格同步

新增 `service/litellm_price_sync.go` 后台任务（`main.go` 启动），数据源为 LiteLLM 维护的
`model_prices_and_context_window.json`（拉取失败回退 `..._backup.json`）。

价格优先级（用户指定）：**页面手填 > LiteLLM 社区默认 > 代码默认表**。

- 换算：`modelRatio = input_cost_per_token(美元/token) * 500_000`。依据 `model_ratio.go` 中 `USD=500`、`1 ratio = $0.002/1K tokens = $2/1M tokens`，故 `ratio = ($/1M) / 2 = input_cost_per_token * 1e6 / 2`。
- 合并策略：仅覆盖「当前值等于代码默认值 或 当前无配置」的模型，**绝不覆盖管理员手填价格**（当前值 ≠ 默认值）。代码默认表 `defaultModelRatio` 保留作第 3 层底表。
- 仅同步按 token 计费的聊天/补全模型；跳过图片/音频按像素/秒计费的产品，以及带 provider 前缀（如 `azure/`、`vertex_ai/`）的条目。
- 执行：仅 master 节点；首次启动立即执行，之后按 `LITELLM_PRICE_SYNC_INTERVAL`（分钟，默认 1440 = 24h）周期重拉；通过 `model.UpdateOption("ModelRatio", ...)` 落库（`FirstOrCreate` + `Save`，兼容 SQLite/MySQL/PG）。

## 关键文件

- `relay/common/relay_info.go` — 新增 `BillingModelName()`
- `relay/helper/price.go` — 查价 key 切换
- `service/quota.go`、`relay/common/tool_usage.go` — 结算/工具附加费查价 key 切换
- `model/model_sync.go`、`controller/channel.go` — 映射上游模型进模型/定价列表
- `service/litellm_price_sync.go`（新增）、`main.go` — LiteLLM 价格同步任务

## 验证

- `go build ./...` 与 `go vet` 通过。
- 新增单测：`TestRelayInfoBillingModelName`（nil / 未映射 / 映射 / 空上游回退）、`TestComputeLitellmModelRatioMerge`（覆盖默认、不覆盖手填、跳过非 token 与 provider 前缀）。
- 回归：`relay/helper`、`relay/common`、`controller` 全绿。

## 备注

`service`、`model` 包各有 2~3 个 `ON CONFLICT (billing_operation_id)` 测试失败，为改动前已存在的问题（`model/log.go:411` 的 `ON CONFLICT` 依赖 `logs.billing_operation_id` 列 UNIQUE 索引，测试 schema 缺失所致），与本次改动无关。
