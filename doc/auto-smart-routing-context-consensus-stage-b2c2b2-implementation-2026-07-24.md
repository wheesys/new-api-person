# ContextConsensus 阶段 B-2c2b2 实施报告

日期：2026-07-24

## 1. 完成范围

本阶段在兼容性文本请求预准备之外，新增独立的权威预准备入口。权威入口要求 adaptor 同时显式声明离线转换能力与纯目标解析能力；缺少接口、任一能力未声明或使用 pass-through 时，均在协议转换和网络访问前失败关闭。

首批仅允许原生 OpenAI 渠道处理 OpenAI、Responses、Claude 和 Gemini 四类文本源协议；原生 Gemini 渠道只处理无需跨协议媒体转换的原生 Gemini 请求。Azure、OpenRouter、自定义 OpenAI 兼容渠道、Vertex 等变体仍走兼容入口，不能生成权威候选证据。

## 2. 双入口与能力门禁

- `PrepareTextRelayAttempt` 保留原有兼容行为，不要求 adaptor 提供权威能力。
- `PrepareAuthoritativeTextRelayAttempt` 仅接受实现 `AuthoritativeTextRelayAdaptor` 的 adaptor。
- 能力接口分别声明离线转换与纯目标解析，避免把“当前碰巧可准备”误认为稳定契约。
- 目标解析输入只包含渠道类型、模型、协议、模式、无查询参数的 URL path 及冻结后的 Gemini 模型规范化开关，不包含 API Key、Base URL、URL query、请求头、请求正文、Gin context 或数据库对象。
- 目标解析发生在协议转换完成后、参数覆盖与正文冻结前；解析器只能规范化模型，不能擅自改变协议、模式或路径。

## 3. 权威目标封印

权威准备成功后，`RelayInfo` 会封印以下证据：

- 最终上游模型；
- 最终请求协议；
- relay mode 与无查询参数的请求 path；
- 冻结正文摘要与字节数。

HTTP、表单与 WebSocket 发送路径会在 URL 解析前后、请求头设置后及真正发起网络请求前校验封印。任何 adaptor 在后续阶段修改模型、协议、模式、路径或正文元数据，都会在网络访问前失败。

对外可读取的权威目标只包含无查询参数的 path。`RelayInfo` 中原始 path 与 query 的完整值仅以 SHA-256 摘要参与私有封印，从而既能检测 query 漂移，又不会把查询凭证传入纯 resolver 或通过 `AuthoritativeTarget()` 暴露。

OpenAI、Responses 和 Claude 权威正文必须真实包含与目标一致的模型字段，不能依赖 `RelayInfo` 回填。Gemini 的模型位于 URL 路径，因此允许正文不含模型，但仍要求目标模型、协议和正文摘要保持一致。

## 4. 渠道边界

### 4.1 原生 OpenAI

- 对四类文本源协议显式声明权威能力。
- 目标解析保持转换后模型、协议、模式和路径不变。
- Responses reasoning effort 后缀规范化会同步更新 `RelayInfo`，确保正文模型与权威目标一致。

### 4.2 原生 Gemini

- 仅对原生 Gemini 源协议显式声明权威能力。
- thinking、nothinking、thinking budget 与 effort 后缀通过无副作用纯函数规范化。
- 解析所需开关在准备开始时冻结，不在纯解析器中读取全局配置。
- 兼容发送路径继续执行同一模型规范化逻辑，既有 streaming 与 `DisablePing` 行为保持不变。
- OpenAI、Responses 和 Claude 转 Gemini 的媒体转换可能下载远程 URL，因此不声明离线转换能力，并在 converter 调用前失败关闭。

### 4.3 失败关闭范围

- Vertex adaptor 未实现权威能力接口，防止 URL 目标解析提前读取凭证或项目配置。
- OpenAI adaptor 的 Azure、OpenRouter、自定义兼容等渠道类型不声明权威能力。
- Gemini adaptor 的非原生 Gemini 渠道类型不声明权威能力。
- pass-through 正文仍可通过兼容入口发送，但不能成为权威候选证据。

## 5. 敏感信息保护

发送层对 HTTP、表单、任务请求及 WebSocket URL 的日志和错误信息统一执行敏感查询参数脱敏。`http.NewRequest`、代理客户端创建、`client.Do` 和 WebSocket 拨号错误都会先替换并脱敏 URL，再写日志或向上返回。实际请求 URL 不变；仅可观测文本隐藏 `key`、`access_token` 等敏感值，避免 Vertex、Baidu 风格查询凭证泄漏。

## 6. 验证

- 验证缺少能力接口、能力未声明及 pass-through 均在 converter 调用前失败。
- 验证兼容入口对未声明能力的 adaptor 保持可用。
- 验证参数覆盖导致正文模型偏离目标时无法建立封印。
- 验证 URL 解析阶段修改目标模型会在网络前被拦截。
- 验证 WebSocket 拨号错误不会暴露查询凭证。
- 验证 HTTP 网络错误的日志输入和返回错误不会暴露查询凭证。
- 验证权威目标与 resolver 输入不包含 URL query，同时 query 变化仍会破坏封印。
- 验证原生 OpenAI/Gemini 的 opt-in 范围、目标解析与模型规范化。
- 验证 Gemini 跨协议媒体转换不声明离线能力。
- 验证 Vertex adaptor 未实现权威能力接口。
- 相关 relay 包普通测试和本阶段定向竞态测试通过。

全量 relay 竞态测试仍会触发已记录的既有问题：流扫描结束信号存在发送/关闭竞争，Gemini 测试并发修改 Gin 全局模式。两项均已保留在 `doc/todo.md`，不属于本阶段引入的回归。

## 7. 边界与后续

本阶段不启用自动压缩，不修改 controller 编排、主请求计费或生产配置。下一阶段 B-2c2c 将冻结完整候选集合，接入生产权威 tokenizer 与上下文上限适配器，并完成单次压缩、请求正文与 DTO 原子提交、逐候选终检和主预扣前失败关闭。
