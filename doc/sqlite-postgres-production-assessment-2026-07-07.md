# SQLite 与 PostgreSQL 生产使用差异评估

评估时间：2026-07-07

## 评估范围

- 数据库初始化与迁移：`model/main.go`、`common/database.go`、`common/init.go`
- 部署配置：`Dockerfile`、`docker-compose.yml`、`README.md`
- 写入热点：消费日志、额度更新、渠道用量、系统任务、性能指标
- 当前本地 SQLite 样本：`data/local/new-api-local.db`

## 总体结论

当前项目代码层面已经按 SQLite、MySQL、PostgreSQL 三库兼容设计；如果单实例、小到中等流量生产部署，SQLite 可以跑，不需要做大规模业务代码改造。

真正的差异主要在生产运行约束：

- PostgreSQL 适合多实例、高并发写入、远程托管、独立备份恢复和更强事务隔离。
- SQLite 适合单实例、低运维、低到中等写入量；必须把数据库文件放到持久化存储，并控制连接池、日志写入和备份方式。

如果目标是把当前项目改成“SQLite 可作为正式生产选项”，建议以配置和少量启动代码增强为主，不建议改业务模型或迁移体系。

## 当前项目里的关键差异

### 1. 数据库选择方式

代码路径：

- `common.SQLitePath` 默认值：`one-api.db?_busy_timeout=30000`
- `SQLITE_PATH` 环境变量会覆盖默认 SQLite 路径
- `SQL_DSN` 为空时使用 SQLite
- `SQL_DSN` 以 `postgres://` 或 `postgresql://` 开头时使用 PostgreSQL
- Docker 镜像最终 `WORKDIR /data`

影响：

- Docker 命令模式不传 `SQL_DSN` 且挂载 `/data` 时，SQLite 文件默认落在 `/data/one-api.db...`。
- 当前 `docker-compose.yml` 默认显式设置了 PostgreSQL `SQL_DSN`，因此 Compose 模式不会使用 SQLite。

### 2. SQLite DSN 参数需要调整

当前默认值使用：

```text
one-api.db?_busy_timeout=30000
```

但当前依赖 `github.com/glebarez/sqlite` / `github.com/glebarez/go-sqlite` 的本地源码说明，PRAGMA 参数应使用 `_pragma=...` 形式。例如：

```text
one-api.db?_pragma=busy_timeout(30000)
```

驱动本身会默认执行 `PRAGMA BUSY_TIMEOUT(5000)`，所以现在不是完全没有超时保护，但项目默认写法的 `30000` 很可能没有被该驱动按预期读取。

生产 SQLite 建议改为显式配置：

```text
SQLITE_PATH=/data/one-api.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)
```

注意：在 shell、Compose、systemd 中需要整体加引号，避免 `&` 被解释。

### 3. 连接池默认值偏向远程数据库

当前 `model.InitDB()` 对所有主库默认：

```text
SQL_MAX_IDLE_CONNS=100
SQL_MAX_OPEN_CONNS=1000
SQL_MAX_LIFETIME=60
```

这对 PostgreSQL 合理，但 SQLite 是单文件单写者模型。生产 SQLite 建议：

```text
SQL_MAX_OPEN_CONNS=1
SQL_MAX_IDLE_CONNS=1
```

如果启用 WAL 并压测确认没有 `database is locked`，可以再谨慎提高连接数；默认先用 1 更稳。

### 4. 写入热点在 SQLite 上更敏感

当前项目每个请求可能涉及多次写入：

- `logs`：消费日志，`LogConsumeEnabled` 默认开启
- `users`：余额、已用额度、请求次数
- `tokens`：API Key 已用额度和请求次数
- `channels`：渠道已用额度
- `quota_data` / `perf_metrics`：统计与性能指标
- `system_tasks` / `system_task_locks`：后台任务调度与租约

PostgreSQL 可以同时承载更多并发写事务；SQLite 会串行化写入。流量越高，请求日志越容易成为 SQLite 写入瓶颈。

