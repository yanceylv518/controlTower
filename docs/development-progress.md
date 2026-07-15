# Control Tower V1 Development Progress

本文档用于查看 Control Tower V1 当前开发阶段、阶段边界、可验证点和下一步顺序。它是阶段看板；每次具体改动仍记录到根目录 `PROJECT_PROGRESS.md`。

## 当前状态

- 当前阶段：M2 Web 管理端完成；v2.0-B1 最小部署与发布闭环完成。
- 当前主线：通过 Docker Compose 部署 Server/Web/MySQL，并通过 tag 流水线发布 Agent、Server 与 GHCR 镜像。
- 当前不是主线：Mobile H5/PWA、Redis 后台任务、Caddy/TLS、HA/异地备份、自动调权。
- 当前边界：不修改 `source/new-api/**`，不修改 new-api 路由、Nginx 或数据库结构，不创建真实 `.env`，不安装新依赖，不删除文件；多实例仅保留后端字段和后续扩展点，第一版页面不强调多实例。

## 阶段总览

M0-lite 工程质量门已完成：根目录 Makefile 提供测试和 Linux 交叉编译目标，GitHub Actions 对 `main` push 与所有 PR 执行 `make test`、`make build`。发布打包、版本注入和 Agent 重构继续挂起。

| 阶段 | 状态 | 目标 | 主要产物 | 可验证点 |
| --- | --- | --- | --- | --- |
| P0 架构边界与需求确认 | 已完成 | 明确 Control Tower 与 new-api 的隔离边界 | 需求设计文档、原型、实施计划 | 人工复核架构边界；确认 Agent 只读、主动上报、不进入请求链路 |
| P1 工作区与契约草案 | 已完成 | 建立独立工作区、API 契约和 schema 草案 | `tools/control-tower`、API 文档、schema 文档、env example | 文档可读；未触碰 new-api；未创建真实 `.env` |
| P2 Server 后端底座 | 已完成 | 完成 Agent Gateway、ingest、聚合、Dashboard API 骨架 | Go 包：`agentgateway`、`ingest`、`aggregator`、`dashboard`、`storage` | `go test ./...` 通过；聚合维度和 DTO 输出符合设计；页面侧只依赖 Control Tower 数据 |
| P3 Agent 最小可运行闭环 | 已完成 | Agent 只读读取 new-api logs 表并主动 HTTPS 上报 | Go Agent 入口、只读 DB 采集、游标、系统探针、Reporter | 测试库增量采集、断点续采、上报契约均已验证 |
| P4 Control Tower 真实存储 | 已完成 | MySQL 持久化与双 Store 行为对齐 | MySQL 迁移、repository、幂等 upsert、查询索引 | 迁移可执行；重复写入幂等；Dashboard 仅查询 Control Tower DB |
| P5 Server 认证与查询 API | 已完成 | Session/Token 双认证、实例权限、命令闭环、IP 限流、保留清理与契约冻结 | 用户/session、命令/审计 API、005 迁移、Dashboard API v1 文档 | 命令状态闭环且幂等；登录第 11 次限流；API 契约冻结 |
| M1-B2 实例与 Agent Token | 已完成 | 实例 CRUD、按实例 Token 签发/轮换/停用、全局 Token 兼容 | 003 迁移、实例 API、网关实例绑定校验、E2E 脚本 | Token 仅回显一次；轮换宽限 24h；实例不匹配 403 |
| M1-B3 告警时间线与通知强化 | 已完成 | 生命周期事件、操作者/备注、重试死信、手动重发、钉钉加签 | 004 迁移、时间线/重发 API、通知配置 | 时间线升序；secret 不回显；exhausted 不再自动重试 |
| M1-B4 命令闭环与契约冻结 | 已完成 | 渠道命令下发/回收、操作审计、登录 IP 限流、分层数据保留 | 005 迁移、命令/审计 API、retention runner、API v1 契约 | pending→delivered→终态；结果重放不重复审计；过期命令不下发 |
| P6 Web 管理端 | 进行中 | 构建单实例桌面 Web 监控页面 | Vue3 正式前端与旧静态页共存 | 新前端托管 `/next/`；旧页面 `/` 不受影响；接口失败有可用提示 |
| P7 Mobile H5/PWA | 未开始 | 构建移动端巡检与轻操作页面 | Vue3 + Vant/H5/PWA | 移动视口验证；关键指标可读；人工确认动作明确 |
| P8 部署与运行闭环 | 最小闭环完成 | Docker Compose 部署 Control Tower Server/Web/MySQL，tag 发布全套产物 | 多阶段镜像、Compose、部署文档、Release workflow | Go 全量质量门、脚本语法、三份发布包与校验和通过；Compose 实测 MySQL healthy、`/healthz` 与登录页 200 |
| v2.2-B1 Nginx timing 延时分诊 | 已完成 | Agent 只读 tail 与分钟聚合，Server 持久化/API，Web 归因与趋势分析 | `nginxtiming`、007 迁移、`/api/dashboard/nginx-timing*`、`/latency` | 失效安全与轮转测试、幂等/上限/清理/API 测试、前端构建及手工混合行冒烟 |
| v2.1-B1 调权评估 observe | 已完成 | Server 只记录可解释的调权建议与 30 分钟事后命中结果，零执行动作 | `tuning`、006 迁移、策略/建议/报表 API | 危险策略拒绝、持续窗口/冷却/恢复模拟、回填三分支与 API 测试 |
| P9 建议与人工确认扩展 | 未开始 | 建立风险建议、人工确认和后续自动调权扩展点 | 建议规则、人工确认流程、审计记录 | 自动调权默认关闭；建议可解释；确认动作有审计 |

