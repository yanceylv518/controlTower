# 验收记录：渠道读打磨批次 + 账单完整性核验（2026-08-07）

范围：f0c43ae（billing-read-polish 批次交付）+ 091f910（后台全量核验，自主扩展）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 绿；真库冒烟含正/负向实测。

## 结论：两笔通过，零缺陷

## f0c43ae 渠道 job 绑定 + 生成中状态（批次全落地）

- 渠道页四入口（抽屉明细/日账单 CSV/Excel 导出/异常导出）全部携带 job_id，blob 下载可报错；渠道 handler 测试 124 行；
- billingJobForRead 状态细分：运行中 → 409 `billing_generating` 带 progress 与 job_id；找不到才 `billing_not_generated`；导出任务错误透传自然带出两种码；
- 前端 billingReadErrorMessage 统一映射："账单生成中（N%），完成后重试"/"账单尚未生成"——当日事件的产品表现闭环；
- 流程记档：**自查清单又未贴**（上一批刚合规，反复）。

## 091f910 账单完整性核验（后台全量对账）

与对账页互补：对账页验"价格口径差多少"，核验任务验"生成过程没丢行没丢 quota"。039 三表幂等；POST /billing/verification 以源 job 创建 verify 任务（FOR UPDATE 串行防双建），复用生成任务的小时步进/分页/限速/断点框架重扫源日志，按当前 AnomalyReasons 分类，累加与游标同事务原子提交（无重放）；终局三源全外连对比（重扫 vs 该 job billing_daily_versions vs 该 job 异常单——**均钉源 job 非活跃版本**，日界三处同为上海日），五等式+内部自洽判 matched。

**冒烟实测**：正向——demo 三行全 matched，行数/quota 逐格一致（异常单 500 quota 正确对上）；**负向——篡改源日志 quota 后核验精准点名该行 mismatch（99999 vs 11000），其余行不受污染，默认 mismatches_only 只返回问题行**。

## 记档

- 核验分类用**当前**模型元数据（max_context），与生成时点之间若改过配置会产生"假 mismatch"——实为配置漂移检测特性，使用时需知情；
- 每个源 job 仅允许一次非 failed 核验，**无 force 重跑口子**——数据后漂（如 newapi 补写）无法二次核验，P3（可加 force 参数复用生成任务的 force 语义）；
- 核验=一次全量源库重扫（限速在，按需触发），大区间成本与生成同量级，勿高频使用——页面提示可后补，P3。

## 部署

迁移 039 幂等。rc35 不含本两笔及对账 B1 全套；**在途批次清零，可打 rc36**。
