# ContextConsensus 阶段 C-2b 实施记录

日期：2026-07-29

## 结论

阶段 C-2b 已完成 managed 增量正文契约、渠道选择前的会话加载/摘要注入/lease 续租，以及非流式主调用的规范化输出和显式结算结果边界。

本阶段不生成下一 revision，不执行 CAS/commit-before-write，不登记 provider state，也不解除 controller 的非流式 `503` 门禁。`managed_context_enabled` 继续默认关闭，阶段 C、C-2 总待办继续保持未完成。

本批未增加管理员接口、管理页面或外部路由配置体系。

## 已完成范围

### 增量正文契约

- managed 客户端只提交本次 current user turn；拒绝重复 user turn、assistant/model 历史、tool result/function response 历史和伪造的托管摘要标记。
- Chat、Responses、Claude、Gemini 保留本次 system/developer/instructions、工具定义、输出 schema 和可移植媒体。
- 拒绝 `previous_response_id`、conversation、Claude container/MCP、Gemini cached content、provider file ID/URI、thinking signature 等 C-3 provider-owned 状态。
- system/developer/instructions 必须位于 current user turn 之前；空 current turn 和不支持的内容块失败关闭。
- 注入前冻结客户端协议和原始增量正文 SHA-256 摘要；不持久化或记录客户端正文。

### 请求生命周期

- `Distribute()` 在解析模型和选择渠道前完成协议/流式校验、Redis runtime 创建、owner 隔离 lease acquire、revision 精确加载、解密和摘要注入。
- owner 继续绑定 `user_id + token_id + endpoint_family`，endpoint family 取客户端协议，不受后续上游协议转换影响。
- 注入后同步替换 `BodyStorage`、兼容正文缓存、`Request.Body`、`ContentLength` 和 `Content-Length`，智能路由看到的是注入后的唯一正文快照。
- lease 最长 30 秒，续租间隔为 TTL 的三分之一，严格小于 TTL 一半；续租失败取消请求 context，并永久禁止该 session 后续提交。
- 正常返回、路由失败、controller `503` 和客户端取消都会停止续租，并用脱离客户端取消的短超时 context 尝试 fencing-safe release。
- revision conflict/状态不存在/lease 占用映射为 `409`；协议、正文和流式错误映射为 `400`；Redis、密钥、解密及状态不可用映射为 `503`。
- Gemini `streamGenerateContent` 和 `alt=sse` 在 Redis runtime 创建前拒绝。

### 主输出与结算契约

- 四协议文本主调用统一暴露 `TextRelayExecutionResult`：规范化 usage、`TextConsumeResult`、settlement error 和完整消费错误不再只能由丢弃错误的包装函数观察。
- 新增纯函数输出规范化：
  - Chat 只接受单 assistant choice 且 `finish_reason=stop`。
  - Responses 只接受 `status=completed` 的单 assistant message/output_text。
  - Claude 只接受 text blocks，终态为 `end_turn` 或 `stop_sequence`。
  - Gemini 只接受单 candidate、非 thought 文本且 `finishReason=STOP`。
- 工具调用、推理签名、多候选、空正文、截断、安全停止和非终态响应不能推进 managed 状态。
- 规范化结果包含客户端协议、合并文本、终态原因和原始响应摘要，不从客户端响应 ID 推断 provider binding。
- controller 内部执行边界将完整非流式响应保留在有界 buffer 中，先完成 usage、消费日志、显式结算和客户端协议输出规范化；不调用 `FlushToClient()`。
- settlement error、消费日志错误、未记录消费日志或无有效 usage 均失败关闭。日志失败与结算失败一样阻止后续 C-2c commit。

## 验证

- 四协议合法 current turn、历史/opaque state/空 turn 拒绝和未知字段保留。
- revision 1 状态在路由前加载、摘要只注入一次、原 current turn/工具定义保持不变。
- 正文缓存、Content-Length、会话对象和原始增量摘要同步更新。
- Gemini 流式 URL 在 Redis 禁用时仍优先返回 `400`，证明未创建 runtime。
- 人工 tick 验证续租失败取消请求、错误 fencing 不释放其他 holder、正常 Close 释放 lease。
- 四协议完整文本响应规范化，以及工具、thinking、多候选、截断和非终态拒绝。
- 显式结算结果同时暴露 settlement/log 错误。
- controller 缓冲执行成功和结算失败均不向客户端写出正文。
- `go test ./...`。
- `go test -race ./service/contextconsensus ./middleware ./relay ./controller`。
- `go vet ./service/contextconsensus ./middleware ./relay ./controller`。
- `git diff --check`。

## C-2 剩余工作

- 生成并严格校验下一 revision 的 L2/L3，绑定本次增量正文摘要、规范化 assistant 输出和策略版本。
- 冻结 managed 响应 buffer 上限及超限错误契约。
- 在上游完整成功、转换成功、消费日志成功、结算成功和下一状态有效后执行 revision CAS。
- 只有 CAS 成功后才调用 `FlushToClient()`；任一步失败均不得写出部分响应。
- 明确“已结算但 Redis commit 失败”的恢复、重试和计费语义，并补齐端到端故障矩阵。
- 完成上述 C-2c 工作后才允许移除非流式 `503` 门禁；C-3 完成前仍不得启用 provider state 托管。
