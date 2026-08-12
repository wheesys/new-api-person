# 功能对比：new-api-person vs cc-switch

对比日期：2026-08-12
对象：本项目（new-api-person，基于 QuantumNous/new-api 的 AI 网关服务端） vs [cc-switch](https://github.com/farion1231/cc-switch)（桌面端 Rust 客户端，本地代理）。

## 定位差异（根本不同）

| 维度 | new-api-person | cc-switch |
|---|---|---|
| 形态 | 服务端网关（Go/Gin） | 桌面客户端（Tauri/Rust 本地代理） |
| 承载角色 | 多用户、多渠道聚合、计费、限流 | 单机切换 Claude Code/Codex 等客户端配置 |
| 配置落点 | 中心化数据库 + 管理后台 | 本地 `~/.claude`、`~/.codex` 等配置文件 |
| 协议转换 | 服务端 relay 层内置转换器 | 本地代理进程逐请求转换 |

## 协议转换

| 能力 | new-api-person | cc-switch | 说明 |
|---|---|---|---|
| Responses↔Chat | ✅ 内置转换器（relaykit） | ✅ 本地代理 | 本项目已内置，本次新增自动降级接线 |
| Chat→Claude | ✅ | ✅ | |
| Claude→Chat | ✅ | ✅ | |
| Chat→Gemini / Gemini→Chat | ✅ | ✅ | |
| Responses→Gemini | ✅ | ✅ | |
| Responses→Claude | ✅ | ✅ | |
| 工具调用兼容（custom 包装、namespace 展平） | ✅（本次新增） | ✅ | 借鉴 cc-switch 思路实现 |
| 按上游格式裁剪工具 | ✅（本次新增，MiniMax 等） | ✅（`CodexCatalogToolProfile`） | 本项目按渠道类型回调裁剪 |

## 高可用 / 可靠性

| 能力 | new-api-person | cc-switch | 结论 |
|---|---|---|---|
| 请求级自动降级 | ⚠️ 协议层自动降级有；渠道级 failover 无 | ✅ 优先级队列换渠道 | 协议降级已完成；渠道级 failover 未做 |
| 熔断器（Open/HalfOpen） | ✅ 进程内 runtime_health（EWMA+连续失败+HalfOpen） | ✅ 内存+DB 持久化 | 复用进程内；持久化不做（单/少实例足够） |
| 熔断消费覆盖 | ✅ 智能路由 + 普通路径（本次补） | ✅ | 修复了"只写不读"，普通路径也会跳过 Open 渠道 |
| 超时触发换渠道 | ❌ | ✅（非流式/首字节超时） | 未做 |
| 失败阈值/健康追踪 | ✅ 有渠道健康度 | ✅ 持久化健康表 | |
| 重试 | ✅ 请求级换渠道重试（RetryTimes 后台可配） | ✅ 至 max_attempts | RetryTimes 默认 0，需后台开启 |

## 功能差距补充（本次详细对比）

从 cc-switch 完整功能清单对比，与本项目定位相关的差距：

| 功能 | cc-switch | 本项目 | 结论 |
|---|---|---|---|
| Provider 健康监控/端点速度测试 | ✅ 端点测速+可视化 | ❌ 无测速看板 | **可借鉴，已立项** |
| 会话管理 | ✅ | ⚠️ 有 task/会话，维度不同 | 定位差异 |
| 配置导入/导出 | ✅ | ⚠️ 有，未必全 | 可核对 |

## 可借鉴结论

1. **请求级降级/重试/熔断**：部分完成。熔断已接入普通路径（跳过 Open 渠道）；请求级换渠道重试已有（RetryTimes 后台可配）。未做：超时触发换渠道、渠道级 failover 优先级队列。
2. **按网关裁剪工具**：已借鉴（缺口3），可扩展为按渠道/网关白名单更细粒度。
3. **Provider 健康监控/端点测速**：新增候选待办。cc-switch 给每个 provider 做端点速度测试并可视化；本项目有自动禁用+熔断，但缺主动测速/健康看板。

## 不适合借鉴的部分

- 本地代理进程架构：本项目是服务端网关，协议转换已在 relay 层内置，无需本地代理。
- 桌面 UI / 本地配置文件同步：与网关定位不符。
- **MCP 配置同步**：cc-switch 把 MCP server 集中存 SQLite 再同步到 Claude Code/Codex/Gemini 各自的本地配置文件（格式各不同）。本项目是服务端网关，不运行本地 CLI，无"同步到客户端配置"需求，故不适用。
- **Skills 管理**：Claude Code 的 skill 分发，客户端侧，网关不适用。
- **云同步/深链接/系统托盘/终端集成**：桌面应用特性，网关无此概念。
