# Codex 任务：M1-B4——渠道命令闭环 + API 硬化 + 契约冻结（M1 收官）

主线第四批，M1 最后一批（背景：`codex-batches-plan.md`、`development-plan.md` M1.5/M1.6）。完成后 M1 进入阶段点验证，API 契约冻结。

**文末自查清单必须逐项填好并粘贴进 commit message**（B3 返工起的强制机制，继续执行）。

## 背景速读

- **命令契约两端已存在，只缺 Server 侧闭环**：`agentgateway/contracts.go` 的 `AgentHeartbeatResponse.Commands []ChannelCommand`（id/type/channel_id/status/weight/priority 指针语义）与 `AgentReportRequest.CommandResults []ChannelCommandResult`（id/channel_id/status/error/applied_at）；Agent 侧 `executeCommands` 自 v1.0 就绪（生产默认 `CT_NEW_API_CONTROL_ENABLED=false`，安全）。Server 目前从不下发命令、忽略 CommandResults。
- `operation_audits` 表已在 001 迁移（id/instance_id/operation_type/target_type/target_id/actor_id/before_summary/after_summary/created_at + 索引）。
- heartbeat 入口：`ingest.Service.SaveHeartbeat`（返回 lastLogID），handler 组装 `AgentHeartbeatResponse`；report 入口 `SaveReport`。
- actor 身份：`auth.Actor(r)`（M1-B3）。登录端点在 `auth/handlers.go`；账号级锁定已有，**IP 级限流没有**。
- 渠道快照保留清理已有先例：`main.go` 的 `startChannelSnapshotRetentionRunner`。
- 迁移新文件编号 005；e2e 脚本继续生长；mux 用 Go 1.24 方法+通配模式。

## 硬性纪律

零新依赖；`agent/**`、`web/**` 不改；SQL 参数化；双 store（mysqlstore + MemoryStore）同步实现；UTF-8/LF；现有测试不删改；`make test` 与 CI 绿。

## 工作项

### 任务 1：迁移 005 + 命令存储

```sql
CREATE TABLE IF NOT EXISTS channel_commands (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  command_type VARCHAR(32) NOT NULL,
  payload_json TEXT NOT NULL,
  status VARCHAR(16) NOT NULL,
  created_by VARCHAR(64) NOT NULL,
  error_summary VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  INDEX idx_channel_commands_instance (instance_id, status)
);
```

`storage.ChannelCommand` 模型 + 存储接口（双实现）：

```go
CreateChannelCommand(storage.ChannelCommand) error
// 取该实例全部 pending 并原子置为 delivered（同事务/同锁），返回取到的命令
ClaimPendingCommands(instanceID string, now time.Time) ([]storage.ChannelCommand, error)
CompleteChannelCommand(id string, status string, errorSummary string, now time.Time) (storage.ChannelCommand, bool, error)
ExpireStaleCommands(before time.Time) (int, error)  // pending 且 created_at < before → expired
QueryChannelCommands(query storage.ChannelCommandQuery) ([]storage.ChannelCommand, error) // instance/status 过滤 + limit/offset
```

状态机：`pending → delivered → succeeded|failed`；`pending --超时--> expired`。

### 任务 2：命令下发 API

`POST /api/dashboard/channels/{channelID}/commands`，body：

```json
{"instance_id":"inst-x","confirm":true,"status":2,"weight":10,"priority":5}
```

- `confirm` 必须为 true，否则 400 `confirm_required`（人工确认是本项目的硬边界：自动调权默认关闭）。
- `instance_id` 必须存在（404 `instance_not_found`）；status/weight/priority 至少一个非空（400 `invalid_command`）；channelID 从路径取且 >0。
- 命令 id 用 `crypto/rand` 16 字节 hex；`command_type` 固定 `channel.update`；payload_json 存变更字段；`created_by = auth.Actor(r)`。
- 响应 201：命令 DTO（snake_case：id/instance_id/channel_id/status/payload/created_by/created_at）。
- `GET /api/dashboard/channel-commands?instance_id=&status=&limit=`：列表 DTO。

### 任务 3：heartbeat 下发与 report 回收

- `SaveHeartbeat`：成功 upsert agent 后 `ClaimPendingCommands(instanceID)`，命令转换为 `agentgateway.ChannelCommand`（payload_json 反序列化出 status/weight/priority 指针）放入 `AgentHeartbeatResponse.Commands`。**先 `ExpireStaleCommands(now - 过期时长)` 再 claim**（过期命令绝不下发）。
- `SaveReport`：遍历 `req.CommandResults`——`CompleteChannelCommand(result.ID, result.Status 归一为 succeeded/failed, result.Error, now)`；命中后同步插入 `operation_audits`：id=命令 id、operation_type=`channel.update`、target_type=`channel`、target_id=渠道 id 字符串、actor_id=命令的 created_by、before_summary=空串、after_summary=payload_json + 执行结果摘要、created_at=now。未知命令 id 忽略（幂等：Agent 缓冲重发同一结果不得重复插审计——以命令当前状态判断，已终态则跳过）。
- 配置 `CT_COMMAND_EXPIRY_MINUTES`（默认 10，1~1440，注册+校验）。

