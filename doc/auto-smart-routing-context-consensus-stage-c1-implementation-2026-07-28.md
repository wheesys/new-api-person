# ContextConsensus 阶段 C-1 实施记录

## 结论

阶段 C 已完成第一批基础设施，但托管请求事务链路尚未完成，因此总阶段 C 待办保持未完成。

本批实现了 owner 隔离、应用层加密、真实 Redis Lua 仓储、revision CAS、lease、fencing、TTL、provider state 绑定记录契约，以及网关头解析和失败关闭门禁。未增加管理员接口、管理页面或外部路由配置体系。

## 已完成范围

### owner 与存储 key

- owner 固定为 `user_id + token_id + endpoint_family`。
- 外部 `Context-Id`、provider state 引用和渠道凭据只参与 HMAC，不进入 Redis key、状态正文和日志。
- 会话 key 使用 `new-api:context_consensus:v1:<owner_hmac>:<conversation_hmac>`。
- provider state 使用独立 owner 隔离 key；credential fingerprint 同时绑定渠道、multi-key 槽位和凭据。

### 加密与配置

- 共识和 provider binding payload 使用 AES-256-GCM，仅接受严格 32 字节密钥。
- AAD 绑定 repository key、用途、revision、算法和 key version，防止密文跨会话、跨用途或跨版本移动。
- 独立读取 `CONTEXT_CONSENSUS_ENCRYPTION_KEY` 和 `CONTEXT_CONSENSUS_ENCRYPTION_KEY_VERSION`；密钥必须是严格 Base64 编码的 32 字节随机值。
- HMAC 子密钥由独立加密主密钥按固定 domain 派生，不复用 session、API Key 或渠道凭据。
- 缺失、非法编码、长度错误或缺少 key version 时失败关闭。

### Redis 原子仓储

- 新增专用 `RedisManagedConsensusRepository`，不调用会在 Debug 模式输出 value 的 `common.RedisSet`，也不回退到 `HybridCache` 或进程内缓存。
- lease acquire、renew、release，revision CAS、删除和 provider binding 冲突保护均使用 Redis Lua 原子执行。
- fencing token 单调递增；计数器保留时间长于允许的状态/lease TTL，过期旧持有者不能提交或释放新 lease。
- 所有状态必须具有正 TTL，且不得超过构造时冻结的最大保留时间；永久 key 和非法 TTL 失败关闭。
- provider binding 保存加密目标绑定，包含协议、模型、渠道、渠道类型、multi-key 槽位、凭据指纹和原因码。

### 请求门禁

- 网关解析并立即删除 `X-New-Api-Context-Id`、`X-New-Api-Context-Mode`、`X-New-Api-Context-Revision`，这些头继续禁止向上游透传。
- `managed` 必须同时提供 opaque Context ID 和非负 revision，并要求系统、managed 开关及 Token 权限均已授权。
- managed 流式请求在预扣费和上游调用前返回 400。
- C-2 事务链路接入前，managed 非流式请求固定返回 503，不允许静默退化成 stateless，也不会误推进 revision。
- 默认 `managed_context_enabled=false`，状态 TTL 默认 3600 秒。

## 验证

- owner、Token、endpoint、Context ID、provider state 和 credential slot 隔离。
- AES-GCM 往返、明文不可见、错误 AAD/revision/key version 和密文篡改拒绝。
- 两个独立 Redis client 并发 CAS 只有一个成功。
- lease 占用、续租、过期重取、旧 fencing 拒绝和 TTL 到期销毁。
- provider binding 冲突、owner 隔离和过期销毁。
- 网关头、managed 授权和流式/非流式失败关闭。
- 定向 Race：`go test -race ./service/contextconsensus ./middleware ./controller ./setting/model_setting`。

Redis 合约测试使用 `miniredis` 执行真实 Lua 路径，并通过可控时间推进验证 TTL，不使用 sleep 或进程锁模拟多实例一致性。

## 阶段 C 剩余工作

### C-2 托管事务与提交屏障

- 在渠道选择前 acquire/load/decrypt，并冻结 owner、revision、lease 和 fencing。
- 明确 managed 请求携带完整历史还是增量 turn；在契约确定前不注入旧摘要，避免重复语义。
- 增加四协议的托管摘要安全注入，但不得删除客户端本次正文或提升为 system/developer 指令。
- 建立非流式有界响应缓冲和 commit-before-write 屏障。
- 主上游完整成功、响应转换成功、结算成功后才 CAS 推进 revision；失败、断连和不完整响应不推进。
- lease 续租失败时取消请求并禁止提交。

### C-3 幂等与 provider state 运行时闭环

- 冻结稳定客户端幂等键契约；当前每次生成的新 request ID 不能虚构为客户端重试幂等键。
- adaptor 显式返回真实上游 `ProviderStateReport`，成功提交后才登记 binding。
- 请求侧在候选选择前校验 state mapping，并固定协议、模型、渠道和精确凭据槽位。
- 补齐 hosted tool、通用 file ID 等当前 extractor 未保留引用的场景。
- 支持 key rotation 的旧版本读取窗口，并完成 Redis 各阶段故障矩阵。

上述 C-2/C-3 完成前，不启用 `managed_context_enabled`，也不将阶段 C 总待办标记完成。
