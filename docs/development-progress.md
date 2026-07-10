# Control Tower V1 Development Progress

本文档用于查看 Control Tower V1 当前开发阶段、阶段边界、可验证点和下一步顺序。它是阶段看板；每次具体改动仍记录到根目录 `PROJECT_PROGRESS.md`。

## 当前状态

- 当前阶段：P6 Web 管理端骨架。
- 当前主线：Control Tower Web 管理端接入现有 Dashboard API，第一版按单个 new-api 实例监控体验实现。
- 当前不是主线：Mobile H5/PWA、Redis 后台任务、Docker Compose 生产部署闭环、自动调权。
- 当前边界：不修改 `source/new-api/**`，不修改 new-api 路由、Nginx 或数据库结构，不创建真实 `.env`，不安装新依赖，不删除文件；多实例仅保留后端字段和后续扩展点，第一版页面不强调多实例。

## 阶段总览

| 阶段 | 状态 | 目标 | 主要产物 | 可验证点 |
| --- | --- | --- | --- | --- |
| P0 架构边界与需求确认 | 已完成 | 明确 Control Tower 与 new-api 的隔离边界 | 需求设计文档、原型、实施计划 | 人工复核架构边界；确认 Agent 只读、主动上报、不进入请求链路 |
| P1 工作区与契约草案 | 已完成 | 建立独立工作区、API 契约和 schema 草案 | `tools/control-tower`、API 文档、schema 文档、env example | 文档可读；未触碰 new-api；未创建真实 `.env` |
| P2 Server 后端底座 | 已完成 | 完成 Agent Gateway、ingest、聚合、Dashboard API 骨架 | Go 包：`agentgateway`、`ingest`、`aggregator`、`dashboard`、`storage` | `go test ./...` 通过；聚合维度和 DTO 输出符合设计；页面侧只依赖 Control Tower 数据 |
| P3 Agent 最小可运行闭环 | 进行中 | Agent 只读读取 new-api logs 表并主动 HTTPS 上报 | Go Agent 入口、只读 DB 采集、游标、系统探针、Reporter | 使用测试库验证增量采集；断点续采；上报 payload 与 Server 契约一致 |
| P4 Control Tower 真实存储 | 进行中 | 将内存 Store 替换为 MySQL 持久化 | MySQL 迁移脚本、repository、幂等 upsert、查询索引 | 迁移可执行；重复写入幂等；Dashboard 查询只查 Control Tower DB |
| P5 Server 认证与查询 API | 未开始 | 补齐 Dashboard 查询、认证、权限和只读 API | API handler、中间件、分页过滤、错误响应 | 未认证请求被拒绝；查询分页稳定；不泄露完整请求体或响应体 |
| P6 Web 管理端 | 进行中 | 构建单实例桌面 Web 监控页面 | 静态 Web Dashboard、日志页、运行态页、配置页 | Server 托管 `/`；接口失败有可用提示；无敏感字段展示 |
| P7 Mobile H5/PWA | 未开始 | 构建移动端巡检与轻操作页面 | Vue3 + Vant/H5/PWA | 移动视口验证；关键指标可读；人工确认动作明确 |
| P8 部署与运行闭环 | 未开始 | Docker Compose 部署 Control Tower Server/Web/DB/Redis | Compose、部署文档、健康检查 | 本地或测试机一键启动；健康检查通过；日志脱敏 |
| P9 建议与人工确认扩展 | 未开始 | 建立风险建议、人工确认和后续自动调权扩展点 | 建议规则、人工确认流程、审计记录 | 自动调权默认关闭；建议可解释；确认动作有审计 |

## P2 当前拆分

