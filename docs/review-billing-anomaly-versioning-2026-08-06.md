# 验收记录：异常单按 job 版本化 + 账单导出修正十笔（2026-08-06）

范围：7c4fa7f..7dd3667 十笔（codex 自主交付，无批次文件）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 全绿。

## 结论：通过，验收修正一处 P1（035 迁移每启动全表重建 + 注释分号会炸启动）

## 逐笔核验

- **7c4fa7f 异常单按 job 隔离**：主键 (instance_id,source_log_id) → (job_id,instance_id,source_log_id)，异常单随账单版本化——重生成不再互相覆盖 job_id，用户/渠道 job 各自留档；查询/导出锁定区间的最新 complete job（无完成任务 → 空结果，与 409 语义一致）；ON DUP 不再改写 job_id 正确。**顺带闭环 P3：计数查询 WHERE job_id 走新主键前缀，此前记档的"无 job_id 索引全表扫"消失**。记档：被取代 job 的异常行永久留存（与 billing_daily_versions 同为版本化设计，量级=异常单×重生成次数，暂无清理）。
- **467d85c 异常金额列**：计数 SQL 加 SUM(reference_amount)，用户/渠道/明细三处 big.Rat 累加显示"异常参考金额"；
- **00c88ec 用户账单生成进度恢复**：summary 无完成任务时也返回 generation_job（此前重构丢了进度条）；
- **c1ff043 操作列固定**：fixed="right"；
- **85c751a 导出任务错误透出**：新 httpError 工具解析响应体错误码，导出失败不再只有笼统文案；
- **27ff826 导出任务鉴权头**：POST 补 X-Requested-With（RequireSessionOrToken 的 CSRF 门槛），修"创建导出任务失败"；
- **1c52b29 轻量导出**：include_requests=0 跳过逐请求页（大用户导出提速），任务 id 哈希纳入该参数避免与全量任务撞缓存——正确；
- **7987684 工作簿合计列对齐**：缓存写列加入后合计行错位，改 18 格显式行；
- **0e77c48 阶梯开关按 job 快照**（036 迁移，CREATE TABLE IF NOT EXISTS 幂等）：任务创建时快照 per-user 阶梯设置 + user 0 哨兵行区分旧任务回退——断点续跑/中途改设置不再产生混合口径；fillAnomalyAmounts 判档改 ContextTokens（与正常行判档一致）；
- **7dd3667 工作簿分类对齐**：导出侧复用 AnomalyReasons/SourcePromptTokens/RequestContextTokens（导出符号+注释+测试），消灭导出与生成两套判定的漂移面。

## 验收修正（P1，我直接修）

**035 迁移原写法 `DROP PRIMARY KEY, ADD PRIMARY KEY` 每次启动都成功执行 = 每次启动全表重建**——ApplyDir 无 applied 记录、全量重放靠 1060/1061 容错，成功语句无从拦截；与 010 迁移 ENGINE 重钉同反模式（migrate_test.go 留有当年教训）。修法：同一条 ALTER 搭载哨兵列 `pk_job_scoped`——重放时 ADD COLUMN 撞 1060、整条 ALTER 原子失败被容忍，零重建。**修正过程中自查出第二个雷**：我初版注释含分号，splitSQLStatements 按分号切割会把纯注释块当语句发 MySQL（1065 空查询不在容忍列表 → 启动失败）——注释已去分号并写明约束，契约测试收紧为"恰好切出一条语句 + ADD COLUMN 与 DROP PRIMARY KEY 同条"（与运行时语义一致，两个雷都锁死）。035 未进任何 tag、生产未应用，就地改写无兼容负担。

## 部署

- 迁移 035（PK 版本化）/036（阶梯快照）均自动执行；035 首次执行对 billing_anomaly_orders 做一次表重建（表小，秒级）；
- **rc31（5a7135a）不含本记录全部内容**——如未开始部署建议直接打 rc32 收齐；若 rc31 已部署，升级 rc32 后异常单需随账单重生成一次刷新 job 归属（request_key 仍 v5 不变，账单本身无需再重生成——但旧 job 的异常单在新主键下按历史留存，页面只显示最新 complete job 的，行为自洽）。
