# 验收记录：上游账单扫描收敛（2026-08-27）

- 范围：`a24593d perf(billing): bound upstream log scans`
- 结论：**通过**。

## 变更内容

1. 上游账单日的日志读取从「每成员渠道各发一次键集查询,合并排序后截断」
   改为**单次扫描**:`created_at` 日范围（`[日初,次日日初)` 半开,与单渠道
   路径同式）+ `(created_at>? OR (created_at=? AND id>?))` 键集续页 +
   `channel_id IN (成员渠道)` 过滤,`ORDER BY created_at,id LIMIT 2000`。
   每页一次查询,代价与渠道数解耦。
2. `uniquePositiveIDs`:成员渠道去重、剔除非正 id;空集合直接返回空页。
3. `ValidateBillingIndexes` 的排除条件删除——upstream_statement 任务执行前
   同样校验只读库 `(created_at,id)` 前缀索引,缺失直接报错不冒险全表扫
   （newapi 原生 `idx_created_at_id`,此前会话已从真实 schema 实证）。

## 核对要点

- **顺带修复旧实现的隐性分页风险**:旧代码按 `ID` 排序合并后截断,而游标
  按页末行 `(created_at,id)` 推进——ID 序与时间序不一致时页末行未必是键集
  最大值,理论可漏行。新查询严格按 `(created_at,id)` 输出,页末行即键集
  最大值,不重不漏。
- 占位符顺序核对:start/end/键集三值/IN 列表/limit,契约测试钉 9 占位符
  （3 渠道）并禁 OFFSET/BETWEEN/FORCE INDEX。
- 行扫描逻辑抽 `scanBillingLogRows` 与单渠道路径共用,投影
  （含 billingOtherProjection）完全一致,计费语义零分叉。
- 时间边界与既有单渠道/整站路径同为半开 `>=start AND <end`,跨日不重不漏。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库）;agent/internal
  未触及;前端无改动。
- 新增契约测试:多渠道查询形态、uniquePositiveIDs 去重。

## 部署观察点

- 首次生产上游账单重生成时,对多渠道日查询跑一次 EXPLAIN 确认命中
  `idx_created_at_id`（IN 过滤在索引扫描内回表滤,行为与整站扫描同级）。
- 若某站点只读库确缺 `(created_at,id)` 索引,上游任务将报错拒跑——属新
  预检行为,报错文案含索引要求,按提示在源库侧确认（CT 不改只读库）。
