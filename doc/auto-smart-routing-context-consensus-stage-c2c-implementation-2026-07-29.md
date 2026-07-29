# ContextConsensus 阶段 C-2c 实施记录

日期：2026-07-29

## 结论

阶段 C-2c 已完成 managed 非流式请求的下一 revision 生成、固定上限响应缓冲、同请求 CAS 恢复和 commit-before-write 提交屏障。阶段 C-2 至此完成，非流式统一 `503` 门禁已移除；流式请求仍返回 `400`。

`managed_context_enabled` 继续默认关闭。阶段 C-3 的稳定客户端幂等键、provider state report/登记/绑定和凭据槽固定尚未实现，因此阶段 C 总待办继续保持未完成，provider-owned state 仍失败关闭。

本批未增加管理员接口、管理页面、数据库对象或外部路由配置项，复用已有 compaction model/channel、额度、输入输出上限和 timeout 配置。

## 已完成范围

### 下一 revision 与 L2/L3

- 使用现有独立计费 compaction 子请求更新 L2/L3，不用固定字段或字符串拼接猜测语义摘要。
- lineage 使用带域分隔的规范 JSON SHA-256，绑定客户端协议、上一 revision、上一摘要/状态摘要、本次原始增量正文摘要、规范化 assistant 输出摘要和策略版本。
- fact 的 user source range 绑定规范化 current-user 文本摘要；完整增量正文摘要仅进入 lineage。assistant source range 绑定规范化文本输出摘要，不绑定供应商响应 ID。
- 摘要 prompt 与冻结 plan 必须完全一致；返回 JSON 执行精确字段、source ranges、source digest、provenance 和 confidence 校验。
- 旧 fact 只能原样保留或删除；新 fact 只能引用当前 `user_confirmed` 或 `assistant_inferred` 证据，禁止 provenance 提权、tool result summary、隐藏推理和 provider binding。
- `next.revision = expected_revision + 1`；保留首次创建时间并更新修改时间。`MaxUint64` 在主上游调用前以 `409` 拒绝。

### 主调用与提交屏障

- managed 主请求继续强制非流式、零网关重试和单次 prepared text attempt。
- 会话、lease、策略、摘要授权和 TTL 在主上游调用前完成预检；失败时不调用上游并按现有计费会话退还预扣。
- 主响应固定使用 2 MiB 内存缓冲；`limit-1` 和 `limit` 可接受，`limit+1` 进入 sticky overflow，并返回 `503 managed_context_response_too_large`。
- 主响应先完成客户端协议规范化和 usage 校验，再写消费日志并结算；畸形、截断、工具调用、多候选或 usage 缺失不会触发结算。
- 主调用结算成功后执行独立摘要子请求；摘要子请求继续拥有独立 request ID、BillingSession、结算和消费日志。
- 下一状态校验成功后执行 revision CAS；只有 CAS 成功或同请求恢复确认成功后，才返回 `X-New-Api-Context-Revision` 并调用一次 `FlushToClient()`。
- CAS 前任一失败均不写出缓冲的上游 status、headers 或 body。CAS 后客户端写失败不回滚 revision、不退款、不重放，也不追加第二段错误响应。

### Redis 提交恢复与计费语义

- revision conflict 和 lease/fencing 无效属于明确未提交，返回 `managed_context_commit_failed`。
- Redis 写结果不确定时，使用脱离客户端取消且有界的 context 只读核验下一 revision、fencing 和完整解密状态。
- 已写入且状态与候选完全一致时恢复为成功；仍停留在 expected revision 时，确认请求未取消、续租当前 lease，并最多重试一次相同 CAS。
- 第二次结果仍无法判定、读取失败、revision/fencing 不相关或 payload 不一致时返回 `managed_context_commit_outcome_unknown`，禁止 flush。
- 主调用或摘要子调用一旦完成结算，后续摘要/CAS/客户端写失败均不退款；未结算的预扣继续按现有 BillingSession 规则退款。
- C-2c 只支持同一请求内恢复。结算成功但明确未提交时，客户端跨请求重发仍可能重复调用和重复计费，不宣称安全重试；该闭环属于 C-3 稳定幂等键待办。

## 验证

- 四协议完整文本响应规范化，响应转换与 usage 校验均早于 settlement。
- 固定 2 MiB 缓冲及 `limit-1`、`limit`、`limit+1` 边界，无提交时客户端保持零上游输出。
- lineage 稳定性、策略/证据变化、旧 fact 保持、新 fact provenance、prompt/plan 错配和 revision 溢出拒绝。
- CAS 写成功但传输报错的 read-after-write 恢复、旧 revision 单次重试、恢复读取失败和客户端取消后的 detached 只读核验。
- commit 时客户端正文仍为空；CAS 失败不返回 revision 头或缓冲正文；CAS 成功后才写响应。
- `go test ./...`。
- `go test -race ./service/contextconsensus ./middleware ./relay ./controller`。
- `go vet ./service/contextconsensus ./middleware ./relay ./controller`。
- `git diff --check`。

## 后续待办

- C-3 冻结稳定客户端幂等键，并建立结算结果与 revision outcome 的跨请求恢复记录，消除结算后明确未提交场景的重复调用/计费风险。
- adaptor 显式返回真实 provider state report；成功提交后登记 owner 隔离 binding，请求前校验并固定渠道、协议、模型和精确凭据槽。
- 补齐 key rotation、provider state TTL/所有权、Redis 故障和多 Key 凭据轮换矩阵。
- C-3 完成并验证前，不启用 provider-owned state，也不将阶段 C 标记完成。
