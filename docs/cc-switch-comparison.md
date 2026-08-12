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
| 请求级自动降级 | ⚠️ 协议层自动降级有；渠道级 failover 无 | ✅ 优先级队列换渠道 | **待办#6 立项** |
| 熔断器（Open/HalfOpen） | ❌ | ✅ | 待办#6 |
| 超时触发换渠道 | ❌ | ✅（非流式/首字节超时） | 待办#6 |
| 失败阈值/健康追踪 | ⚠️ 有渠道健康度，无熔断阈值 | ✅ 持久化健康表 | 待办#6 |
| 重试 | ⚠️ 部分重试参数，非自动换渠道 | ✅ 至 max_attempts | 待办#6 |

## 渠道 / Provider 管理

| 能力 | new-api-person | cc-switch |
|---|---|---|
| 支持渠道/Provider 数 | 57 种渠道类型，40+ 上游 | 主流（Claude/Codex/Gemini/Grok/OpenCode 等） |
| 渠道优先级 | ✅ 按分组/优先级调度 | ✅ 按 P1→P2 队列 |
| 多 Key 轮换 | ✅ | ⚠️ 单 Key 为主 |
| 计费/倍率/限流 | ✅（完整计费体系） | ❌（无计费） |
| 模型映射 | ✅（模型→模型、渠道→模型） | ⚠️ 模型目录注入 |
| 管理后台 | ✅（Web 后台） | ✅（桌面 UI） |

## 其它能力

| 能力 | new-api-person | cc-switch |
|---|---|---|
| 用户/Token/分组管理 | ✅ | ❌ |
| 消费日志/报表 | ✅ | ❌ |
| MCP 管理 | ⚠️ 仅协议内部引用，无 MCP 客户端管理 | ✅（mcp_servers 同步） |
| 配置同步（claude/codex config） | ❌（服务端无本地配置概念） | ✅（核心功能） |
| WebAuthn/Passkey/OAuth 登录 | ✅ | ❌ |
| 多数据库（SQLite/MySQL/PG） | ✅ | 本地文件 |

## 可借鉴结论（待办#6）

1. **请求级降级/重试/熔断**：选中的渠道失败后自动换下一个健康渠道、连续失败熔断（Open/HalfOpen）、超时触发换渠道。本项目当前单次尝试直接返回。是最大可借鉴点。
2. **按网关裁剪工具**：已借鉴（本次缺口3），可扩展为按渠道/网关白名单更细粒度。
3. **MCP 配置同步**：若未来支持给 Codex/Claude Code 客户端同步 MCP，可参考 cc-switch 的 `mcp_servers` 同步实现。

## 不适合借鉴的部分

- 本地代理进程架构：本项目是服务端网关，协议转换已在 relay 层内置，无需本地代理。
- 桌面 UI / 本地配置文件同步：与网关定位不符。
