# Control Tower V1 交付开发计划

本文档是 Control Tower 从当前状态到"可交付、可上线"的完整开发计划。目标交付物：

- **Agent**：可在真实 new-api Linux 服务器上长期稳定运行的采集端。
- **Server**：带用户认证、多实例、告警、通知、渠道轻操作的后端。
- **Web**：正式前端工程的桌面管理端。
- **App**：移动端 PWA（可安装到手机主屏），核心巡检与告警处理能力；预留后续原生打包。
- **部署**：Docker Compose 一键部署 + Agent 安装脚本 + 运维手册。

阶段状态与逐项进度仍记录在 `development-progress.md`；本文档定义"做什么、怎么做、做到什么程度算完成"。

---

## 双轨迭代路径（2026-07-13 定稿）

v1.0 错误预警 Agent 已在生产运行并持续迭代（见 `iteration-log.md` v1.0.x 系列），产品主线（Server/Web/App）在其之上推进。两条线共享同一个仓库和 Agent 二进制，靠配置开关区分能力，**永远不 fork**。

**执行顺序与版本映射**（2026-07-13 修正：主线优先——v1.1 的 B2/B3 挂起，B1 已合入随下次升级带上，先做 M0-lite CI 后直进 M1；详见 `codex-batches-plan.md` 顶部说明）：

| 顺序 | 版本 | 内容 | 对应里程碑 | 预估 |
| --- | --- | --- | --- | --- |
| 1 | v1.1 | 告警增强：证据驱动探测、静默/吞吐骤降→探测确认、慢返回窗口、正向证据恢复 + episode 三种收尾（设计已定稿：`design-v1.1-early-warning.md`） | 独立告警线 | 1~2 周 |
| 2 | — | 工程基础：CI 跑测试 + 构建 + 自动打部署包（tar.gz/LF/SHA256），根治 Windows 手工打包事故类（v1.0.2/3/4 三次踩坑的根源）；修掉剩余 P0 渠道快照常驻化；**含 new-api 补丁镜像流水线**（见下方 new-api 维护模式） | M0 | 3~5 天 |
| 3 | — | Server 产品化：认证、按实例 token、告警时间线、通知强化、渠道命令闭环，API 冻结；期间生产 Agent 保持独立模式不动 | M1 | 2~3 周 |
| 4 | v2.0 | Vue3 桌面 Web + 最小 Compose 部署；**汇合点**：生产 Agent 配置加 `CT_SERVER_URL` 进入双模式——钉钉直发告警保留为独立冗余链路（Server/Web 故障不影响告警），同时向 Server 上报供看板展示 | M2 + M4 最小部署 | 3~4 周 |
| 5 | v3.0 | 移动端 PWA App | M3 | 2~3 周 |
| 随时 | v1.x.y | 生产告警问题的穿插修复（如 v1.0.5/v1.0.6 模式），修完回主线 | 独立告警线 | — |
| 暂缓 | v1.2 | 在途请求检测与请求参数采集（new-api 中间件，方案与维护模式见下，设计随时可启动）——2026-07-13 用户决定暂不改 new-api，先用现有数据源（logs/channels 表 + admin API）获取监控数据；v1.1 上线运行一段时间后按真实盲区数据再评估 | 独立告警线 | ~1 周 |

**双轨原则**：告警链路的可靠性永远独立于产品主线（Agent 直连钉钉的路径不因接入 Server 而移除）；告警逻辑与上报逻辑分属不同模块，并行开发天然低冲突；告警线小步快发，主线按里程碑发大版本，CI 出包后共用同一条流水线。

**new-api 维护模式（2026-07-13 决策，取代原"不修改 new-api 源码"边界）**：固定版本 + 按业务需求打补丁，默认不升级。

- fork new-api，从选定版本 tag 切 `ct-patch` 分支，每个补丁一个独立 commit（中间件、后续业务改动各自成 commit，理由记入迭代记录）。
- CI 从 `ct-patch` 分支构建自有镜像，版本号 = 上游版本 + 补丁号（如 `v0.x.y-ct2`）；文档记录固定的上游版本/commit 与补丁清单。
- 所有补丁必须 fail-safe（不阻塞请求、panic 必须 recover、写事件失败即丢弃），且不修改 new-api 数据库结构。
- 代价自知：不升级即放弃上游的新模型支持与修复；真需迁移时（如必须接入固定版本不支持的新模型 API），补丁以 rebase 方式重放到新 tag，成本约半天 + 一轮回归——该选项始终廉价保留。

## 总览

