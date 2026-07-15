# Codex 任务：v2.3-B2——Web 打磨收尾 + 渠道清晰化

四件事：v2.3-B1 验收发现的两个前端未接线收尾、系统状态页图表化（B1 文件里追加的任务 6 移到本批,以本文为准）、渠道列表多渠道场景重组。**纯 Server 读侧 + 前端改动,零 Agent 改动,零新依赖。**

**文末自查清单粘贴进 commit message。**

## 背景速读

- v2.3-B1 已交付：nameResolver（60s 缓存）、metrics/alerts/commands/audits/snapshots 响应已带 `instance_name`/`display_key` 新字段;前端已接线的只有维度页/总览/审计三处。
- `ServerMetricQuery` 已支持 StartTime/EndTime;`server_metrics_10s` 每 30 秒一条采样。
- 渠道页数据：`dashboard.metrics(dimension_type=instance_channel)` 有请求量/错误率/display_key;`channelSnapshots(latest_only)` 有 status（enabled/disabled/auto_disabled）。
- 组件复用：TrendChart、HoursSelect、MetricMini、RateBar、StatusTag、`utils/format.ts`。样式进 `b8.css`。

## 工作项

### 任务 1：告警中心接名称字段

`AlertsView.vue`：维度列改用 `display_key`（无值回退旧显示）;实例列改 `instance_name`（悬停 tooltip 露 instance_id）;时间列走 `formatTime`。时间线弹层里同样处理。

### 任务 2：样本分析页格式收尾

`SamplesView.vue`：created_at 列走 `formatTime`;total_tokens 千分位（formatNumber）;use_time 保留两位小数带 s 后缀（≥10s 红色,与延时分诊页 hot 样式一致）;error_summary 列 show-overflow-tooltip。顺手检查 UsageView/NotificationsView/InstancesView/LatencyView 的时间与数字列,未走 format.ts 的一并接上（改动点逐页列进交付说明）。

### 任务 3：系统状态页图表化（**已由 ac14fad 交付,本批只做核对**：对照下述要求逐条检查,达标则跳过,缺项补齐）

1. **当前值卡片区**（每实例一组）：CPU%、内存%、磁盘%、负载、网络收发——取最新采样,MetricMini + 阈值配色（≥90% 红、≥70% 橙、否则绿）,带采集时间戳,超过 2 分钟未更新显示"数据陈旧"标记;
2. **趋势图**（TrendChart + HoursSelect,1h/6h/24h）：图① CPU%+内存%（percent 轴）;图② 磁盘使用率;图③ 网络收/发速率。`/api/dashboard/server-metrics` 若未透出 start/end 查询参数则补上（additive,存储层已支持）;多实例按当前实例选择器过滤,未选实例时提示选择;
3. 原始采样表收进折叠面板（默认收起）;Agent/健康检查/容器表保留并按 format.ts 规范化。

### 任务 4：渠道列表多渠道重组（客户页同样受益的部分一并生效）

`DimensionWorkspace`/渠道页：

1. **搜索框**：左列顶部,按 display_key/ID 实时过滤;
2. **状态分组过滤签**：`异常(错误率≥10%) | 注意(>0) | 正常 | 无流量(窗口内请求=0) | 已禁用(快照 status≠enabled)` 带计数,点击过滤,可多选;默认视图：异常+注意置顶展示,正常按请求量降序,**无流量与已禁用折叠成两个可展开分组**;
3. **健康墙视图**：列表/网格切换按钮（选择存 localStorage）。网格模式：每渠道一个色块（背景按状态红/橙/绿/灰,块内显示名称、错误率、请求量）,点击进入右侧详情;≤900px 自动退回列表;
4. 客户/模型页复用搜索与状态置顶逻辑（无"已禁用"概念,不显示该签;健康墙仅渠道页启用）。

## 验证要求

1. `pnpm build`、`pnpm test`、`go test ./...` 全绿;
2. server-metrics 若新增 start/end 参数:handler 单测（时间过滤、非法参数 400）;
3. 手工走查（seed-demo-data.sh）：告警中心名称化、样本页格式、系统状态三图有数+折叠表、渠道页搜索/过滤签/折叠分组/健康墙截图,逐条记入交付说明;
4. 最终视觉验收以用户浏览器走查为准。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 零 Agent 改动、零新依赖;Dashboard API 只增不改
- [ ] 告警中心/样本页再无裸 ID 与原始时间格式
- [ ] 系统状态:卡片阈值配色+陈旧标记、三张趋势图、原始表默认折叠
- [ ] 渠道页:搜索、五个状态签、无流量/禁用折叠、健康墙切换并记忆
- [ ] 任务 2 顺手检查的页面改动逐页列进交付说明
- [ ] 一个 commit:`feat(web): alerts naming, runtime charts and channel triage view (v2.3-B2)`

## 明确不做

按供应商/用途分组（依赖渠道 group 字段,需 Agent 快照补采,与 priority 字段同批,归下次 Agent 升级）;暗色主题;移动端 PWA;虚拟滚动（渠道数到千级再说）。
