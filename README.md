<div align="center">

![new-api-person](/web/public/logo.png)

# New API Person

🍥 **基于 New API 调整的个人版 · 新一代大模型网关与 AI 资产管理系统**

<p align="center">
  <strong>简体中文</strong> |
  <a href="./README.zh_TW.md">繁體中文</a> |
  <a href="./README.en.md">English</a> |
  <a href="./README.fr.md">Français</a> |
  <a href="./README.ja.md">日本語</a>
</p>

<p align="center">
  <a href="#-快速开始">快速开始</a> •
  <a href="#-主要特性">主要特性</a> •
  <a href="#-部署">部署</a> •
  <a href="#-文档">文档</a> •
  <a href="#-帮助支持">帮助</a>
</p>

</div>

## 📝 项目说明

**New API Person** 是基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 调整的个人版分发。它继承了上游 New API 的全部能力——将 40+ 上游 AI 服务商（OpenAI、Claude、Gemini、Azure、AWS Bedrock 等）聚合在统一 API 之后，并附带用户管理、计费、限流与管理后台。本仓库在上游基础上做面向个人使用场景的调整与维护，并非官方版本。

> [!IMPORTANT]
> - 本项目仅面向合法授权的 AI API 网关、组织内部鉴权、多模型管理、用量统计、成本核算和私有化部署场景。
> - 使用者须合法获取上游 API 密钥、账号、模型服务及接口权限，并遵守上游服务条款与适用法律法规。
> - 向公众提供生成式 AI 服务时，使用者应遵守所在司法管辖区的监管要求，完成所需的备案、许可、内容安全、实名认证、日志留存、税务及上游授权等义务。

---

## 🙏 特别感谢

<p align="center">
  <a href="https://www.jetbrains.com/?from=new-api" target="_blank">
    <img src="https://resources.jetbrains.com/storage/products/company/brand/logos/jb_beam.png" alt="JetBrains Logo" width="120" />
  </a>
</p>

<p align="center">
  <strong>感谢 <a href="https://www.jetbrains.com/?from=new-api">JetBrains</a> 为上游项目提供的免费开源开发授权</strong>
</p>

<p align="center">
  <strong>感谢 <a href="https://github.com/farion1231/cc-switch">cc-switch</a> 提供的 Responses↔Chat 工具调用兼容方案思路（custom 工具包装、namespace 展平、协议降级）</strong>
</p>

---

## 🚀 快速开始

### 使用 Docker Compose（推荐）

```bash
# 克隆项目
git clone https://github.com/wheesys/new-api-person.git
cd new-api-person

# 复制并编辑环境变量配置
cp .env.example .env
nano .env  # 设置 SESSION_SECRET 和 REDIS_PASSWORD

# 启动服务
docker compose up -d
```

<details>
<summary><strong>使用 Docker 命令</strong></summary>

```bash
# 拉取最新镜像
docker pull walllee/new-api-person:latest

# 使用 SQLite（默认）
docker run --name new-api-person -d --restart always \
  -p 3000:3000 \
  -e TZ=Asia/Shanghai \
  -v ./data:/data \
  walllee/new-api-person:latest

# 使用 MySQL
docker run --name new-api-person -d --restart always \
  -p 3000:3000 \
  -e SQL_DSN="root:123456@tcp(localhost:3306)/oneapi" \
  -e TZ=Asia/Shanghai \
  -v ./data:/data \
  walllee/new-api-person:latest
```

> **💡 提示：** `-v ./data:/data` 会将数据保存在当前目录的 `data` 文件夹，也可改为绝对路径如 `-v /your/custom/path:/data`

</details>

---

🎉 部署完成后，访问 `http://localhost:3000` 即可开始使用！

> [!WARNING]
> 将本项目作为公开生成式 AI 服务或 API 转售服务运营时，应先完成所需的备案、许可、内容安全、实名认证、日志留存、税务、支付及上游授权等义务。

