# 验收记录：viewer 导航收紧 + 账单区间/监控细节修正（2026-08-05）

范围：7e3d30b（viewer 导航）+ 83ac4d3（账单区间与监控细节）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 全绿。

## 结论：通过；验收方补一处测试；一个产品问题待拍板

## 逐项核验

### 账单时区与区间语义（83ac4d3 核心）
- **BusinessLocation = Asia/Shanghai 固定时区**：生成（parseBillingInputRange）与查询（billingPeriodQuery from/to 与 month 分支）全部统一到上海时区解析——存取对称已核验（LatestBillingJob 以 range_from/range_to 精确匹配，两侧同源解析，UTC 容器上无 8 小时错配）；
- **区间统一为半开 [from, to)**：删掉了旧的 +1s/+1天 收尾补偿；页面显示同步改为 `[from, to)` 记法并区分"选择区间"与"下方数据区间"；
- **request_key 版本升级 v2→v3**：旧任务作废一次、新算法下相同任务仍复用——正确的失效方式。**注意：升级部署后，旧版本生成过的账单会显示"未生成"，需重新生成**（生产尚未部署账单功能，无实际影响）；
- NewJob 小时步进用 `Truncate(time.Hour)`，时区无关（上海为整小时偏移），安全；billing_hourly 写入统一 `.UTC()`，active_versions 按上海日切——一致；
- **兜底读路径删除**：summary/detail 无完成任务时返回空，workbook/channel-workbook 返回 409 billing_not_generated——与前端"未生成"状态一致，杜绝了"显示的数字与所选区间不符"这类误导。

### summary 缓存
缓存键含 jobID（`period+":"+jobID`），任务完成后键自然翻转，无失效遗漏——比显式失效更稳的设计。

### 日志页统计拆分
- `/passthrough/logs/stat` 独立端点（15s 超时，列表查询同步放宽到 15s）；列表与统计并行加载，统计失败不阻塞列表（前端 Promise.allSettled + 独立告警条）；审计 passthrough.logs.stat 在；
- **越权矩阵测试未覆盖新端点，验收方补齐**（GET 200 / POST 403 两例）；
- **行为变更记档：日志筛选从 LIKE 模糊改为精确匹配**（username/token_name/model_name/request_id）——与 newapi 语义一致且可走索引，但用户输入部分名称将查不到结果。

### viewer 纵深修正
- metricUserID 显式找 `user` 段取 ID（原实现取末段，`inst:channel:5` 会被误当 user 5 放行、`inst:user:9:model:x` 会被误排除）——真实的 scope 修正，有跨维度测试；
- DimensionDetailView：viewer 不再请求 alerts/samples（非白名单端点，原先 Promise.all 一起失败导致页面报错），慢样本/告警两个 Tab 对 viewer 隐藏——正确；
- viewer 导航改为独立三项清单（客户监控/用户管理/使用日志），路由守卫同步收紧。

### 调权工作台
- 渠道分组列上线（channel_current.group_name 021 迁移原生存在，agent 一直在报，SQL 安全已核验）；模型列表按 auto>observe>off + 渠道数排序；系数与计算权重红绿着色；渠道按优先级/权重排序。

## 问题与记档

- **P2 已闭环（2026-08-05 用户拍板："现在不让 viewer 看用户账单了"）**：确认为有意收紧。验收方同步收掉服务端白名单的 billing/summary+detail 放行，矩阵测试改断言 403，设计文档 §4 记录决定。viewer 可见范围最终定格为客户监控、用户管理、使用日志三页。
- P3：导出任务把 workbook 的 409 原因折叠为通用 "export generation failed"，用户看到的是"导出任务失败"而非"该区间账单尚未生成"；
- P3：time.Local 残留四处未随本次统一——billing_config_handler（价格生效日期）、billing_log_export_handler（month）、billing_models_handler（当日零点）、billing_handler（旧 Rollup 回填，本身是清理候选）。UTC 容器上价格生效日边界会偏 8 小时，配价功能上线前建议统一到 BusinessLocation；
- P3：QueryBillingAggregates / QueryBillingChannelAggregates 的区间版本已无 handler 调用（仅测试 fake 依赖接口），清理候选。

## 部署

rc26 已被用户打在 7e3d30b——**不含 83ac4d3（本批主体修复），建议弃用重打**（与 rc24 同类情况）。
