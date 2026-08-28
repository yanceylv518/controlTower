# 验收记录：账单分页与日聚合修正（2026-08-28）

- 范围：`b5a485a fix: correct billing pagination and daily details`
- 结论：**通过**。

## 变更一：统一时间键集分页（推翻用户/渠道 id 游标路径,理由成立）

原用户任务路径 `FORCE INDEX(idx_user_id_id) AND l.id>? ORDER BY l.id`
存在真实缺陷:游标从 0 起,(user_id,id) 索引扫描从该用户**历史第一行**开始,
created_at 账期范围只是行级过滤——老用户+近期账期时首页要空扫全部历史行
（rc62 用户导出终版靠「时间窗转 id 窗」规避了这一点,本扫描路径当时没带
下界）。渠道 id 游标同理。

修正:三条路径（整站/单用户/单渠道）统一
`(created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id`,
由 created_at 索引直接定位账期窗口,代价被窗口封顶。FORCE INDEX 全部移除。

成本取舍记档:单用户账单日在繁忙站点=对当日全站行的一次有界扫描
（user_id 过滤需回表）,较导出路径的 id 窗方案读放大;正确性优先,慢页
日志（>10s）会暴露;若生产单用户任务显著变慢,可把导出的时间窗转 id 窗
方案移植过来。

## 变更二：索引预检放宽为「created_at 打头任意索引」

原双档要求（用户任务 (user_id,id)/其余 (created_at,id)）放宽为任意
created_at 打头索引。依据:InnoDB 二级索引隐含主键后缀,单列 created_at
索引等效 (created_at,id),支撑时间键集;CT 不要求客户改 newapi 索引。
语义成立。昨日 PROJECT_PROGRESS 条目写的「(created_at,id) 前缀」表述
现略有过时（内涵一致）,本笔未更新该条目。

## 变更三：日聚合键补 day、BillDay 按日志实际业务日

用户/令牌/渠道三张聚合 map 的键加入 `dateOnly(log.CreatedUnix)`（业务
时区）,行 Day 与 RequestDetail.BillDay 同源——步进窗口日对齐时为幂等
加固,任何非对齐窗口下都不会再把跨日行折进 step.From 的日期。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库,语句任务套件
  覆盖新查询形态）;契约测试同步改写（时间键集/无 FORCE INDEX/7 占位符）;
  agent/internal/前端未触及。

## 部署观察点

- 无 FORCE INDEX 后优化器自主选路:生产首次生成单用户/单渠道账单时
  EXPLAIN 确认走 created_at 索引;若优化器误选 user_id 索引导致回归,
  再评估是否恢复提示。
