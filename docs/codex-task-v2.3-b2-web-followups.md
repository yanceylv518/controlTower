# Codex 任务：v2.3-B2——Web 打磨收尾 + 渠道清晰化

四件事：v2.3-B1 验收发现的两个前端未接线收尾、系统状态页图表化（B1 文件里追加的任务 6 移到本批,以本文为准）、渠道列表多渠道场景重组。**纯 Server 读侧 + 前端改动,零 Agent 改动,零新依赖。**

**文末自查清单粘贴进 commit message。**

## 背景速读

- v2.3-B1 已交付：nameResolver（60s 缓存）、metrics/alerts/commands/audits/snapshots 响应已带 `instance_name`/`display_key` 新字段;前端已接线的只有维度页/总览/审计三处。
- `ServerMetricQuery` 已支持 StartTime/EndTime;`server_metrics_10s` 每 30 秒一条采样。
- 渠道页数据：`dashboard.metrics(dimension_type=instance_channel)` 有请求量/错误率/display_key;`channelSnapshots(latest_only)` 有 status（enabled/disabled/auto_disabled）。
- 组件复用：TrendChart、HoursSelect、MetricMini、RateBar、StatusTag、`utils/format.ts`。样式进 `b8.css`。

## 工作项

### 任务 1：告警中心可读化（用户点名：告警太笼统,不知道是哪个渠道/模型、啥情况）

分三层,前两层动 Server 告警生成（`alert_handler.go` 的 appendMetricAlerts 等）,第三层动前端：

1. **维度名进标题与摘要**：内置指标告警（high_p95_latency / high_error_rate / 资源类）的 Title/Summary 必须带 display_key（走 nameResolver）,如 `渠道 openai-主力(ID 5) P95 耗时偏高`;AlertItem 若未单独暴露 dimension_key/display_key 字段则补（additive）;
2. **摘要带行动上下文**：P95 告警摘要格式改为 `最近 1 分钟 P95 {值}, 共 {request_count} 条请求`;**P95 等于直方图最高桶上界（120s）时,禁止显示 "120.00s" 这种伪精确值,改为 "≥60s（超出直方图量程）"**——当前文案会误导排障;错误率告警同理带上 `{error_count}/{request_count}`（已有,核对格式统一）;
3. **一键到现场**（前端）：`AlertsView.vue` 维度列改 `display_key`、实例列改 `instance_name`（tooltip 露原 id）、时间走 formatTime;告警行加"查看维度"跳转——按 dimension_type 跳对应维度页并选中该维度（维度页支持 `?key=` 查询参数定位选中项,没有则补,顺带总览页告警列表同款跳转）。时间线弹层同样处理。

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

### 任务 5：自动刷新静默化（用户点名：每 30 秒整页卡一下,体验很差）

根因：`useAsyncData.reload()` 不区分首次加载与后台刷新,每次都置 `loading=true`,AsyncPanel 把内容区整体换成转圈再重建;`useAutoRefresh` 的定时器与 visibilitychange 都走这条路。改造（一处改全站受益）：

1. `useAsyncData` 增加 `refresh()`（静默）：仅当 `data` 尚无值时才置 loading;**静默刷新失败时保留旧数据不动**,不清空页面、不弹错误态（可在内部记录最近错误供调试）;`reload()` 保持现状供首次加载与手动重试;
2. `useAutoRefresh` 及全部调用点（维度页/总览/系统状态/告警等）：定时器与 visibilitychange 一律走 `refresh()`;组件挂载首次仍走 `reload()`;
3. 维度页历史曲线的周期刷新同样静默（图表数据原地更新,不得出现图表卸载重建的闪烁）;
4. 验收标准：打开渠道页静置 2 分钟,内容区不得出现任何 loading 闪烁或整页重建;断网 30 秒再恢复,页面数据始终可见。

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
- [ ] 自动刷新静默:静置 2 分钟无 loading 闪烁;静默失败保留旧数据
- [ ] 一个 commit:`feat(web): alerts naming, runtime charts and channel triage view (v2.3-B2)`

## 明确不做

按供应商/用途分组（依赖渠道 group 字段,需 Agent 快照补采,与 priority 字段同批,归下次 Agent 升级）;暗色主题;移动端 PWA;虚拟滚动（渠道数到千级再说）。
