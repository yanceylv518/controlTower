# Codex 任务：对账 B1——独立对账页（L1 差额 + L2 三类归因 + CSV）

背景：design-billing-reconciliation.md（2026-08-07 定稿，首版=三类分类器最小版）。数据全部现成：聚合行 quota=newapi 实际扣费事实值，CT 金额查询时现算，异常单已有 actual_amount。**无新迁移、不碰 newapi 生产库、不需要重新生成账单**。

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck` + `pnpm build`。**

**撞车提示**：billing-read-polish 批次（渠道 job 绑定+生成中状态）可能在途。本批次尽量新文件承载（reconciliation handler/页面独立），若 billingJobForRead 已被该批次扩展出 billing_generating 语义，直接复用。

## 设计

### server：GET /api/dashboard/billing/reconciliation

参数：instance_id、from、to、可选 job_id（billingJobForRead 语义：校验实例/类型/精确区间，无完成任务 409 billing_not_generated）、可选 user_id（给 L2 下钻）。

**L1（无 user_id）**返回：

- 每用户行：ct_amount（现行 BuildSummary 口径）、actual_amount=quota/QuotaPerUnit、diff=actual−ct、diff_rate；
- 合计卡：total_ct / total_actual / total_diff + **三类分解**：
  1. `anomaly`：该 job 异常单 actual_amount 合计（BillingAnomalyCountsForJob 已有金额，异常单不进聚合行，故为 actual 侧独立已知项）；
  2. `cache_write_policy`（估算，响应标注 estimated）：聚合行 cache_write_5m/1h tokens × 估算 newapi 写价（快照 ratio 现算输入价 × CreateCacheRatio 配置值；未配置按 newapi 内置默认 5m=1.25、1h=2——与生产样本 ratio 互证过）；
  3. `residual`：total_diff − anomaly − cache_write_policy——**期望恒零区**，非零即漂移信号。

**L2（带 user_id）**返回该用户 day×model×group 行：ct_amount、actual_amount、diff、cache_write_policy 估算、residual、标签（三类中占比最大者）。

分类器为纯函数（billing 包），不做 SQL 内计算。

**诚实声明必须写进代码注释与页面**：未配 CT 价的模型 ct_amount 走 quota 换算回退——与 actual 同源，diff 恒零无信息；对账分解只对 CT 配价模型有意义。响应对每用户/每行附 `fallback_priced` 标记，页面淡化显示这类行。

### web：独立对账页 /billing-reconciliation（admin only）

- 站点+区间选择（复用 billingRange 快捷项与校验）+ job 绑定语义与账单页一致（读 generation_job.id 传 job_id）；
- L1：三类分解卡（残差非零标红）+ 用户差异表（按 |diff| 降序，含 fallback_priced 淡化）；
- 行点击 → L2 抽屉（day×model 表，分解列+标签）；
- "导出对账 CSV"（L1+L2 平铺+分类小计，BOM 头照旧）；
- 菜单入口挂账单组；viewer 无需白名单改动（默认拒绝即可）。

## 接线点（逐个核对，不得遗漏）

新 handler + mux 注册（protect）、复用 QueryBillingAggregatesForJob（全量与单用户两口径）/BillingAnomalyCountsForJob/BillingRatioSnapshots/billingJobForRead、分类器纯函数+估算价推导、路由+菜单+页面、api-contracts 增补、CSV、审计（operation_audits 记 reconciliation 查询可选——与账单页现状对齐即可）。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck` + `pnpm build`；新增测试：分类器（配价模型三类分解数值断言、回退模型 diff 恒零且标记 fallback_priced、残差=总差−两已知项）、handler 绑 job/假 job 409、CSV 列头；
2. 手工（真库冒烟栈：scratchpad MariaDB 33061 / ct-server 18090，已有 demo 站点与已生成账单）：对账页 L1 三类卡数值与手算一致（异常单 0.001 应落 anomaly 类）；配 CT 价后 diff 变化立现（不重生成）；L2 抽屉行分解合计=该行 diff。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 残差=总差−异常−缓存写估算，三处（卡/L2 行/CSV）口径一致
- [ ] 回退计价模型 diff 恒零并标记，页面淡化，不污染 Top 差异排序
- [ ] job 绑定与账单页同语义（无完成任务 409，不静默兜底）
- [ ] 一个 commit：`feat(server,web): billing reconciliation page (B1)`

## 明确不做

L3 请求级下钻（二期）；阈值告警（后置）；账单页/渠道账单页任何改动（避撞 billing-read-polish）；xlsx 报告（CSV 够用）；价格配置差/分组倍率差细分类（并入残差，残差非零时再立项细分）。