| 里程碑 | 内容 | 预估工作量 | 前置依赖 |
| --- | --- | --- | --- |
| M0 工程基础与 Agent 可靠性 | git/CI、Agent P0/P1 缺陷修复 | 4–6 人日 | 无 |
| M1 Server 产品化补齐 | 用户认证、多实例、告警时间线、通知强化、渠道命令闭环 | 10–14 人日 | M0 |
| M2 Web 正式前端 | Vue3 工程化桌面管理端，替换现有静态页 | 12–16 人日 | M1 API 冻结 |
| M3 Mobile App（PWA） | 移动端巡检 App，可安装 | 6–9 人日 | M2 工程骨架 |
| M4 部署与运行闭环 | Docker Compose、Agent Linux 安装、备份 | 5–8 人日 | M1 |
| M5 试运行与发布 | 真实环境 7 天试运行、验收、v1.0.0 发布 | 4 人日 + 7 天观察 | M0–M4 |

关键路径：M0 → M1 → M2 → M3；M4 可与 M2/M3 并行。

**需要人工批准的依赖**（项目规则：不安装新依赖除非人工批准），在进入 M2 前一次性确认：

- Node.js ≥ 20 + pnpm（前端工具链）
- Vue 3、Vite、TypeScript、Vue Router、Pinia
- Element Plus（桌面 UI）、Vant 4（移动 UI）、ECharts（图表）
- Go 侧：`golang.org/x/crypto`（bcrypt，用户密码哈希）

**技术决策（已定，实现时不再讨论）**：

- V1 不引入 Redis。通知队列、聚合调度锁继续用 MySQL（现有实现已满足）。
- V1 不做系统级消息推送。告警触达靠 Webhook → IM 群机器人；PWA 预留 Web Push 扩展点。
- TLS 由 Server 前置反代（Caddy）承担，Go 代码不实现 TLS 终结。
- "App" 的 V1 形态是 PWA；原生上架（Capacitor 打包）作为 V1.x 可选项，不在本计划验收范围。
- 移动端与桌面端同一个前端工程（monorepo 两个入口），共享 API client 和类型定义。

---

## M0 工程基础与 Agent 可靠性

目标：代码进入版本管理和 CI；Agent 达到"接入真实 new-api 库不会停摆"的可靠性。

**开发思路**：

1. **先立基线，再动代码**。第一步是 `git init` + 现状快照 commit，之后每个修复都是一个独立、可回看的 diff。CI 先只跑 `go test ./...`，让"测试红了"从第一天起就是硬信号。
2. **先做结构重构，再在新结构上修 bug**。渠道快照失效和"每 pass 开关 DB 连接"是同一个结构问题——长生命周期对象（DB 连接、collector、心跳）被塞进了单次 pass 里。所以先把 `run()` 重构为"启动期创建常驻对象 → 循环只做采集"，心跳 goroutine 也在这次重构中一并拆出。如果反过来先修 bug 再重构，等于同一段代码改两遍。
3. **每个缺陷先写失败测试**。这批问题全部可以用单测复现（NULL 行 Scan、毒条目队头阻塞、命令重复下发），先写红再改绿，测试就地变成回归防线。
4. **收尾用三个破坏性 E2E 场景验收**，模拟真实环境最可能发生的事故：① 源表插入含 NULL 的行；② Server 停机 10 分钟再恢复（验证缓冲积压与 flush）；③ 缓冲中人为放一条超大报文（验证毒条目丢弃且心跳不受影响）。三个场景 Agent 均不停摆、不丢游标，M0 才算完成。

### M0.1 工程基础

| 任务 | 内容 | 验收标准 |
| --- | --- | --- |
| git 仓库化 | 当前目录 `git init`，首个 commit 为现状快照；确认 `.gitignore` 覆盖 `dist/`、`local/`、`*.env`、`data/` | `git log` 有初始提交；`git status` 干净 |
| Makefile | `make test`（go test ./...）、`make build-agent`（linux/amd64 + arm64，`-ldflags` 注入版本）、`make build-server` | 三个目标在 Linux 上可执行 |
| CI | GitHub Actions（或等价）：push 触发 `go vet` + `go test ./...` + 双平台构建 | CI 绿色 |

### M0.2 Agent P0 缺陷修复

详细问题描述见 `development-progress.md`「Agent 采集与上报修复计划」。

| 任务 | 实现要点 | 验收标准 |
| --- | --- | --- |
| logs 采集 NULL 防护 | `collectLogsSQL` 中全部可空列包 `COALESCE(col, '')` / `COALESCE(col, 0)`；`is_stream` 用 `COALESCE(is_stream, 0)` | 单测覆盖 NULL 行；源测试库插入含 NULL 行后采集不中断、游标推进 |
| DB 连接与 collector 常驻化 | `OpenMySQL` 移到 `run()`，`logcollector`、`channelcollector` 实例在循环外创建一次；pass 内只执行查询；连接断开靠 `database/sql` 池自愈 | 渠道快照间隔与哈希去重生效：30s 轮询下 10 分钟内只查一次 channels，内容不变不上报 |

