# Codex 任务：Web 监控展示修复（P1 批次）

本任务修复 `docs/review-web-monitoring-2026-07-13.md` 中编号 P1-1、P1-3 ~ P1-8 的问题（P1-2 是设计项，不在本任务）。逐项完成、逐项自测，全部完成后按"完成标准"整体验证。

## 背景速读

- 仓库根目录即 Go module（`controltower`），Web 静态页在 `web/`（原生 JS，无框架无构建），Dashboard API 在 `server/internal/dashboard`，MySQL 存储在 `server/internal/mysqlstore`，测试用内存存储在 `server/internal/ingest/memory_store.go`，路由组装在 `server/internal/httpapi/mux.go`。
- 指标数据模型：`aggregator.Metric`（含 `LatencyBuckets`，直方图工具在 `internal/latencyhist`），1 分钟桶存 `metric_1m`、5 分钟桶存 `metric_5m`，维度类型 `instance` / `instance_user` / `instance_channel` / `instance_model` 等，维度键形如 `inst-x:user:9`。
- Dashboard 鉴权：Bearer token 中间件已存在，新路由挂在 mux 里 dashboard 组即可继承。

## 硬性纪律（每一条都因真实事故而设，违反即返工）

1. **编码**：所有文件 UTF-8 无 BOM、LF 换行。**JS/HTML 中新增的任何中文或特殊字符（含 ·）一律写 `\uXXXX` 转义**（现有 `app.js` 风格），禁止直接输入中文字面量——本项目已发生两次 Windows 编码事故。
2. **零新依赖**：不引入任何 npm 包、Go module、CDN 资源；图表继续用原生 canvas 自绘。
3. **不碰 Agent**：`agent/**` 目录禁止改动（生产告警在跑）。
4. **API 向后兼容**：现有接口的既有参数与响应字段不得变更语义，只允许新增。
5. **安全**：所有插入 DOM 的动态值必须过 `escapeHTML`；新 SQL 一律参数化。
6. 每个工作项配套测试；不删除、不跳过任何现有测试。

## 工作项

### 任务 1（对应 P1-1）：指标历史 API + 维度 latest 模式

**后端**：
1. `mysqlstore`：新增 `QueryMetricHistory(table string 由 window 映射, dimensionType, dimensionKey string, since time.Time) ([]aggregator.Metric, error)`——按维度精确匹配、`bucket_time >= since`、按 bucket_time 升序；复用现有行扫描逻辑。新增 `Latest5mMetrics()`（复刻 `Latest1mMetrics`，表换成 metric_5m）。
2. `ingest.MemoryStore`：实现同名方法（内存过滤+排序），供测试与内存模式。
3. `dashboard`：新增 handler `GET /api/dashboard/metric-history`，参数 `dimension_type`（必填）、`dimension_key`（必填）、`window`（1m|5m，默认 1m）、`hours`（1~24，默认 1）；响应 `{items: [...]}`，item 结构复用现有 metrics DTO（含 display_key）。参数非法返回 400 与现有错误格式一致。
4. 现有 `GET /api/dashboard/metrics` 增加可选参数 `latest=true`：走 `Latest1mMetrics`/`Latest5mMetrics`（按维度各取最新桶）；不带参数行为不变。
5. `httpapi/mux.go` 注册新路由；`mux_test.go` 补路由存在性断言。

**前端（`web/assets/app.js`）**：
6. 维度数据加载（`loadMetrics` 被 overview 维度表与客户/渠道/模型工作台使用的路径）改为携带 `latest=true`——安静维度不再从列表消失；总览 KPI 与告警逻辑不变。
7. 总览趋势图数据源改为 `GET /api/dashboard/metric-history?dimension_type=instance&dimension_key=<instanceKey>&window=<当前窗口>&hours=1`；instanceKey 从 metrics(latest) 里 dimension_type=instance 的项取 dimension_key（无数据时趋势图显示现有空态文案）。

**测试**：mysqlstore 方法有 SQL 契约测试风格可循（见 `metrics_test.go`）；dashboard handler 测试覆盖参数校验、时间过滤、升序；memory store 路径覆盖端到端（handler + memory store）。

### 任务 2（对应 P1-3）：修乱码分隔符