| 子项 | 状态 | 文件范围 | 验证方式 |
| --- | --- | --- | --- |
| Agent Gateway 合约与鉴权骨架 | 已完成 | `server/internal/agentgateway/**` | `go test ./server/internal/agentgateway` |
| ingest 接收与内存存储 | 已完成 | `server/internal/ingest/**` | `go test ./server/internal/ingest` |
| 1m 聚合维度 | 已完成 | `server/internal/aggregator/aggregator.go` | `go test ./server/internal/aggregator` |
| 5m rollup | 已完成 | `server/internal/aggregator/rollup.go` | `go test ./server/internal/aggregator` |
| 聚合调度器 `RunOnce` | 已完成 | `server/internal/aggregator/scheduler.go` | `go test ./server/internal/aggregator` |
| 聚合任务锁、周期调度和失败重试 | 已完成 | `server/internal/aggregator/runner.go`、`lock.go`、`backoff.go` | runner/lock/backoff 单元测试；失败后 backoff，成功后恢复 interval |
| Dashboard overview API 骨架 | 已完成 | `server/internal/dashboard/overview.go`、`handler.go` | `go test ./server/internal/dashboard` |
| Dashboard logs API 骨架 | 已完成 | `server/internal/dashboard/logs.go`、`log_handler.go` | `go test ./server/internal/dashboard` |
| Dashboard logs 真实 Store 查询 | 已完成 | `server/internal/dashboard/**`、`server/internal/storage/**`、`server/internal/ingest/**` | Store 查询接口、内存 Store 查询实现和 handler 查询下沉测试 |
| Server 认证中间件 | 已完成 | `server/internal/dashboard/auth.go`、`auth_test.go` | `go test ./server/internal/dashboard` |



## P3 当前拆分

| 子项 | 状态 | 文件范围 | 验证方式 |
| --- | --- | --- | --- |
| Agent 可运行入口 | 已完成 | `agent/cmd/control-tower-agent/**` | `go test ./...`；fake event 上报 smoke |
| Agent heartbeat 上报 | 已完成 | `agent/internal/reporter/**` | Reporter heartbeat 单元测试 |
| Agent 本地 smoke 上报脚本 | 已完成 | `deploy/send-agent-smoke-report.ps1`、`agent/README.md` | Server + Agent 端到端 smoke 通过 |
| 只读 logs 数据库采集 | 已完成 | `agent/internal/logcollector/**`、`deploy/create-agent-source-test-data.ps1` | 源 logs 测试库真实采集并上报通过 |
| Agent 单次采集与断点续采 | 已完成 | `agent/cmd/control-tower-agent/**`、`agent/internal/state/**`、`deploy/run-agent-collector-local.ps1` | 第二次采集不重复上报；Dashboard 中同一 request 保持 1 条 |
| Agent 周期采集运行循环 | 已完成 | `agent/cmd/control-tower-agent/**`、`agent/internal/config/**`、`deploy/run-agent-collector-local.ps1` | `go test ./...`；本地真实 collector 一次性模式 E2E 通过 |
| Agent 系统指标采集 | 已完成 | `agent/internal/syscollector/**`、`agent/cmd/control-tower-agent/**` | `go test ./...`；本地 E2E 验证 `server_metrics_10s` 有记录 |
| new-api 健康检查采集与落库 | 已完成 | `agent/internal/healthcheck/**`、`server/internal/ingest/**`、`server/internal/mysqlstore/**`、`server/migrations/001_init.sql` | `go test ./...`；本地 E2E 验证 `health_checks` 有记录 |
| Docker 状态采集与落库 | 已完成 | `agent/internal/dockercollector/**`、`server/internal/ingest/**`、`server/internal/mysqlstore/**`、`server/migrations/001_init.sql` | `go test ./...`；Agent Gateway payload E2E 验证 `docker_statuses` 有记录 |
| Agent 本地缓冲与退避重试 | 已完成 | `agent/internal/localbuffer/**`、`agent/internal/state/**`、`agent/cmd/control-tower-agent/**` | `go test ./...`；本地 `report-buffer.json` flush E2E 通过 |

## P4 当前拆分

