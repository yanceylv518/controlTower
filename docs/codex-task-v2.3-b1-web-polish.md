# Codex 任务：v2.3-B1——Web 体验打磨（图标 / 名称化 / 指标可视化）

用户反馈：页面粗糙——菜单没图标、到处显示裸 ID、指标缺少视觉表达。本批做一轮系统性打磨：**所有 ID 显示处换成名称、菜单加图标、关键指标加颜色和图形语义**。不加新功能页、不改业务逻辑。

**文末自查清单粘贴进 commit message。**

## 背景速读

- Web：pnpm workspace，`packages/desktop`（Vue3 + Element Plus + ECharts 按需），代码是压缩单行风格，样式按批次放 `src/b1.css`~`b6.css`（本批新增 `b7.css`）。
- 裸 ID 的根源在 server：`server/internal/dashboard/metric_handler.go` 的 `displayDimensionKey` 只拼出 "渠道 5"/"用户 12"。
- **名称数据全部现成**：渠道名=最新 `channel_snapshots.channel_name`；用户名=`log_events.username`（按 user_id 取最近一条）；实例名=`instances.name`（`InstanceSelect.vue` 已在用 `name || instance_id`，可作参考）。
- 组件可复用：`RateBar.vue`（比例条）、`StatusTag.vue`、`MetricMini.vue`、`TrendChart.vue`。
- 视图清单：Overview / Dimension(客户/渠道/模型共用) / Alerts / Samples / Usage / Latency / Runtime / Notifications / Instances / Audits / Settings / Login。

## 硬性纪律

- **唯一允许的新依赖**：`@element-plus/icons-vue`（官方图标库，按需 import）。其余前后端零新增依赖。
- Dashboard API **只增不改**：现有字段名与语义不动；名称化通过增强 `display_key` 内容与新增附加字段（如 `instance_name`）实现，老字段照旧返回。
- Server 侧只允许"名称解析"级改动：不动存储 schema、不动告警/聚合/调权逻辑、不新增采集。
- 名称解析必须做**进程内缓存**（60 秒 TTL 即可），不能每个请求都全表查名称。
- 找不到名称时**回退现状**（"渠道 5"），绝不空白或报错。
- `pnpm build`（含 typecheck）与 `go test ./...` 全绿。

## 工作项

### 任务 1：Server 名称解析（集中一处，全端受益）

新建 `server/internal/dashboard/names.go`：`nameResolver`（带 TTL 缓存），提供 `ChannelName(instanceID, channelID)`、`UserName(instanceID, userID)`、`InstanceName(instanceID)`。数据源见背景速读。接入点：

1. `displayDimensionKey` 升级：`instance_channel` → `渠道名 (ID 5)`（无名回退 `渠道 5`）；`instance_user` → `用户名 (ID 12)`（同回退）；`instance_model` 不变；
2. metrics / metric-history / alerts / channel-commands / operation-audits / channel-snapshots 各列表响应**新增** `instance_name` 字段（有则填，无回退 instance_id）；
3. alerts 响应若维度是裸 key，新增 `display_key` 字段同样走解析；
4. 单测：渠道/用户名映射、缺失回退、缓存生效（同 key 二次调用不再查库，用 fake store 计数断言）。

### 任务 2：菜单图标与侧栏

`AppShell.vue`：每个菜单项加图标（`@element-plus/icons-vue` 按需引入，建议：总览 Odometer、客户 User、渠道 Connection、模型 Box、告警 Bell、样本 DataAnalysis、用量 PieChart、延时分诊 Timer、系统状态 Monitor、通知 Message、实例 OfficeBuilding、审计 List、设置 Setting）；active 态左侧高亮条；顶部 logo 区加同款图标风格。不做折叠侧栏。

### 任务 3：维度页左列可读化（客户/渠道/模型三页共用）

`DimensionWorkspace` 列表行升级为固定结构：**名称（走 display_key）+ 请求量 + 错误率色条**（复用 RateBar，错误率 >10% 行首红点、>0 橙点、0 绿点）；列表默认按请求量排序，提供"按错误率"切换；选中项样式强化。

