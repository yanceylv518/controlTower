# Codex 任务：v2.3-B3——维度页加载性能优化

用户反馈三个维度监控页打开很慢。定位结论：`recentMetricsSQL(latestOnly=true)` 的相关子查询在 metric_1m/5m 全表逐行跑 MAX，且现有索引 `(bucket_time, dimension_type, dimension_key)` 对子查询条件（instance+dimension → MAX(bucket_time)）完全不可用；随表增长线性恶化。**目标：维度页 metrics 接口在百万行级 metric_1m 上 <100ms。**

**文末自查清单粘贴进 commit message。**

## 硬性纪律

- Dashboard API 契约零变化（响应字段与语义不变,新增可选查询参数除外）;前端行为除"更快出首屏"外无感;
- 迁移幂等,索引钉名;零新依赖,零 Agent 改动;
- 每项优化在交付说明里附 **EXPLAIN 前后对比**（用造数脚本灌 ≥50 万行验证）。

## 工作项

### 任务 1：迁移 `008_metric_indexes.sql`

```sql
CREATE INDEX idx_metric_1m_dim_bucket ON metric_1m (dimension_type, instance_id, dimension_key, bucket_time);
CREATE INDEX idx_metric_5m_dim_bucket ON metric_5m (dimension_type, instance_id, dimension_key, bucket_time);
```

（1061 重复索引可容忍的既有迁移语义不变;不删旧索引——retention 清理还靠 bucket_time 打头那条。）

### 任务 2：重写 latest 查询

`recentMetricsSQL(latestOnly)` 改为分组 + 自联结,并做两个下推：

```sql
SELECT m.* FROM metric_1m m
JOIN (
  SELECT instance_id, dimension_type, dimension_key, MAX(bucket_time) AS mb
  FROM metric_1m
  WHERE bucket_time >= ?            -- 活跃视野下推：NOW()-24h（常量,写死即可）
    AND (? = '' OR dimension_type = ?)   -- 维度类型下推
  GROUP BY instance_id, dimension_type, dimension_key
) t ON m.instance_id=t.instance_id AND m.dimension_type=t.dimension_type
   AND m.dimension_key=t.dimension_key AND m.bucket_time=t.mb
LIMIT ?
```

- 新索引下子查询是松散索引扫描,行数只与"活跃维度数"相关,与表总量无关;
- **语义变化声明**：超过 24 小时无流量的维度不再出现在"最新"列表——这是合理语义（维度页展示活跃对象）,写进交付说明;
- 接口层：`Latest1m/5mMetrics` 增加 dimensionType 参数,`HandleMetrics` 把 `dimension_type` 下推,Go 内过滤保留作兜底;memory store 同步语义;
- `metricHistorySQL` 确认走新索引（EXPLAIN 附上）,不用改 SQL。

### 任务 3：名称解析批量化（B1 遗留的放大器）

`nameResolver` 增加按类型整批预载：缓存未命中时**一次查询**载入全部渠道名（latest 快照 GROUP BY channel）与近 24h 出现过的 user_id→username（log_events 一条 GROUP BY 查询）,整表缓存 60s TTL,单键查询只在整批里找。杜绝"100 个渠道=100 次单查"。加测试：一次 miss 后同类型 100 个键零额外查询（fake store 计数）。

### 任务 4：维度页首屏拆分（前端）

`DimensionView.vue`：`state` 的加载器里去掉 `await loadHistory()`——列表(metrics+快照)到达即渲染左列与指标卡,趋势图区域用独立 loading 态异步补上。切换选中项/时间范围行为不变。

### 任务 5：造数与验证

- `deploy/perf-seed.sql`（或脚本）：灌 2 实例 × 120 维度 × 7 天分钟桶（≈120 万行）;
- 交付说明记录：旧查询耗时 vs 新查询耗时（同一数据集）、两条 EXPLAIN、维度页首屏水掉的请求瀑布说明;
- `go test ./...`、`pnpm build`、`pnpm test` 全绿;迁移 sanity 自动覆盖 008。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 008 索引迁移幂等;EXPLAIN 显示子查询走 idx_metric_*_dim_bucket 松散扫描
- [ ] 120 万行数据集上 latest 查询实测耗时写入交付说明（目标 <100ms）
- [ ] dimension_type 与 24h 活跃视野已下推;API 响应字段零变化;24h 语义变化已声明
- [ ] nameResolver 批量预载有零额外查询测试
- [ ] 维度页首屏不再等待历史曲线;图表区独立 loading
- [ ] 一个 commit：`perf(server,web): dimension page latest-metrics query and first paint (v2.3-B3)`

## 明确不做

metric 表分区/归档（retention 已控量,先索引+改写,不够再说）;缓存层（Redis 等,违背零依赖）;聚合结果物化表（等真实数据证明仍慢再上）;Agent 改动。
