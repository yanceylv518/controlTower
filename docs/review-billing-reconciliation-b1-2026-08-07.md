# 验收记录：对账 B1（2026-08-07）

范围：55e1e63（codex 按批次 codex-task-billing-reconciliation-b1.md 交付；**自查清单首次完整贴进 commit message，合规好转记档**；插队于 billing-read-polish 之前交付，记档）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 绿；真库冒烟栈全链路数值级验证。

## 结论：通过，验收修正两处 P1 + 一处 P3

## 交付核验（冒烟数值全部逐位核对）

- **L1**：用户差额行 + 三类分解卡——bob 异常单 0.001 落 anomaly ✓、alice 缓存写估算 0.023820=2382×10/1M ✓、合计残差=总差−两分量 ✓、分类取最大绝对分量 ✓；
- **L2**：day×model 行分解与 L1 口径一致 ✓；异常单独立成行（claude-x/anomaly 标签）✓；
- **L3**：job_id 必填、范围钉死单用户×单日×单模型、有界扫描 truncated 如实；重建路径逐位对——rebuilt 0.051794 手算一致，四泳道差额（含 write 0.023820 **与 L1/L2 政策估算跨层同值**）✓；缺 completion_ratio 的行如实标 unexplained 不硬算 ✓（守卫按设计工作）；
- **CSV**：L1+L2 平铺+分类小计+BOM ✓；job 绑定/假 job 409 有测试 ✓；回退计价行 diff 恒零、标记、沉底排序 ✓。

## 验收修正（我直接修）

1. **P1 缓存写分解项双计**：分解项算的是"newapi 写费全额估算"而非"超出 CT 已收部分"——模型在 newapi 配了 CreateCacheRatio 且 CT 配价时（两边都收写费，真实政策差≈0），残差被错拉成负的全额写费，恰好污染"残差恒零"守护性质。修复：`estimate − ctCacheWriteCharged`（镜像 Amount() 的 5m/1h 泳道），CT 写价为 0 时退化回全额估算。守卫测试：两边同价时分解项=0。
2. **P1 有符号差额被静默丢弃（单根因四症状）**：`decimalRat` 是拒绝负数的校验解析器，被用于解析有符号差额——①rebuild_residual 只计"实扣>重建"一侧（冒烟实测 0 应为 0.027794）；②泳道分量合计丢负项；③折扣分组（ratio<1）下 GroupDiff 恒 0（两负数相减被拒）；④L1/L3 排序对负差额行按 0 处理。修复：新增 `signedRat` 用于全部有符号场景，回归测试钉负泳道分量与双向残差。修后冒烟 rebuild_residual=0.027794 逐位吻合。
3. **P3 标签泄漏**：用户粒度行携带首个聚合行的 day/model/group 标签（多模型用户 JSON 误导，前端未显示故 UI 无感）——用户粒度置空。

## 记档

- decimalRat/signedRat 二分法值得全仓复查：其他用 subtractDecimal/decimalRat 处理可能为负值的地方（本批已全改，历史代码待抽查）——P3；
- L3 重建基座=行内 model_ratio（非 CT 输入价），批次文件手工验证预期值 0.010004 系我笔误（按 CT 价基座算），实际正确值 0.023820——批次文件不改，此处更正留档；
- 冒烟数据 quota 与倍率故意不自洽 → 残差非零属预期，恰好验证了自检有效性。

## 部署

无迁移。rc35 不含本批与修正；billing-read-polish 批次仍在途，齐了一起打 rc36。
