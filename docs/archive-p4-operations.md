# P4 核验、差异修复与封存版本

2026-09-21，本地实现；尚未部署到业务环境。P4 提供核验、不可变事实和封存后端，正式归档出账仍等待 P5 历史金额配置及 P6 账单任务。新版页面操作按 P7 接入，现有页面不会因为本次后端完成而自动出现封存按钮。

## 升级与账号

沿用 [P3 操作说明](archive-p3-operations.md)：暂停归档、等待批次和租约结束、停止旧写入器并隔离旧写权限，再升级 Agent。通过显式 `-archive-prepare` 将目标库迁移到 **019**；常规启动不执行结构升级。CT 复用 085–087 的数据集、队列和权限，本次不增加 CT 迁移。

| 目标迁移 | 表 / 变化 | 用途 |
| --- | --- | --- |
| 011 | archive_reconcile_runs | 每次核验的固定声明、schema、摘要、阶段与持久游标 |
| 012 | archive_reconcile_issues | 第二轮每个源 ID 的摘要及 missing_target / different / extra_target 差异 |
| 013 | archive_seal_builds | 封存任务、版本集合、分页构建与复核进度 |
| 014 | archive_billing_evidence_template | 空模板；生成 archive_billing_evidence_YYYYMM，不可变计费字节 |
| 015 | billing_facts_template | 空模板；生成 billing_facts_YYYYMM，不可变类型 2 事实 |
| 016 | archive_fact_issues | 每个事实版本的固定异常代码及证据引用 |
| 017 | archive_subject_index | 按日版本保存历史用户/令牌名字，检索必须关联 published 日版本 |
| 018 | archive_batch_receipts.cohort_dates_json | 同一行跨日期修正需要一起发布的日期集合 |
| 019 | archive_raw_repairs | 手动补扫的修复前后摘要、日期与批次审计，不保存原文 |

001–010 校验和不变。P1 预建的 archive_days / archive_day_versions 在本阶段开始用于正式冻结与发布。月表 DDL 位于事务外，实际写入和发布再次校验身份、结构、事务引擎及 writer 授权。

Agent 增加 `daily_verify_v4`、`day_seal_v4` 能力。Server 专用只读账号允许增加指定表级 SELECT：archive_reconcile_runs、archive_reconcile_issues、archive_fact_issues、archive_raw_repairs；未来账单按月授予 billing_facts_YYYYMM。仍拒绝原文、计费证据表、整库、写权限、角色和转授权。连接配置中只保存部署管理的环境变量引用，不从请求接受 DSN。

## 持久按日核验

全部接口要求登录、`archive.manage`；POST 使用现有 CSRF。所有路径都必须带匹配数据集的 `site_id`。

`POST /api/dashboard/archive-datasets/{dataset}/verify-tasks?site_id=...`：

```json
{
  "request_id": "11111111111141118111111111111111",
  "date": "2026-09-01",
  "assurance": {
    "stable_before_unix": "1788278400",
    "valid_until_unix": "1790784000",
    "evidence": "示例：站点运维已核实该时间之前历史不再变化，且在有效期内不会清理"
  }
}
```

这些是示例日期与声明，不代表已核实任何业务站点。`stable_before_unix` 覆盖所核验日期结束时刻，且不能晚于当前时间；`valid_until_unix` 必须在未来。声明含义是历史行稳定并在有效期内保留，包括迟到写入、修改和删除。P3 coverage/source_retained_from 仍必须覆盖该日；不能通过一次空扫描推导历史从未清理。

任务执行四次独立扫描：源、目标、源、目标，均以 `(created_at,id)` 分页，遵守行/字节/时间预算。持久摘要包括所有行及类型 2 数量、quota/Token 精确整数、每字段 NULL 数量和完整原文字节摘要。只有两轮源数据一致、两轮目标数据一致、源目标相同、目标 mutation revision 不变且声明有效才得到 matched。方法明确记为 `stable_window_paged`，不声称跨页一致性快照或 CDC。

GET 同一路径查看任务；`POST .../verify-tasks/{task}/retry` 对 mismatched/blocked 等可重试任务新开 attempt。重试不接受 force/matched 字段；过期声明必须用新 request_id 提交新声明，不能替换旧核验的条件。同一 request_id 的日期或声明变更被拒绝。

## 差异与修复

核验差异持久保存，源有目标无、两端不同、目标多余分别展示。手动 P3 `date_backfill` 可修复本次实际读取到的源 ID：包括贡献台账摘要相同但目标原文被删/改坏的情况。修复审计、原文、统计差额、日期修订、批次回执和游标在同一事务提交；先锁定并检查所有实际受影响日期的冻结状态。

