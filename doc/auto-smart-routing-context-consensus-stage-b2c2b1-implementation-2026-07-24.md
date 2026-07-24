# ContextConsensus 阶段 B-2c2b1 实施报告

日期：2026-07-24

## 1. 完成范围

本阶段将 B-2c2a 的文本请求预准备边界扩展到原生 Responses、Responses Compact、Claude 和 Gemini。加上已有的 OpenAI Chat Completions 与 Chat-to-Responses 路径，文本转发入口现在统一遵循以下生命周期：

```text
解析并复制请求 DTO
  -> 保持协议原有顺序完成模型映射、协议转换和参数覆盖
  -> 冻结最终上游正文快照
  -> 返回 PreparedTextRelayAttempt
  -> 使用同一个快照执行发送与响应处理
  -> Close 释放正文并恢复临时协议状态
```

本阶段只迁移原有请求准备与发送逻辑，不启用自动压缩，也不改变主请求计费、结算或失败重试策略。

## 2. 协议行为

### 2.1 Responses 与 Responses Compact

- 保持 Compact 渠道能力校验、请求规范化、模型映射、协议转换、字段过滤和参数覆盖的原有顺序。
- 显式 `0` 输出上限在深拷贝、转换和正文冻结后仍被保留。
- Responses Compact 的重新定价与结算继续留在 handler 中，不进入通用执行器。
- Responses 响应不根据 `Content-Type` 覆盖请求阶段确定的流式状态。

### 2.2 Claude

- 保持默认输出上限、thinking/effort、system prompt 和参数覆盖的原有顺序。
- Claude-to-Responses 仍按 Claude -> Chat Completions -> Responses 的既有转换链执行，不重复进入完整 OpenAI 准备流程。
- 显式 `0` 输出上限不会被默认值覆盖。

### 2.3 Gemini

- 保持 no-thinking、thinking、system instruction、协议转换和参数覆盖的原有顺序。
- 不对 Gemini 请求错误套用 OpenAI 的 disabled-fields 过滤。
- 仅网络请求失败时保留原有 Gemini 请求错误日志前缀。

## 3. 所有权与副作用边界

- 转换后的正文由 `PreparedTextRelayAttempt` 独占；调用方必须在准备成功后立即安排 `Close()`。
- pass-through 正文仍借用 Gin 请求体存储，只用于兼容发送，不能作为自动压缩的权威候选证据。
- prepare 阶段不调用 `GetRequestURL`，不执行网络请求，不处理响应，也不触发计费。
- 每个候选必须使用独立的 Gin context、`RelayInfo` 和 DTO；`Close()` 只负责正文生命周期及 Chat-to-Responses 的临时 mode/path 恢复，不回滚其他候选元数据。
- prepared attempt 仍为一次性执行对象，实际发送的正文就是准备阶段冻结的同一份快照。

## 4. 尚未完成的权威证据能力

当前实现是兼容性预准备边界，不能直接声明最终模型、协议和目标地址均为权威证据，原因包括：

- 部分 adaptor 会在 `GetRequestURL` 中继续解析或修改最终模型。
- Vertex 等路径的 URL 构造可能读取凭据，不能在离线准备阶段直接调用。
- pass-through 会绕过协议转换并借用共享正文。

下一阶段 B-2c2b2 将增加 adaptor 显式 opt-in 的纯目标解析能力，并为权威候选提供单独入口；未声明能力的 adaptor 必须在权威终检时失败关闭。

## 5. 验证

- 原生 Responses、Claude、Gemini 均验证 prepare 阶段零网络、零响应处理、零目标 URL 解析。
- 验证准备后修改原始 DTO 不会改变实际发送正文。
- 验证显式 `0`、协议特有字段过滤规则和流式状态行为。
- 验证三协议 pass-through 不执行 converter，且 attempt 关闭后共享请求体仍可读取。
- 验证 Claude-to-Responses 只执行一次 Responses 转换，并在 attempt 关闭前保持有效 mode/path。
- 验证不支持 Responses Compact 的 adaptor 在转换前失败。
- 通过 relay 定向测试和竞态测试。

## 6. 后续阶段

1. B-2c2b2：实现无凭据输入的纯目标解析接口及 adaptor 离线能力门禁。
2. B-2c2c：冻结完整候选集合，接入权威 tokenizer/上下文上限，完成单次压缩编排、原子提交和主预扣前失败关闭。
