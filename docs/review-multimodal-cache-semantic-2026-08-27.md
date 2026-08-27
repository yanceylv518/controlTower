# 验收记录：多模态缓存语义修正（2026-08-27）

- 范围：`a42a40e fix(billing): avoid duplicate multimodal input charges`
- 结论：**通过，零缺陷**。系 f09b7d5 图片计费的直接跟进。

## 问题

`resolveBillingCacheSemantic` 的形态守卫（3d2c172 引入）以「cache 通道 > prompt」
推断无标记行为 Anthropic 语义。该推断只对纯文本成立（OpenAI 缓存必为 prompt
子集）；多模态行的图片/音频通道与 prompt/cache 计数存在重叠，OpenAI 语义下
cache 也可能合法超过 prompt。误判成 anthropic 后 prompt 通道不做减法，
基础输入与缓存通道叠加 = 普通输入重复计费。

## 修正

守卫追加条件：四条多模态通道（ImageInput/ImageOutput/AudioInput/AudioOutput）
全为 0 才允许形态推断；显式标记（`usage_semantic=anthropic` / `claude=true`）
不受影响照常生效。多模态无标记行按 OpenAI 处理：减缓存减图片后钳位 0。

## 源码实证

- newapi `service/text_quota.go`：`other["image_output"]` 的写入与
  `other["usage_semantic"]="anthropic"` 在同一函数同一版本——凡能产生多模态
  键的 newapi 版本，Claude 语义行必带显式标记。「多模态且无标记 ⇒ 非
  anthropic」不存在漏判面。
- newapi OpenAI 路径源码注释明确承认 cached+cache_write 可超过 prompt
  （unadjusted prefix counts），其处理是减法后钳位 0——与 CT 修正后行为一致；
  旧行为（改判 anthropic）反而偏离 newapi 实收。
- 纯文本形态守卫保留：历史生产钉死样例（prompt=298/cache=8507 无标记
  anthropic）回归测试仍通过。

## 测试

- 新增 `TestMultimodalCacheOverflowDoesNotInferAnthropic`
  （cache=18688 > prompt=18686 且带图片：判 openai、prompt 钳 0）；
  既有图片对账样例改为过 `resolveBillingCacheSemantic` 全链路断言。
- server 树 `go vet` + `go test`（含 CT_MYSQL_TEST_DSN 真库）全绿；
  agent / internal 两树全绿。前端无改动。

## 注意事项

- 与 f09b7d5 同口径：含图片/音频请求的历史账单日需重新生成才采用新语义；
  此前误判行大多因 quota 对不上落 mismatch 异常桶，重生成后应归位。
- 理论边角：若存在「无标记+多模态+真 anthropic」的历史行（现版源码证实
  不会产生），会按 openai 钳位后落 mismatch 异常桶，属安全网行为非静默错账。
