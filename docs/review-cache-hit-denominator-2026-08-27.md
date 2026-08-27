# 验收记录：缓存命中率分母修正（2026-08-27）

- 范围：`d643b23 fix cache hit rate denominator`
- 结论：**通过**。触及 agent + server + internal 三树，**需升 agent 才完整生效**。

## 问题

缓存 Token 比率（调权缓存因子与监控页共用）以 `prompt_tokens` 为分母。
Anthropic 语义下 `prompt_tokens` 只含未缓存输入，缓存读/写不在其内——分母
偏小，比率虚高甚至超过 1；大输入门槛（512）也按偏小的 prompt 判定，漏计
「未缓存部分小、缓存大」的请求。

## 修正

- `cachemetrics.PromptTotal(prompt, cache, normalizedTotal)`：归一化分母，
  外加 `total ≥ cacheTokens` 下限，数学上比率封顶 1。
- agent 解析器新增 `CachePromptTokens`：优先取日志内显式
  `input_tokens_total`；无则对 `claude=true` 行按
  `prompt + cache_tokens + cache_creation_tokens` 重建；其余行回退 prompt。
- agent/server 两条聚合路径分母与大输入门槛都改用归一化总量。
- 读路径 `ClampRate`：历史落库的 >1 比率显示时钳到 [0,1]。

## 源码实证

- newapi `service/text_quota.go:514-518`：`input_tokens_total` 仅在转换链路
  提供可靠归一化总量（`billingUsage.InputTokens` 且带 usage source 标记、
  非 Claude 格式）时写入——CT 优先信任它、不自行推断的取向与 newapi 注释
  告诫（"Do not infer it from prompt/cache fields"）一致。
- newapi `service/billing_usage.go:137`：Claude 归一化总输入 =
  `input + cache_read + cache_creation`——agent 的 claude 回退公式与之逐项
  相同（cache_creation_tokens 为 5m/1h 之和的总量,不重复计）。

## 测试

- 新增：显式 `input_tokens_total` 优先、claude 行重建 1050、agent 聚合用
  归一化分母且畸形行封顶 1、server 回落同断言。
- 三树 vet + test 全绿（server 含 CT_MYSQL_TEST_DSN 真库）。前端无改动。

## 记档

- **server 回落聚合分叉（P3,结构性）**：`storage.LogEvent` 不携带 `other`,
  回落路径 `PromptTotal` 只能传 nil——anthropic 行分母取
  `max(prompt, cache)`,较 agent 真实总量仍偏小（比率偏高但不超 1）。主路径
  为 agent 上报,回落仅在旧 agent/缺报时使用;与既有「回落聚合与 agent 口径」
  记档同族。
- **部署后缓存因子一次性重排属预期**：分母变大 + 大输入门槛改按总量判定,
  anthropic 重缓存渠道的比率会明显下降（更真实）,调权缓存因子随之重排,
  与既往口径切换同性质。
- 历史落库比率不回改,读路径钳位仅影响显示;新数据自然覆盖。