| 子项 | 状态 | 文件范围 | 验证方式 |
| --- | --- | --- | --- |
| MySQL repository 骨架 | 已完成 | `server/internal/mysqlstore/**` | `go test ./server/internal/mysqlstore` |
| MySQL 迁移脚本校准 | 已完成 | `server/migrations/001_init.sql`、`server/internal/storage/schema_test.go` | schema 测试通过；后续 MySQL 空库执行 |
| 真实 MySQL 驱动接入 | 已完成 | `go.mod`、`server/internal/config/**`、`server/internal/mysqlstore/open.go`、`deploy/server.env.example` | `go test ./server/internal/config ./server/internal/mysqlstore` |
| 真实库集成验证 | 已完成 | `deploy/create-mysql-test-db.ps1`、`server/internal/mysqlstore/integration_test.go` | 本地 `control_tower_test` 创建成功；MySQL 集成测试通过 |
| Server HTTP 启动入口 | 已完成 | `server/cmd/control-tower-server/**`、`server/internal/httpapi/**` | `go test ./...`；healthz、Agent、Dashboard 路由组装测试 |
| Server 本地启动脚本 | 已完成 | `deploy/start-server-local.ps1`、`server/README.md` | PowerShell Parser 语法检查；README 运行说明 |
| 聚合 Runner Server 后台接入 | 已完成 | `server/cmd/control-tower-server/main.go`、`server/internal/config/**` | `go test ./...`；支持 `CT_AGGREGATION_INTERVAL_SECONDS` |
| 本地 Server smoke test | 已完成 | `deploy/start-server-local.ps1`、本地 `control_tower_test` | `/healthz` 与 Dashboard overview 请求通过；测试后进程已停止 |
| Dashboard 运行态查询接口 | 已完成 | `server/internal/dashboard/**`、`server/internal/mysqlstore/**`、`server/internal/httpapi/**` | `go test ./...`；本地 E2E 验证 server-metrics、health-checks、docker-statuses 返回数据 |
| Dashboard overview 运行态汇总 | 已完成 | `server/internal/dashboard/overview.go`、`handler.go` | `go test ./...`；本地 E2E 验证 overview runtime summary 返回最新状态 |


## P6 当前拆分

| 子项 | 状态 | 文件范围 | 验证方式 |
| --- | --- | --- | --- |
| Web 静态面板骨架 | 已完成 | `web/index.html`、`web/assets/**` | 单实例监控分析台布局；`node --check`；Server `/` 与 `/assets/app.js` E2E 通过 |
| Server 静态托管 | 已完成 | `server/internal/httpapi/mux.go` | `go test ./...`；`/api/**` 不被静态路由抢占 |
| Dashboard 首页接入 | 已完成 | `web/assets/app.js` | `/api/dashboard/overview` 与 `/api/dashboard/metrics` 同源请求通过 |
| Dashboard metrics API | 已完成 | `server/internal/dashboard/metric_handler.go`、`server/internal/httpapi/**` | `go test ./...`；本地 E2E 验证 `/api/dashboard/metrics` 鉴权与 JSON 返回 |
| 当前告警 API | 已完成 | `server/internal/dashboard/alert_handler.go`、`server/internal/httpapi/**` | `go test ./...`；本地 E2E 验证 `/api/dashboard/alerts` 鉴权与 JSON 返回 |
| 告警确认/静默/自动恢复 | 已完成 | `server/internal/dashboard/alert_handler.go`、`server/internal/mysqlstore/alert_store.go`、`server/internal/ingest/memory_store.go` | `go test ./...`；本地 E2E 验证 acknowledge、silence 状态落库 |
| Webhook 通知渠道与发送记录 | 已完成 | `server/internal/dashboard/notification_handler.go`、`server/internal/mysqlstore/notification_store.go`、`server/migrations/001_init.sql` | `go test ./...`；本地 E2E 验证渠道保存、脱敏回显和 failed 发送记录 |
| 通知后台队列与失败重试 | 已完成 | `server/internal/dashboard/notification_runner.go`、`server/cmd/control-tower-server/main.go`、`server/internal/mysqlstore/notification_store.go` | `go test ./...`；本地 E2E 验证后台 worker 自动投递并写入 next_attempt_at/attempts |
| 告警历史筛选与静默过期 | 已完成 | `server/internal/dashboard/alert_handler.go`、`server/internal/mysqlstore/alert_store.go`、`web/assets/app.js` | `go test ./...`；本地 E2E 验证 status/severity/active_only 筛选 |
| 日志查询页接入 | 已完成 | `web/assets/app.js` | 页面可请求 `/api/dashboard/logs` |
| 运行态页面接入 | 已完成 | `web/assets/app.js` | 页面可请求 metrics、health、docker 三类 API |
| 告警中心页面接入 | 已完成 | `web/index.html`、`web/assets/**` | 总览当前告警与告警中心接入 `/api/dashboard/alerts`，支持确认、静默、状态筛选和历史切换 |
| 通知设置页接入 | 已完成 | `web/index.html`、`web/assets/app.js` | 设置页支持 Webhook 表单、通知渠道列表和发送记录列表，展示重试次数和下次重试 |
## 每阶段验收规则