## P2 当前拆分

| 子项 | 状态 | 文件范围 | 验证方式 |
| --- | --- | --- | --- |
| M2-B1 Vue3 行走骨架 | 已完成 | `webapp/**`、`server/internal/httpapi/**`、CI | pnpm typecheck/build；Go vet/test；`/next/` SPA fallback 与 API 隔离测试；旧静态页共存 |
| M2-B2 通用组件与六个只读页 | 已完成 | `webapp/packages/shared/**`、`webapp/packages/desktop/**` | 客户/渠道/模型共用 DimensionWorkspace；样本、系统状态、用量页接入冻结 API；pnpm typecheck/build、Go 全测 |
| M2-B3 操作页 | 已完成 | `webapp/packages/shared/**`、`webapp/packages/desktop/**` | 告警、通知、实例、渠道命令与审计交互；危险操作确认及一次性 Token 展示 |
| M2-B4 设置、打磨与根路径切换 | 已完成 | `webapp/**`、`server/internal/httpapi/**`、`web/**` | 设置与 404、标题/favicon、空环境引导；Vue SPA 转正到 `/`，旧静态页退役，`/next/*` 兼容重定向 |
| M2-B5 维度指标趋势图 | 已完成 | `webapp/packages/desktop/**` | 客户/渠道/模型详情共用四张趋势图；1h 使用 1m 桶，6h/24h 自动使用 5m 桶 |
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


## M4 当前拆分

| 子项 | 状态 | 文件范围 | 验证方式 |
| --- | --- | --- | --- |
| v2.0-B1 最小部署与发布闭环 | 已完成 | `Dockerfile`、`deploy/compose/**`、`deploy/package.sh`、`.github/workflows/release.yml` | 多阶段镜像以 uid 1001 运行并保留默认迁移/前端路径；Compose 使用持久 MySQL 8，实测健康接口与登录页 200；本地生成双架构 Agent、Server、SHA256SUMS，rc tag 流水线待提交后演练 |

## P6 当前拆分