### M0.3 Agent P1 可靠性

| 任务 | 实现要点 | 验收标准 |
| --- | --- | --- |
| 心跳解耦 | 心跳独立 goroutine，固定 30s，不依赖采集 pass；心跳体增加 `buffer_depth`、`consecutive_failures`、`last_pass_duration_ms`（契约向后兼容，Server 忽略未知字段则先加 Agent 侧） | 源库不可用时心跳仍按周期到达；Server agents 表能看到 Agent 自身健康 |
| 缓冲毒条目防护 | `localbuffer.Entry` 增加 `attempts`；flush 失败递增；超过上限（默认 10）丢弃该条并记 ERROR 日志 | 单测：毒条目达上限后被丢弃，后续条目继续投递 |
| flush 独立超时 | flush 循环每条独立 `context.WithTimeout(ReportTimeout)`，不占用采集 pass 预算 | 积压 N 条时恢复过程不整体超时 |
| 命令去重与审计 | state 增加 `executed_command_ids`（保留最近 200 个）；执行前查重，重复则直接返回上次结果状态；执行流水追加写 `DATA_DIR/command-audit.log`（JSON lines） | 单测：同一 command ID 二次下发不重复执行；审计文件有完整流水 |

### M0.4 Agent P2 打磨（可与 M1 并行）

backlog 估算按 `type IN (2,5)` 过滤、首个采样跳过 CPU/网络假零值、退避加 ±20% 抖动、删除 `metricaggregator.p95` 死代码。各项验收见 `development-progress.md`。

**M0 出口标准**：`go test ./...` 通过；用含 NULL 行、含积压缓冲、源库中断三个场景做本地 E2E，Agent 均不停摆、不丢游标。

---

## M1 Server 产品化补齐

目标：API 层达到可以支撑正式前端和多实例生产使用的完成度。M1 结束时 **API 契约冻结**，写入 `api-contracts.md`，M2/M3 只消费不改动。

**开发思路**：

1. **每个子项做垂直切片**：迁移 SQL → store 层（含单测）→ handler（含单测）→ 追加到本地 E2E 脚本 → 更新 `api-contracts.md`。一个子项完整走完再开下一个，任何时刻主干都是可运行、可演示的。
2. **顺序由"谁改动横切面"决定**：先做 M1.1 认证——它替换所有 Dashboard 路由的中间件，动的是横切面，最先做完可以让后续子项都在最终认证模型下开发和测试，避免返工。接着 M1.2（Agent Gateway 鉴权改造，同样是横切面）。M1.3/M1.4/M1.5 三项互相独立，可任意顺序或穿插。M1.6 硬化放最后，因为统一分页/错误格式要覆盖所有已完成的接口。
3. **兼容策略统一为"只加不改"**：迁移只新增表和列（002/003/004 递增编号，沿用现有 migrate 幂等机制）；旧的静态 Dashboard Token 和全局 Agent Token 保留为兼容回退，让现有静态 Web 和已部署 Agent 在 M1 期间持续可用，到 M5 上线检查时才移除。这保证 M1 全程系统不停机、随时可演示。
4. **维护一个持续增长的 E2E 脚本**（`deploy/e2e-server.sh`）：M1.1 完成时它覆盖"登录→改密→登出"；每完成一个子项追加一段；M1 出口时它就是完整回归脚本——"登录→建实例→Agent 上报→触发告警→通知送达→下发渠道命令→审计可查"。写它不是额外成本，是每个子项的验收动作本身。
5. **契约冻结是一个显式动作**：M1 最后一天逐接口核对 `api-contracts.md` 与实现（用 E2E 脚本的真实请求/响应做校对），在文档标注"v1 冻结"并 commit。之后前端开发中发现的任何 API 缺口，按"新增字段"处理，禁止改语义。

### M1.1 用户认证体系（替换静态 Dashboard Token）

现状：Dashboard 全部接口用单个静态 Bearer Token（`dashboard/auth.go`），不能满足产品化（无法区分用户、无法登出、Token 泄露只能改配置重启）。

**数据库**（新增迁移 `002_users.sql`，迁移体系沿用现有 `mysqlstore/migrate.go`）：

```sql
CREATE TABLE IF NOT EXISTS users (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  username VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,        -- bcrypt
  role VARCHAR(16) NOT NULL DEFAULT 'admin',  -- V1 只有 admin，字段预留
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  id VARCHAR(64) PRIMARY KEY,                 -- 随机 256bit hex
  user_id BIGINT NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  created_at DATETIME(3) NOT NULL,
  INDEX idx_sessions_expires (expires_at)
);
```