### 任务 4：Overview 卡片语义化

- 错误率卡：>5% 红、>1% 橙、否则绿；成功率同理反向；
- 健康检查/容器卡用 `StatusTag` 展示（异常数 >0 变 danger）；
- 数字千分位（请求数/TPM）；每张卡配小图标；
- 活跃告警列表里的维度显示 display_key（不是裸 key）。

### 任务 5：全站表格与细节 sweep（逐页过一遍，按此清单）

对 Alerts / Samples / Usage / Audits / Instances / Notifications / Latency / Runtime 每页检查：

- [ ] 裸 `instance_id` 列 → 显示实例名（新 instance_name 字段，悬停 tooltip 露原 id）；
- [ ] 裸渠道/用户 id → display_key；
- [ ] 时间列统一 `YYYY-MM-DD HH:mm:ss` 本地时间（新建 `src/utils/format.ts`：`formatTime`/`formatNumber`/`formatBytes`，全站复用，删除各页零散实现）；
- [ ] 成功率/错误率列 → RateBar 或红绿色文字；
- [ ] token/bytes/金额列 → 千分位或人性化单位；
- [ ] 状态列（告警状态/命令状态/实例在线）→ StatusTag 统一色板；
- [ ] 空态 → `el-empty` + 一句"为什么会空/怎么让它有数据"的引导文案。

### 任务 6：系统状态页图表化（用户点名：列表看不懂）

现状：`RuntimeView.vue` 把 server-metrics 最近 100 条原始采样直接铺表格,无法回答"机器现在怎么样/过去一小时什么走势"。改造为：

1. **当前值卡片区**（每实例一组）：CPU%、内存%、磁盘%、负载、网络收发——取最新一条采样,复用 MetricMini,阈值配色（≥90% 红、≥70% 橙、否则绿）,带采集时间戳（超过 2 分钟未更新显示"数据陈旧"标记）；
2. **趋势图**（复用 TrendChart + HoursSelect,1h/6h/24h）：图① CPU% + 内存%（percent 轴）；图② 磁盘使用率；图③ 网络收/发速率。数据走既有 `/api/dashboard/server-metrics`——`ServerMetricQuery` 已支持 StartTime/EndTime,handler 若未透出 start/end 查询参数则补上（additive）；多实例时按实例分组或用实例选择器过滤；
3. 原始采样表**收进折叠面板**（排障还要用,默认收起）；Agent/健康检查/容器三个表保留,按任务 5 清单打磨。

### 任务 7：样式统一（`b7.css`）

表格行高与 hover 底色统一；卡片圆角阴影统一；侧栏图标与文字间距；≤900px 下侧栏不破版（已有 media query 风格延续）。

## 验证要求

1. `pnpm build`、`pnpm test`（typecheck）、`go test ./...` 全绿；
2. Server 名称解析单测（见任务 1 第 4 点）；
3. 手工走查：`deploy/seed-demo-data.sh` 起演示环境，逐页截图核对本文清单，把"每页改了什么"逐条记入交付说明；
4. 本批 UI 质量的最终判定是**用户浏览器走查**（交付后进行），交付说明里写清走查入口和建议路径。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 全站再无裸 ID 直接展示（instance/channel/user 三类，找不到名称回退旧文案）
- [ ] 名称解析集中在 nameResolver 且有 TTL 缓存与回退测试
- [ ] Dashboard API 老字段零改动，新增字段列表写进交付说明
- [ ] 13 个菜单项全部有图标；新增依赖仅 @element-plus/icons-vue
- [ ] 任务 5 清单逐页核对并在交付说明中列出每页改动
- [ ] 系统状态页：卡片阈值配色 + 三张趋势图有数 + 原始表默认折叠
- [ ] 格式化函数集中 utils/format.ts，无重复散落实现
- [ ] 一个 commit：`feat(web): ui polish with icons, name resolution and metric visuals (v2.3-B1)`

## 明确不做

暗色主题、国际化、侧栏折叠、移动端 PWA（归 v3.0）、新图表类型、路由/页面结构调整、Server 业务逻辑改动、渠道/用户名的新采集链路。