| 子项 | 状态 | 文件范围 | 验证方式 |
| --- | --- | --- | --- |
| Web 静态面板骨架 | 已完成 | `web/index.html`、`web/assets/**` | 单实例监控分析台布局；`node --check`；Server `/` 与 `/assets/app.js` E2E 通过 |
| Server 静态托管 | 已完成 | `server/internal/httpapi/mux.go` | `go test ./...`；`/api/**` 不被静态路由抢占 |
| Dashboard 首页接入 | 已完成 | `web/assets/app.js` | `/api/dashboard/overview` 与 `/api/dashboard/metrics` 同源请求通过 |
| Dashboard metrics API | 已完成 | `server/internal/dashboard/metric_handler.go`、`server/internal/httpapi/**` | `go test ./...`；本地 E2E 验证 `/api/dashboard/metrics` 鉴权与 JSON 返回 |
| Web 监控展示修复（P1 批次） | 已完成 | `server/internal/dashboard/**`、`server/internal/mysqlstore/**`、`server/internal/ingest/memory_store.go`、`web/**` | 完成 `review-web-monitoring-2026-07-13.md` P1-1、P1-3～P1-8：指标历史/latest、自动刷新、网络列、P50/P99、用量排行、双线趋势；`go vet ./...`、`go test ./...`、`node --check web/assets/app.js` 通过 |
| 当前告警 API | 已完成 | `server/internal/dashboard/alert_handler.go`、`server/internal/httpapi/**` | `go test ./...`；本地 E2E 验证 `/api/dashboard/alerts` 鉴权与 JSON 返回 |
| 告警确认/静默/自动恢复 | 已完成 | `server/internal/dashboard/alert_handler.go`、`server/internal/mysqlstore/alert_store.go`、`server/internal/ingest/memory_store.go` | `go test ./...`；本地 E2E 验证 acknowledge、silence 状态落库 |
| Webhook 通知渠道与发送记录 | 已完成 | `server/internal/dashboard/notification_handler.go`、`server/internal/mysqlstore/notification_store.go`、`server/migrations/001_init.sql` | `go test ./...`；本地 E2E 验证渠道保存、脱敏回显和 failed 发送记录 |
| 通知后台队列与失败重试 | 已完成 | `server/internal/dashboard/notification_runner.go`、`server/cmd/control-tower-server/main.go`、`server/internal/mysqlstore/notification_store.go` | `go test ./...`；本地 E2E 验证后台 worker 自动投递并写入 next_attempt_at/attempts |
| 告警历史筛选与静默过期 | 已完成 | `server/internal/dashboard/alert_handler.go`、`server/internal/mysqlstore/alert_store.go`、`web/assets/app.js` | `go test ./...`；本地 E2E 验证 status/severity/active_only 筛选 |
| 日志查询页接入 | 已完成 | `web/assets/app.js` | 页面可请求 `/api/dashboard/logs` |
| 运行态页面接入 | 已完成 | `web/assets/app.js` | 页面可请求 metrics、health、docker 三类 API |
| 告警中心页面接入 | 已完成 | `web/index.html`、`web/assets/**` | 总览当前告警与告警中心接入 `/api/dashboard/alerts`，支持确认、静默、状态筛选和历史切换 |
| 通知设置页接入 | 已完成 | `web/index.html`、`web/assets/app.js` | 设置页支持 Webhook 表单、通知渠道列表和发送记录列表，展示重试次数和下次重试 |
| recent_errors 告警规则 + 钉钉通知（Server 侧） | 已完成 | `server/internal/dashboard/recent_errors.go`、`notification_handler.go`、`notification_runner.go`、`mysqlstore/notification_store.go`、`ingest/memory_store.go`、`web/**` | 规则：任一渠道/任一客户最近 10 条请求中错误 ≥3（≥5 升级 critical）触发告警；通知渠道支持 `channel_type=dingtalk`（msgtype=text，校验 errcode）；告警恢复后再次触发可重新通知（sent 投递随告警 resolved 过期）；此路径需 Agent `CT_LOG_EVENT_MODE=full_debug`；`go test ./server/...` 含链路 E2E（规则→告警→钉钉→恢复→再通知）通过 |
| Agent 端错误告警直发企业微信 | 已完成 | `agent/internal/erroralert/**`、`agent/internal/config/**`、`agent/cmd/control-tower-agent/main.go`、`deploy/agent.*.example` | 配置 `CT_WECOM_WEBHOOK_URL` 启用；窗口、episode、提醒、缓存失效规则保持不变；发送校验企业微信 `errcode`，失败下轮重试；不配 `CT_SERVER_URL` 时进入独立告警模式；安装脚本和部署文档均使用企业微信机器人 |
| v1.1 B1：慢返回规则与 episode 事件持久化 | 已完成 | `agent/internal/erroralert/**`、`agent/internal/config/**`、`agent/cmd/control-tower-agent/main.go`、Agent 配置样例 | 错误与慢返回独立窗口/episode/提醒/重臂；流式单独阈值；alert/remind/rearm 写入 5 MiB 轮转 JSONL，写失败 fail-safe；`go vet ./...`、`go test ./...` |

## Agent 采集与上报修复计划

