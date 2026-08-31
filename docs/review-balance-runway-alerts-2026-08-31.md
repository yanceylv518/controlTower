# 验收记录：账单文件日历日期修复 + 用户余额续航告警（2026-08-31）

- 范围：`f7a8fa4 fix: preserve billing file calendar dates` + `2ff05a3 feat: add user balance runway alerts`
- 结论：**两笔均通过**，附两条 P3 记档。

## f7a8fa4 账单文件日历日期修复

日文件登记表（billing_channel_daily_files / billing_user_daily_files）的
bill_day 原以 `time.Time` 直传驱动——序列化时区随连接配置走,上海零点可能
变成前一天 16:00 UTC,DATE 截断后错一天;且读写两侧格式化不一致（渠道读
已格式化、用户读传裸值）。修正:新增 `billingCalendarDate` 统一按业务时区
格式化日历字符串,写入/查询四处全部收口;测试钉住上海零点的 UTC 瞬间跨界。

影响:生产此前若 CT 库连接无 loc 参数,已登记的日文件 bill_day 可能偏移,
症状为按日下载明细 404;**受影响账单日重生成后自愈**（与既有重生成要求
同批）。

## 2ff05a3 用户余额续航告警（三条 PROJECT_PROGRESS 拍板条目对应）

- **061 迁移**:balance_alert_user_settings（站点+用户显式启用,默认无人
  参与;CREATE TABLE IF NOT EXISTS 单语句幂等,真库双重放核过）。
- 数据路径全部有界:余额=站点只读连接单次 `users` 全表读（5s 超时）;
  速度=CT 自库 metric_5m 的 instance_user 维度 72h 聚合,**不扫源站日志**;
  结果缓存 5 分钟。
- 判据:续航=余额/日均消费;默认 <7 天 warning、<3 天 critical、样本
  <10 次不预测、余额耗尽直接 critical;停用用户跳过;阈值/开关设置页可调。
- 投递:复用告警确认/静默/恢复/Webhook 链路;级别变化释放已发送投递重新
  通知（alert_store 状态比较扩 severity）;`CT_NOTIFY_BALANCE_ONLY`
  默认开——外发渠道只投余额告警,告警中心仍显全部。
- 权限:启用/关闭仅 admin(PUT 有角色闸);viewer 被中间件路径白名单挡。
- 未配只读连接的站点返回空列表不报错,不扩大告警中心失败面。

## P3 两条当日已修（755d27f,用户发令我实施）

1. **失败面**:单站点设置/只读查询失败改为逐站点降级——沿用该站点上一轮
   缓存的余额告警+记日志,其余站点与整个告警周期照常;不再整体返回 error。
   降级期间沿用旧值也避免了「告警被误 resolve 又重新 firing」的抖动。
2. **空启用站点开销**:enabledUsers 无任何启用条目时直接跳过该站点的只读
   `users` 查询,未参与预警的站点零源站开销。
   两条各有单测钉住（失败降级保留缓存项/未启用站点禁止调用源查询）。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库,061 重放核过）;
  webapp typecheck + build 通过;agent/internal 未触及。
- 新增单测覆盖:续航判级、启用门槛+最小样本、日历日期跨界。
