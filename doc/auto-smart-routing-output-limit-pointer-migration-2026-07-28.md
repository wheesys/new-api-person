# 智能路由旧渠道输出上限指针语义迁移报告

更新时间：2026-07-28

## 1. 实施结果

本轮完成旧渠道输出上限字段审计和迁移。OpenAI 请求中的输出上限现在沿用 `GetMaxTokensPointer()` 的统一优先级：`max_completion_tokens` 存在时优先于 `max_tokens`，字段缺失保持 `nil`，显式 `0` 保持非空指针并继续发送到上游。

本轮未新增接口、配置、数据库字段或管理员功能。

## 2. 渠道迁移

- Baidu：已有 `*int` 目标字段改为按源字段存在性赋值；显式 `0` 不再被丢弃，供应商要求的 `1 → 2` 最小值规范化保持不变。
- Cloudflare：`max_tokens` 改为 `*uint`；缺失时省略，显式 `0` 保留。
- Cohere：`max_tokens` 改为 `*uint`；仅源字段缺失时沿用既有 `4000` 默认值，显式 `0` 不再被默认值覆盖。
- Ollama Chat/Generate：按指针存在性写入 `options.num_predict`；缺失时不写，显式 `0` 写入 `0`。
- Xunfei：嵌套 `parameter.chat.max_tokens` 改为 `*uint`，保持缺失与显式 `0` 的差异。
- AWS Nova：`inferenceConfig.maxTokens` 改为 `*uint`，并支持 `max_completion_tokens` 优先语义；显式 `0` 会创建并保留推理配置。

## 3. 审计边界

全渠道扫描同时确认：

- Mistral、Perplexity、Zhipu 4V 已通过源字段存在性条件创建指针，能够保留显式 `0`，无需迁移。
- Claude 与 Gemini 已使用指针输出上限字段。
- OpenRouter 的 `reasoning.max_tokens` 是推理预算，不是请求输出上限，本轮不调整。
- Responses 的响应字段、Embedding 的 `num_predict` 及 token 计数元数据不属于“客户端输出上限转发”范围，本轮不扩大修改边界。

## 4. 验证

新增六组确定性回归测试，覆盖字段缺失、显式 `0` 以及新旧输出字段优先级；Ollama 同时覆盖 Chat 与 Generate 两条转换路径。

以下验证通过：

```bash
go test ./relay/channel/baidu ./relay/channel/cloudflare ./relay/channel/cohere ./relay/channel/ollama ./relay/channel/xunfei ./relay/channel/aws -count=1
go test -race ./relay/channel/baidu ./relay/channel/cloudflare ./relay/channel/cohere ./relay/channel/ollama ./relay/channel/xunfei ./relay/channel/aws -count=1
go test ./... -count=1
git diff --check
```