`web/assets/app.js`：`alertExtraTime` 两处、`renderAlertList` 的 alert-meta 一处、`renderChannelListItem` 的 channel-list-meta 一处——字面 ` ? ` 全部替换为 ` · `（与第 463 行既有风格一致）。全文件 `grep '\?'` 复查不遗漏同类。

### 任务 3（对应 P1-4）：自动刷新

`app.js`：`setInterval(30000)` 调 `refreshCurrentView`，仅当 `document.visibilityState === "visible"` 时执行；页面从隐藏恢复可见时（`visibilitychange`）立即刷新一次。手动刷新按钮与视图切换行为不变。

### 任务 4（对应 P1-5）：runtime 历史表补网络列

`web/index.html` 系统指标表头增加一列 `网络 RX/TX`；`app.js` `renderRuntimeTables` 的 metrics 行渲染增加 `${formatNumber(rx)} / ${formatNumber(tx)}`（字段 `network_rx_bytes_per_second` / `network_tx_bytes_per_second`）；空态 colspan 5→6。

### 任务 5（对应 P1-6）：P50/P99 展示

1. `dashboard` metrics DTO（metric_handler 的 item 结构）新增 `p50_use_time`、`p99_use_time`（`*float64`，json omitempty）：当 `LatencyBuckets` 非空时用 `latencyhist.Quantile(buckets, 0.5)` / `(buckets, 0.99)` 计算，空时为 null。metric-history 响应同结构自动获得。
2. `app.js`：渠道详情与客户/模型详情的 P95 小卡改为三值展示：主值 P95 不变，subtext 改为 `P50 x.xxs · P99 x.xxs`（值缺失显示 `--`）。
3. handler 测试断言分位数字段。

### 任务 6（对应 P1-7）：用量统计

**后端**：
1. `mysqlstore` + `MemoryStore`：新增 `UsageSummary(since time.Time) ([]storage.UsageRow, error)`（`storage` 包定义 `UsageRow{DimensionType, DimensionKey string; RequestCount, PromptTokens, CompletionTokens, Quota int64}`）：对 metric_1m 过滤 `dimension_type IN ('instance_user','instance_channel','instance_model')` 且 `bucket_time >= since`，按维度 GROUP BY 求和，按 Quota 降序。
2. `dashboard`：`GET /api/dashboard/usage?hours=N`（默认 24，1~720），响应 `{items:[{dimension_type, dimension_key, display_key, request_count, total_tokens, prompt_tokens, completion_tokens, quota}]}`，display_key 复用现有映射函数。
3. mux 注册 + 测试（聚合正确性、时间过滤、参数校验）。

**前端**：
4. `index.html` 导航新增视图 `usage`（`用量统计`），页面含 hours 选择（24h/72h/7d=168）与三张排行表（客户/渠道/模型 × 请求数 / Token(in/out) / Quota），风格复用现有 `table-card`。
5. `app.js`：`titles` 补条目；`refreshCurrentView` 接入；渲染三表（按 dimension_type 分组，各取前 20）。

### 任务 7（对应 P1-8）：趋势图升级

`renderTrend`：
1. 底部时间轴：至少首、中、末三个桶的 `HH:MM` 标签。
2. 叠加第二条线：`error_count`，颜色 `#d64545`；左上角加简易图例（两个色块 + `请求` / `错误`）。
3. 数据源为任务 1 的 metric-history（升序无需再排序）。

## 完成标准（全部满足才算完成）

1. `go vet ./...` 与 `go test ./...` 全部通过。
2. `node --check web/assets/app.js` 通过（无 Node 则跳过并在交付说明注明）。
3. `git grep -n ' ? ' -- web/assets/app.js` 无输出（任务 2 验证）。
4. 手工核对：`web/assets/app.js`、`web/index.html` 中新增文案无中文字面量（全部 `\uXXXX`）。
5. 更新 `docs/development-progress.md` P6 表：新增一行"Web 监控展示修复（P1 批次）"，状态已完成，注明对应 review 文档编号与验证方式。
6. 提交信息：`fix(web): monitoring display fixes per 2026-07-13 review (P1 batch)`，正文逐条列出 P1 编号；一个 commit 完成，不夹带无关改动。

## 明确不做（防越界）

- P1-2（Agent 告警可见性）：设计项，另行处理。
- P2/P3 全部条目（分页、全量日志入口、健康检查趋势、多实例、暗色主题、M2 规格修订）。
- 不改 `agent/**`、不改数据库 schema（本任务只读现有表）、不改认证。
