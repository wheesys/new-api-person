# 本地启动方案

更新时间：2026-07-07

## 默认约定

- 如无特别说明，启动服务统一使用本地单端口模式。
- 默认端口是 `3000`。`13000` 等其他端口不是项目默认值，只能通过 `PORT=<port>` 显式指定。
- 不默认启动 Docker，不默认启动前端 dev server。
- 只有用户明确要求前端热更新、调试前端源码变更，才使用双端口热更新模式。

## 方案一：本地单端口启动（默认）

适用场景：

- 验证后端接口和当前已构建前端。
- 用户只说“启动服务”“本地启动”“我要测试”等，没有额外说明。
- 希望一个地址同时访问前端页面和 API。

访问地址：

```bash
http://127.0.0.1:3000/
```

启动命令：

```bash
mkdir -p data/local

GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp \
PORT=3000 \
SQLITE_PATH='./data/local/new-api-local.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)' \
SESSION_SECRET=local-test-session-secret \
GIN_MODE=debug \
go run main.go
```

如果需要使用 `13000` 端口，必须显式指定：

```bash
mkdir -p data/local

GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp \
PORT=13000 \
SQLITE_PATH='./data/local/new-api-local.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)' \
SESSION_SECRET=local-test-session-secret \
GIN_MODE=debug \
go run main.go
```

注意：

- 单端口模式由 Go 后端直接服务 `web/default/dist` 静态资源。
- 如果修改了前端源码，需要先重新构建 `web/default/dist`，再重启后端。
- 本地 SQLite 示例路径放在项目内 `data/local/`，用于保留初始化用户和本地配置。
- `data/` 已在 `.gitignore` 中忽略，本地数据库、WAL、journal 等持久化文件不会随提交泄露。

前端重新构建命令：

```bash
cd web
bun install --filter ./default
cd default
bun run build
```

## 方案二：本地双端口热更新启动

适用场景：

- 明确需要前端热更新。
- 正在调试 `web/default/src` 下的前端源码。
- 需要 Rsbuild dev server 的 HMR 能力。

访问地址：

```bash
http://127.0.0.1:5173/
```

后端仍监听：

```bash
http://127.0.0.1:3000/
```

终端一：启动本地后端。

```bash
mkdir -p data/local

GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp \
PORT=3000 \
SQLITE_PATH='./data/local/new-api-local.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)' \
SESSION_SECRET=local-test-session-secret \
GIN_MODE=debug \
go run main.go
```

终端二：启动默认前端 dev server，并代理 API 到本地后端。

```bash
cd web/default
VITE_REACT_APP_SERVER_URL=http://127.0.0.1:3000 \
bun run dev -- --host 127.0.0.1 --port 5173
```

注意：

- 双端口模式访问 `5173`，不是访问 `3000`。
- `5173` 只用于前端热更新；API 请求通过 Rsbuild proxy 转发到 `3000`。
- 完成热更新测试后，如需回到默认方式，应停止 `5173` 前端 dev server，仅保留单端口后端。