**API**：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/auth/login` | `{username, password}` → 设置 `ct_session` HttpOnly+SameSite=Strict Cookie；连续失败 5 次锁定 10 分钟（内存计数即可） |
| POST | `/api/auth/logout` | 删除 session |
| GET | `/api/auth/me` | 当前用户信息，前端启动时探测登录态 |
| POST | `/api/auth/password` | 修改密码，校验旧密码 |

**实现要点**：

- 新包 `server/internal/auth`：bcrypt 哈希、session 创建/校验/清理（过期 session 由后台任务每小时清一次）。
- Dashboard 路由的 `RequireBearerToken` 替换为 `RequireSession`；**保留** Bearer Token 作为纯 API 访问方式（供脚本/巡检），两种认证任一通过即可。
- 首个管理员账号：Server 启动时若 users 表为空，从 `CT_ADMIN_USERNAME`/`CT_ADMIN_INITIAL_PASSWORD` 创建，并在日志提示首登后修改密码。
- 变更 API（POST/PUT/DELETE）校验 `X-Requested-With: XMLHttpRequest` 头作为 CSRF 缓解（SameSite=Strict 为主防线）。

**验收**：未登录访问 Dashboard API 返回 401；登录→操作→登出→操作 401 全链路 E2E；错误密码 5 次后锁定；bcrypt 哈希落库、日志不含密码。

### M1.2 Agent Token 管理与多实例落地

现状：Agent Token 是全局静态配置；instances/agents 表已存在但没有管理 API。

**API**：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/dashboard/instances` | 实例列表 + 每实例 Agent 状态（最后心跳、游标、积压、版本） |
| POST | `/api/dashboard/instances` | 创建实例 `{instance_id, display_name}`，同时生成该实例的 agent token（随机 256bit，只回显一次，库存 SHA-256） |
| PUT | `/api/dashboard/instances/{id}` | 改名、启用/停用 |
| POST | `/api/dashboard/instances/{id}/rotate-token` | 轮换 token，旧 token 保留 24h 宽限期 |

**实现要点**：

- `agentgateway/auth.go` 从"比对全局 token"改为"按 token 哈希查 agents/instances 表"；保留 `CT_AGENT_TOKEN` 作为兼容回退（配置存在时仍接受），M5 前移除。
- 所有 Dashboard 查询 API 统一支持 `?instance_id=` 过滤；overview 返回按实例分组的摘要数组（现有单实例字段保留，向后兼容）。

**验收**：两个不同 instance_id 的 Agent 同时上报互不串数据；停用实例后其 token 被拒；轮换后旧 token 24h 内仍可用、之后 401。

### M1.3 告警事件时间线

**数据库**（迁移 `003_alert_events.sql`）：

```sql
CREATE TABLE IF NOT EXISTS alert_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  alert_id BIGINT NOT NULL,
  event_type VARCHAR(16) NOT NULL,  -- firing/acknowledged/silenced/unsilenced/resolved
  actor VARCHAR(64) NOT NULL,       -- 用户名或 'system'
  note VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  INDEX idx_alert_events_alert (alert_id, created_at)
);
```

**实现要点**：alert_store 的每次状态变更（触发、确认、静默、静默过期、自动恢复）同事务写入一条事件；`GET /api/dashboard/alerts/{id}/events` 返回时间线；确认/静默接口增加可选 `note` 字段。

**验收**：一条告警从 firing → acknowledged → silenced → resolved 全程时间线完整，actor 正确区分用户与 system。

### M1.4 通知投递强化

现状：后台 worker 有重试和 `next_attempt_at`，但没有重试上限、退避策略简单、不能手动重发。

| 任务 | 实现要点 | 验收标准 |
| --- | --- | --- |
| 最大重试与死信 | deliveries 增加 `max_attempts`（默认 8）；超限置 `status='exhausted'` 不再调度 | 持续失败的投递 8 次后停止，状态可查 |
| 指数退避 | `next_attempt_at = now + min(30s * 2^attempts, 1h)`，±20% 抖动 | 单测验证退避序列 |
| 手动重发 | `POST /api/dashboard/notifications/deliveries/{id}/resend`：重置 attempts、status='pending' | 失败/死信记录重发后 worker 重新投递 |
| Webhook 模板 | 支持钉钉/企微/飞书三种预置格式 + 自定义 JSON 模板（Go template，变量：告警名、实例、级别、时间、详情 URL） | 三种 IM 真实群机器人收到可读消息 |

### M1.5 渠道命令闭环（轻操作）

现状：契约有 `Commands`/`ChannelCommandResult`，Agent 能执行 channel.update，但 Server 侧没有创建命令的 API 和状态跟踪。

**数据库**（迁移 `004_channel_commands.sql`）：

