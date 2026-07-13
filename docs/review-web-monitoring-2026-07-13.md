# Web 监控界面与设计文档 Review（2026-07-13）

对静态 Web 监控界面（`web/`）、Dashboard API 数据供给和相关设计文档的完整 review。总体结论：**信息架构合理、工程质量过关（escapeHTML 全覆盖、空态/连接态齐全、渠道快照联动做得细）；存在 1 个结构性数据缺失、1 个系统性告警可见性缺口、若干"采了未展示"与小缺陷**。

问题按优先级编号，P1 为本轮修复（Codex 任务：`codex-task-web-monitoring-fixes.md`），P2/P3 为规划项（归入 v1.1 / M1 / M2）。

## P1：本轮修复

### P1-1 指标历史深度不足（结构性缺失）

**问题**：`/api/dashboard/metrics` 只返回全维度混合的最近 200 行（`mysqlstore.recentMetrics(table, 200, false)`）。每个请求每分钟产生最多 8 个维度行，有流量时 200 行只覆盖最近几分钟。后果：
- 总览"近实时请求趋势"实际只有几个数据点；
- 客户/渠道/模型详情页只有单个分钟桶的数据；
- **安静维度从页面消失**：某客户 10 分钟没请求，其桶掉出 200 行窗口，客户监控页"查无此人"。

**方案**：
1. 新增指标历史 API：`GET /api/dashboard/metric-history?dimension_type=&dimension_key=&window=1m|5m&hours=N`（默认 1m/1h，上限 24h），按桶时间升序返回单维度的历史桶；新增 store 方法（mysqlstore + memory store）按维度和时间范围查询。
2. 现有 `/api/dashboard/metrics` 增加 `latest=true` 参数：按维度取各自最新桶（mysqlstore 已有 `Latest1mMetrics`，补 `Latest5mMetrics`），维度工作台改用它——安静维度不再消失。
3. 总览趋势图改用 metric-history（instance 维度，window 跟随选择器），趋势深度从"几分钟"变为可配置小时级。

### P1-2 生产告警在 Web 不可见（本轮先做设计对齐，完整实现在 v1.1/v2.0）

**问题**：告警中心页只展示 Server 端规则计算的告警；生产实际在用的 Agent 独立模式钉钉告警（错误窗口 episode、提醒、恢复）只存在于 Agent 内存和钉钉群，Web 完全不可见；v1.1 新信号（探测/静默/吞吐骤降）无任何展示规划。

**方案**（分阶段）：v1.1 实现时 Agent 将 episode 事件持久化到本地 JSON lines（触发/提醒/恢复/收尾，含维度、时间、计数），双模式接入后随 report 上报；v2.0 Web 增加"Agent 告警时间线"。**本轮仅在 v1.1 设计文档补"信号持久化与可视化"章节**（已补，见 `design-v1.1-early-warning.md` 第 9 节前）。

### P1-3 乱码分隔符（Windows 编码事故同类）

**问题**：`app.js` 中告警 meta 与渠道 meta 的分隔符是字面 `?`（`alertExtraTime` 两处、`renderAlertList` 一处、`renderChannelListItem` 一处）——对照第 463 行正确的 `·`，这是 Windows 侧编辑时"·"被转码；界面显示"? 恢复 xxx"。

**方案**：统一替换为 `·`；**今后 JS 里新增中文/特殊字符一律用 `\uXXXX` 转义**（本文件既有风格），防复发。

### P1-4 无自动刷新

**问题**：监控页需要手动点刷新按钮。

**方案**：30 秒 `setInterval` 调 `refreshCurrentView`，`document.visibilityState !== "visible"` 时跳过；手动刷新与视图切换保持现有行为。

### P1-5 runtime 历史表缺网络列

**问题**：server_metrics 每条都带网络 RX/TX 速率，总览显示了最新值，但系统状态页历史表只有 CPU/内存/磁盘/Load。

**方案**：历史表增加"网络 RX/TX"列（`formatNumber` B/s），空态 colspan 同步 5→6。

### P1-6 延迟直方图完全未用

**问题**：metric_1m/5m 每桶带完整 `latency_buckets`，UI 只显示 avg/P95 两个点值；P50/P99 与分布信息浪费。

**方案**：metric DTO 增加 `p50_use_time`/`p99_use_time`（服务端用 `latencyhist.Quantile(buckets, 0.5/0.99)` 计算，buckets 为空时为 null）；渠道/客户/模型详情面板的 P95 小卡升级为 P50/P95/P99 一行展示。

### P1-7 缺用量/成本视角

**问题**：quota、token 每桶每维度都有，但只在详情面板显示点值；没有按客户/渠道/模型的消耗排行与趋势——LLM 网关的运营刚需。

**方案**：新增 `GET /api/dashboard/usage?hours=N`（默认 24，上限 720）：对 metric_1m 按维度聚合 SUM(quota)、SUM(prompt+completion tokens)、SUM(request_count)，返回 instance_user/instance_channel/instance_model 三组排行；新增"用量统计"导航视图，三张排行表（客户/渠道/模型 × 请求数/Token/Quota）。

### P1-8 趋势图无时间轴、只画请求量

**方案**：canvas 增加首/末桶时间标签；叠加错误数第二条线（红色）；保留零依赖自绘。

## P2：规划项（不在本轮 Codex 任务）

| # | 问题 | 归属 |
| --- | --- | --- |
| P2-1 | 样本页固定 limit 100、无分页、无时间范围过滤 | M1（统一分页规范时一并做） |
| P2-2 | full_debug 模式下 log_events 全量查询无 UI 入口（`/api/dashboard/logs` API 已存在未接）；P6 看板"日志查询页接入 /api/dashboard/logs"与实现（log-samples）漂移 | M2 |
| P2-3 | 健康检查延迟只有表格无趋势（延迟爬升是故障前兆） | M2（图表体系建立后） |
| P2-4 | 无多实例选择器 | M1（instances API）+ M2 |
| P2-5 | canvas 硬编码白底，不适配暗色主题 | M2（正式前端工程主题体系） |
| P2-6 | Dashboard token 默认值 `local-dashboard-token` 硬编码 | M1（认证体系替换后消失） |

## P3：设计文档修订项

- **M2 规格与静态版分叉**：静态版的客户/模型监控工作台（主从布局）不在 M2 九页清单中，但体验好——M2 启动时应继承这两页而非推倒（届时更新 `development-plan.md` M2.2 页面表）。
- **v1.1 设计补章**：Agent 告警 episode 事件持久化与上报（见 P1-2）。
- **P6 看板漂移**：日志查询页实际接的是 log-samples（见 P2-2）。

## 验证基线

review 时代码位于 commit `a35a66e`；`go test ./...` 23 包全绿。
