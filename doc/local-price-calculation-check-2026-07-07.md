# 本地请求价格计算检查报告

检查时间：2026-07-07

## 检查范围

- 本地 SQLite：`data/local/new-api-local.db`
- 当前运行日志：`logs/oneapi-20260707133851.log`
- 检查对象：`claude-opus-4-8` 的本地消费日志与渠道倍率计费

## 数据概况

- 本地库已初始化：`users=1`、`setups=1`
- 消费日志：6 条，日志 ID 为 `4` 到 `9`
- 渠道：`id=1`，名称 `socheap claude cconly`
- 渠道表当前倍率：`channels.price_ratio=0.25`
- 消费日志记录倍率：6 条日志的 `other.channel_ratio` 均为 `1`

## 计费公式核对

当前日志是 Anthropic 语义，按代码路径 `service/text_quota.go` 的文本计费公式重算：

```text
quota = round(
  (
    prompt_tokens
    + cache_tokens * cache_ratio
    + cache_creation_tokens_5m * cache_creation_ratio_5m
    + completion_tokens * completion_ratio
  )
  * model_ratio
  * group_ratio
  * channel_ratio
)
```

日志中 `model_ratio=2.5`、`completion_ratio=5`、`group_ratio=1`、`cache_ratio=0.1`、`cache_creation_ratio_5m=1.25`。

使用日志记录的 `channel_ratio=1` 重算，结果与日志 `quota` 完全一致，说明日志与实际扣费链路一致。

| 日志 ID | 日志扣费 | 按日志倍率重算 | 如果应用渠道倍率 0.25 |
| --- | ---: | ---: | ---: |
| 4 | 1990 | 1990 | 498 |
| 5 | 212694 | 212694 | 53173 |
| 6 | 291239 | 291239 | 72810 |
| 7 | 33435 | 33435 | 8359 |
| 8 | 103616 | 103616 | 25904 |
| 9 | 41795 | 41795 | 10449 |

汇总：

- 当前实际扣费：`684769`
- 如果应用渠道倍率 `0.25`：`171193`
- 差异：`513576`

用户、Token、渠道的已用额度均与实际日志扣费一致：

- `users.used_quota=684769`
- `tokens.used_quota=684769`
- `channels.used_quota=684769`

## 结论

本地请求价格计算不正常：当前请求实际扣费没有应用渠道表中的 `price_ratio=0.25`，而是按 `channel_ratio=1` 计费。

从日志看，计费公式自身执行一致；异常点是渠道倍率没有进入 `PriceData`。表现为：

- `channels.price_ratio=0.25`
- 消费日志 `other.channel_ratio=1`
- 应用日志补扣金额与 `channel_ratio=1` 的 quota 一致

检查过程中新增的日志 ID `9` 仍记录 `channel_ratio=1`，因此该问题不是历史请求在修改渠道倍率前产生的差异。

## 初步定位

普通文本请求在 `controller/relay.go` 中先执行 `helper.ModelPriceHelper(...)` 计算价格并预扣费，然后才进入 retry/channel 循环。

虽然 `/v1/messages` 路由会经过 `middleware.Distribute()`，且该中间件会调用 `SetupContextForSelectedChannel()` 写入 `ContextKeyChannelPriceRatio`，但本地实际日志显示 `PriceData` 中仍为 `channel_ratio=1`。

## 修复记录

修复点：`relay/helper/price.go`。

根因：文本请求在 `controller.Relay` 中调用 `ModelPriceHelper` 时，`relayInfo.ChannelMeta` 尚未由具体 relay handler 初始化；此时 `info.GetChannelPriceRatio()` 回退为 `1`。分发中间件已经把渠道倍率写入 Gin 上下文，但价格 helper 没有在 `ChannelMeta` 为空时读取该上下文值。

修复方式：

- 保留已有 `ChannelMeta.ChannelPriceRatioSet` 优先级。
- 当 `ChannelMeta` 尚未初始化时，从 `ContextKeyChannelPriceRatio` 读取分发阶段写入的渠道倍率。
- 仅调整文本请求使用的 `ModelPriceHelper`，不扩大到按次任务计费路径。

回归测试：

- `TestModelPriceHelperUsesContextChannelRatioBeforeChannelMetaInit`

本地状态：

- 已重启本地服务，当前使用项目内 SQLite：`data/local/new-api-local.db`
- 本地地址：`http://127.0.0.1:3000`
- 无凭证状态接口 `/api/setup` 响应正常

后续需要继续排查：

- 重新发起一条 `/v1/messages` 请求，确认新消费日志中的 `other.channel_ratio` 变为渠道表倍率。

## 修复后复查

复查时间：2026-07-07

复查范围：

- 新增消费日志：ID `10` 到 `24`
- 渠道：`id=1`，`channels.price_ratio=0.25`
- 请求路径：`/v1/messages`

复查结果：

- 15 条新增消费日志的 `other.channel_ratio` 均为 `0.25`
- 按日志中的 token、缓存、模型倍率、分组倍率、渠道倍率复算，15 条日志逐条与 `quota` 一致
- 新增日志实际扣费合计：`657262`
- 按 `channel_ratio=0.25` 复算合计：`657262`
- 差异合计：`0`

结论：修复后新增本地请求的渠道倍率已进入计费链路，当前价格计算正常。
