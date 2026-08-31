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

## P3 记档（两条,建议下批带上）

1. **失败面**:`currentAlerts` 中余额告警出错整体返回 error——某站点
   已配置的只读库宕机期间,整个告警中心与通知轮空转。建议逐站点降级
   （出错站点跳过+日志,其余照常）,与货币读取降级同型。
2. **空启用站点开销**:即使站点无任何启用用户,每 5 分钟仍对其只读库跑
   `SELECT ... FROM users` 全表（无 LIMIT）。用户表巨大的站点可能稳定
   超 5s 超时并触发上条失败面。建议 enabledUsers 为空时跳过余额查询,
   两条一并缓解。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库,061 重放核过）;
  webapp typecheck + build 通过;agent/internal 未触及。
- 新增单测覆盖:续航判级、启用门槛+最小样本、日历日期跨界。
