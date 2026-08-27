# 验收记录：现代 NewAPI 缓存语义修正（2026-08-27）

- 范围：`e6b9758 fix(billing): preserve NewAPI cache semantics`
- 结论：**通过**。同日形态守卫的第三次收窄，方向一致：形状推断只服务无计费元数据的旧日志。

## 问题

a42a40e 排除了多模态行后，纯文本现代行仍有漏网：OpenAI 语义下上游按未调整
前缀计数上报时 `cache_tokens` 可略超 `prompt_tokens`（实例 18688 vs 18686，
无图片）。newapi 对此按 OpenAI 处理——base 钳位 0、只收缓存通道；CT 形态守卫
却据此误判 anthropic，重算时保留完整 prompt 通道再叠加缓存通道，普通输入
重复计入，quota 对不上落异常桶。

## 修正

守卫再加条件：行内带请求级计费快照（model_price/model_ratio/completion_ratio/
cache_ratio/三档 creation ratio/group_ratio/image_ratio/billing_mode/expr_b64/
matched_tier/request_rules 十三键任一非空）即不做形状推断。与
`billingCacheUsage` 的快照字段清单逐一核对，覆盖完整。显式标记
（`usage_semantic=anthropic` / `claude=true` 双标记解析器都认）始终优先。

## 验证

- 修后重算与 newapi 位级一致：openai 路径 prompt=18686−18688 钳 0，仅收
  缓存通道，与 newapi 同式钳位吻合，该类行回归正常账单。
- 钉死场景全保留：298/8507 无快照裸行仍触发守卫判 anthropic
  （`TestUnmarkedAnthropicShapeKeepsInputLane`）；2026-08-05 生产校准样本
  （双标记+全快照）走显式标记不受影响。
- server（含 CT_MYSQL_TEST_DSN 真库）/ agent / internal 三树 vet+test 全绿。
  无迁移无前端。

## 记档取舍

- 残余窗口：若存在「带 ratio 快照+双标记皆无+真 anthropic 语义」的历史行
  （某些旧 newapi 版本理论可能),现按 openai 钳位后落 mismatch 异常桶,
  实扣 quota 保留,属安全网降级非静默错账;与守卫误伤现代行相比是正确取舍——
  现代行是持续增量,历史行有限且可辨识。
- 与前两笔同口径：受影响账单日（含缓存溢出 prompt 的行）重新生成后采用新语义。