### 任务 4：审计查询 API

`GET /api/dashboard/operation-audits?instance_id=&limit=`：按 created_at 降序，DTO snake_case（operation_type/target_type/target_id/actor_id/after_summary/created_at）。双 store 查询实现（MemoryStore 需补 operation_audits 的存取——若 001 时代 memory store 已有则复用）。

### 任务 5：登录 IP 限流

`auth`：登录端点按**客户端 IP** 限流——每 IP 每分钟最多 10 次登录请求（无论成败），超出 429 `rate_limited`；内存滑动窗口（互斥锁保护，定期清老条目防泄漏）；IP 取 `r.RemoteAddr` 去端口（**不要信任 X-Forwarded-For**，注释注明原因：反代部署时由反代层另行限流）。与账号级锁定叠加、互不替代。

### 任务 6：数据保留清理

- 配置：`CT_RETENTION_DETAIL_DAYS`（默认 30：log_events、log_samples、metric_1m）、`CT_RETENTION_METRIC5M_DAYS`（默认 90）、`CT_RETENTION_RUNTIME_DAYS`（默认 7：server_metrics、health_checks、docker_statuses）。均 0=关闭该组清理，负数拒绝。
- 存储方法 `PruneBefore(kind string, cutoff time.Time) (int64, error)`（或分方法，注明选择）双实现；`main.go` 起每日 runner（首次启动后 1 分钟先跑一轮，之后每 24h），日志输出各表清理行数。
- alerts/alert_events/notification_deliveries/channel_commands 本期不清（体量小、审计价值高），注释注明。

### 任务 7：e2e-server.sh 生长

追加：① 未带 confirm 下发命令 → 400；② confirm 下发（status=2）→ 201 取命令 id；③ 模拟 Agent heartbeat（实例 token）→ 响应 JSON 断言含该命令 id；④ 模拟 report 回传 `command_results`（succeeded）→ `GET /api/dashboard/channel-commands` 断言 succeeded；⑤ `GET /api/dashboard/operation-audits` 断言含该命令的审计行（actor 为登录用户名）。

### 任务 8：契约冻结与文档

- `docs/api-contracts.md`：补齐**全部** Dashboard 端点章节（认证、实例、指标/历史/用量、日志样本、运行态、告警+时间线+动作、通知渠道/投递/重发、渠道命令、审计），每端点含方法/路径/参数/响应示例；文首加横幅：`Dashboard API v1 — 契约冻结（2026-07-13）：此后仅允许向后兼容的新增，禁止修改既有字段语义`。
- `docs/development-progress.md`：M1 相关行全部更新；P5 阶段状态改为已完成。

## 测试要求

1. 命令存储：pending→claim 原子置 delivered（二次 claim 为空）、complete 幂等（终态不重复）、过期只影响 pending、查询过滤。
2. 下发 handler：confirm 缺失 400、实例不存在 404、全空字段 400、created_by=actor、DTO 字段名断言。
3. heartbeat/report 集成（ingest service 测试）：heartbeat 返回已 claim 命令且过期命令不下发；report 回传写终态 + 审计一次且仅一次（重发结果不重复审计）。
4. IP 限流：同 IP 第 11 次 429、不同 IP 独立、窗口滑过后恢复；与账号锁定并存。
5. 保留清理：边界（恰好 cutoff 的行为，注明含或不含）、0 天关闭不删、各 kind 独立。
6. mux 新路由断言；e2e 无法本地跑通时在 commit message 注明原因。

## 交付前自查清单（逐项填 [x] 粘贴进 commit message）

- [ ] 任务 1~8 逐节核对（特别是任务 7 e2e 与任务 8 契约文档——历史上最常漏）
- [ ] 测试要求 1~6 每组有对应测试函数
- [ ] 双 store 同步（对照接口逐方法核对）
- [ ] `go vet ./...` + `go test ./...` 本地绿
- [ ] api-contracts 冻结横幅存在；progress 更新
- [ ] 一个 commit：`feat(server): channel command loop, hardening, contract freeze (M1-B4)`

## 明确不做

- Web 界面（M2）；Agent 侧改动；channel.update 之外的命令类型；分页风格改造（保持 limit/offset，冻结即定版）。
