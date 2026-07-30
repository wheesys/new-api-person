# ContextConsensus 阶段 D-1b 实施记录

日期：2026-07-30

## 结论

阶段 D-1b 已完成服务端构造、版本化且默认拒绝的结构化工具结果脱敏注册表。它只接受 D-1a 在同一进程内生成且未被篡改的单工具串行结构证据，并输出可验证的有界标量投影。

本批未接入 `BuildCompactionPlan`、摘要 prompt、rewrite 或 runtime，未增加配置、数据库、管理员接口或页面。所有工具上下文继续被现有压缩门禁拒绝，Summary v1 和 managed 行为不变。

## 策略与输入绑定

- 注册表只在构造时接收策略，内部深复制规则和字符串枚举；不存在全局注册、运行时追加或修改入口。
- 策略和请求精确绑定 sanitizer version、policy version、tool identity digest 与 schema digest；任一不匹配均默认拒绝。
- D-1a 证据增加不可序列化的进程内完整性 seal。证据经 JSON 序列化、字段篡改或 digest 格式非法后不能进入脱敏阶段。
- 标准 Chat 工具结果的 `content` 为 JSON 字符串时，先按原始字符串校验 result digest，再在内部严格解析其 JSON 对象；解析后的对象不能替代原始值绕过 digest 绑定。

## 白名单和失败关闭

- 策略使用 RFC 6901 JSON Pointer，支持 `~0`、`~1` 和明确数组索引；指针、输出字段和版本不做隐式空白归一化。
- 输入顶层必须是 JSON object。未知字段、重复 object key、缺失路径、类型不匹配、非 JSON、指针前缀冲突、敏感字段和值全部拒绝。
- string 只允许服务端静态登记的 ASCII 枚举值；number 和 boolean 仅允许对应 JSON 标量。string、number、输入、输出、深度、规则数、策略数、指针和字段名均有硬上限。
- API key、认证头、cookie、credential、token、secret、密码、签名、URL/URI、provider file、支付和个人信息相关标识失败关闭；错误只返回固定错误，不包含原始结果或敏感值。

## 输出证明

脱敏输出只保存 sanitizer/policy/source/projection digest 和白名单字段。字段访问器返回深复制；所有内容使用不可序列化的进程内 seal 绑定。字段或证明被篡改后 `Validate` 失败，公开 JSON 序列化也直接拒绝，避免无效投影进入后续 prompt 或日志。

本阶段不宣称对任意自然语言做可靠脱敏。为避免凭据、个人信息和 prompt injection 穿透，字符串只开放静态 ASCII 枚举；需要自由文本的工具仍保持不可压缩。

## 验证

- 覆盖真实 `Extract -> AssessSingleSerialToolCompaction -> Sanitize` Chat JSON 字符串结果闭环及原始内容篡改。
- 覆盖版本/tool/schema/result 精确绑定、证据和输出篡改、序列化能力边界及注册后策略不可变。
- 覆盖未知/敏感字段、敏感字符串、重复 JSON key、非 object、缺失/错误类型、深度和输入/输出上限。
- 覆盖 RFC 6901 转义、数组索引、重复/前缀冲突指针、字符串枚举和 number 字节上限。
- `go test ./common ./service/contextconsensus -count=1`。
- `go test -race ./service/contextconsensus -count=1`。
- `go vet ./service/contextconsensus`。
- `go test ./... -count=1`。

补充执行 `go vet ./common` 时仍会命中本批之前已存在的 `CustomEvent` 锁复制和 IPv6 测试地址格式告警；本批新增 JSON 校验代码未产生新的 vet 告警，未扩大范围修改既有问题。

## 后续拆分

- D-1c：新增 Summary v2，消费前强制验证脱敏输出，将单个旧串行 Chat 工具调用、结果和最终 assistant 回复作为不可拆分原子段接入 plan、prompt、rewrite 与 runtime。
- D-1d：增加只含有限 reason code 的聚合诊断 API 和后台页面，不展示原始工具状态、会话内容或 digest。
- D-2：在稳定 ID、统一 group 语义和完整闭合证明后评估并行工具组。
- D-3：待 adaptor 提供权威所有权、到期和删除能力后，再实现 provider file 生命周期。
