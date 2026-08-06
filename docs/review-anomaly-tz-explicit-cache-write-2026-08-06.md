# 验收记录：异常单导出时区 + 缓存写显式计价（2026-08-06）

范围：eedda46（异常单 CSV 时区）+ 48185bd（缓存写回退价改显式制）。验收环境：Linux，Go 全量 vet+test 绿；无前端/迁移改动。

## 结论：通过，零缺陷

## eedda46 异常单 CSV 导出改上海时区

请求时间列从 UTC 裸值改 `CreatedAt.In(billing.BusinessLocation)` 显示——清掉时区 P3 家族一处（异常单导出时间偏 8h）。带 handler 级测试（UTC 06-30 16:30 → 上海 07-01 00:30，含 BOM 剥离与 CSV 解析断言）。time.Local 残留其余三处（价格生效日期/导出 month/旧 Rollup）仍在 P3 清单。

## 48185bd 缓存写回退价 1.25 → 0（显式配置制）

**对 2445cf8 的政策反转，经用户确认为其本人指示（2026-08-06 问询确认）**：未在 newapi 配置 CreateCacheRatio 的模型，缓存写一律计 0 元——宁可少算并在 quota 对照列暴露差额，不隐式发明价格。决策含义记档：此类模型（典型为未配倍率的 claude 模型）CT 账单会低于 newapi 实际扣费，差额可从对照列看到；要计费需在 newapi 配 CreateCacheRatio 或在 CT 计价页显式配 cache_write_price。

- request_key 升 v5-explicit-cache-write-price：正确——计价政策变化必须使旧账单失效；v4 从未部署，无叠加代价，**从 rc26 直接升级仍只需一次账单重生成**；
- 测试同步完整（默认 0 断言 + 显式配置 1.4→5.6 换算断言保留）；计价页 `cache_write_price_configured` 标注（2445cf8 引入）在显式制下语义更关键——导入时未配置模型的写价为 0 属预期而非缺失。

## 部署

纯 server 改动。**rc30（e344ded）不含此两笔**——rc30 刚打即被超越，如未开始部署建议直接打 rc31 收齐（内容差异仅此两笔小修，rc30 部署了也无碍，只是异常导出时间偏 8h + 缓存写默认 1.25 口径，升 rc31 后账单因 v5 需重生成）。
