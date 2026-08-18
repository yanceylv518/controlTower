# 验收记录：大工作簿导出稳定化——按目标切换游标形态（2026-08-17）

范围：813e385（codex 交付）。验收环境：Linux,vet+server 全量测试绿（改动纯 server 侧,agent/前端无涉）。

## 结论：通过,零缺陷;一条部署后动作

## 核验

- **游标形态分流**：单用户/单渠道导出（工作簿请求明细页的逐页拉取）改纯 id 主键游标+`ORDER BY l.id`——旧形态每页都对同一时间范围做过滤+按 (created_at,id) 排序,大区间导出页页重复该开销;新形态配合 newapi 的 (user_id,id)/channel_id 索引可索引序直走。**无筛选任务保留时间游标**（三个 SQL 形态测试分别钉住 channel/user/unfiltered 三分支,并断言半开区间、无 OFFSET/BETWEEN）;
- 两分支的占位符与参数顺序逐一核对一致（id 分支:start,end,id,user?,channel?,limit;时间分支:start,end,created,created,id,...）;游标推进复用既有 LogCursor,id 分支只消费 ID 字段,首页零值语义正确;
- 可观测性補齐：渠道工作簿按 overview/daily/request_details/write 四阶段标注失败日志（含此前被静默吞掉的 `book.Write` 错误）、导出任务失败带耗时与参数、翻页错误包裹页号与游标位置——正对"大导出失败无从排查"的痛点;
- 行为记档：单用户/单渠道导出的请求明细行序从时间序变为 id 序（newapi id 近似时间序,乱序提交处偶有邻行交换）,展示层差异,可接受。

## 部署后动作

对生产 newapi 库跑一次单渠道导出翻页 SQL 的 EXPLAIN（`WHERE type=2 AND created_at range AND id>? AND channel_id=? ORDER BY id LIMIT`）,确认走 channel_id 或主键索引无 filesort——与日志列表选路的既有 EXPLAIN 惯例同批做即可。

## 跟进（cb422c5,2026-08-18,验收通过零缺陷——用户导出游标形态部分反转）

生产实测反馈:**用户导出退回时间键集**（(created_at,id) 复合游标）,仅渠道导出保留 id 主键游标——注释记载原因:大 logs 表上用户行分布稀疏时,newapi 对有界时间范围的规划优于纯 id 走查（前天"(user_id,id) 索引直走"的假设被生产证伪,SQL 形态测试已相应反转）。**连带正确性已核**:昨日请求明细端点只传 after_id,退回时间游标后 CreatedUnix=0 会致 `created_at>0` 恒真、翻页原地踏步——本笔同步补 after_created 复合游标参数+next_after_created 回传+前端跟进,处理完整。另:账单翻页超时 30s→2min（对齐既有明细读路径上限,测试钉住）;用户工作簿补四阶段失败日志（与渠道工作簿对齐）。教训延续:**索引假设必须以生产 EXPLAIN 实证,"应该能走索引"不算数**——单渠道导出 EXPLAIN 仍在部署后清单上,现在多一条用户导出时间键集的 EXPLAIN 一起做。
