# 验收记录：NewAPI 日志归档（CT 托管）、Nginx 本机日志免域名绑定、调权说明（2026-09-10）

- 范围：`a90ffd0 feat(archive): add site-managed database log archival and reconciliation`、
  `48e049b fix(logs): query local nginx logs without domain binding`、
  `728df0c docs(tuning): explain metrics, weight formulas and recovery controls`（合并 `a18c1f8`）
- 结论：**三笔均通过，零缺陷**。归档功能的真实 RDS 联调本机不具备条件（见记档）。

## a90ffd0 日志归档

### 安全底线核对（代码级）
- 源库连接（复用 `CT_LOG_DSN`，独立 1 连接池）上只有 SELECT：`@@server_uuid`/`DATABASE()`
  身份校验、`UNIX_TIMESTAMP()` 截止时间、`SELECT * FROM logs WHERE id>? ORDER BY id LIMIT ?`、
  对账用的索引元数据与分页读取。**没有任何针对源库的写语句**。
- 目标库：`CREATE TABLE IF NOT EXISTS`（月表 LIKE 模板、统计与去重辅助表）、
  `INSERT ... ON DUPLICATE KEY UPDATE`、`DELETE FROM logs_旧月 WHERE id=?`（仅 created_at 跨月
  修正时在同一事务内移走旧月归档行）。明细、辅助记录、日/月统计同事务提交后才原子保存
  本地检查点；每批提交前按 ID 回读目标逐字段核对，不一致整体回滚。
- 源目标 `server_uuid`+库名相同即拒绝；严格 SQL 模式与同结构检查；DSN/密码不进 CT
  （控制契约无任何凭据字段，CT 只知道"目标是否已配置"）。

### CT 托管控制面
- 071/072/073 三个迁移（补齐 069→074 之间的空号，台账按文件名，烟测库在 074 之后正常套用）；
  072 把旧实例策略合并为暂停的站点策略。新权限 `archive.manage`；页面"系统管理 → 日志归档"。
- 统一控制入口 `POST /api/agent/control/poll`（只收实例专属 Token，全局 Token 401），
  归档与容器日志分段复用各自校验；每站点一个执行节点+Agent，同一时刻只发一个 120 秒
  授权会话；配置带版本校验；写操作审计。

### 实证
- `go vet`、`go test ./...` 39 包全绿（logarchive 含检查点/月表/核对/对账/延迟单测，
  mysqlstore 真库控制面测试）；`pnpm typecheck`/`build` 通过。
- 控制面接口实测（假 Agent 走统一轮询）：全局 Token 401；未登记目标前启用 409；Agent 以
  32 位 session 上报 `waiting` 后目标登记；启用后同 session 轮询 `granted=true, lease 120s`；
  另一个 session 同站点轮询不授权、状态不接受；续期正常；页面暂停后下一轮 `granted=false`；
  带 `site_id` 与每日记录的状态上报被接受并落库，页面显示已提交 ID/最近一批/每日归档任务。
  审计 `archive.manage` 入库。
- 页面 1366：执行状态、三张统计卡、每日任务表、站点策略渲染正常。

### 记档（P3）
- **本机无 MySQL 5.7/8.x（MariaDB 不受支持：无 `server_uuid`），Agent 真实归档写入、月表
  建表、写入校验与按日对账均只由单测覆盖，未做端到端。** 上线必须按文档先用
  `-preflight` 预检，再在低峰用小批量在真实 RDS 验证追赶速度与源库负载。
- 默认吞吐上限约 1,000 行/分钟，高流量实例需要调批量与间隔；首次从 `id>0` 补齐全量
  历史会长期占用源库读取。
- 启用条件之一是目标在 90 秒内有上报，未满足时接口统一返回 `archive_config_conflict`，
  页面提示偏笼统（"配置冲突"），排障时留意。
- `CT_LOG_ARCHIVE_MANAGED=true` 后首次等待 CT 启用，不沿用本地开关；回退 managed=false
  会绕过 CT 暂停，文档已提醒。

## 48e049b Nginx 本机日志免域名绑定
- 取消"实例 Base URL hostname 必须匹配 server_name"的绑定，改为按 Agent 所属实例把本机
  Nginx 日志归入当前站点；`host` 筛选变为可选。昨日验收中"域名不符 409"的行为随之取消，
  同一台机上多站点共用的 Nginx 日志会一起归入该实例（`logs.query` 本就是全站点权限）。
  单测已更新，真库站点绑定测试通过。

## 728df0c 调权说明
- 仅调权中心说明文案（指标含义、权重公式、恢复控制），无逻辑改动，typecheck/build 通过。

## 部署要点
- 先 CT（071–073 自动套用）+ 前端，再升级 Agent（归档循环与统一控制轮询）；reader 本批
  未变。归档默认关闭：需在 Agent 配置 `CT_LOG_ARCHIVE_ENABLED/DSN/MANAGED` 并先 `-preflight`，
  再在页面启用小批量验证。
- rc102 打在 1df1dcf 不含三笔，上线需重打 rc103（server + 前端 + agent，三个迁移）。
