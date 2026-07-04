# 后台页面点击验证阶段进度

更新时间：2026-07-04 23:43（Asia/Shanghai）

## 背景

本阶段目标是使用管理员账号登录本地 classic 前端，逐项点击后台核心页面，确认以下近期改动没有引入页面渲染错误：

- 移除钱包、充值、支付网关、兑换码管理、模型部署等入口。
- 移除模型资产供应商模块和 `/api/vendors/*` 前端调用。
- 在 classic 渠道新增/编辑弹窗中补齐渠道倍率字段，并允许设置为 `0`。
- 修复数据看板打开后 VChart 报 `createCanvas` / `filter` 运行时错误的问题。

## 已完成改动

- classic 渠道新增/编辑弹窗已补齐 `price_ratio` 输入项，默认值为 `1`，允许输入 `0`，提交前会拦截负数。
- classic 模型管理页已移除供应商筛选、供应商列、供应商管理弹窗和 `/api/vendors` 调用。
- classic 模型新增弹窗已移除供应商字段。
- classic Dashboard 图表初始化已移除 `@visactor/vchart-semi-theme` 监听初始化。
- classic Rsbuild 已为 VChart 1.8 的运行时依赖增加 scoped alias，将 `@visactor/vrender-*`、`vdataset`、`vscale`、`vutils` 等固定到 `web/classic/node_modules/@visactor/vchart/node_modules/@visactor` 下，避免 classic 的 VChart 1.8 与顶层 web 的 VChart 2.0 / VRender 1.0 混用。

## 已完成验证

- `web/classic` 执行 `bun run build` 通过。
- `web/classic` 对本轮 classic JS/JSX 改动执行 `bunx eslint` 通过。
- `rsbuild.config.ts` 不纳入当前 classic ESLint 结果：该配置文件已有 TypeScript `as const` 语法，classic 当前 ESLint 解析配置会报 `Unexpected token as`，本次以 `bun run build` 验证 Rsbuild 配置有效性。
- 本地服务曾确认加载新 classic 静态资源：
  - `static/js/index.213bfa2db1.js`
  - `static/js/3998.c5c2e234d1.js`
  - `static/css/3998.48a22731f5.css`
- 在修复 VChart alias 之前，Playwright 已验证：
  - `/console/channel` 能打开。
  - `/console/models` 能打开。
  - `/pricing` 能打开。
  - 渠道新增弹窗能打开，并能看到渠道倍率字段。
  - 模型新增弹窗能打开，未发现供应商字段。
  - 未捕获 `/api/vendors` 请求。
  - 未看到钱包管理、兑换码管理、模型部署等已移除菜单文本。
- 2026-07-04 使用临时 SQLite 数据库和 Playwright 重新完成管理员登录后的 classic 后台点击验证：
  - 验证环境：`http://127.0.0.1:18080`，视口 `1440x980`，临时库 `/private/tmp/new-api-admin-click.db`。
  - 覆盖页面：`/console`、`/console/channel`、`/console/models`、`/pricing`。
  - `/console` 数据看板未出现 `createCanvas` / `filter` 控制台错误，未捕获 `pageerror`。
  - 所有覆盖页面均非空白，未出现前端框架错误覆盖层，未出现 HTTP 4xx/5xx 响应。
  - 渠道新增弹窗打开正常，展开高级设置后可见 `Channel Price Ratio / 渠道倍率` 字段，并成功输入 `0`。
  - 模型新增弹窗打开正常，可见 `Model Name / 模型名称` 字段，未发现 `Provider / 供应商` 字段。
  - 全流程未捕获 `/api/vendors` 请求。
  - 截图证据保存于本机临时目录 `/private/tmp/new-api-admin-click/`。
- 最终验证命令已重跑通过：
  - `web/classic` 执行 `bun run build` 通过。
  - 仓库根目录执行 `GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp go test ./...` 通过。

## 未完成验证

- 渠道编辑弹窗的倍率字段尚未在本地空渠道数据下验证；本轮遵守不额外修改数据库数据的规则，未创建测试渠道。

## 下一步建议

1. 按仓库规则提交当前文档更新，并在 `git commit` 后立即执行 `git push`。
2. 如需补齐渠道编辑弹窗验证，需要使用已有渠道数据，或在得到明确允许后向临时库创建测试渠道。
