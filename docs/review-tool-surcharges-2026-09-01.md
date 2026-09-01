# 验收记录：工具附加费纳入计费重算（2026-09-01）

- 范围：`56a7cdc fix: include tool surcharges in billing verification`
- 结论：**通过，零缺陷**。

## 问题与修复

newapi 对内置工具调用（web_search / google_search 等）按次收附加费并计入
实扣 quota,日志写 `other.tool_surcharges`=[{name,count,price}]。CT 重算
此前不认这笔钱,含工具调用的请求 quota 恒对不上落核对差异桶。

修复:投影补 `tool_surcharges` 键,`addToolSurcharges` 在三种计费模式
（per_request / token / tiered）统一追加附加费,LogCharge 增
ToolSurchargeAmount 通道;modernSnapshot 判据同步纳入该键。

## 源码实证（newapi）

- `text_quota.go:38` `other["tool_surcharges"] = items`,结构
  {name,count,price}（price float,货币价/1000 次）——键名与结构吻合。
- `calculateTextToolCallSurcharge`:surcharge = price×count/1000×
  groupRatio×QuotaPerUnit——CT 先算货币额（price×count/1000×group）再于
  finishLogCharge 统一乘 qpu,代数等价。
- newapi 在 UsePrice 与 ratio 两分支都加 `ToolCallSurchargeQuota`,与 CT
  的 per_request/token 双覆盖一致;阶梯路径由真实样例钉住。
- newapi 端 merge 重名工具仅影响展示,求和不变,CT 直接累加等价。

## 边界

- 附加费不乘模型倍率,只乘分组倍率——两边一致。
- 无效 JSON / 非正价格 → 报错落 billing_price_incomplete 桶（安全网）;
  count≤0 条目跳过。
- 空/null/[] 直接跳过零开销。

## 测试

- 新增:固定价+附加费（0.02+0.01=0.03 位级）、阶梯+附加费（真实样例
  quota=2089843 位级对账）。
- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库）;agent/internal/
  前端未触及。
- 含工具调用请求的账单日重生成后该类行从差异桶归位正常单。
