# 验收记录：图片 Token 计费对齐 NewAPI（2026-08-27）

- 范围：`f09b7d5 fix(billing): match NewAPI image token charges`
- 结论：**通过**。

## 变更内容

普通 Token 计费新增图片输入通道，使 CT 重算口径与 NewAPI 一致：

1. `parseBillingCacheUsage`（passthrough_handler.go）：
   - 新增投影键 `image_ratio`、`image_tokens`、`image_output_tokens`；
   - 键规范化：无显式 `image_input`/`image_tokens`/`image_output_tokens` 时，把历史键
     `image_output` 归入图片**输入**通道（`ImageInput`），`ImageOutput` 仅取
     `image_output_tokens`。
2. `normalizedBillingPromptTokens`（新抽出的纯函数）：基础 prompt 通道在扣除缓存
   （非 anthropic 语义）后再扣除图片输入 Token，最终一次性钳位到 0。
3. `CalculateLogCharge`（log_pricing.go）：`ImageInputTokens>0` 时要求
   `image_ratio`，按 `inputUnit × image_ratio` 计费并入 total；新增
   `ImagePrice`/`ImageAmount` 通道字段。仅作用于 ratio 计费路径（per_request
   模式提前返回，不涉及）。
4. tiered 表达式：`img`/`img_o` 因键规范化而取到正确通道（tiered_expr.go 使用
   `ImageInputTokens`/`ImageOutputTokens`，此前 `image_output` 被误入 `img_o`）。

## 源码实证（newapi-src，标准做法：对接外部数据结构必须以源码为准）

- `service/text_quota.go:264` `summary.ImageTokens = usage.PromptTokensDetails.ImageTokens`
  —— 图片 Token 取自**输入侧** details。
- `service/text_quota.go:486` `other["image_output"] = summary.ImageTokens`
  —— 日志键名为 `image_output`，但实际承载图片输入 Token（commit 前提成立）。
- 计费公式：`baseTokens = baseTokens.Sub(dImageTokens)`；
  `imageTokensWithRatio = dImageTokens.Mul(dImageRatio)`；
  `promptQuota = base + cachedWithRatio + imageWithRatio + cacheCreateWithRatio`，
  再整体乘 `modelRatio × groupRatio`，负数钳位为 0 —— 与 CT 新实现逐项一致。
- anthropic 语义：newapi 不从 base 扣缓存但**仍扣图片**，CT 的
  `normalizedBillingPromptTokens` 同序处理，一致。
- `image_ratio` 与 `image_output` 在 newapi 中总是成对写入
  （`if summary.ImageTokens != 0` 块），故 CT 端 `requiredLogRatio` 强制存在合理；
  缺失时落 `billing_price_incomplete` 异常桶而非算错。
- tiered：`service/tiered_settle.go:37-39` `img = PromptTokensDetails.ImageTokens`、
  `imgO = CompletionTokenDetails.ImageTokens`，与 CT 通道映射一致。

## 测试

- 单测：`TestCalculateLogChargeMatchesNewAPIImageBilling` 用真实生产样例
  （prompt=130983、cache=128768、image=3432、quota=191388）位级对账通过；
  `TestCalculateLogChargeRequiresImageRatio` 验证缺 ratio 落 incomplete；
  `TestParseBillingImageUsagePreservesNewAPILegacySemantics` 与
  `TestNormalizeBillingPromptMatchesNewAPIImageCalculation` 覆盖键规范化两分支。
- 全量：server / agent / internal 三棵树 `go vet` + `go test` 全绿；
  server 树在 `CT_MYSQL_TEST_DSN`（ct_direct_smoke 实库）下全绿。
- 前端无改动，未跑 pnpm。

## 注意事项

- **既有账单口径**：包含图片请求的历史账单日在重新生成前仍按旧口径（图片 Token
  留在基础输入通道、无 image_ratio 通道）；此前这类行大多因 quota 对不上落
  `billing_quota_mismatch` 异常桶。需要新口径的账单日按需重新生成（已记入
  PROJECT_PROGRESS.md 2026-08-27 条目）。
- 已知边界（非本次引入）：newapi 的音频单独计价（`audio_input_seperate_price`）
  CT 未实现对应通道，此类行会落 mismatch 异常桶，属既有安全网行为。