```sql
CREATE TABLE IF NOT EXISTS channel_commands (
  id VARCHAR(64) PRIMARY KEY,          -- 随机生成，即下发给 Agent 的 command ID
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  command_type VARCHAR(32) NOT NULL,   -- channel.update
  payload_json TEXT NOT NULL,          -- status/weight/priority 变更内容
  status VARCHAR(16) NOT NULL,         -- pending/delivered/succeeded/failed/expired
  created_by VARCHAR(64) NOT NULL,
  error_summary VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  INDEX idx_channel_commands_instance (instance_id, status)
);
```

**流程**：Web 发起 `POST /api/dashboard/channels/{id}/commands`（必须带确认标记 `confirm=true`）→ 落库 pending → 该实例下次心跳响应携带 pending 命令并置 delivered → Agent 执行、report 回传结果 → 置 succeeded/failed → 写 `operation_audits`。pending 超过 10 分钟未投递置 expired。

**验收**：Web 停用一个渠道 → Agent 实际调用 new-api 管理 API → 下一次渠道快照反映新状态 → operation_audits 有完整记录。全程自动调权保持默认关闭（只有人工发起的命令）。

### M1.6 API 硬化与契约冻结

- 统一错误响应 `{"error": {"code": "...", "message": "..."}}`；统一分页参数 `page`/`page_size`（上限 100）与响应 `{items, total, page, page_size}`。
- 全部列表接口过一遍分页和索引（配合 `EXPLAIN` 确认无全表扫描）。
- 登录接口限流（IP 级，10 次/分钟）。
- 更新 `api-contracts.md`：补齐 Dashboard 全部接口的请求/响应示例，标记为 **v1 冻结**。

**M1 出口标准**：`go test ./...` 通过；本地 E2E 脚本覆盖：登录→建实例→Agent 上报→看板查询→触发告警→通知送达→确认告警→下发渠道命令→审计可查；`api-contracts.md` 与实现一致。

---

## M2 Web 正式前端

目标：用 Vue3 工程替换现有静态页（`web/`），达到可交付的桌面管理端。现有静态页保留到 M2 验收通过后删除。

**开发思路**：

1. **第一周先打通"行走骨架"**：脚手架 → `shared` 包的 typed API client（401 拦截、错误归一）→ 登录页 → 总览页 → `pnpm build` 产物由 Go Server 托管。这条最小链路验证了工程结构、认证流、图表库、构建集成四个最大的技术风险；骨架通了，剩下的页面开发就是纯粹的重复劳动，不会再有意外。
2. **新旧共存、灰度切换**：开发期间新前端挂在 `/next/` 路径，旧静态页继续服务 `/`；全部页面验收通过后一次性切换路由并删除 `web/assets`。这样任何时候系统都有可用的管理端，演示和自用不中断。
3. **先做通用件，页面只做拼装**：进入批量页面开发前，先沉淀五个通用组件——分页表格（封装 page/page_size 契约）、时间范围选择器（1h/6h/24h + 自定义）、实例选择器、状态标签（告警级别/在线状态的统一色板）、危险操作确认弹窗（渠道命令、token 轮换共用）。九个页面的开发量大头在这里，通用件质量决定整体交付速度。
4. **页面按"只读 → 有操作"排序**：日志、运行态先做（纯查询，验证图表和筛选模式）；然后告警中心、通知设置、实例管理（有状态变更）；渠道管理最后（唯一会反向操作 new-api 的页面，等其他页面把交互模式趟熟了再做）。每完成一页，对照验收标准逐条打勾并 commit。
5. **数据接口全部走 shared 层的 mock 开关**：`shared/api` 提供 mock 模式（本地 JSON 固定数据），页面开发不依赖后端起服务、不依赖真实数据积累；联调只在每页收尾时做一次。这让前端开发可以完全并行于 M4。

### M2.1 工程骨架

```
webapp/                     # 新前端工程（monorepo）
├── package.json            # pnpm workspace
├── packages/
│   ├── shared/             # API client、类型定义、时间/格式化工具
│   ├── desktop/            # 桌面端（Element Plus + ECharts）
│   └── mobile/             # 移动端（M3，Vant + PWA）
```

- Vite 构建，`desktop` 产物输出到 `web/dist/desktop`，`mobile` 输出到 `web/dist/mobile`。
- Server 静态托管调整（`httpapi/mux.go`）：`/` → desktop、`/m/` → mobile、`/api/**` 优先级不变；SPA fallback（非 /api 未命中路径返回对应 index.html）。
- `shared/api`：基于 fetch 的 typed client，401 统一跳登录；所有接口类型手写自 `api-contracts.md`（V1 不引入代码生成）。
- 开发模式：Vite dev server proxy `/api` → 本地 Go Server。

### M2.2 页面与验收标准

