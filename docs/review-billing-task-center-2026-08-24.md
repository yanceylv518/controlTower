# 验收记录：账单任务中心+已生成账单+生成闸变更（2026-08-24）

范围：75bcaca（codex 交付,20 文件）。验收环境：vet+server 全量测试、typecheck+build、**新 server 二进制实跑+两新页面截图实证**（后台任务中心/已生成账单,烟测库真实任务数据渲染核）。

## 结论：实现通过,零缺陷;**一项产品语义变更待用户确认（强制重生成被移除）**

## 交付面

- 后台任务中心页（/billing/tasks,admin）:全站点切换/状态/进度/错误列;任务列表接口（status 过滤,limit≤200,admin 闸）;
- 取消任务:DELETE 接口（admin,仅 pending/running）,步骤+任务同事务置 failed"cancelled manually";**runner 每步后复读任务状态,取消=当前小时步跑完即停**,已完成部分保留（用户账单页"停止生成"按钮接线）;
- 已生成账单页（/billing/generated）:complete 任务列表按类型分 Tab,"查看账单"跳转对应账单页并钉 job;
- **covered=1 读模式**（好设计）:账单查看支持"已生成大区间的任意子区间"——按 job 的 billing_hourly 子范围重聚合（业务时区日切）,summary 响应带 data_from/data_to 实际数据边界;
- 生成预检两道新 409:`billing_job_busy`（全局单任务串行,带 active_job）与 `billing_range_already_covered`（带 covering_job）;api-contracts 已更新;httpError 工具统一错误文案。

## ⚠️ 产品语义变更（须用户拍板确认）

**`billing_range_already_covered` 检查在 force 分支之后无条件执行,SQL `range_from<=? AND range_to>=?` 含等区间——强制重生成自此不存在**：任何被完成任务覆盖的区间,force=true 也 409。同区间重复请求走 request_key 复用返回旧任务（不重算）。后果：

1. 改价/口径升级后的"重生成出新版本"路径断路（账单版本化的既有语义被收窄为"生成后不可变"）;
2. **rc63+ 部署清单里的"重生成账单一次(令牌数据)"在本笔之后无法执行**——若生产尚未做该步,须**先在旧版完成重生成再升级到含本笔的版本**,或要求 codex 恢复 force 旁路;
3. 完成任务无删除界面/接口,覆盖区间的重算没有任何逃生口。

若"账单生成后不可变+任务中心显式管理"正是你的产品意图,确认后本记录即收档（部署顺序警示保留）;若仍需重生成能力,需补 force 旁路或完成任务删除。

## 部署

无新迁移。**部署顺序警示见上第 2 条**。
