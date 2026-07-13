# Codex 任务：M1-B3——告警事件时间线 + 通知强化

主线第三批（背景：`codex-batches-plan.md`、`development-plan.md` M1.3/M1.4）。两块内容：① 告警全生命周期落事件时间线（谁在什么时候确认/静默/恢复，Web 告警详情的数据地基）；② 通知投递补齐产品级能力（重试上限与指数退避、手动重发、钉钉加签）。

**文末有交付前自查清单，提交前逐项核对**——前两批的返工全部源于"实现完成但测试/子任务遗漏"。

## 背景速读

- 告警状态机分散在四处（`dashboard/alert_handler.go` + `mysqlstore/alert_store.go` + `ingest/memory_store.go` 对应方法）：`UpsertCurrentAlerts`（新告警插入 firing；resolved→firing 复燃）、`ResolveMissingAlerts`（→resolved）、`ExpireSilencedAlerts`（silenced→firing）、`UpdateAlertAction`（用户 acknowledge/silence/resolve）。`alerts.id` 是 sha1 hex 字符串。
- 通知：`dashboard/notification_handler.go` 的 `dispatchAlertNotifications` + `sendWebhookNotification`（失败固定 `next_attempt_at=now+5min`，无次数上限）；投递行由 `InsertNotificationDelivery` 以 (alert,channel) 派生 id upsert，attempts 自增；`NotificationDeliveryDue` 决定是否到期。`storage.NotificationChannel.SecretValue` 字段已存在但 API 未暴露。
- 认证中间件 `auth.RequireSessionOrToken`（M1-B1）——本批要往 request context 注入操作者身份。
- 迁移目录按字典序全量应用，新文件编号 004；`ApplySQL` 容忍重复建表/列错误（1060/1061），ALTER ADD COLUMN 可安全重放。
- e2e 脚本 `deploy/e2e-server.sh` 已覆盖登录+实例生命周期，本批继续生长。

## 硬性纪律

零新依赖；`agent/**`、`web/**` 不改；SQL 参数化；secret/密码不落日志不回显明文；UTF-8 无 BOM、LF；现有测试不删改；`make test` 与 CI 绿。

## 工作项

### 任务 1：迁移 004 + 告警事件写入

`server/migrations/004_alert_events.sql`：

```sql
CREATE TABLE IF NOT EXISTS alert_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  alert_id VARCHAR(64) NOT NULL,
  event_type VARCHAR(32) NOT NULL,
  actor VARCHAR(64) NOT NULL,
  note VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  INDEX idx_alert_events_alert (alert_id, created_at)
);
```

**事件类型与写入点**（只在**状态真正变化**时写，持续 firing 的例行 upsert 不写）：

| event_type | 触发点 | actor |
| --- | --- | --- |
| `firing` | UpsertCurrentAlerts 首次插入 | `system` |
| `refired` | UpsertCurrentAlerts 把 resolved 复燃为 firing | `system` |
| `acknowledged` / `silenced` / `resolved` | UpdateAlertAction | 操作者（见任务 3） |
| `silence_expired` | ExpireSilencedAlerts（silenced→firing） | `system` |
| `resolved` | ResolveMissingAlerts（自动恢复） | `system` |

**实现要点**：MySQL 侧 `UpsertCurrentAlerts` 目前是盲 upsert，无法感知变迁——改为同事务内先 `SELECT id,status FROM alerts WHERE id IN (...)` 建现状 map，再 upsert，再按差异批量插事件。`ExpireSilencedAlerts`/`ResolveMissingAlerts` 同理（先查受影响 id 再更新）。Memory store 对应实现。新增存储方法：

```go
InsertAlertEvents(events []storage.AlertEvent) error
QueryAlertEvents(alertID string, limit int) ([]storage.AlertEvent, error) // created_at 升序，limit 默认 100 上限 500
```

`storage.AlertEvent{ID, AlertID, EventType, Actor, Note, CreatedAt}`。

### 任务 2：时间线 API + 动作备注

- `GET /api/dashboard/alerts/{id}/events?limit=`：返回 `{items:[{event_type, actor, note, created_at}]}`（snake_case DTO，不裸序列化）。告警不存在返回空列表即可（事件表本身就是判断依据，不强查 alerts 表）。
- `AlertActionRequest` 增加可选 `note` 字段（≤500 字符，超长 400 `invalid_alert_action`），随 acknowledge/silence/resolve 写入事件。
- mux 注册（方法+通配模式），Dashboard 鉴权中间件后。

### 任务 3：操作者身份贯通

- `auth` 包：定义 `type actorKey struct{}` 与 `Actor(r *http.Request) string`；`RequireSessionOrToken` 在放行时把身份注入 request context——session 通道注入 username，token 通道注入 `"token"`。
- `HandleAlertAction` 用 `auth.Actor(r)` 作为事件 actor（取不到时回退 `"unknown"`，理论不可达）。
- 注意 import 方向：dashboard 已依赖 auth 则直接用；若产生循环依赖，把 Actor 读取函数放到独立小包或用约定的 context key 字符串（注明选择理由）。