📖 更多部署方式请参考[部署指南](https://docs.newapi.pro/en/docs/installation)

---

## 📚 文档

<div align="center">

### 📖 [官方文档](https://docs.newapi.pro/en/docs)

</div>

**快速导航：**

| 分类 | 链接 |
|------|------|
| 🚀 部署指南 | [安装文档](https://docs.newapi.pro/en/docs/installation) |
| ⚙️ 环境配置 | [环境变量](https://docs.newapi.pro/en/docs/installation/config-maintenance/environment-variables) |
| 📡 API 文档 | [API 文档](https://docs.newapi.pro/en/docs/api) |
| ❓ 常见问题 | [FAQ](https://docs.newapi.pro/en/docs/support/faq) |
| 💬 社区交流 | [交流渠道](https://docs.newapi.pro/en/docs/support/community-interaction) |

---

## ✨ 主要特性

> 详细特性请参考[特性介绍](https://docs.newapi.pro/en/docs/guide/wiki/basic-concepts/features-introduction)

### 🎨 核心功能

| 特性 | 说明 |
|------|------|
| 🎨 全新界面 | 现代化 UI 设计 |
| 🌍 多语言 | 支持简体中文、繁体中文、英文、法文、日文 |
| 🔄 数据兼容 | 完全兼容原 One API 数据库 |
| 📈 数据看板 | 可视化控制台与统计分析 |
| 🔒 权限管理 | Token 分组、模型限制、用户管理 |

### 💰 授权用量计费

- ✅ 合法授权场景下的内部充值与额度分配（EPay、Stripe）
- ✅ 组织级的按请求、按用量、按缓存命中计费
- ✅ OpenAI、Azure、DeepSeek、Claude、Qwen 及支持模型的缓存计费统计
- ✅ 面向内部管理或授权企业客户的灵活计费策略

### 🔐 授权与安全

- 😈 Discord 授权登录
- 🤖 LinuxDO 授权登录
- 📱 Telegram 授权登录
- 🔑 OIDC 统一认证
- 🔍 Key 额度查询（配合 [new-api-key-tool](https://github.com/Calcium-Ion/new-api-key-tool)）

### 🚀 高级特性

**API 格式支持：**
- ⚡ [OpenAI Responses](https://docs.newapi.pro/en/docs/api/ai-model/chat/openai/create-response)
- ⚡ [OpenAI Realtime API](https://docs.newapi.pro/en/docs/api/ai-model/realtime/create-realtime-session)（含 Azure）
- ⚡ [Claude Messages](https://docs.newapi.pro/en/docs/api/ai-model/chat/create-message)
- ⚡ [Google Gemini](https://doc.newapi.pro/en/api/google-gemini-chat)
- 🔄 [Rerank 模型](https://docs.newapi.pro/en/docs/api/ai-model/rerank/create-rerank)（Cohere、Jina）

**智能路由：**
- ⚖️ 渠道加权随机
- 🔄 失败自动重试
- 🧭 显式 `auto:*` / `smart:*` 虚拟模型，真实模型池由管理员配置
- 🧩 面向 OpenAI Chat Completions、OpenAI Responses、Claude Messages、Google Gemini 的协议感知上下文校验
- 🔒 绑定上游的上下文会阻止不安全的虚拟模型切换，并禁用显式模型的跨渠道智能重试
- 🚦 用户级模型限流
- 🛡️ 渠道/模型级熔断，路由时跳过熔断中的渠道并在渠道列表展示运行时健康状态

**格式转换：**
- 🔄 **OpenAI 兼容 ⇄ Claude Messages**
- 🔄 **OpenAI 兼容 → Google Gemini**
- 🔄 **Google Gemini → OpenAI 兼容**（仅文本，暂不支持函数调用）
- 🚧 **OpenAI 兼容 ⇄ OpenAI Responses**（开发中）
- 🛠️ **Responses ⇄ Chat Completions** 工具调用兼容（GLM / DeepSeek / Codex）
- 🔄 **思考转内容功能**

**推理强度支持：**

<details>
<summary>查看详细配置</summary>

**OpenAI 系列模型：**
- `o3-mini-high` - 高推理强度
- `o3-mini-medium` - 中推理强度
- `o3-mini-low` - 低推理强度
- `gpt-5-high` - 高推理强度
- `gpt-5-medium` - 中推理强度
- `gpt-5-low` - 低推理强度

**Claude 思考模型：**
- `claude-3-7-sonnet-20250219-thinking` - 启用思考模式

**Google Gemini 系列模型：**
- `gemini-2.5-flash-thinking` - 启用思考模式
- `gemini-2.5-flash-nothinking` - 关闭思考模式
- `gemini-2.5-pro-thinking` - 启用思考模式
- `gemini-2.5-pro-thinking-128` - 启用思考模式，思考预算 128 tokens
- 也可在任意 Gemini 模型名后追加 `-low`、`-medium`、`-high` 以请求对应推理强度（无需额外的 thinking-budget 后缀）。

</details>

---

## 🤖 模型支持

> 详情请参考 [API 文档 - 网关接口](https://docs.newapi.pro/en/docs/api)

| 模型类型 | 说明 | 文档 |
|---------|------|------|
| 🤖 OpenAI 兼容 | OpenAI 兼容模型 | [文档](https://docs.newapi.pro/en/docs/api/ai-model/chat/openai/createchatcompletion) |
| 🤖 OpenAI Responses | OpenAI Responses 格式 | [文档](https://docs.newapi.pro/en/docs/api/ai-model/chat/openai/createresponse) |
| 🎨 Midjourney-Proxy | [Midjourney-Proxy(Plus)](https://github.com/novicezk/midjourney-proxy) | [文档](https://doc.newapi.pro/api/midjourney-proxy-image) |
| 🎵 Suno-API | [Suno API](https://github.com/Suno-API/Suno-API) | [文档](https://doc.newapi.pro/api/suno-music) |
| 🔄 Rerank | Cohere、Jina | [文档](https://docs.newapi.pro/en/docs/api/ai-model/rerank/creatererank) |
| 💬 Claude | Messages 格式 | [文档](https://docs.newapi.pro/en/docs/api/ai-model/chat/createmessage) |
| 🌐 Gemini | Google Gemini 格式 | [文档](https://docs.newapi.pro/en/docs/api/ai-model/chat/gemini/geminirelayv1beta) |
| 🔧 Dify | ChatFlow 模式 | - |
| 🎯 自定义上游 | 支持配置合法授权的上游端点 | - |

### 📡 支持接口

<details>
<summary>查看完整接口列表</summary>

- [对话接口（Chat Completions）](https://docs.newapi.pro/en/docs/api/ai-model/chat/openai/createchatcompletion)
- [响应接口（Responses）](https://docs.newapi.pro/en/docs/api/ai-model/chat/openai/createresponse)
- [图像接口（Image）](https://docs.newapi.pro/en/docs/api/ai-model/images/openai/post-v1-images-generations)
- [音频接口（Audio）](https://docs.newapi.pro/en/docs/api/ai-model/audio/openai/create-transcription)
- [视频接口（Video）](https://docs.newapi.pro/en/docs/api/ai-model/audio/openai/createspeech)
- [向量接口（Embeddings）](https://docs.newapi.pro/en/docs/api/ai-model/embeddings/createembedding)
- [重排接口（Rerank）](https://docs.newapi.pro/en/docs/api/ai-model/rerank/creatererank)
- [实时对话（Realtime）](https://docs.newapi.pro/en/docs/api/ai-model/realtime/createrealtimesession)
- [Claude 对话](https://docs.newapi.pro/en/docs/api/ai-model/chat/createmessage)
- [Google Gemini 对话](https://docs.newapi.pro/en/docs/api/ai-model/chat/gemini/geminirelayv1beta)

</details>

---

## 🚢 部署

> [!TIP]
> **最新 Docker 镜像：** `walllee/new-api-person:latest`

### 📋 部署要求

| 组件 | 要求 |
|------|------|
| **本地数据库** | SQLite（Docker 需挂载 `/data` 目录）|
| **远程数据库** | MySQL ≥ 5.7.8 或 PostgreSQL ≥ 9.6 |
| **容器引擎** | Docker / Docker Compose |
| **系统架构** | 仅支持 64 位（amd64 / arm64），不支持 32 位系统 |

### ⚙️ 环境变量配置

<details>
<summary>常用环境变量配置</summary>

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `SESSION_SECRET` | 鉴权签名密钥，每个节点必须一致 | - |
| `SESSION_COOKIE_SECURE` | `false`/未配置时关闭本地 HTTP 开发代理的 refresh/logout OriginGuard；`true` 启用 Secure Cookie 和严格 Origin 校验 | `false` |
| `SESSION_COOKIE_TRUSTED_URL` | Secure 模式下必填：逗号分隔的允许调用 refresh/logout 的精确 HTTPS Origin；非 relay CORS 白名单 | - |
| `TRUSTED_PROXIES` | 未配置/留空时信任回环、RFC 1918 和 IPv6 ULA 并打印启动告警；`none` 不信任任何代理；显式 IP/CIDR 列表替代默认值 | `127.0.0.0/8, ::1, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7` |
| `USER_SESSION_ACTIVE_LIMIT` | 每用户最大活跃登录 Session 数 | `50` |
| `USER_SESSION_ISSUANCE_LIMIT` | 签发窗口内每用户可创建的 Session 总数（含已撤销） | `100` |
| `USER_SESSION_ISSUANCE_WINDOW_SECONDS` | 每用户 Session 签发窗口；大于撤销保留期时自动钳制 | `86400` |
| `USER_SESSION_REVOKED_RETENTION_DAYS` | 已撤销 Session 的审计保留天数 | `7` |
| `USER_SESSION_HOURLY_ALERT_THRESHOLD` | 全局每小时 Session 签发量超过此值时记录告警，不会拒绝登录 | `5000` |
| `CRYPTO_SECRET` | 缓存键 HMAC 密钥；共享 Redis 的节点须使用相同有效值 | 默认取 `SESSION_SECRET` |
| `SQL_DSN` | 数据库连接字符串 | - |
| `REDIS_CONN_STRING` | Redis 连接字符串 | - |
| `CONTEXT_CONSENSUS_ENCRYPTION_KEY` | 托管 ContextConsensus 状态的严格 Base64 编码 32 字节活跃密钥 | - |
| `CONTEXT_CONSENSUS_ENCRYPTION_KEY_VERSION` | 当前托管 ContextConsensus 密钥的唯一版本标签 | - |
| `CONTEXT_CONSENSUS_PREVIOUS_ENCRYPTION_KEYS` | 轮换期间保留的最多 4 个只读历史密钥 JSON 数组（`version` 和严格 Base64 `key`） | - |
| `RELAY_IDLE_CONN_TIMEOUT` | Relay HTTP 客户端空闲保活超时（秒），默认跟随 Go 标准库，设 `0` 关闭 | `90` |
| `STREAMING_TIMEOUT` | 流式超时（秒） | `300` |
| `STREAM_SCANNER_MAX_BUFFER_MB` | 流扫描器每行最大缓冲（MB），上游发送大图/base64 时可调大 | `64` |
| `MAX_REQUEST_BODY_MB` | 最大请求体（MB，按**解压后**计算，防止超大请求/zip 炸弹耗尽内存），超出返回 `413` | `32` |
| `AZURE_DEFAULT_API_VERSION` | Azure API 版本 | `2025-04-01-preview` |
| `ERROR_LOG_ENABLED` | 错误日志开关 | `false` |
| `PYROSCOPE_URL` | Pyroscope 服务地址 | - |
| `PYROSCOPE_APP_NAME` | Pyroscope 应用名 | `new-api` |
| `PYROSCOPE_BASIC_AUTH_USER` | Pyroscope Basic Auth 用户 | - |
| `PYROSCOPE_BASIC_AUTH_PASSWORD` | Pyroscope Basic Auth 密码 | - |
| `PYROSCOPE_MUTEX_RATE` | Pyroscope 互斥锁采样率 | `5` |
| `PYROSCOPE_BLOCK_RATE` | Pyroscope 阻塞采样率 | `5` |
| `HOSTNAME` | Pyroscope 的主机名标签 | `new-api` |

📖 **完整配置：** [环境变量文档](https://docs.newapi.pro/en/docs/installation/config-maintenance/environment-variables)

</details>

托管的非流式 ContextConsensus 请求须发送 `X-New-Api-Context-Mode: managed`、`X-New-Api-Context-Id`、`X-New-Api-Context-Revision` 和唯一的 `X-New-Api-Context-Idempotency-Key`。幂等键须为 16-128 个 ASCII 字符，取自 `A-Z`、`a-z`、`0-9`、`.`、`_`、`~`、`-`；重试须复用同一密钥与请求意图。

托管 OpenAI 文件默认关闭。启用时需选择使用单一 API Key 的官方 OpenAI 渠道，并设置文件生命周期与删除选项。使用 `POST /v1/files` 上传，`purpose=user_data`，一个 `file` part，以及唯一的 `X-New-Api-File-Idempotency-Key`；用 `GET /v1/files/{file-id}` 获取元数据。原生 `POST /v1/responses` 请求可在 `input_file.file_id` 中使用返回的 ID。文件到期自动删除，不提供文件列表、内容下载和客户端主动删除。

### 🔧 部署方式

<details>
<summary><strong>方式一：Docker Compose（推荐）</strong></summary>

```bash
# 克隆项目
git clone https://github.com/wheesys/new-api-person.git
cd new-api-person

# 编辑配置
cp .env.example .env
nano .env

# 启动服务
docker compose up -d
```

</details>

<details>
<summary><strong>方式二：Docker 命令</strong></summary>

**使用 SQLite：**
```bash
docker run --name new-api-person -d --restart always \
  -p 3000:3000 \
  -e TZ=Asia/Shanghai \
  -v ./data:/data \
  walllee/new-api-person:latest
```

**使用 MySQL：**
```bash
docker run --name new-api-person -d --restart always \
  -p 3000:3000 \
  -e SQL_DSN="root:123456@tcp(localhost:3306)/oneapi" \
  -e TZ=Asia/Shanghai \
  -v ./data:/data \
  walllee/new-api-person:latest
```

> **💡 路径说明：**
> - `./data:/data` - 相对路径，数据保存在当前目录的 data 文件夹
> - 也可使用绝对路径，如：`/your/custom/path:/data`

</details>

### ⚠️ 多机部署注意事项

> [!WARNING]
> - 所有节点必须使用同一主数据库和同一 `SESSION_SECRET`，否则 Access Token、刷新会话和临时鉴权流程无法一致校验。
> - 连接同一 Redis 的节点还须使用同一 `CRYPTO_SECRET`，否则缓存键摘要不一致，共享条目无法复用。

数据库是登录 Session 和每用户活跃/签发限额的权威来源。Redis Session 条目是短时缓存，TTL 跟随 `SYNC_FREQUENCY`（默认 60 秒），且不超过 Session 剩余生命周期。

| Redis 拓扑 | Session 传播 | 限流 |
| --- | --- | --- |
| 共享 Redis | 撤销和版本发布通常立即传播 | Redis 限额跨节点共享 |
| 每节点独立 Redis | 节点在有效 `SYNC_FREQUENCY` 内从数据库收敛；新轮换 Token 在缓存过期节点上可能临时 401 | 每节点独立额度，总容量约为配置限额乘以节点数 |
| 无 Redis | 每次 Session 校验读取数据库 | 内存限额每节点独立 |

缩短 `SYNC_FREQUENCY` 可减小独立 Redis 的过期窗口，但会增加每活跃 SID、每节点、每 TTL 一次主键 Session 查询。上述保证使 Session 鉴权在支持的拓扑内有界过期；限流及其他 Redis 支撑的控制面缓存仍随拓扑变化。

Token、Origin 校验和 PAT 契约详见[用户认证与登录会话](./docs/authentication.md)。

### 🔄 渠道重试与缓存

**重试配置：** `设置 → 运营设置 → 通用设置 → 失败重试次数`

**缓存配置：**
- `REDIS_CONN_STRING`：Redis 缓存（推荐）
- `MEMORY_CACHE_ENABLED`：内存缓存

---

## 🔗 相关项目

### 上游项目

| 项目 | 说明 |
|------|------|
| [QuantumNous/new-api](https://github.com/QuantumNous/new-api) | 本个人版的直接上游项目 |
| [One API](https://github.com/songquanpeng/one-api) | 经由直接上游继承的原始项目基础 |
| [Midjourney-Proxy](https://github.com/novicezk/midjourney-proxy) | Midjourney 接口支持 |

### 配套工具

| 项目 | 说明 |
|------|------|
| [new-api-key-tool](https://github.com/Calcium-Ion/new-api-key-tool) | Key 额度查询工具 |
| [new-api-horizon](https://github.com/Calcium-Ion/new-api-horizon) | New API 高性能优化版 |

---

## 💬 帮助支持

### 📖 文档资源

| 资源 | 链接 |
|------|------|
| 📘 常见问题 | [FAQ](https://docs.newapi.pro/en/docs/support/faq) |
| 💬 社区交流 | [交流渠道](https://docs.newapi.pro/en/docs/support/community-interaction) |
| 🐛 问题反馈 | [问题反馈](https://docs.newapi.pro/en/docs/support/feedback-issues) |
| 📚 完整文档 | [官方文档](https://docs.newapi.pro/en/docs) |

### 🤝 贡献指南

欢迎各种形式的贡献！

- 🐛 报告 Bug
- 💡 提出新特性
- 📝 改进文档
- 🔧 提交代码

---

## 📜 许可证

AGPLv3 © 2026 QuantumNous and contributors; modifications © 2026 wheesys

本项目基于 [GNU Affero General Public License v3.0 (AGPLv3)](./LICENSE) 授权，原始许可文本不变地保存在 `LICENSE` 中；项目声明、署名及分发相关修改记录维护在 `NOTICE` 中。

本仓库是 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 的修改分发，上游项目本身基于 [One API](https://github.com/songquanpeng/one-api)（MIT License）开发。AGPLv3 第 7 条附加条款适用：修改版本须在相应法律声明及界面中显著的关于、法律、页脚或署名位置保留作者署名声明 `Frontend design and development by New API contributors.`，并保留指向原始项目的可见链接：<https://github.com/QuantumNous/new-api>。

Docker 镜像及其他目标码分发应可通过 Git tag、release 或 commit 追溯到本仓库对应源码，发布镜像元数据应标识源仓库、修订版本及 AGPLv3 许可证。

<div align="center">

### 💖 感谢使用 New API Person

如果本项目对你有帮助，欢迎给个 ⭐️ Star！

**[官方文档](https://docs.newapi.pro/en/docs)** • **[问题反馈](https://github.com/wheesys/new-api-person/issues)** • **[最新发布](https://github.com/wheesys/new-api-person/releases)**

</div>