2026-07-10 代码复核结论：Agent 采集/上报主干逻辑正确（游标断点续采、缓冲先落盘再推进游标、`metric_batch_id` 幂等已验证 Server 端 `metric_batches` INSERT IGNORE 去重 + 同分钟跨批次累加合并）。以下为复核发现的问题与完善项，按优先级排序。

### P0 确定性缺陷（真实库验收前必须修）

| 子项 | 状态 | 问题 | 文件范围 | 验证方式 |
| --- | --- | --- | --- | --- |
| logs 采集 NULL 字段防护 | 已完成 | 采集 SQL 对 `created_at`、文本、维度 ID、token、quota、耗时和流式标记等可空列统一使用 `COALESCE`，避免 NULL 扫描进 Go 基础类型导致 pass 永久阻塞；`id/type` 保持为必需字段 | `agent/internal/logcollector/mysql.go`、`agent/internal/logcollector/mysql_test.go` | SQL 契约测试覆盖可空列默认值；`go test ./agent/...` 与 `go test ./...` 通过；真实源库 NULL 行验证待部署环境执行 |
| 渠道快照 collector 常驻化 | 未开始 | `channelcollector.MySQLCollector` 在每个采集 pass 内新建（DB 连接也是每 pass 开关），`lastCheckedAt`/`lastHash` 每次归零，`CT_CHANNEL_SNAPSHOT_INTERVAL_SECONDS` 与内容哈希去重完全失效，实际每 30s 全量查询并全量上报 channels | `agent/cmd/control-tower-agent/main.go`、`agent/internal/channelcollector/**` | 30s 轮询下验证快照间隔内只采一次、内容未变不上报；DB 连接池常驻不再每 pass 开关 |

### P1 可靠性完善

| 子项 | 状态 | 问题 | 文件范围 | 验证方式 |
| --- | --- | --- | --- | --- |
| 心跳与采集解耦 | 未开始 | 心跳是采集 pass 的第一步之后（缓冲 flush 之后），源库慢/挂或缓冲堵塞时心跳一起消失，Server 无法区分"Agent 挂了"和"源库出问题"；心跳应独立 goroutine 并携带 Agent 自身健康信息（连续失败次数、buffer 深度、上次采集耗时） | `agent/cmd/control-tower-agent/main.go`、`agent/internal/reporter/**` | 模拟源库不可用时心跳仍按周期到达 Server |
| 缓冲毒条目防护 | 未开始 | `flushBufferedReports` 队头阻塞：某条目因确定性原因（如超过 Server 413 大小限制）永远发不出去时，flush 卡死队头，新采集与心跳全部停摆 | `agent/internal/localbuffer/**`、`agent/cmd/control-tower-agent/main.go` | 单元测试：条目连续失败达上限后丢弃并记日志，后续条目继续投递 |
| 缓冲 flush 独立超时预算 | 未开始 | `collectPassTimeout` 为 report+query 超时简单相加，宕机恢复后一个 pass 需串行发 N 条积压缓冲，容易整体超时→失败→继续积压，恢复过程打滑 | `agent/cmd/control-tower-agent/main.go` | 积压多条缓冲时恢复过程可在有限 pass 内清空 |
| channel.update 命令去重与本地审计 | 未开始 | report 失败重发或 Server 重复下发时同一命令可能执行两次；命令执行前应按 command ID 本地去重（执行过的 ID 记入 state），并在 Agent 本地追加执行流水。启用 `CT_NEW_API_CONTROL_ENABLED` 前必须完成 | `agent/cmd/control-tower-agent/command_executor.go`、`agent/internal/state/**` | 单元测试：同一 command ID 二次下发不重复执行；本地审计文件有完整流水 |

### P2 数据质量与打磨

| 子项 | 状态 | 问题 | 文件范围 | 验证方式 |
| --- | --- | --- | --- | --- |
| backlog 估算按采集类型过滤 | 未开始 | `Backlog` 用全表 `MAX(id)` 减游标，而游标只推进到 type 2/5 行；源表最新行为充值/管理类日志时 `backlog_estimate` 永久虚高，面板显示假积压 | `agent/internal/logcollector/mysql.go` | 源表末尾插入 type 1/3/4 行后 backlog 显示为 0 |
| 首个采样跳过假零值 | 未开始 | CPU 使用率与网络速率靠前后采样差值计算，Agent 重启后首个 pass 无前值直接上报 0，图表出现假数据点 | `agent/internal/syscollector/collector.go` | 首个 pass 不上报 CPU/网络速率（或置 null），第二个 pass 起正常 |
| 退避加抖动 | 未开始 | `BackoffDelay` 为固定阶梯，多 Agent 在 Server 恢复后同步重试冲击 | `agent/internal/reporter/backoff.go` | 单元测试验证退避带随机抖动 |
| 清理死代码 | 未开始 | `metricaggregator` 中 `p95` 函数已被 latencyhist 直方图取代但未删除 | `agent/internal/metricaggregator/aggregator.go` | `go test ./...` 通过 |