### 任务 4：通知重试上限 + 指数退避 + 死信

- 配置 `CT_NOTIFICATION_MAX_ATTEMPTS`（默认 8，1~100，注册+校验）。
- `sendWebhookNotification` 失败路径：`next_attempt_at = now + min(30s × 2^(attempts-1), 1h)`，加 ±20% 随机抖动；当**本次失败后总 attempts ≥ 上限**时 status 置 `exhausted`（`NotificationDeliveryDue` 对 exhausted 返回 false——按现状"status 非 sent 且到期即 due"需要显式排除 exhausted，两个 store 同步改）。
- 注意现有 upsert 的 attempts 自增语义：退避基数用**库中真实 attempts**（Insert 后的值），说明实现取值方式。
- M1-B1 起的"resolved 告警投递过期释放"逻辑（`ExpireDeliveriesForResolvedAlerts`）保持不变，exhausted 行同样可被释放（新 episode 重新计数：释放时 attempts 归零——评估此处语义并在代码注释说明选择）。

### 任务 5：手动重发

- `POST /api/dashboard/notification-deliveries/{id}/resend`：将该投递 `status='failed'`、`attempts=0`、`next_attempt_at=now`，由通知 Runner 在下个周期投递；投递不存在 404 `delivery_not_found`。响应 `{ok:true}`。
- 新存储方法 `MarkDeliveryForResend(id string, now time.Time) (bool, error)` 双实现；mux 注册。

### 任务 6：钉钉加签

- `NotificationChannelRequest` 增加可选 `secret` 字段：仅 `channel_type=dingtalk` 时有意义，存入 `SecretValue`；**列表响应只回 `has_secret: true/false`，永不回显**。
- 发送时若 dingtalk 且 secret 非空：按钉钉加签规范在 webhook URL 追加 `&timestamp=<毫秒>&sign=<urlencode(base64(hmac_sha256(secret, timestamp+"\n"+secret)))>`（`crypto/hmac` 标准库）。
- 通用 webhook 的 `X-Control-Tower-Secret` 头行为不变。

### 任务 7：e2e-server.sh 生长

追加步骤（沿用现有 step 风格，失败即非零退出）：

1. 用实例 token 发一个 report（gzip），携带同一渠道的 3 条 `log_events`（type error）→ 触发 server 端 recent_errors 告警。
2. `GET /api/dashboard/alerts?status=firing` 找到该告警 id。
3. `POST /api/dashboard/alerts/action`（acknowledge + note="e2e"）→ `GET /api/dashboard/alerts/{id}/events` 断言含 `firing` 与 `acknowledged` 且 acknowledged 的 actor 为登录用户名、note 为 e2e。
4. 创建一个 dingtalk 渠道（带 secret，指向 `http://127.0.0.1:1`——必然失败）→ 触发通知后 `GET /api/dashboard/notification-deliveries` 看到 failed 行 → resend 端点 200。（通知 Runner 周期依赖运行配置，允许该步为"尽力断言"：拿不到投递行时打印 skip 而非失败，注明原因。）

### 任务 8：文档

`docs/api-contracts.md` 补时间线/重发/渠道 secret 三节；`docs/development-progress.md` 对应行更新。

## 测试要求

1. **事件写入**：新告警→firing 事件；持续 firing 例行 upsert **不产生**新事件（关键负断言）；复燃→refired；自动恢复→resolved(system)；静默过期→silence_expired；用户动作→对应类型且 actor/note 正确。MySQL 侧 SQL 契约测试按既有风格，行为主测走 memory store。
2. **时间线 API**：升序、limit 生效、DTO 字段名断言、不存在的告警空列表。
3. **actor 贯通**：session 通道动作事件 actor=用户名；token 通道 actor=token。
4. **退避与死信**：attempts=1/4/8 的 next_attempt 呈指数且带抖动（断言区间）；达上限后 exhausted；exhausted 不再 due；resend 后重新 due 且 attempts 归零。
5. **加签**：固定 secret+timestamp 下 sign 值与手算一致（表驱动）；无 secret 不追加参数；列表 has_secret 正确且响应无 secret 明文（负断言）。
6. mux 新路由断言。

## 交付前自查清单（提交前逐项确认，未完成项不得提交）

- [ ] 任务 1~8 每一项都有对应代码/文件（对照本文件逐节核对）
- [ ] 「测试要求」1~6 每一组都有对应测试函数（逐组核对，特别是负断言）
- [ ] 两个 store（mysqlstore + MemoryStore）实现同步
- [ ] `make test` 本地通过；push 后 CI 绿
- [ ] `git grep` 确认 secret/SecretValue 不出现在任何日志与列表响应
- [ ] api-contracts 与 progress 文档已更新；一个 commit：`feat(server): alert timeline and notification hardening (M1-B3)`

## 明确不做

- Web 界面展示（M2）；Agent 侧任何改动；通知渠道的删除端点（M2 需要时再加）。