| 页面 | 路由 | 内容 | 验收标准 |
| --- | --- | --- | --- |
| 登录 | `/login` | 用户名密码、错误提示、锁定提示 | 登录后跳回原路径；401 全局拦截生效 |
| 总览 | `/` | 实例卡片（在线状态、心跳、积压）；核心指标图表：请求量/错误率/P95 延迟/Token 消耗（1m/5m 切换，最近 1h/6h/24h）；当前告警栏 | 图表数据与 metric_1m/5m 一致；实例离线（心跳超 2 分钟）明显标红 |
| 日志查询 | `/logs` | 按实例/用户/渠道/模型/类型/时间筛选，分页表格，行点击抽屉看采样详情 | 筛选组合正确；分页稳定；无完整请求/响应体展示 |
| 运行态 | `/runtime` | CPU/内存/磁盘/网络时序图、健康检查历史、Docker 容器状态 | 三类数据均按实例过滤；时间范围切换正确 |
| 告警中心 | `/alerts` | 当前/历史 Tab、级别与状态筛选、确认/静默（带备注）、时间线抽屉 | 时间线展示 M1.3 全部事件；操作后列表即时刷新 |
| 渠道管理 | `/channels` | 快照列表（状态/权重/模型）、启停与权重调整（二次确认弹窗，明确提示"将调用 new-api 管理接口"）、命令历史 | 命令状态流转 pending→delivered→succeeded 可见；失败显示错误摘要 |
| 通知设置 | `/notifications` | 渠道 CRUD（含 IM 模板选择与测试发送按钮）、发送记录（重试次数、下次重试、手动重发按钮） | 测试发送真实送达；死信记录可手动重发 |
| 实例管理 | `/instances` | 实例 CRUD、token 生成/轮换（只显示一次并提示保存）、Agent 详情（版本/游标/buffer 深度） | token 明文只出现一次；轮换宽限期提示 |
| 设置 | `/settings` | 修改密码 | 改密后旧 session 失效需重新登录 |

### M2.3 通用交互要求

- 所有列表/图表有 loading、空态、错误态（带重试按钮）三种状态。
- 总览/告警页 30s 自动轮询，页面不可见时暂停（`visibilitychange`）。
- 时间统一展示本地时区，接口传输统一 UTC RFC3339。
- 图表遵循单一色板；错误率超阈值红色高亮。
- 深浅色跟随系统。

**M2 出口标准**：`pnpm build` 产物由 Go Server 托管、无 CDN 外链；九个页面 E2E 冒烟（可用 Playwright 或手工清单）通过；浏览器控制台无报错；`rg -n "sk-|Bearer " webapp/` 无硬编码敏感值。验收通过后删除旧 `web/assets`。

---

## M3 Mobile App（PWA）

目标：手机上"添加到主屏"即可用的巡检 App。复用 shared 包，只做移动端 UI。

**开发思路**：

1. **移动端是"第二个消费者"，不是新系统**：API client、类型、格式化逻辑全部来自 M2 沉淀的 `shared` 包，M3 只写 Vant UI 层。如果开发中发现需要 shared 之外的新逻辑，先问一句"桌面端为什么不需要"——大概率应该下沉到 shared 而不是写在 mobile 里。
2. **按价值排序页面**：巡检 Tab 最先（这是移动端存在的理由——躺在沙发上确认所有实例正常），其次告警 Tab（移动端唯一的写操作），运行态和我的最后。做完巡检 + 告警两个 Tab 就具备可用性，可以先给真机试用收反馈，剩余页面边用边补。
3. **真机验证前置，不留到最后**：第一个页面完成时就走一遍 iOS Safari 和 Android Chrome 真机（局域网访问开发服务器）。iOS 对 PWA、viewport、safe-area 的限制多且文档不可靠，早发现早绕过；留到收尾才上真机是移动端项目最常见的翻车方式。
4. **PWA 壳最后加**：manifest 和 Service Worker 是包在稳定页面外面的壳，页面没定型前加 SW 反而让缓存干扰调试。顺序：页面全部完成 → 加 manifest 验证可安装 → 加 SW（只缓存应用壳，API 永远 network-only）→ Lighthouse 过检。
5. **离线策略从简**：监控数据的价值在于"新鲜"，离线缓存旧数据是误导。断网就显示全局横幅，不做任何数据级离线能力——这条克制住，SW 的复杂度会低一个量级。

### M3.1 页面（Vant 4，底部 Tab 导航）

| Tab | 内容 | 验收标准 |
| --- | --- | --- |
| 巡检 | 实例健康卡片列表：在线状态、错误率、P95、积压；下拉刷新 | 一屏内看清所有实例是否正常；异常实例置顶标红 |
| 告警 | 当前告警列表、滑动操作确认/静默（带备注弹层）、历史切换 | 手机上 3 步内完成一次告警确认 |
| 运行态 | 单实例切换、CPU/内存/磁盘迷你图、健康检查、容器状态 | 375px 视口可读 |
| 我的 | 登录态、修改密码、登出、版本信息 | — |

