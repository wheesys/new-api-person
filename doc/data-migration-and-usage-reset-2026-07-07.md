# 数据迁移版本与用量清空实现说明

日期：2026-07-07

## 背景

旧版 new-api 数据库可能缺少当前项目新增的派生数据和字段，例如 `channels.price_ratio`、`abilities` 路由能力、`models` 模型元数据。直接在每次启动时扫描旧库特征成本不稳定，因此改为使用 `options.DataMigrationVersion` 记录一次性数据迁移版本。

用户管理页此前默认前端主列突出展示 `users.quota` 剩余额度。当前业务链路已经以 API Key 限额与用户 `used_quota` 统计为主，因此用户管理列表改为展示用户已用额度。

## 迁移机制

启动数据库 `AutoMigrate` 完成后执行 `RunDataMigrations()`：

- 读取 `options` 表中的 `DataMigrationVersion`。
- 如果版本号已经达到当前版本，直接跳过，后续启动只产生一次主键查询成本。
- 如果版本号缺失或低于当前版本，按版本顺序执行迁移。
- 每个版本迁移在事务中执行，成功后写入对应版本号。

当前版本：

```text
2026070701
```

## 旧库补齐内容

版本 `2026070701` 执行以下非破坏性补齐：

- 将旧渠道中 `channels.price_ratio IS NULL` 的行补为 `1`。
- 如果 `abilities` 表为空，则扫描已有 `channels.models` 和 `channels.group`，按现有渠道重建能力路由。
- 从 `abilities.model` 与 `channels.models` 收集已有渠道支持的模型名，补齐缺失的 `models` 元数据。
- 不覆盖已有模型配置、价格、渠道配置、模型映射或密钥。

## 清空累计用量

新增 Root 级接口：

```text
POST /api/user/reset_usage
```

该接口用于用户管理页的“清空使用量”按钮，行为如下：

- 先 flush 一次批量更新队列，避免旧队列在清空后回写累计值。
- 清空内存 `CacheQuotaData`。
- 将所有用户的 `used_quota` 和 `request_count` 归零。
- 将所有令牌的 `used_quota` 归零。
- 删除 `quota_data` 聚合统计。
- 删除 API 调用相关日志：消费、退款、错误请求。
- 保留用户剩余额度 `quota`、令牌剩余额度 `remain_quota`、渠道密钥、账号设置、登录日志、管理审计日志和充值日志。

清空操作会写入管理审计日志 `user.usage_reset`。

## 前端调整

- 默认前端用户管理页的额度列从 `Quota` 改为 `Used Quota`，主展示 `used_quota`。
- Tooltip 保留已用额度、剩余额度、总额度和请求次数。
- Root 用户在用户管理页顶部可见“清空使用量”按钮。
- 点击按钮后需要二次确认，确认文案说明不会改变剩余额度和账号设置。

## 验证

已新增后端回归测试覆盖：

- 旧渠道迁移会补齐 `price_ratio`、`abilities` 和 `models` 元数据，并写入数据迁移版本。
- 数据迁移版本已是最新时跳过旧库扫描。
- 清空累计用量不会清空用户余额和令牌剩余额度，只清累计用量、统计和 API 使用日志。
