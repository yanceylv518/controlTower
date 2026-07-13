# Codex 任务：v2.2-B1——Nginx timing 延时分诊（采集上报 + Web 分析页）

Agent tail 生产 Nginx 的 `timed` 访问日志，按分钟聚合分段延时指标并随既有上报链路送到 Server，Web 新增「延时分诊」页做趋势与归因分析。**不发钉钉、不产生任何告警消息**——这是分析型数据，在 Control Tower 里看（用户决策 2026-07-13，替代原信号 E 告警方案）。设计依据：`latency-diagnosis.md`（字段语义与分诊公式）。

**文末自查清单粘贴进 commit message。**

## 背景速读

- 生产两台 new-api 前的 Nginx 已启用 `timed` 日志格式，Agent 与 Nginx 同机，可只读访问日志。日志样例（字段名固定）：

```
1.2.3.4 - "POST /v1/chat/completions HTTP/1.1" [13/Jul/2026:10:00:00 +0800] status=200 rt=12.345 uct=0.001 uht=1.234 urt=12.344 bytes=45678 req_len=890
```

- 字段坑：`uct`/`uht`/`urt` 可能为 `-`（无 upstream）；Nginx 层重试时为逗号分隔多值（解析时**求和**）；`rt` 恒为单值。
- 分诊公式（`latency-diagnosis.md`）：`uht` ≈ 首字节段（TTFT，new-api/上游前段）；`urt−uht` ≈ 传输段（流式/链路）；`rt−urt` ≈ 客户端段。
- 上报契约：`agent/internal/reporter/contracts.go` 的 `AgentReportRequest` 加数组字段即可（对齐 `server/internal/agentgateway/contracts.go`）；Server 侧 `HandleReport` 有数组上限校验（参考 ChannelSnapshots ≤5000）。
- 迁移编号：**用 `007_nginx_timing.sql`**——006 已被在途的 v2.1-B1（tuning）占用，两批可能乱序合入，迁移各自幂等 + ApplyDir 全量应用，编号错开即可。
- 所有 CREATE TABLE 钉 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`（sanity test 强制）。
- Web：pnpm workspace（packages/shared 的 ApiClient + packages/desktop 的 Vue3/Element Plus/ECharts），趋势图参考 `TrendChart.vue` 用法。

## 硬性纪律

- **零消息推送**：本模块不接钉钉、不接任何 webhook，也不写 alert-events.jsonl——纯采集与展示。
- **失效安全（第一验收项）**：`CT_NGINX_ACCESS_LOG` 留空 → 模块完全不启动，零副作用；配置了但文件不存在/无读权限 → 启动 WARN 一条，之后每 30 秒静默重试，**绝不 panic、不退出、不影响采集上报主链路**；脏行静默跳过（内部计数）。
- 独立模式（无 `CT_SERVER_URL`）下配置了日志路径 → 启动 WARN"nginx timing 需要 Server 上报，独立模式不启动"，不采集。
- 零新增依赖（Go 与前端均是）；不改 Nginx 任何东西（只读）。
- Dashboard API 新增端点为**增量**，不动既有 v1 契约；响应不泄漏存储层结构体。

## 工作项

### 任务 1：Agent 解析与 tail（新包 `agent/internal/nginxtiming`）

- `ParseLine(line string) (Entry, bool)`：提取 `status`、`rt`、`uct`、`uht`、`urt`、`bytes` 与 `$request` 的 method+path（**只留 path，丢弃 query**，防敏感信息落库）。缺 `status=` 或 `rt=` 返回 false；`-` 视为缺失；逗号多值求和。
- Tailer：启动 seek 到文件末尾（不回放历史）；轮转检测（inode 变化或文件变小 → 重开从头读）；文件消失按失效安全纪律重试。
- 每 10 分钟一行 info 统计日志（parsed/skipped 计数），确认模块活着。

### 任务 2：分钟桶聚合与慢样本（同包）

每 UTC 分钟一个桶，字段：

```
bucket_at, request_count, upstream_count(有 upstream 的条数),
status_4xx, status_5xx, status_504(单列，不计入 5xx 列),
rt_p50/p95/max, uht_p50/p95/max, transfer_p50/p95/max(urt−uht),
bytes_total,
slow_count(rt ≥ CT_NGINX_SLOW_RT_SECONDS，默认 10),
slow_ttft_count(慢请求中 uht ≥ rt/2，首字节段主导),
slow_transfer_count(其余慢请求，传输段主导)
```

- 分位数：桶内存原始值算精确分位，每桶值数组上限 10000（超出丢弃并计数，注明近似）。
- 慢样本：每桶保留 rt 最大的 ≤5 条（path/status/rt/uht/urt/bytes/发生时间），供 Web 钻取。
- 桶关闭（下一分钟到来或 flush 间隔）后进入待上报队列；队列上限 720 桶（约 12 小时），满了丢最旧并 WARN 一次。

### 任务 3：上报接线

- `AgentReportRequest` 新增 `nginx_timing_buckets`、`nginx_slow_samples` 两个数组字段（omitempty，老 Server 忽略未知字段不受影响）；上报成功才出队，失败留队随下轮重试（幂等靠 Server 端 upsert）。
- 配置新增：`CT_NGINX_ACCESS_LOG`（默认空=禁用）、`CT_NGINX_SLOW_RT_SECONDS`（默认 10）。
- `main.go`：完整模式且日志路径非空时启动常驻 goroutine（随进程 ctx 退出）。

### 任务 4：Server 存储（`007_nginx_timing.sql` + mysqlstore + gateway）

- 表 `nginx_timing_1m`：instance_id + bucket_at 唯一键，其余列同任务 2 桶字段，**upsert**（重复上报覆盖，天然幂等）。
- 表 `nginx_slow_samples`：id PK、instance_id、occurred_at、path、status、rt、uht、urt、bytes；索引 (instance_id, occurred_at)。
- `HandleReport`：解码新字段、数组上限（buckets ≤1500、samples ≤5000）、超限整体 400（与既有行为一致）；写入走 Sink 接口扩展。
- 保留清理：并入既有 retention runner，两表都按 `CT_RETENTION_DETAIL_DAYS` 清理（与明细数据同档，无新 env）。

### 任务 5：Dashboard API（增量端点）

- `GET /api/dashboard/nginx-timing?instance_id=&hours=`（hours 上限 168）→ 按分钟返回桶序列 + 汇总（总请求、5xx/504 数、慢请求数、slow_ttft/slow_transfer 计数与占比）。
- `GET /api/dashboard/nginx-timing/slow-samples?instance_id=&hours=&limit=`（limit 默认 50 上限 200）→ 倒序慢样本。
- 都走 `protect(...)`；无数据返回空数组不报错。

### 任务 6：Web「延时分诊」页（packages/desktop）

- 新路由 `/latency`，侧边导航加入口，页面结构对齐既有维度页（实例选择器 + 时间范围 1h/6h/24h/7d）。
- 内容自上而下：
  1. **归因卡**：所选范围内慢请求总数、首字节段主导 vs 传输段主导的计数与占比（这就是 latency-diagnosis.md 命令② 的自动化版）+ 5xx/504 计数；
  2. **趋势图 ×3**（复用 TrendChart 模式）：TTFT（uht p50/p95）、传输段（transfer p50/p95）、请求量与 5xx/504（柱/线混合）；
  3. **慢样本表**：时间/path/status/rt/uht/urt/bytes，rt 与 uht 超阈值单元格高亮。
- 空态：该实例无数据时显示引导文案"未启用 Nginx timing 采集，配置 CT_NGINX_ACCESS_LOG 后生效"。
- shared 包补类型与 client 方法；`pnpm build`、`pnpm test` 通过。

### 任务 7：文档

- `deploy/agent.config.example`、`deploy/agent.env.example` 追加 2 个新变量（注明"留空禁用、缺文件不报错、独立模式不生效"）；standalone example **不加**（独立模式不支持，加了误导）。
- `agent/README.md` 新增小节：启用前提（Nginx `timed` 格式，链接 `docs/latency-diagnosis.md`）、日志读权限两种给法（`setfacl -m u:ct-agent:r` 或加 adm 组）。
- `docs/development-progress.md` 记一行。

## 验证要求

1. `make test` 全绿；`pnpm build && pnpm test` 通过。
2. Agent 单测：解析（正常/`-`/逗号多值/非 timed 行/query 剥离）；tailer（追加可读、轮转续读、**文件不存在不报错持续重试**）；聚合（分位数、504 不计入 5xx、慢请求归因分类、桶上限、队列满丢最旧）；独立模式不启动。
3. Server 单测：upsert 幂等（同桶重报覆盖）；数组超限 400；retention 清理两表；API 汇总计算与时间过滤。
4. 手工冒烟：临时文件当日志 + 本地 server，`printf` 追加若干混合行（快/慢 TTFT/慢传输/504），确认桶入库、`/latency` 页三图与归因卡有数、慢样本表可见，过程记入交付说明。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 零消息推送：全批无钉钉/webhook/alert-events 相关代码
- [ ] 失效安全四场景自测：未配置零启动 / 文件缺失 WARN+重试 / 无权限同上 / 脏行静默跳过
- [ ] 独立模式配置了日志路径只 WARN 不采集
- [ ] 迁移用 007 且钉 ENGINE/CHARSET/COLLATE；nginx_timing_1m upsert 幂等
- [ ] path 剥离 query；慢样本每桶 ≤5 条
- [ ] Dashboard 既有端点零改动；新端点走 protect 且不泄漏存储结构体
- [ ] 配置示例与 agent/README 已更新；standalone example 未加新变量
- [ ] 一个 commit：`feat: nginx timing latency analytics from agent to web (v2.2-B1)`

## 明确不做

任何告警/消息推送（分析型数据，用户明确不发钉钉；将来真要告警另起批次）；渠道/客户维度归因（Nginx 日志无此信息）；客户端段（rt−urt）单独成图（先并入慢样本表看原始值）；独立模式支持；修改 Nginx 配置或 logrotate；`uct` 入桶（保留在慢样本原始值里即可）；网关开销分解探测（归挂起的 v1.1 探测批次）。
