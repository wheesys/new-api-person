# ContextConsensus 阶段 B-2c2c 实施报告

日期：2026-07-28

## 1. 完成范围

本阶段完成请求内自动压缩的主链闭环：在智能路由 middleware 冻结完整候选集，在 controller 主预扣费之前逐候选生成权威最终请求、校验 token 与上下文上限证据，并在所有候选明确超限且三重授权完整时最多执行一次独立压缩子请求。压缩后重新改写源协议正文，并从完整冻结候选集重新执行终检。

最终提交对象同时持有源协议 DTO、`BodyStorage`、候选及渠道快照、`RelayInfo`、`PreparedTextRelayAttempt` 和 `TokenBudget`。主调用实际发送预准备阶段冻结的同一正文；任何准备、计数、上限、压缩、改写或提交错误均发生在主 `ModelPriceHelper`、`PreConsumeBilling` 和主上游网络调用之前。

相关系统开关继续默认关闭，压缩模型池、渠道白名单和权威上下文上限继续默认留空，因此升级不会自动改变现有请求行为。

## 2. 完整候选冻结

- 新增忽略推断 `context_too_small` 的权威终检排名入口。
- 虚拟模型候选仍先经过模型池、Token 模型权限、endpoint/工具/schema/媒体能力和运行时健康硬隔离。
- 普通估算窗口淘汰全部候选时，只在 ContextConsensus 系统门禁开启后允许冻结候选进入 controller 终检；默认关闭路径保持原行为。
- 显式模型只冻结同模型候选，虚拟模型冻结完整跨模型候选。
- 冻结切片使用请求内副本，不受后续排序、重试或配置热更新污染。

## 3. 权威预算边界

生产 token 计数器当前只声明以下安全支持范围：

- 最终协议为原生 OpenAI Chat Completions。
- `tiktoken-go` 能为最终模型返回明确 tokenizer，不使用默认 `cl100k` 回退。
- 最终请求是纯文本 Chat 消息，不含工具、schema、媒体、stateful tool message、`prompt` 或 `input` 兼容字段。
- 使用协议感知的消息文本计数和消息/name/assistant framing 开销，不把最终 JSON 字段名、引号和转义字符错误计为 prompt token。

OpenAI Responses、原生 Gemini、未知 tokenizer、工具/schema/媒体及缺少权威默认输出上限的请求均失败关闭。Claude、Gemini 或 Responses 源协议只有在权威 adaptor 最终转换为上述 OpenAI Chat 安全范围时才可进入本阶段主链。

上下文上限解析器在请求开始时深拷贝 `smart_routing.authoritative_context_limits`，并严格匹配最终模型、渠道 ID、最终协议和配置版本。显式输出上限 `0` 保持非空指针；输出上限缺失不会按 `0` 放行。

## 4. 单次压缩与独立计费

编排顺序固定为：

```text
完整冻结候选逐个权威准备与预算终检
  -> 任一候选可容纳：直接提交，不压缩
  -> 全部候选有完整证据且明确超限：检查三重授权和硬上限
  -> 构造一次 CompactionPlan 与安全提示
  -> 独立子请求预扣、执行、结算和审计
  -> 用结构化摘要重写源协议正文
  -> 对完整冻结候选重新逐个权威终检
  -> 原子提交
  -> 主请求定价、预扣和发送
```

任一候选证据未知时，不以“未知”冒充“超限”，也不会触发压缩。压缩调用次数配置必须精确为 `1`；模型池、渠道白名单、费用上限、输入上限和 timeout 必须全部有效。子执行器增加实际准备阶段输入上限检查，并在普通随机选择落到白名单外时确定性查找白名单内可用渠道。

压缩子请求继续使用独立 request ID、`RelayInfo`、`BillingSession`、价格快照、预扣/结算/退款和消费日志。子调用成功而主终检或主预扣失败时，子调用保持正常结算，主调用保持零发送。

## 5. 原子提交与发送屏障

- 每个候选使用独立 Gin context、HTTP request、DTO、源正文存储、渠道上下文、`RelayInfo` 和 prepared attempt。
- 多 Key 渠道离线准备使用不推进轮询游标的冻结凭据槽位；只有选中 artifact 会提交到主请求。
- 淘汰候选立即关闭 prepared attempt 和临时正文。
- 提交前先完成正文读取和 rewind；成功后一次性替换主请求 context keys、HTTP request、DTO 和 `BodyStorage`，再关闭旧正文存储。
- 主请求直接执行提交对象中的 `PreparedTextRelayAttempt`，不会重新转换或重新生成正文。
- 首版 ContextConsensus 主调用禁用网络失败重试，避免未终检候选进入发送路径；通用重试路径同时增加首个下游输出后的停止屏障。

## 6. 审计与敏感信息

智能路由消费日志在压缩成功时增加：压缩前后 token、摘要 digest、压缩模型、渠道 ID、子 request ID、结算额度和结果码。日志不包含原始正文、摘要正文、工具参数、认证头、TokenKey、渠道密钥或 provider state。

内部压缩请求仍只复制计费身份白名单，不继承父请求 Authorization、Cookie、渠道凭据、完整 headers、smart-routing 重试状态或客户端 response writer。

## 7. 验证

新增或扩展测试覆盖：

- 推断窗口不足的候选仍进入权威冻结集。
- 冻结候选深拷贝和系统门禁。
- 严格 tokenizer、未知模型不 fallback、非 OpenAI Chat 不估算、正文模型绑定和配置快照冻结。
- 后置长上下文候选可容纳时不压缩。
- 任一候选证据未知时失败关闭。
- 显式 `0` 在源模型重写后保持。
- 多 Key 离线准备不推进轮询状态。
- 压缩输入上限和渠道白名单失败审计。
- 压缩审计元数据不包含正文或摘要正文。

验证结果：

- 相关五包普通测试通过。
- 相关五包定向 `go test -race` 通过。
- 相关五包 `go vet` 通过。
- 沙箱内全仓测试仅因禁止绑定本地临时端口失败；在允许本地测试端口后，`go test ./... -count=1` 全量通过。

## 8. 后续边界

本阶段没有新增管理员接口或后台页面，也没有开启生产配置。后续如需扩大自动压缩适用范围，应为 OpenAI Responses、原生 Gemini、工具/schema/媒体和更多模型 tokenizer 增加协议语义明确的权威计数适配器；不得用 JSON 字节数、通用估算器或未知 tokenizer fallback 扩大放行范围。

阶段 C 的 Redis 加密托管共识、revision/CAS、lease、TTL 和 provider state 绑定映射仍是独立待办。
