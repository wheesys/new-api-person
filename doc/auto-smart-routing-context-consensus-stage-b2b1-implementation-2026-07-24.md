# 智能路由 ContextConsensus 阶段 B-2b1 实施报告

更新时间：2026-07-24

## 1. 实施结果

本轮完成 B-2b1：在 `service/contextconsensus` 建立进程内压缩子请求的纯执行契约。该契约不依赖 Gin、controller、relay、数据库或网络，因此不能向客户端响应 writer 写入数据，也不会误用主请求的渠道、正文、请求头或凭据。

B-2b1 是真实内部执行器的安全基础，不代表自动压缩已经可用。实际创建独立 `RelayInfo`、`BillingSession`、分层计费快照和请求输入，以及选择渠道并调用上游，继续由 B-2b2 完成。

## 2. 执行边界

`CompactionChildExecutor` 通过注入端口依次执行：

1. 校验父请求 ID、显式真实模型、策略版本、来源摘要和最大输出 token 上限。
2. 生成独立 child request ID，禁止与 parent request ID 相同。
3. 准备不透明子请求，并冻结准备结果摘要。
4. 独立预扣压缩调用额度。
5. 执行非客户端响应型 runner，返回结构化共识摘要、usage 和摘要 digest。
6. 独立结算实际额度。
7. 写入仅含安全元数据的父子关联审计。

执行器实例只能执行一次，状态转换由互斥锁保护。终态区分 `logged`、`rejected`、`refunded`、`settlement_failed`、`audit_failed` 和普通 `failed`。

## 3. 计费与失败语义

- 准备前拒绝不会预扣或执行子请求。
- 预扣失败、执行失败或结算失败时，仅在计费端口明确返回 `NeedsRefund` 时退款。
- 退款只作用于子调用的计费凭据，不接收或修改父请求计费对象。
- 结算成功后的审计失败不会退款，避免退回已经真实产生的压缩调用成本。
- 返回值独立记录预扣额度、结算额度、输入/输出 token、摘要 digest、审计状态和结果码。

## 4. 安全审计

审计记录只包含：

- 固定用途 `context_compaction`。
- parent/child request ID。
- 显式模型和策略版本。
- 来源、准备请求和摘要的 digest。
- token、最大输出上限、预扣/结算额度、状态和结果码。

执行契约不持有原始请求正文、请求头、API Key、TokenKey、Cookie、渠道密钥、摘要输入正文或 provider state，因此这些内容不会通过该审计对象泄露。

## 5. 阶段边界

B-2b2 仍需在 controller 层完成真实适配：构造全新且白名单化的内部上下文，创建独立 `RelayInfo`、`BillingSession`、`BillingSnapshot` 和 `BillingRequestInput`，限制显式压缩模型池，执行真实非流式上游请求，解析响应并记录独立消费日志。

B-2c 再接入权威 tokenizer、上下文上限、单次压缩编排、主请求 DTO/正文原子提交及最终失败关闭。在 B-2b2/B-2c 完成前，自动压缩继续保持默认关闭。

## 6. 验证范围

测试覆盖成功生命周期、虚拟模型提前拒绝、无效输出上限、各阶段失败的条件退款、成功结算后审计失败不退款，以及同一执行器禁止重复执行。

验证命令：

```bash
GOCACHE=/private/tmp/new-api-go-cache GOTMPDIR=/private/tmp go test ./service/contextconsensus -count=1
GOCACHE=/private/tmp/new-api-race-cache GOTMPDIR=/private/tmp go test -race ./service/contextconsensus -count=1
```