自动 recent 扫描不执行该额外的目标损坏修复。目标独有行绝不自动删除；同 ID 在跨月目标已有额外副本时阻断，避免覆盖证据。实际目标行无法归日时记录 `repair_unscoped_target`，按源 ID 去重为全局阻断；后续成功修复同事务解除。修复后必须重新核验，旧 matched 不随数据变化继续有效。

## 封存、恢复与原子发布

`POST /api/dashboard/archive-datasets/{dataset}/seal-tasks?site_id=...`：

```json
{
  "request_id": "22222222222242228222222222222222",
  "dates": ["2026-08-31", "2026-09-01"]
}
```

日期集合最多 31 天，排序并拒绝重复。每一天必须存在当前 revision 的 matched 核验和仍有效的声明，且无全局阻断。源行从旧日移到新日时两天必须同时封存；跨月同样适用。旧 P2/P3 回执没有 cohort 字段时，保守地把 affected_dates 作为同批集合，可能需要把相关日期一起提交，不自动拆分。

流程如下：

1. 持当前 epoch 锁定元数据及全部日期，复查核验 revision 与完整日期集合，建立 building 版本并冻结写入。
2. 分页构建白名单计费证据、全部可归日类型 2 事实、固定异常和按版本历史身份索引；每页与目标进度同事务。未知、NULL、非法数据保留为明确状态；不读实时 users/tokens/options，不计算金额。
3. 再分页复核原文、每个事实字段及证据字节，检查数量、已知 quota 合计、NULL 计数和原文摘要守恒。事实的解析异常不会变成正常零值。合计是已知值之和，不能替代未来账单的逐行异常门禁。
4. 最终短事务只发布固定的日期/版本集合、规范化 manifest 摘要和同一个 catalog revision；building/abandoned 版本不成为 current。历史版本和已发布事实不改写。身份索引按版本分批建立，读取时必须 JOIN published 版本，不在发布事务扫描全日事实。

冻结标记不会因墙上时钟超时自动消失。重启/新 epoch 使用同一任务、attempt 和持久 build 继续，旧 epoch 不能写入。确定性构建失败将未发布版本标为 abandoned，并仅释放自己的冻结；显式重试取得新版本 ID。临时失败保留冻结和进度，CT 退避后恢复同 attempt；持有 BuildID 的 retry_wait 不允许手工增加 attempt，以免遗留旧冻结。

GET 同 `/seal-tasks` 列表查看结果。`POST .../seal-tasks/{task}/retry` 仅对允许重试状态生效。CT 接收报告必须匹配身份、会话、epoch、attempt 和单调进度；仅特定 running→retry_wait 操作状态可保持相同持久进度，不能顺便改核验摘要。重复退避上报不延长等待。队列与 P3 共用，旧近期任务有等待提升；Agent 给增量和任务扫描分配执行机会。

## 权威读取与后续账单

`GET /api/dashboard/archive-datasets/{dataset}/days/{date}/verification?site_id=...` 从专用归档只读连接返回最近 100 次核验、选定运行的差异及最近 100 个 published 日版本。指定 `run_id` 选择运行；差异每页 200 条，使用响应的 `next_issue_id` 作为 `after_id`，翻页必须同时固定 `run_id`，ID 全程用十进制字符串。

版本 manifest 校验内容摘要和身份/日期/版本/revision/run 绑定；接口不读取日志原文或证据 payload。任务上报只保存运行/版本引用，不是权威目录或可出账许可。`POST .../{dataset}/sync` 仍由 Server 从专用只读连接同步正式日目录。这里的接口固定返回 `archive_billing=false`。

P5 将增加历史金额单位/倍率/配置有效期；P6 才会绑定固定日版本、配置、算法和产物并生成账单，且需对事实异常逐项执行门禁。P7 接入页面核验/封存/队列。P8 负责旧数据显式导入、真实负载、恢复和单站灰度。

## 验证边界

本地测试覆盖真实 MySQL 9.7 的迁移、核验/修复/封存事务、epoch 接管、跨月原子发布和只读账号；实际执行记录见[任务交接](ai-tasks/2026-09-20-log-archive-billing-design.md)。没有对生产执行 DDL、归档写入、账单生成或部署。

实际规模的四轮扫描成本、历史 cohort 查询、每次核验逐 ID 摘要存储容量、真实源保留/迟到约束、生产 MySQL 版本、备份恢复和物理故障尚待验收。核验索引中的 equal 行用于分页恢复而保留，不计入差异数；本阶段没有自动清理其历史运行。源/目标旁路写入者必须被隔离，P4 不证明任意外部数据库篡改不存在。
