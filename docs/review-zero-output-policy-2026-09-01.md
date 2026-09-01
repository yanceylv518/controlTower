# 验收记录：零输出订单过滤可配置（2026-09-01）

- 范围：`8740445 feat: configure zero-output statement filtering`
- 结论：**实现通过**；含一处默认行为反转且无 PROJECT_PROGRESS 条目，
  **待用户确认**（见末尾）。

## 变更内容

- 账单任务新增「排除输出 Token 为 0 的订单」勾选（063 迁移加
  billing_jobs.exclude_zero_output 列,按新迁移规则发新编号文件）。
- 勾选:零输出订单进异常桶（不写入客户账单）,异常原因 output_token_zero;
  不勾（默认）:零输出订单正常计费进账单。策略随任务创建时固化,重生成
  同参不变。
- request key 升 v2 纳入该标志——同对象同账期不同勾选是两个不同任务,
  可并存;前端查重条件同步。任务列表显示「完整账单/排除零输出」标签。
- AnomalyReasons 拆分:通用版只保留 output_token_missing,
  StatementAnomalyReasons 按任务策略追加零输出判定。

## 核对要点

- 零输出订单本身带实扣 quota（输入照收费）,计费路径 CalculateLogCharge
  对 completion≤0 跳过输出通道,重算仍与实扣核对——计入账单的金额是
  真实金额,非虚增。
- 边角:旧逻辑判 `<=0`,新策略只判 `==0`——负输出 Token（理论不存在）
  不再标异常,但输出通道计费同样跳过、差额会落核对差异桶,安全网在。
- 063 在 schema_migrations 台账下恰跑一次;ApplySQL 对 1060 重复列容忍
  兜底。真库套件含迁移重放通过。

## 默认行为反转（用户已确认:默认计入,2026-09-01 收档）

旧逻辑:输出 Token≤0 的订单一律判异常单,不进客户账单。新口径（用户
拍板）:**默认计入客户账单**,需要排除时创建任务勾选。同一账期按新默认
重生成,账单金额比旧口径高出零输出订单的实扣部分,属预期口径切换。
PROJECT_PROGRESS 条目建议 codex 补记。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库）;策略三分支
  单测钉住;webapp typecheck + build 通过;agent/internal 未触及。