渠道轻操作在移动端 V1 **只读**（查看快照与命令历史，不发起变更），降低误操作风险。

### M3.2 PWA 能力

- `manifest.webmanifest`：名称、图标（512/192）、`display: standalone`、主题色。
- Service Worker：预缓存应用壳（HTML/JS/CSS），API 请求 network-only + 离线时全局"离线中"横幅；**不缓存任何 API 数据**（监控数据过期即误导）。
- 登录态持久（session cookie 30 天 + `/api/auth/me` 静默续期）。

**M3 出口标准**：Android Chrome 与 iOS Safari 均可添加到主屏并以独立窗口打开；Lighthouse PWA 检查通过（installable）；弱网/断网有明确离线提示不白屏。

### M3.3 原生打包（V1.x 可选，不在本计划验收）

Capacitor 包一层出 Android APK；iOS 视需求。前置条件是 PWA 版稳定运行一个月。

---

## M4 部署与运行闭环

目标：一台全新 Linux 服务器 30 分钟内完成 Server 部署；一台 new-api 服务器 10 分钟内完成 Agent 安装。

**开发思路**：

1. **由内向外逐层包装**：Dockerfile 单容器可跑 → compose 加 MySQL 与依赖顺序 → 加 Caddy 反代 → 加备份和日志轮转。每层独立验证再包下一层，出问题时能立刻定位在哪一层。
2. **Agent 侧先手工装通，再脚本化**：先在一台测试机上手工完成全部步骤（建用户、放二进制、写 unit、配置、preflight、启动）并记录每条命令——这份记录就是 `install.sh` 的初稿和 README 的初稿。脚本化的本质是把验证过的手工步骤固化，而不是凭空写脚本再调试。
3. **文档即测试**：部署 README 的验收方式是"找一个没参与开发的人（或自己在全新 VM 上严格照文档执行，禁止凭记忆补步骤）从零走一遍"。走不通的每一步都是文档 bug，当场修文档。
4. **破坏性演练是必做项，不是可选项**：`docker compose down` 再 `up` 验证数据卷；备份→删库→恢复完整走一次；Agent 机器 `reboot` 验证自启和续采。上线后第一次故障不该是第一次演练。
5. **与 M2/M3 并行的接缝**：M4 开始时前端可能未完成，Dockerfile 的前端构建阶段先用现有静态页占位，M2 验收后替换为 `webapp` 构建——镜像结构不变，只换构建阶段的输入。

### M4.1 Server 部署（Docker Compose）

```
deploy/compose/
├── docker-compose.yml     # mysql:8 + control-tower-server + caddy
├── .env.example           # 全部环境变量带注释
├── Caddyfile              # TLS 自动证书（或内网 HTTP）
└── README.md              # 部署步骤、升级步骤、常见问题
```

| 任务 | 实现要点 | 验收标准 |
| --- | --- | --- |
| Server Dockerfile | 多阶段：Node 构建前端 → Go 构建（嵌入 `web/dist`，用 `embed` 或 COPY）→ 运行层 alpine 非 root 用户 | 镜像 < 50MB；`docker run` 单容器可起 |
| Compose 编排 | MySQL healthcheck 就绪后 Server 启动；Server 启动自动执行迁移（现有 migrate 幂等）；数据卷持久化；`restart: unless-stopped` | 全新机器 `docker compose up -d` 一次成功；`docker compose down && up` 数据不丢 |
| Caddy 反代 | 443 TLS → Server 8080；自动证书；内网模式提供 HTTP 配置注释 | 公网域名 HTTPS 访问通过 |
| 备份 | `deploy/compose/backup.sh`：mysqldump 到本地目录，保留 14 天；文档含恢复演练步骤 | 备份→删库→恢复演练通过一次 |
| 日志与资源 | compose 配 `logging: max-size 50m, max-file 5`；MySQL/Server 内存限制 | 长跑不撑爆磁盘 |

### M4.2 Agent 安装

```
deploy/agent/
├── install.sh             # 下载/复制二进制、创建 ct-agent 用户、写 systemd unit、引导填配置、跑 preflight
├── control-tower-agent.service
└── README.md              # 含 new-api 侧只读账号创建 SQL
```

| 任务 | 实现要点 | 验收标准 |
| --- | --- | --- |
| 只读账号文档 | `CREATE USER ... ; GRANT SELECT ON newapi.logs TO ...; GRANT SELECT ON newapi.channels TO ...;` 写进 README | 按文档操作 preflight 全绿 |
| systemd unit | 非 root 运行、`Restart=on-failure`、`ReadWritePaths` 限定 DATA_DIR、环境变量文件 `EnvironmentFile=/etc/control-tower/agent.env`（0600） | `systemctl status` 正常；机器重启后自启 |
| install.sh | 交互式引导：Server URL、instance token、DSN → 写配置 → preflight → 启动 | 全新 new-api 机器 10 分钟完成安装并在 Web 实例页看到心跳 |
| 升级流程 | 文档：替换二进制 + `systemctl restart`，游标自动续采 | 升级演练一次，无数据缺口 |

