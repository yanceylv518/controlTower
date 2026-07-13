# Codex 任务：M2-B5（增强）——维度页重要指标趋势图

用户需求：客户/渠道/模型三个监控页的详情面板，重要指标要有**趋势曲线**，而不是只有最新周期的统计快照。数据侧现成（`metric-history` 每桶含全量指标字段），**纯前端批次**：`server/**`、`agent/**` 零改动、零新依赖。三页共用 `DimensionView`，改一处三页生效。

**文末自查清单粘贴进 commit message。**

## 工作项

### 任务 1：可复用趋势图组件 `TrendChart.vue`

props：`title`、`series: {name, color, data: [time, value][], unit?}[]`、可选 `percent`（y 轴 0~100）；内部 ECharts 折线（沿用已注册的 LineChart/Grid/Tooltip/Legend，**不得新增 ECharts 模块导入**），tooltip 显示桶时间与各序列值（带单位），空数据显示既有空态样式。四张图共用此组件，禁止复制粘贴图表代码。

### 任务 2：DimensionView 详情面板改造

KPI 卡行保留（标注"最新周期"），下方新增 **2×2 趋势网格**（每图高约 180px，随 HoursSelect 联动）：

| 图 | 序列（字段 → 曲线） |
| --- | --- |
| 请求与错误 | `request_count`（主色）、`error_count`（红） |
| 成功率 / 错误率 | `success_rate×100`（绿）、`error_rate×100`（红），y 轴 0~100% |
| 延迟（秒） | `p50_use_time`、`p95_use_time`、`p99_use_time` 三线（空值跳点，不画 0） |
| Token 消耗 | `prompt_tokens`（入）、`completion_tokens`（出） |

数据来源：现有 `metricHistory(dimension_type, dimension_key, window, hours)` **一次请求喂四张图**（不要请求四次）。

### 任务 3：时间范围与窗口自适应

- HoursSelect 现有 1h/6h/24h 继续作用于本区；**hours ≥ 6 时自动改用 `window=5m`**（1h 用 1m）——24h 的 1m 桶有 1440 个点，5m 降到 288 个，渲染与可读性都更好；tooltip 中体现桶粒度。
- 切换维度（左侧列表选中变化）与切换时间范围都重新拉取；加载中不闪空（保留旧图至新数据到达或用 loading 遮罩，注明选择）。

### 任务 4：文档

`docs/development-progress.md` 补一行；`webapp/README.md` 页面说明更新。

## 验证要求

1. `pnpm typecheck`、`pnpm build`、CI 双 job 绿；`go test` 不应有变化。
2. **手工验证**（用 `deploy/seed-demo-data.sh`——每维度 12 个 1m 桶，正好画曲线）：三个维度页详情各出四张趋势图、曲线与种子数据规律吻合（请求量 20~38 波动、错误率约 5%~15%、P95 3.x 秒）；切换 1h/6h/24h 图刷新且 ≥6h 时为 5m 桶；切换左侧维度图随之变化；无数据维度显示空态；浏览器控制台无报错。逐项记入 commit message。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 任务 1~4 逐节核对；四图共用 TrendChart、单次请求喂四图
- [ ] 零新依赖、零 ECharts 新模块、server/agent 零改动、产物未提交
- [ ] 手工验证逐项记录
- [ ] 一个 commit：`feat(web): metric trend charts on dimension pages (M2-B5)`

## 明确不做

列表行内迷你 sparkline（后续按需）；总览页改动；自定义时间范围选择器。