### 通用验收

- 不修改 `source/new-api/**`。
- 不修改 new-api 路由、Nginx 或数据库结构。
- 不读取、输出或修改真实 `.env` 和秘钥配置。
- 不安装新依赖，除非用户人工批准。
- 每次改动后更新根目录 `PROJECT_PROGRESS.md`。
- 每次进入代码开发前先给出详细实现计划。

### P2 可验证点

- 命令：`$env:GOCACHE='D:\CodexProjects\codex\newApi\.gocache'; & 'C:\Program Files\Go\bin\go.exe' test ./...`
- 期望：Control Tower 当前所有 Go 包测试通过。
- 命令：`Get-ChildItem -Recurse -Force tools\control-tower -Filter .env`
- 期望：无输出，表示没有创建真实 `.env`。
- 命令：`rg -n "TO[D]O|TB[D]|implement[ ]later|待[定]|占[位]" tools/control-tower`
- 期望：无输出，表示没有未完成标记。

### P3 可验证点

- Agent 使用只读 DB 账号连接测试 new-api logs 表。
- 同一批 logs 重复采集不会重复上报。
- Agent 重启后从本地游标继续采集。
- Server 收到心跳和 report 后更新 Agent 状态。
- 上报失败时 Reporter 按退避策略重试，不丢失游标。

### P4 可验证点

- 迁移脚本可在空库执行。
- 重复执行迁移不会破坏已有数据。
- report 写入具备幂等约束。
- 1m/5m 指标 upsert 不重复。
- Dashboard 查询只访问 Control Tower 自有数据库。

### P8 可验证点

- Docker Compose 能启动 Server、Web、DB、Redis。
- 健康检查接口返回正常。
- 日志不输出 token、密码、Authorization、完整请求体或完整响应体。
- 停止、重启后核心数据仍在数据库中。

## 当前推荐下一步

当前单实例 Web 监控分析台 MVP 已可运行。建议下一步优先补齐产品闭环能力：

1. 补独立告警事件时间线，记录 firing/acknowledged/silenced/resolved 变更。
2. 补通知手动重发、最大重试次数和指数退避。
3. 将当前静态 Web 继续产品化为正式前端工程，补移动端/H5 和交互状态。
4. 做正式部署编排与真实 new-api 只读账号端到端验收。

这样顺序更稳：先把单实例监控、告警和查询闭环做完整，再扩展多实例与正式部署。

## 更新规则

- 阶段状态只在有验证依据后更新。
- 每完成一个子项，在本文档对应表格更新状态和验证方式。
- 每次具体变更仍追加到根目录 `PROJECT_PROGRESS.md`。
- 若用户改变优先级，先在本文档记录新顺序，再进入实现。
