生产 SQLite 建议：

- 低流量可以保留 `LogConsumeEnabled=true`。
- 中等以上流量建议关闭或定期清理消费日志。
- 如果必须保留大量日志，优先考虑 `LOG_SQL_DSN` 单独指向 ClickHouse 或 PostgreSQL，而不是把所有日志写入同一个 SQLite 文件。

### 5. 行级锁语义不同

代码中多处使用 `FOR UPDATE` 类查询保护充值、兑换码、订阅和用户额度相关流程。PostgreSQL 支持行级锁；SQLite 没有同等行级锁语义，事务会落到数据库文件级写锁。

影响：

- 单实例、低并发下通常可接受。
- 高并发支付回调、兑换码、订阅扣费场景更适合 PostgreSQL。
- 如果生产 SQLite 必须承载这些场景，应配合单实例、单连接池和幂等业务逻辑。

### 6. 迁移体系已做 SQLite 兼容

项目已经有显式 SQLite 兼容逻辑：

- `subscription_plans` 在 SQLite 下用手写建表和 `ALTER TABLE ADD COLUMN` 维护
- `model_limits` 迁移在 SQLite 下跳过，因为 TEXT/VARCHAR 类型亲和性足够
- `price_amount` 从 float 迁移到 decimal 的 `ALTER COLUMN` 在 SQLite 下跳过
- 保留字列名 `group`、`key` 通过 PostgreSQL 双引号和 SQLite/MySQL 反引号分支处理

结论：当前没有看到为了生产 SQLite 必须重写 schema 的问题。

## 如果生产改用 SQLite，需要改哪些

### 必改配置

1. Compose 模式删除或注释 `SQL_DSN=postgresql://...`
2. Compose 模式删除 `depends_on: postgres`，并可删除 `postgres` 服务和 `pg_data` volume
3. 保留并确认 `/data` 持久化挂载
4. 显式设置 `SQLITE_PATH`
5. 设置 SQLite 连接池：

```yaml
environment:
  - SQLITE_PATH=/data/one-api.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)
  - SQL_MAX_OPEN_CONNS=1
  - SQL_MAX_IDLE_CONNS=1
  - SESSION_SECRET=<生产随机值>
  - TZ=Asia/Shanghai
```

### 强烈建议

1. 保持单实例部署，不做多容器共享同一个 SQLite 文件。
2. 保留 Redis 可选，但不要把 Redis 当成 SQLite 多写实例的解决方案。
3. 规划数据库文件备份，WAL 模式下需要同时考虑主库、`-wal`、`-shm` 文件。
4. 建立日志清理策略，避免 `logs` 表无限增长。
5. 把数据库文件和备份视为敏感文件处理，因为里面会有用户、渠道配置和运行数据。

### 可选代码增强

这些不是阻断项，但可以让 SQLite 生产化更稳：

1. 将 `common.SQLitePath` 默认值从 `_busy_timeout=30000` 改成 `_pragma=busy_timeout(30000)`。
2. 当检测到主库为 SQLite 且未显式设置 `SQL_MAX_OPEN_CONNS` 时，自动把连接池降到 1。
3. 增加 SQLite 生产启动日志，提示当前数据库文件路径、连接池、journal mode。
4. 在文档中补一个 SQLite Compose 示例，避免用户误以为当前 `docker-compose.yml` 默认就是 SQLite。

## 建议决策

- 如果只是个人/小团队/低流量单实例：SQLite 可以作为生产库，主要改配置即可。
- 如果有多节点、公开服务、高并发计费、支付回调、长期保留大量请求日志：继续用 PostgreSQL。
- 如果想低运维但又要保留大量请求日志：主库 SQLite 可以考虑，但日志库建议拆到 ClickHouse/PostgreSQL。

当前项目“改成生产 SQLite”的工作量不大：配置层面 1 次 Compose 调整即可跑；代码层面建议做 2 到 4 个小增强，主要是 DSN 默认值、连接池和文档提示。
