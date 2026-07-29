# ContextConsensus 阶段 C-3a 实施记录

日期：2026-07-29

## 结论

阶段 C-3a 已完成托管会话 state 的应用加密密钥轮换闭环：运行时使用 active key 写入，并可配置最多 4 个 read-only previous key；读取时同时覆盖 AEAD envelope 版本和因 key version 变化产生的 HMAC Redis namespace。旧 namespace 在下一次业务 revision 提交时原子迁移到 active namespace，不能持续刷新旧 key 的 TTL。

阶段 C-3 和阶段 C 继续保持未完成。稳定客户端幂等键、持久化计费 operation 去重、跨请求 outcome、provider state report/原子登记及精确凭据槽固定仍属于 C-3b/C-3c。`managed_context_enabled` 继续默认关闭，provider-owned state 继续失败关闭。

本批未增加管理员接口、页面、数据库对象或外部路由配置；只增加托管状态加密密钥轮换所需的环境变量读取。

## 已完成范围

### 有界 keyring

- `CONTEXT_CONSENSUS_ENCRYPTION_KEY` 与 `CONTEXT_CONSENSUS_ENCRYPTION_KEY_VERSION` 继续定义唯一 active key。
- 新增 `CONTEXT_CONSENSUS_PREVIOUS_ENCRYPTION_KEYS`，格式为 JSON 数组，每项包含 `version` 与 strict Base64 `key`，最多 4 项。
- active/previous 版本必须唯一；每把 key 必须严格解码为 32 字节。错误只报告版本和配置类型，不回显 key 内容。
- active key 始终位于读取顺序首位，所有新 envelope 和 HMAC namespace 只使用 active key；previous key 只用于定位和解密旧记录。

### 旧 namespace 唯一定位

- 会话开始前派生 active 与 previous conversation storage keys，并读取全部候选。
- 只有一个 namespace 可存在；current/previous 同时存在时返回 key rotation conflict，不根据 revision、更新时间或 key 顺序猜测权威状态。
- 找到旧记录后仍先获取旧 namespace 的 lease，并在 lease 后再次扫描全部候选，关闭定位与加锁之间的并发迁移窗口。
- envelope 根据自身 `key_version` 选择解密 key；未配置对应版本时失败关闭，不回退 active key 猜测解密。

### revision 原子迁移

- 旧 namespace 的下一 revision 使用 active key 和 active repository key 生成 AEAD AAD/ciphertext。
- 新增专用 Redis Lua：校验旧 lease/fencing 和 expected revision、确认 active state 不存在、写入 active state、推进 active fencing counter、保留有界 TTL，最后删除旧 state。
- 任一校验失败不会产生部分迁移。active state 已存在时返回 key rotation conflict。
- Redis 已执行迁移但响应丢失时，现有同请求 read-after-write 恢复会从 active namespace 加载、解密并比较完整候选状态；一致时恢复成功。
- 迁移后只配置 active key 即可继续读取；未迁移的旧状态在 previous key 提前移除后无法定位，因此 previous key 必须至少保留一个最大 state TTL 窗口。

## 验证

- active + previous keyring 构造、版本重复、非法 Base64、非 32 字节和超过 4 项限制。
- previous namespace 加载和解密、新 revision 使用 active envelope、旧 state 删除、active state 可由 active-only runtime 读取。
- current/previous 双 namespace 冲突在 lease 前失败关闭。
- Redis Lua 真实迁移、旧 state 删除、active fencing 单调推进和 TTL。
- Redis 迁移写成功但响应丢失时的完整状态恢复。
- `go test ./service/contextconsensus ./middleware -count=1`。
- `go test -race ./service/contextconsensus ./middleware ./relay ./controller -count=1`。
- `go vet ./service/contextconsensus ./middleware ./relay ./controller`。
- `go test ./... -count=1`。
- `git diff --check`。

## 配置与轮换顺序

1. 将新 key/version 设置为 active。
2. 将旧 key/version 放入 `CONTEXT_CONSENSUS_PREVIOUS_ENCRYPTION_KEYS`。
3. 保持 previous key 至少一个最大托管 state TTL；活跃会话会在下一 revision 原子迁移，非活跃记录自然过期。
4. 确认旧 state 保留窗口结束后，再移除 previous key。

该顺序只覆盖本批已存在的托管会话 state。未来 C-3b/C-3c 引入的 idempotency outcome 和 provider mapping 必须在各自事务中实现相同的 active 写入、previous 读取与原子迁移后，才能纳入统一退役窗口。

## 后续待办

- C-3b 先为主调用和摘要子调用增加持久化计费 operation 去重，再接入稳定客户端幂等键、revision intent 和跨请求 outcome。当前 API Key 额度调整与消费日志没有完整 operation 去重，不能只靠 Redis phase 宣称 exactly-once。
- C-3c 首批只支持从原生 OpenAI Responses 非流式真实上游响应提取 `id`，登记为下一请求 `previous_response_id` binding；转换协议、Claude/Gemini opaque state 和 provider file 继续失败关闭。
- provider mapping/outcome 落地后补齐它们的 key rotation、TTL、所有权、Redis 不确定结果及多 Key credential rotation 矩阵。
