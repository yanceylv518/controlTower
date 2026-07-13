# Codex 返工任务：M1-B3 验收未通过项

对 `b712086` 的 review：以下已通过——actor context 贯通、指数退避（确定性抖动设计良好）、exhausted 语义与 resolved 释放归零、resend 双实现与 404、钉钉加签与 has_secret 掩码、时间线 API 与 note、**MemoryStore 的全部转换事件**、004 迁移、已交付的三组测试。

以下 3 项返工。完成后一个 commit：`fix(server): M1-B3 rework per review`，**并把文末自查清单逐项填好粘贴进 commit message 正文**（本批两处遗漏均为清单明列项，清单未被执行）。

## R1（致命）：MySQL 侧系统转换事件完全缺失

MemoryStore 写了 firing/refired/silence_expired/resolved 事件，但 `mysqlstore/alert_store.go` 的三个方法**原样未动**——生产（MySQL）时间线将只有用户动作事件，系统事件全部丢失。双 store 行为必须一致：

1. `UpsertCurrentAlerts`：同一事务内先 `SELECT id, status FROM alerts WHERE id IN (...)`（按传入 alerts 的 id 集合，参数化 IN），建现状 map；不存在的 → 事件 `firing`；现状 resolved 的 → 事件 `refired`；再执行现有 upsert；事件与 upsert 同事务提交。
2. `ResolveMissingAlerts`：先 `SELECT id FROM alerts WHERE status <> 'resolved' AND id NOT IN (...)` 拿受影响集合，再 UPDATE，再批量插 `resolved`（actor=system）事件，同事务。
3. `ExpireSilencedAlerts`：先查 `status='silenced' AND silence_until <= ?` 的 id 集合，再 UPDATE，再插 `silence_expired` 事件，同事务。
4. 空集合时不要生成 `IN ()` 空括号 SQL（现有 ResolveMissingAlerts 的占位符拼接方式可参考）。
5. **SQL 契约测试**按 `alert_store` 既有测试风格补齐（断言语句含 SELECT 前查与事件 INSERT）。

## R2（任务 7 缺失）：e2e-server.sh 生长

按原任务 7 原文实现（report 触发 recent_errors 告警 → 查 firing 告警 → acknowledge 带 note → 时间线断言 firing+acknowledged、actor=登录用户名、note=e2e → 通知渠道 + resend 尽力断言）。注意 report 需要 gzip 与实例 token（脚本里已有 heartbeat 函数可参考扩展）。

## R3（小项）

1. `HandleAlertEvents` 补 `h.alertStore == nil` 守卫（同文件其他 handler 均有）。
2. 补两组规格测试：①「持续 firing 的例行 upsert 不产生新事件」负断言（memory 与 mysql SQL 契约两侧）；②「actor 贯通」——经 `RequireSessionOrToken` 包裹的 alert action，session 通道事件 actor=用户名、token 通道 actor=token（httptest 全链路）。

## 交付前自查清单（逐项填 [x] 并粘贴进 commit message）

- [ ] R1：mysqlstore 三方法先查后写、事件同事务、空 IN 处理、SQL 契约测试
- [ ] R2：e2e-server.sh 新增告警时间线段落且本地跑通（无环境则注明）
- [ ] R3：nil 守卫 + 两组测试
- [ ] 双 store 行为一致（对照 memory store 逐事件核对）
- [ ] `make test` 本地绿；push 后 CI 绿
