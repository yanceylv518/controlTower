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
