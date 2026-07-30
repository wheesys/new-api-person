# ContextConsensus 阶段 D-1c 实施记录

日期：2026-07-30

## 结论

阶段 D-1c 已完成 Summary v2，并闭合 OpenAI Chat Completions 单个旧串行工具原子段的 plan、prompt、rewrite、内部摘要执行器和主请求候选终检。调用前 user、assistant tool call、tool result 与最终 assistant 回复只能整体压缩或整体保留。

`allow_tool_result_compaction` 默认关闭。服务端编译期策略注册点当前为空，因为仓库不存在可稳定归属到服务端的内置业务工具；任意客户端自定义工具不会自动获得脱敏策略。未登记策略只允许将压缩前缀缩到工具轮之前，不会跨过工具轮继续压缩，也不会把原始工具内容发给摘要模型。

## 原子计划与策略冻结

- 首版只接受 OpenAI Chat、一个已闭合 function call、一个紧邻 tool result 和一个紧邻最终 assistant 回复；当前消息必须是 user。
- `PreservedRecentTurns` 按逻辑轮次计数，普通轮为两条消息，工具轮为四条消息。
- assistant tool call 不得夹带文本、推理、name、prefix 或额外关联字段；工具结果和最终 assistant 回复也不得夹带未声明消息元数据。
- 工具轮进入 covered prefix 前必须重新执行结构评估和 D-1b 脱敏。plan 保存不可序列化的冻结 provider 与已封印投影，rewrite 前从原始正文重新构建并深比较计划。
- production provider 只从代码内静态策略构造，不接受请求、管理员、数据库或环境变量动态注册。

## Summary v2 与提示隔离

- v1 和 managed 契约保持不变；工具轮被覆盖时才使用 v2。
- 原始 call ID、函数名、参数、schema、tool result 和最终 assistant 原文均不进入摘要子请求。
- 每个脱敏标量生成一个确定性 `tool_observed` 事实，字段顺序、值、来源范围、digest 和 confidence 必须与服务端模板完全一致。
- `tool_observed` 禁止出现在其他事实组；普通事实禁止引用隐藏的 call/result/final 序号。
- v2 拒绝重复 JSON key，并将无来源约束的 `current_phase` 固定为空字符串，避免绕过确定性事实约束。

## Runtime 门禁

- 中间件冻结 `allow_tool_result_compaction`；工具请求同时抑制普通 debug 日志，解析失败的孤立工具结果也保持日志抑制。
- 内部摘要执行器显式绑定 summary version，并按 v1/v2 分派校验；v2 digest 基于规范化且已验证的摘要 JSON。
- rewrite 原子删除 covered 工具轮并保留原始 `tools` 定义；压缩后继续执行权威 token 重算和完整候选终检。
- 工具上下文要求最终 prepared protocol 仍为 OpenAI Chat；Chat-to-Responses 及其他跨协议转换失败关闭。

## 不支持范围

OpenAI Responses、Claude、Gemini、managed 工具摘要、多个或并行工具、非连续或未闭合工具段、MCP/hosted/custom tool、媒体、provider file、opaque state、自由文本工具结果、未登记工具/schema 和跨协议转换继续失败关闭。

最终 assistant 回复不会直接进入摘要子请求。其需要保留的语义必须由静态白名单脱敏投影表达，否则整个工具轮保持原样。

## 验证

- 覆盖静态 provider 唯一策略选择、未登记策略缩短前缀、四消息原子边界、v2 prompt 原始内容隔离和 rewrite 原子删除。
- 覆盖工具事实值与顺序篡改、隐藏段来源引用、无来源阶段字段注入、工具调用夹带隐藏文本和最终协议门禁。
- 覆盖配置显式加载、策略快照冻结、工具请求 debug 日志抑制及 v1/managed 回归。
- `go test ./... -count=1`。
- `go test -race ./service/contextconsensus ./controller ./middleware -count=1`。
- `go vet ./controller ./middleware ./setting/model_setting ./service/contextconsensus`。
- `gofmt -d` 与 `git diff --check`。

## 后续拆分

- D-1d：增加只含有限 reason code 的有界聚合诊断 API 和后台页面，不展示原始工具状态、会话内容、digest 或摘要正文。
- D-2：在稳定 ID 和统一 group 语义闭合后评估并行工具组。
- D-3：待 adaptor 提供权威所有权、到期和删除能力后实现 provider file 生命周期。
