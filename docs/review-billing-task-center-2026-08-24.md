# 验收记录：账单任务中心+已生成账单+生成闸变更（2026-08-24）

范围：75bcaca（codex 交付,20 文件）。验收环境：vet+server 全量测试、typecheck+build、**新 server 二进制实跑+两新页面截图实证**（后台任务中心/已生成账单,烟测库真实任务数据渲染核）。

## 结论：实现通过,零缺陷;**一项产品语义变更待用户确认（强制重生成被移除）**

## 交付面

- 后台任务中心页（/billing/tasks,admin）:全站点切换/状态/进度/错误列;任务列表接口（status 过滤,limit≤200,admin 闸）;
- 取消任务:DELETE 接口（admin,仅 pending/running）,步骤+任务同事务置 failed"cancelled manually";**runner 每步后复读任务状态,取消=当前小时步跑完即停**,已完成部分保留（用户账单页"停止生成"按钮接线）;
- 已生成账单页（/billing/generated）:complete 任务列表按类型分 Tab,"查看账单"跳转对应账单页并钉 job;
- **covered=1 读模式**（好设计）:账单查看支持"已生成大区间的任意子区间"——按 job 的 billing_hourly 子范围重聚合（业务时区日切）,summary 响应带 data_from/data_to 实际数据边界;
- 生成预检两道新 409:`billing_job_busy`（全局单任务串行,带 active_job）与 `billing_range_already_covered`（带 covering_job）;api-contracts 已更新;httpError 工具统一错误文案。

## 产品语义变更（已按用户方案闭环）

**`billing_range_already_covered` 检查在 force 分支之后无条件执行,SQL `range_from<=? AND range_to>=?` 含等区间——强制重生成自此不存在**：任何被完成任务覆盖的区间,force=true 也 409。同区间重复请求走 request_key 复用返回旧任务（不重算）。后果：

1. 改价/口径升级后的"重生成出新版本"路径断路（账单版本化的既有语义被收窄为"生成后不可变"）;
2. **rc63+ 部署清单里的"重生成账单一次(令牌数据)"在本笔之后无法执行**——若生产尚未做该步,须**先在旧版完成重生成再升级到含本笔的版本**,或要求 codex 恢复 force 旁路;
3. 完成任务无删除界面/接口,覆盖区间的重算没有任何逃生口。

**用户拍板方案（同日实施,验收方直接修）**:covered 拦截保留但改为确认交互——server 端 covered 检查对 force=true 放行（测试钉:无 force 409 带 covering_job/force 直通 202）;前端用户/渠道两账单页捕获 billing_range_already_covered 弹确认框（含覆盖区间信息,"重新生成"按钮带 force 重提)。**用户随后追加拍板"查看与生成职责分清"（同日实施）**:①covered 闸挪到 request_key 复用之前——精确同区间也进弹框,复用只剩防并发重复提交的本职（同区间在途任务仍幂等返回）;②弹框改三选一:查看账单（主按钮,用户页走 covered 子区间读/渠道页钉覆盖任务并对齐区间）/重新生成（force 重提）/关闭取消;③工具栏"强制重新生成"按钮删除(与弹框重复)。**烟测环境实测:无 force→409 带 covering_job,force→202 新任务**;测试钉 force 直通。重生成路径回归,rc63 部署顺序警示解除。

## 部署

无新迁移。部署顺序警示已随 force 旁路恢复解除;含本笔与闭环修正需新 tag。

## 跟进（542e863,同日,验收通过零缺陷——异常单拆令牌+covered 明细读补全）

- **047 增量迁移**（单语句,真库经 ApplyDir 双重放核过,token 两列+job/user/token 索引）:异常单落 token_id/token_name——令牌批次"异常单不拆令牌"的记档项由用户追加指令补上;令牌汇总/日账单/CSV 增"异常订单数/异常金额"列,与用户级口径对齐;
- **covered 子区间读补全**:明细抽屉与令牌视图对覆盖任务做超集校验+ForJobRange 子范围聚合,异常计数同步按区间过滤——上一笔只做了 summary 的 covered 模式,本笔把读路径补齐;
- 记档:旧任务异常行 token_id=0,重生成后拆分归位（与 token_data_missing 同一故事,部署后重生成一次全解）。

无新 tag 前累计:任务中心系列（75bcaca+两轮拍板闭环 082cbac/3dc7e10）+本笔,迁移 033-047。
