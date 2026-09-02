# 验收记录：调权中心当前速率与当前权重展示（2026-09-02）

- 范围：`bd49263 fix(tuning): show current channel rates and weights`
- 结论：**通过，验收修正一处后端缺陷（P2，`582919b`）**，前端零缺陷。

## 变更

- RPM / TPM 不再按评估窗口均值折算，改为读取站点最近一个已关闭的 1 分钟桶
  （`metric_1m` 的 `instance_channel` 维度，向前最多找 5 分钟；无桶即为 0）。
  容量守卫 `capacity_limited` 与页面展示同源。缺该查询接口的存储（测试假件）
  回落到窗口均值。
- 前端：状态里 `last_write_at` 晚于渠道快照 `snapshot_at` 时，"当前权重"列
  显示本系统最近写入值，避免 10 分钟快照周期内显示过期权重；
  `last_written_weight` 类型改为可空。

## 缺陷与修正（后端）

**最新关闭的分钟桶在评估时通常尚未填满（P2）**。证据链：

- Agent 每 `CT_LOG_POLL_INTERVAL_SECONDS`（默认 30s）拉一次日志，按分钟聚合
  后上报；Server 端 `upsertMetrics` 对同一桶是**累加**
  （`request_count = request_count + VALUES(...)`），一个分钟桶要经过关闭后的
  下一次拉取才完整。
- 调权引擎每 30 秒评估一次，`end = now.Truncate(minute)` 只排除当前分钟，
  评估落在分钟前半段时上一分钟桶最多缺一半数据。
- 守卫只拦升不拦降：低估一次即放行一次加权，下一轮达上限后以新权重为上界，
  形成单向棘轮，上限失去意义。

修正：`QueryCurrentChannelRates` 增加 `metricBucketSettleDelay = 95s`
（60s 桶长 + 30s 拉取间隔 + 上报余量），只取关闭超过 95 秒的最新桶，展示滞后
约 1.5–2.5 分钟。新增真库回归测试 `TestQueryCurrentChannelRatesSkipsSettlingBucket`
（`CT_MYSQL_TEST_DSN` 门控）：10:05:10 评估取 10:03 桶而非仍在填充的 10:04 桶；
10:05:35 时 10:04 桶沉淀后成为当前值；15 分钟无桶读为空。

## 记档（P3，不阻塞）

- 多实例站点用 `MAX(bucket_time)` 跨实例取同一分钟：某实例 Agent 掉线时该
  实例的流量不计入，速率偏低；沉淀延迟已覆盖正常上报抖动。
- 单分钟瞬时速率比窗口均值抖动大：安静的一分钟即解除限升。守卫本身只拦升，
  这是"当前值"语义的既定取舍，如需平滑可改为最近 N 个沉淀桶的最大值。
- 若生产把拉取间隔调大于 30s，需同步调大沉淀延迟；目前常量未随配置联动。
- 前端 `applyEffectiveCurrentWeights` 直接改写 `bases` 行的 `current_weight`，
  保存时随载荷回传但服务端不读该字段，无副作用。

## 测试与实证

- `go vet ./...`、`go test ./...` 33 包全绿（含真库：新增回归 + 集成测试）。
- `pnpm typecheck`、`pnpm build` 通过。
- 烟测栈换新二进制启动，调权中心 1366 宽度渲染正常，无 `current_rates failed`
  日志。

## 部署要点

- 纯 server + 前端改动，无迁移，未动 agent。
- rc90 不含本批（rc90 打在 4463595），上线需重打 rc91。