### P3 生产化（Agent 部署形态）

| 子项 | 状态 | 问题 | 文件范围 | 验证方式 |
| --- | --- | --- | --- | --- |
| Linux 构建与部署形态 | 未开始 | `deploy/` 全部为 PowerShell、构建产物仅 Windows exe，而 new-api 生产环境以 Linux + Docker 为主；缺 Linux 构建脚本、systemd unit（或 sidecar Dockerfile） | `deploy/**` | Linux 下一键构建；systemd/容器方式启动并通过 preflight |
| 构建版本注入 | 未开始 | 版本号硬编码 `agentVersion = "0.1.0"`，未用 `-ldflags` 注入构建版本，多机排查无法确认运行版本 | `agent/cmd/control-tower-agent/main.go`、`deploy/**` | 构建产物 `-version` 或心跳上报中体现构建版本 |

### 已评估暂缓

- 传输安全（TLS 强制、自定义 CA、mTLS）：当前本地/内网部署阶段不做；跨公网部署前优先采用 Server 前置反代（Nginx/Caddy）承担 TLS，Agent 仅需改配置 URL，代码零改动。
- Docker 采集换 Docker API（容器 CPU/内存、重启次数）、多磁盘路径监控、结构化日志：按需排期。

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

当前单实例 Web 监控分析台 MVP 已可运行。**从当前状态到可交付、可上线产品的完整开发计划见 `development-plan.md`**（里程碑 M0–M5：工程基础与 Agent 修复 → Server 产品化 → Web 正式前端 → Mobile App/PWA → 部署闭环 → 试运行与发布）。执行顺序：

1. M0：git/CI 工程基础 + 「Agent 采集与上报修复计划」P0/P1 项（NULL 字段防护、渠道快照常驻化、心跳解耦、毒条目防护、命令去重），这是真实库端到端验收的前置条件。
2. M1：Server 产品化补齐——用户登录认证、按实例 Agent Token、告警事件时间线、通知重试上限/手动重发、渠道命令闭环；结束时 API 契约冻结。
3. M2：Vue3 正式前端工程替换静态 Web（九个页面，验收后删除旧 `web/assets`）。
4. M3：移动端 PWA App（巡检/告警/运行态，可安装到主屏）。
5. M4：Docker Compose 部署 + Agent Linux 安装脚本与 systemd（可与 M2/M3 并行）。
6. M5：真实 new-api 只读账号 7 天试运行，验收后发布 v1.0.0。

这样顺序更稳：先把单实例监控、告警和查询闭环做完整，再扩展多实例与正式部署。

## 更新规则

## 2026-07-15：v2.3-B3 维度页加载性能优化

- 新增指标复合索引和 latest 分组自联结查询，维度类型与 24 小时活跃视野下推。
- 名称解析改为批量预载；维度页首屏不再等待历史曲线。
- 新增 120 万行基准造数与 EXPLAIN 脚本；开发机因无测试库管理员凭据未执行真实灌数，见 `docs/v2.3-b3-delivery.md`。

## 2026-07-15：v2.3-B1 Web 体验打磨

- 完成名称解析缓存及 Dashboard API 增量名称字段。
- 完成侧栏图标、总览 KPI 语义化、维度列表可读化和 b7 样式批次。
- 完成系统状态页图表化：最新值阈值卡片、1h/6h/24h 趋势以及默认折叠的原始采样。
- 验证：`pnpm build`、`pnpm test`、`go test ./...` 全部通过。
- 交付说明：`docs/v2.3-b1-delivery.md`。

- 阶段状态只在有验证依据后更新。
- 每完成一个子项，在本文档对应表格更新状态和验证方式。
- 每次具体变更仍追加到根目录 `PROJECT_PROGRESS.md`。
- 若用户改变优先级，先在本文档记录新顺序，再进入实现。
















