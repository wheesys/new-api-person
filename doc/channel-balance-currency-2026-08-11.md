# 渠道计价币种（balance_currency）实施记录

日期：2026-08-11

## 背景

渠道余额刷新后展示值不对：DeepSeek 渠道返回的余额本身就是人民币（CNY），但前端把所有渠道余额统一当作 USD，再用全局 `usdExchangeRate`（美元→人民币）乘一遍展示，导致 DeepSeek 余额被多乘一次汇率（例如 10 元显示成 ¥70）。

根因不在后端：`controller/channel-billing.go` 的 `updateChannelDeepSeekBalance` 取到的是 CNY 原值，没做任何换算。问题在前端展示层——它对所有渠道一视同仁地按 USD 换算。

## 方案

不做按渠道类型的特判（不可扩展），而是给渠道增加一个「计价币种」字段，前端按「计价币种 → 展示币种」汇率换算。支持 USD、CNY 两种，枚举留扩展位。

### 1. 后端：Channel 增加 BalanceCurrency

`model/channel.go`：

- `Channel` 新增 `BalanceCurrency string`，`gorm:"type:varchar(16);default:'USD'"`，带 COMMENT。GORM AutoMigrate 自动加列，SQLite/MySQL/PG 全兼容；存量渠道默认 USD，行为与旧实现一致。
- `UpdateBalance(balance float64, currency string)` 改签名，同时持久化 `balance_currency` 并同步内存字段。

`controller/channel-billing.go`：各 `updateChannelXxxBalance` 在写余额时声明该 provider 返回的币种——**DeepSeek = "CNY"**，其余（OpenAI 系、Moonshot、SiliconFlow、OpenRouter 等）= "USD"。Moonshot 在后端已把 CNY 除以 `PriceRatio` 换算成 USD 存储，故仍标 USD。

「刷新校正」语义：余额刷新时由 provider 权威声明币种并重新盖章，覆盖用户可能的手误。新增人民币渠道只需在对应余额函数里传 "CNY"。

`UpdateChannelBalance` 接口响应新增 `balance_currency` 字段，前端 toast/弹窗刷新后能立即用对币种。

### 2. 前端：表单可编辑 + 类型默认 + 刷新校正

- `types.ts` / `lib/channel-form.ts`：`balance_currency` 字段（zod 枚举 USD/CNY，默认 USD），含表单加载、默认值、新增/复制两处序列化。
- `components/drawers/channel-mutate-drawer.tsx`：
  - 新建渠道选 DeepSeek 类型时自动默认 CNY（`CHANNEL_TYPE_DEEPSEEK = 43`）；编辑态不覆盖用户已存值。
  - 价格倍率旁新增「Balance Currency」币种选择器（USD/CNY），可手动覆盖。
- `lib/channel-actions.ts`：`handleUpdateChannelBalance` 第二参改为 `balanceCurrency`；toast 优先用接口返回的 `balance_currency`，回退到渠道字段。
- `components/dialogs/balance-query-dialog.tsx`：余额查询弹窗按 `balance_currency` 展示；刷新成功后同步更新 `currentRow.balance_currency`。

### 3. 前端：展示换算

`lib/channel-utils.ts` 的 `formatBalance(balanceCurrency, balance, options?)`：先把余额按计价币种归一到 USD（CNY ÷ 全局 `usdExchangeRate`），再交给原有 `formatCurrencyFromUSD` 管线。这样 USD/CNY/TOKENS/CUSTOM 四种展示模式全部自动正确，无需为每种展示分支单独处理。

三处展示点统一改为传 `channel.balance_currency`：

- `components/channels-columns.tsx` 表格余额单元格（精确 + compact 两个分支）、`handleUpdateChannelBalance` 调用；
- 余额查询弹窗；
- 更新成功 toast。

### 效果

- DeepSeek 余额 10：展示 CNY = ¥10，展示 USD = $1.43。
- OpenAI 余额 10：展示 CNY = ¥70，展示 USD = $10。
- 完全对称、可扩展。

## 关键文件

- `model/channel.go` — `BalanceCurrency` 字段、`UpdateBalance` 签名
- `controller/channel-billing.go` — 各余额函数声明币种、接口响应带回币种
- `web/src/features/channels/types.ts`、`lib/channel-form.ts`、`lib/channel-utils.ts`、`lib/channel-actions.ts`
- `web/src/features/channels/components/drawers/channel-mutate-drawer.tsx` — 类型默认 + 币种选择器
- `web/src/features/channels/components/channels-columns.tsx`、`components/dialogs/balance-query-dialog.tsx` — 展示点

## 验证

- `go build ./...`、`go vet ./model/... ./controller/...` 通过。
- `bunx tsgo -b` 前端类型检查通过。

## 备注

- 存量 DeepSeek 渠道 DB 里 `balance_currency` 仍是默认的 "USD"，首次显示会偏小一次；点一次「更新余额」即被后端盖成 "CNY"，或在编辑表单手动选 CNY 保存。新建 DeepSeek 渠道直接默认 CNY。
- 新增英文 i18n key（`Balance Currency`、`Select currency`、`Currency of the upstream balance...` 等），下次 `bun run i18n:sync` 补全各语言翻译。
