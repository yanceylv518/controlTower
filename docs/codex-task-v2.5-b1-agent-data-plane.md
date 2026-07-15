# Codex 任务：v2.5-B1——Agent 数据面升级（精确分位数 / 缓存命中率 / TTFT / 快照补采）

攒了一车的 Agent 数据面需求一次交付：①精确 P50/P95/P99（终结直方图近似）;②大输入缓存命中率（用户需求：只聚合 prompt>512 的请求）;③流式 TTFT（用户需求：只聚合流式,数据源已生产验证）;④渠道快照补采 group/priority（供应商分组与调权 severe 规则的前置）。全链路：Agent 聚合 → 契约 → 010 迁移 → Server 存储/聚合 → API → 维度页展示。

**文末自查清单粘贴进 commit message。**

## 已验证前提（2026-07-16,生产 logs.other 实测）

```json
{"admin_info":{"use_channel":["62"]},"cache_tokens":0,"frt":5310,"stream_status":{...},...}
```

- `frt` 存在,**毫秒**整数,流式请求的首响应时间;
- `cache_tokens` 已在解析（缓存失效告警在用）;
- 解析 `other` 的入口在 `agent/internal/logcollector/parser.go`（现有 parseCacheTokens 模式照抄,单字段独立容错,缺失/非法不影响整行）。

## 硬性纪律

- 只读边界不变：不改 new-api,不读请求体/密钥;快照补采只是 SELECT 多两列;
- 契约与 API **只增不改**;旧 Agent + 新 Server（新列 NULL）、新 Agent + 旧 Server（未知字段被忽略）都必须无害,交付说明写明**先升 Server 后升 Agent** 的部署顺序;
- 迁移编号用 **010**（009 已被在途的 v2.4-B1 预留）,钉 ENGINE/CHARSET/COLLATE;
- 数值防御：frt ≤0 或 >3,600,000ms 视为缺失;原始值数组每桶上限 10000（超出丢弃计数,与 nginxtiming 同语义）。

## 工作项

### 任务 1：Agent 解析与聚合（parser.go + metricaggregator）

1. `Event` 增加 `FirstResponseMs *int64`（other.frt,仅正值有效）;
2. 聚合器每维度每分钟桶新增：
   - **精确分位数**：保留桶内原始 use_time 值（上限 10000）,关桶时算精确 P50/P95/P99;`p95_use_time` 现有字段改写精确值（语义同,精度升）,新增 `p50_use_time`、`p99_use_time`;
   - **大输入缓存命中**：`big_input_count`（consume 且 prompt_tokens > `CT_CACHE_HIT_MIN_PROMPT_TOKENS`,默认 512,与缓存失效告警阈值语义对齐）、`big_input_cache_hits`（其中 cache_tokens>0 的条数）;
   - **流式 TTFT**：`ttft_count`、`ttft_sum_ms`（仅 IsStream 且 frt 有效）,关桶时算 `ttft_p95_ms`（精确,来自原始值）;
3. 直方图照旧维护（5m 回退与历史兼容仍靠它）。

### 任务 2：契约与迁移

- `AggregatedMetricPayload`（agent/reporter 与 server/agentgateway 两侧）新增上述字段,omitempty;
- `010_metric_dataplane.sql`：`metric_1m`/`metric_5m` 增加可空列 `p50_use_time`、`p99_use_time`、`big_input_count`、`big_input_cache_hits`、`ttft_count`、`ttft_sum_ms`、`ttft_p95_ms`;`channel_snapshots` 增加可空 `group_name VARCHAR(128)`、`priority BIGINT`;历史行保持 NULL 不回填。

### 任务 3：渠道快照补采

`channelcollector` SELECT 增加 ``group``（注意 MySQL 保留词要反引号）与 `priority`（COALESCE 兜底）;快照 payload/存储/dashboard 响应透传（additive）;渠道页详情头部展示分组与优先级。**这是调权 severe 规则和供应商分组的前置,交付说明注明"已解锁"。**

### 任务 4：Server 聚合与读侧

- ingest 存储写入新列;
- **1m→5m 汇总**：可加和字段（big_input_*、ttft_count/sum）正常累加;**精确分位数列在 5m 不可合成,保持 NULL**——读侧规则：优先取存储的精确列,NULL 回退直方图插值（v2.4-B2 的实现）。也即 ≥6h 视图（5m 窗口）的分位数仍是插值近似,1h 视图（1m 窗口）是精确值,交付说明与页面 tooltip 注明;
- `MetricItem` 新增：`cache_hit_rate`（= hits/count,分母 0 时 null）、`big_input_count`、`ttft_avg_ms`（= sum/count）、`ttft_p95_ms`,latest/history 查询带出。

### 任务 5：维度页展示

- 指标卡新增两张：**缓存命中率（>512 输入）**（值 + 分母样本数,分母 0 显示"无大输入请求"）、**TTFT（流式）**（avg / p95,无流式显示"—"）;
- 趋势图新增两张：缓存命中率（%,percent 轴）、TTFT（avg+p95 两线,毫秒转秒显示）;旧数据 NULL 段曲线断开（connectNulls 保持 false）,不画零;
- 渠道页详情头部：分组标签 + 优先级(与权重并列)。

## 验证要求

1. `go test ./...`、`pnpm build`、`pnpm test` 全绿;010 迁移 sanity 自动覆盖;
2. 聚合单测：精确分位数与已知数组对拍;大输入命中分子分母（含阈值边界 512 不计入、513 计入）;TTFT 仅流式、frt 缺失/非法/超界不计;桶上限丢弃计数;
3. 兼容测试：旧 payload（无新字段）入库正常;NULL 列的 API 输出为 null 且前端不画;
4. 手工冒烟：本地起 server,构造含 frt/cache_tokens 的 logs 数据跑 Agent 一轮,维度页两张新卡两张新图有数,截图记入交付说明。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 部署顺序声明（先 Server 后 Agent）;新旧混布双向无害有测试
- [ ] 512 阈值边界、frt 防御、仅流式聚合有单测
- [ ] 5m 精确分位数列 NULL + 读侧回退插值;tooltip 注明近似
- [ ] 快照 group/priority 采集使用反引号并 COALESCE;渠道页已展示
- [ ] 010 钉 ENGINE/CHARSET/COLLATE;API 只增不改
- [ ] 一个 commit：`feat: agent data plane with exact quantiles, cache hit rate and ttft (v2.5-B1)`

## 明确不做

`admin_info.use_channel` 重试链路解析（多元素=内部重试,极有价值,但独立成批,记档待议）;缓存命中率/TTFT 的告警规则（先看数据再定阈值）;直方图桶边界调整;心跳解耦与可靠性三件套（独立批次）;供应商分组 UI（group 字段就位后归 Web 批次）。
