# Codex 任务：v2.7-B3——稀疏指标曲线渲染修正（TTFT/缓存命中率断线问题）

用户反馈 TTFT P95 曲线大量断裂。机制：①TTFT 仅流式分钟有值,无流量分钟 NULL 属正确语义;②TrendChart `connectNulls:false + showSymbol:false` 使**孤立数据点完全不可见**;③5m 桶的 ttft_p95 为 NULL,≥6h 视图 P95 线整条消失。修渲染与 5m 合成,不改数据语义。

**清单进 commit;禁止 force push;Linux 跑全量测试。**

## 工作项

### 任务 1：TrendChart 稀疏序列模式（前端）

`TrendSeries` 增加 `sparse?: boolean`：为 true 时该序列 `connectNulls:true`（跨空隙连线,虚线段区分不了就全线连,tooltip 空分钟仍显示"—"）且 `showSymbol:true`（小符号,孤立点可见）。TTFT 图（avg+p95）与缓存命中率图标记 sparse;请求量/错误率等密集序列保持现状。图标题或副标注一句"无流式流量的时段无数据"。

### 任务 2（升级,根因）：MergeMetric 的 1m 桶内合并不再抹掉 TTFT P95

**新发现的真正主因**：Agent 每 30 秒上报,一个 1m 桶的事件通常分两份部分聚合到达,Server `MergeMetric` 合并时 `TTFTP95MS = nil`——繁忙维度几乎每分钟都被合并,P95 几乎每分钟被抹掉（这才是 1h 视图大量断点的主因,比渲染问题影响更大）。修法：合并时 `TTFTP95MS = max(两侧非 NULL 值)`（保守上界,与任务 3 的 5m 合成同一数学）;单测:两份部分聚合合并后 p95 取大者、一侧 NULL 取另一侧、双 NULL 保持 NULL。
**同时在交付说明里如实记录连锁影响**：use_time 的"1m 精确分位数"在被合并过的分钟实际是直方图插值(现状行为,不改,但承诺要写准);彻底根治（Agent 跨 pass 持有整分钟桶、关桶才上报,与 nginxtiming 开放桶模式同构）记为 Agent 升级候选项,本批不做。

### 任务 3：5m 桶的 TTFT P95 合成（Server rollup）

1m→5m 汇总时 `ttft_p95_ms = MAX(五个 1m 的 ttft_p95_ms)`（忽略 NULL;全 NULL 则 NULL）。数学依据写进注释：**合并集的 P95 ≤ 各子集 P95 的最大值**（每个子集超过该值的占比 ≤5%,合并后仍 ≤5%）,故 MAX 是保守上界,常见场景下贴近真值——比整条线消失诚实得多,交付说明注明"5m 粒度 P95 为保守上界近似"。同规则适用 use_time 的 p50/p99? **不适用**——它们有直方图插值回退,保持现状。

### 任务 4：缓存命中率 5m 视图核对

big_input_count/hits 是可加字段,5m 合成应已正确;核对 rollup 与读侧确实在 5m 输出命中率,缺则补,有则在交付说明记"已核对"。

## 验证要求

1. 全量测试绿;rollup 的 MAX 合成单测（含部分 NULL/全 NULL）;
2. 手工：稀疏数据下 1h 视图孤立点可见、跨空隙连线;6h/24h 视图 P95 线存在;截图前后对比。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] sparse 序列孤立点可见;密集序列渲染未变
- [ ] 1m 合并不再抹 p95（MAX 语义,三分支测试）;5m 合成同规则
- [ ] use_time 精确分位数被合并降级为插值的现状已在交付说明写明
- [ ] 缓存命中率 5m 视图核对结论在交付说明
- [ ] 一个 commit：`fix(web,server): render sparse ttft series and roll up 5m p95 (v2.7-B3)`

## 明确不做

TTFT 原始值直方图化（数据量不值得）;把 NULL 画成 0;改 TTFT 采集语义。
