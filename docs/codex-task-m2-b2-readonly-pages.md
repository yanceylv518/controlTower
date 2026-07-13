# Codex 任务：M2-B2——通用组件沉淀 + 六个只读页

M2 第二批。骨架（B1）已打穿技术栈，本批进入批量页面模式：**先沉淀通用件，页面只做拼装**。全部数据来自已冻结契约（`docs/api-contracts.md`），**纯前端批次，`server/**`、`agent/**`、旧静态页 `web/index.html`/`web/assets` 一律不改**。

**文末自查清单填好粘贴进 commit message。**

## 硬性纪律

零新依赖（只用 B1 已装的：Vue3/Router/Pinia/Element Plus/ECharts）；lockfile 若变动仅允许 workspace 内部联动；`web/dist` 不提交；UTF-8/LF；`pnpm typecheck` + `pnpm build` + 全部 Go 测试 + CI 双 job 必须绿；沿用 B1 的代码风格与目录结构。

## 工作项

### 任务 1：shared 包补 API 与类型（对照冻结契约逐字段写）

`api/dashboard.ts` 增补：`instances()`（GET /api/dashboard/instances）、`channelSnapshots(params)`（latest_only/limit）、`logSamples(params)`（sample_kind/model_name/user_id/request_id/limit/offset）、`agents/serverMetrics/healthChecks/dockerStatuses(params)`（instance_id/limit）、`usage(hours)`。类型 snake_case 与契约一致。

### 任务 2：通用件（`packages/desktop/src/components/` 与 `composables/`）

| 名称 | 职责 |
| --- | --- |
| `useAutoRefresh(fn, ms=30000)` | 从 OverviewView 抽出：定时 + `visibilitychange`（恢复可见立即刷一次）+ 卸载清理；Overview 改用它（回归验证） |
| `useAsyncData(loader)` | loading/error/data 三态 + `reload()`；错误态含重试按钮的配套渲染组件 `AsyncPanel.vue`（slot 注入内容区） |
| `StatusTag.vue` | 统一状态→颜色/文案映射：log_type(consume/error)、health(up/down)、docker(running/stopped)、渠道(enabled/disabled/auto_disabled)、severity(critical/warning/info)、告警/命令/投递 status——一处维护 |
| `RateBar.vue`、`MetricMini.vue` | 复刻旧静态页的比率条与迷你指标卡（视觉延续） |
| `DimensionWorkspace.vue` | 主从布局通用件：左列表（插槽渲染行）+ 右详情（插槽），受控 selectedKey，空态/计数内建——客户/渠道/模型三页共用 |
| `HoursSelect.vue` | 时间范围选择（1h/6h/24h，值为小时数），供趋势图与用量页 |
| `InstanceSelect.vue` + Pinia `filters` store | 顶栏全局实例筛选（“全部实例”默认，选项来自 instances API）；各页查询携带所选 instance_id（契约支持处） |

### 任务 3：六个只读页（路由 + 左侧导航分组：监控 / 分析 / 系统）

1. **客户监控 `/customers`**、**模型监控 `/models`**：`DimensionWorkspace` + metrics(latest) 过滤 `instance_user`/`instance_model`；列表行：display_key、请求数、成功率 Tag、错误率 RateBar、P95；详情：KPI 行（请求/成功率/错误率/P50·P95·P99/Token in-out/Quota）+ 质量信号条（成功/错误/流式/cache）+ **该维度的历史趋势图**（metric-history，dimension_key 精确传参，HoursSelect 联动）——这是对旧静态页“单桶详情”的实质升级。
2. **渠道监控 `/channels`**：同上，另叠加 channel-snapshots 联动（latest_only）：名字 `渠道名 (#id)`、状态 Tag、权重、模型覆盖 chips（超 4 个折叠 +N）；快照缺失时优雅回退纯指标展示。
3. **样本分析 `/samples`**：筛选行（sample_kind/model_name/user_id/request_id）+ el-table（时间/样本类型/结果/模型/用户/Token/耗时/request_id+错误摘要）+ **limit/offset 分页**（上一页/下一页，页大小 50）。
4. **系统状态 `/runtime`**：四张卡片表——Agent（含 online 判定、游标/源库最新、积压 Tag、上报延迟）、系统指标（含**网络 RX/TX 列**）、健康检查、容器状态。
5. **用量统计 `/usage`**：HoursSelect（24/72/168）+ 三张排行表（客户/渠道/模型 × 请求数/Token in/out/Quota，各前 20）。

全部页面：AsyncPanel 三态、useAutoRefresh、随全局实例筛选联动（usage 契约无 instance 参数则不联动并注释说明）。

### 任务 4：文档

`docs/development-progress.md` M2-B2 行更新；`webapp/README.md` 补页面清单一览。

## 验证要求

1. `pnpm typecheck`、`pnpm build` 零错误；`go test ./...` 全绿（不应有 Go 改动，若有说明理由）；CI 双 job 绿。
2. **手工验证并记录**：起 Server + 构建后，逐页打开六个路由：有数据环境下渲染正确；无数据时空态不报错；断开 Server 后错误态出现且重试按钮工作；实例筛选切换后表格内容变化（多实例数据时）。逐页记入 commit message（或如实注明未验证项）。
3. 通用件复用自查：三个维度页必须共用 `DimensionWorkspace`，禁止各自复制布局代码；Overview 必须改用 `useAutoRefresh`。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 任务 1~4 逐节核对；shared 类型与冻结契约逐字段一致
- [ ] 零新依赖（git diff package.json 确认）；web/dist 未提交；node_modules 未提交
- [ ] server/agent/旧静态页零改动（git diff 确认）
- [ ] 通用件复用达标（维度页共用工作台、Overview 用 useAutoRefresh）
- [ ] 手工验证结果逐页记录
- [ ] 一个 commit：`feat(web): shared components and read-only pages (M2-B2)`

## 明确不做

操作类页面（告警/通知/实例管理/命令下发，B3）；`/next`→`/` 切换（B4）；暗色主题；vitest。
