# 验收记录：逐请求价格重建 + 内置完成倍率（2026-08-07）

范围：184ad75（codex 自主交付，server-only，非在途批次——billing-read-polish 批次仍待交付）。验收环境：Linux，Go 全量 vet+test 绿；真库冒烟栈复用实测。

## 结论：通过，零缺陷（本笔）；顺带实证发现两处老缺口记档

## 交付核验

- **逐请求倍率重建**：parseBillingCacheUsage 增提 model/completion/cache/cache_creation/group 五倍率（SQL 投影同步补齐五键——首查即核，无遗漏），PagedLogRecord 携带；工作簿请求明细页 RequestPrice 以 CT 价为基座、行内记录倍率覆盖派生单价——"newapi 实际收了什么倍率就按什么计"，与显式计价政策一致（行内有 cache_creation_ratio = newapi 真收了写费 → 计；无记录 → 沿用基座价含 0）；旧日志无倍率字段回退基座价（双向测试在）；
- **defaultCompletionRatio 内置表**：镜像 new-api 代码内置完成倍率（只读库看不到代码内默认值），未配 CompletionRatio 的模型回退价从恒 1× 改按模型前缀表（claude 5×/gpt-4o 4× 等）——方向正确；**表值本机无法证实，列为生产对账校准点**（回退金额主口径是 quota 换算，此表只影响单价显示与请求页参考价，风险有限）。

## 真库冒烟实证（复用 scratchpad 栈，配 CT 价 2.10/8.40/0.42 后导出）

- req-a1（anthropic 行）：读价 0.21=2.10×行内 0.1 ✓、写价 2.625=2.10×行内 1.25 ✓、**写费 2382×(2.625×1.6)/1M=0.010004——1h 档倍率与行内 cache_creation_ratio_1h=2 自洽互证** ✓、四项求和 ✓；
- req-a2（openai 行）：无行内倍率回退 CT 价、输入 600=1000−400 ✓、金额逐项 ✓；
- 汇总/日明细与改动前逐位一致（回归零漂移）。

## 顺带实证发现（老缺口，非本笔回归）

- **P2→批次**：请求明细页对未配 CT 价的模型 `SelectPrice !ok → continue` 静默跳行——整页可能为空且无任何提示（冒烟首轮即触发）。改动前行为相同，但随请求页价值提升该缺口变痛：建议并入 billing-read-polish 批次（回退价作基座而非跳行，或至少页尾标注"N 行未配价省略"）；
- P3：配价接口空 cache_write_price 能过校验（decimalOrZero）但 INSERT 空串进 DECIMAL 挂（update_failed）——validate/store 不一致，前端总发数字未暴露，顺手修候选；
- P3：导出任务按参数哈希缓存复用磁盘旧文件——**改价后同 job 再导出拿到旧结果**（job_id 已入哈希故重生成后无此问题；纯改价场景有）。缓存键纳入价格表更新时间或提供强制重导，记档。

## 部署

无迁移、无前端改动。rc34 不含本笔。