**M4 出口标准**：从零开始的完整部署演练（Server + 1 个 Agent）由非开发者按文档独立完成。

---

## M5 试运行与发布

**开发思路**：

1. **清单驱动，不靠感觉**：M5 不写新功能，全部工作是执行三张清单（安全/功能回归/性能基线）。清单逐项打勾并在本节记录执行日期与结果，任何一项不过就回到对应里程碑修复——"差不多了"不是上线标准，清单全绿才是。
2. **试运行像值班**：接入真实 new-api 后，每天固定时间过一遍检查表（游标推进、积压趋势、告警误报/漏报、通知送达、错误日志），发现的问题记入问题清单并分级——P0（停摆/数据错误）当天修，P1（功能缺陷）试运行期内修，P2（体验）记入 V1.x backlog。**试运行期间只修 bug 不加功能**，任何"顺手加个小功能"的冲动都记 backlog。
3. **用试运行数据校准默认值**：告警阈值、采集批大小、通知退避参数的出厂默认值，用 7 天真实流量数据回头修订一次——这是试运行除验证稳定性外的第二个目的。
4. **发布是可重复的动作**：tag、构建、归档、CHANGELOG 全部走 Makefile/CI，手工步骤为零；发布完成的定义是"另一个人只凭 tag 和文档能复现同样的部署产物"。

### M5.1 上线前检查清单

安全：

- [ ] 全部 `/api/dashboard/**` 与 `/api/auth/me` 需认证；`/healthz` 与静态资源除外。
- [ ] `rg` 扫描日志输出：无 token、密码、Authorization、完整请求/响应体。
- [ ] Agent 全局静态 token 兼容逻辑已移除，全部实例使用独立 token。
- [ ] 登录限流与失败锁定生效；session 过期清理任务运行。
- [ ] 数据库账号：Agent 对 new-api 只读；Server 对 control_tower 库独立账号。

功能回归（对照 `development-progress.md` 各阶段可验证点全部重跑）：

- [ ] 采集：NULL 行、断点续采、缓冲 flush、毒条目丢弃。
- [ ] 多实例隔离、token 轮换宽限期。
- [ ] 告警全生命周期 + 时间线 + 通知送达 + 死信重发。
- [ ] 渠道命令闭环 + 审计。
- [ ] Web 九页面 + PWA 安装。

性能基线（试运行期间采集）：

- [ ] Agent 常态 CPU < 5%、内存 < 100MB（30s 轮询、单批 1000 行）。
- [ ] Dashboard 查询 P95 < 500ms（30 天数据量下，靠 metric_1m/5m 预聚合）。
- [ ] Server + MySQL 常态内存 < 1.5GB。

### M5.2 试运行

真实 new-api 实例接入（只读账号），观察 7 天：

- 每日检查：游标推进、积压趋势、告警准确性（误报/漏报记录）、通知送达率、Agent/Server 日志错误。
- 数据修正：根据真实流量校准告警阈值默认值。
- 试运行期间只修 bug 不加功能。

### M5.3 发布

- [ ] `CHANGELOG.md`；git tag `v1.0.0`；Agent/Server 二进制与镜像归档。
- [ ] 运维手册 `docs/runbook.md`：架构图、常见故障（Agent 失联/积压增长/通知失败/磁盘告警）与处置步骤、备份恢复、升级步骤。
- [ ] `docs/user-guide.md`：面向使用者的 Web/App 功能说明（截图）。

---

## 范围外（明确不做，防蔓延）

- 自动调权/自动切换渠道（P9，保持默认关闭，V1 只有人工命令）。
- 多租户/RBAC 细粒度权限（V1 单角色 admin）。
- 系统级推送（APNs/FCM）、短信/电话告警。
- new-api 之外的其他网关类型接入。
- 数据超过 90 天的长期存储与降采样（V1 定期清理任务：明细 30 天、1m 指标 30 天、5m 指标 90 天——此清理任务在 M1.6 一并实现）。

## 执行规则

- 每个里程碑内的任务按表格顺序串行；每完成一项，更新 `development-progress.md` 对应状态并提交一次 git commit。
- 里程碑出口标准全部满足才进入下一个；不满足的项要么修复要么明确移入范围外并记录原因。
- 契约变更只允许发生在 M1；M2 起如需改 API，按"新增字段向后兼容"原则处理，禁止破坏性变更。
