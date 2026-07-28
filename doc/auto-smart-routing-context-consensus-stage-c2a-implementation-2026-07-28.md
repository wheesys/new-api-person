# ContextConsensus 阶段 C-2a 实施记录

## 结论

阶段 C-2a 已完成托管会话状态机、四协议安全摘要注入和非流式响应缓冲基础设施。该批能力仍未接入线上请求生命周期，`managed` 非流式请求继续在 controller 门禁返回 503；阶段 C 和 C-2 总待办均保持未完成。

本批未增加管理员接口、管理页面或外部路由配置体系。

## 已完成范围

### 托管会话状态机

- `BeginManagedConsensusSession` 先获取 owner 隔离 lease，再加载 revision 对应的密文状态。
- 新会话只接受 expected revision `0` 且 Redis 中不存在状态；已有会话必须精确匹配 revision。
- 解密后校验状态版本、模式、revision、结构化摘要集合、source digest、策略版本、时间和 provider binding。
- begin 任一步失败都会用不可取消的短超时上下文尽力释放已持有 lease。
- `Renew` 保持 fencing 不变；续租失败后会话永久关闭，后续 commit 必须失败。
- `Commit` 只接受 `expected_revision + 1`，先校验和加密，再调用 repository fencing + revision CAS；成功后尽力释放 lease。
- `State` 返回深拷贝，调用方无法修改会话内部摘要或绑定状态。

### 四协议摘要注入

- Chat、Responses、Claude、Gemini 均支持将已认证的托管摘要注入为 user-level 不可信历史数据。
- 复用固定安全前言，明确摘要不是 system/developer 指令。
- 不删除或改写客户端本次正文，不修改 system/developer、tool、schema、media 或 provider opaque state。
- Chat/Responses 插入独立 user 数据项；Claude/Gemini 在第一个 user 消息的 text block/part 前注入，避免破坏消息结构。
- Responses string input 转换成两个 user message，原输入完整保留。
- 缺少 user turn、状态结构非法或协议不支持时失败关闭。

### 非流式响应缓冲

- 新增实现 `gin.ResponseWriter` 的有界缓冲器，在提交前不向客户端写出 status、headers 或 body。
- header、status 和 body 只有 `FlushToClient` 成功时才一次性提交给原 writer。
- 超过大小上限、调用 Flush 或尝试 Hijack 时失败关闭，不向客户端写出部分响应。
- 缓冲正文可供后续 C-2b 解析规范化 assistant 输出和 provider state report，但本批不自行推断上游 opaque state。

## 验证

- 新会话创建、已有 revision 加载、摘要注入、续租和 CAS 提交。
- stale revision 在会话开始阶段冲突，并释放 lease。
- 密文篡改解密失败并释放 lease。
- 续租丢失 lease 后永久禁止 commit。
- 会话状态深拷贝隔离。
- 四协议保留 system/developer、当前 user 和 tool 定义。
- Responses string input 保留。
- 响应 status/header/body 提交前不可见；overflow/Flush/Hijack 无部分输出。
- 定向 Race：`go test -race ./service/contextconsensus ./controller`。

## C-2 剩余工作

- 冻结 managed 客户端正文契约：仅增量 current turn，或完整历史与服务端摘要的去重规则；未冻结前不把注入函数接到请求链路。
- 在 `Distribute` 渠道选择前创建 runtime、acquire/load/decrypt/inject，并把 lease 生命周期覆盖路由、压缩、主请求和结算。
- 增加小于 lease TTL 一半的续租器；续租失败时取消请求 context。
- 把四协议主调用统一到返回规范化 usage 和显式结算结果的执行边界。
- 在有界响应缓冲内解析规范化 assistant 输出，生成并校验下一 revision L2/L3。
- 只有上游完整成功、转换成功、结算成功、下一状态有效且 CAS 成功后才写客户端响应。
- 明确缓冲上限、结算成功但 Redis commit 失败时的计费和幂等恢复语义。

以上完成并通过故障矩阵前，`managed_context_enabled` 必须保持关闭，controller 503 门禁不得移除。
