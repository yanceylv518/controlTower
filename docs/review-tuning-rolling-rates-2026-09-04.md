# 验收记录：调权中心 60 秒滚动负载（2026-09-04）

- 范围：`1e5b5d0 feat(tuning): report rolling channel rates without evaluation delay`
  及合并提交 `f0b163b`（合并时把 09-02 的 95 秒沉淀桶实现替换为滚动秒桶）。
- 结论：**通过，零阻塞缺陷**。验收补一条真库回归测试（秒桶分流 / 重试去重 /
  过期清理），设计文档 `docs/tuning-rolling-rates.md` 与实现一致。

## 变更

- Agent：每次采集在原有分钟指标之外，按渠道、按秒汇总 consume 日志的请求数与
  prompt+completion Token，以 `channel_rate_second` 维度混入同一 metric batch。
  渠道 0 是覆盖标记，即使本次无新请求也上报；追赶积压（backlog>0）时不带标记。
  不新增 new-api 查询，采集周期不变。
- Server：`ApplyMetricBatch` 把该维度分流进新表 `channel_rate_seconds`
  （065 迁移），不进 metric_1m / metric_5m；同一事务里顺带清理 10 分钟前的秒桶。
  `QueryCurrentChannelRates` 改读 `(now-60s, now]` 滚动窗；站点内任一启用实例
  90 秒内没有覆盖标记即返回不可用。
- 引擎：速率不可用时，配了上限的渠道 `capacity_limited=true`（只拦升），
  不再回落 15 分钟均值，也不把缺数据当零负载。
- 接口：`continuous-states?rates_only=1` 直接查库返回 `{items,as_of,window_seconds}`，
  不依赖评估落库；无覆盖返回 503 `current_rates_unavailable`。
- 前端：容量面板每 5 秒独立拉取实时负载（页面隐藏时暂停），"已限升"按实时值
  判定；不可用时显示提示条与 "—"。

## 实证

- `go vet` 与 `go test ./...` 33 包全绿，含真库（`CT_MYSQL_TEST_DSN`）：
  codex 的 `TestQueryCurrentChannelRatesRollingWindow` 与新增的
  `TestApplyMetricBatchRoutesChannelRateSeconds`（同批 id 重放不重复累加、
  不同批 id 累加、非法渠道键丢弃、分钟表零污染、11 分钟前的行被清理）。
- `pnpm typecheck`、`pnpm build` 通过。
- 烟测栈换新二进制启动，065 自动套用。无秒桶时 `rates_only=1` 返回 503；
  灌入标记 + 三个渠道秒桶后返回 200，60 秒窗内求和正确（70 秒前的行被排除）。
- 页面 1366 宽截图两档：有数据时 RPM 130 ≥ 上限 120 标红并显示"已限升"；
  删除覆盖标记后显示黄色提示条，四列速率为 "—"。

## 记档（P3，不阻塞）

- **不可用时页面不显示"已限升"**：引擎已对配上限的渠道拦升，但页面只有通用
  提示条，渠道行仍显示"0 表示不限制"。自动模式下操作者看不出该渠道为何不升权。
  建议不可用时对 max_rpm/max_tpm>0 的行显示"暂停上调"。
- 覆盖判定要求站点内**所有**启用实例 90 秒内有标记：一个长期离线但未禁用的
  实例会让整站配上限的渠道一直拦升。这是"缺数据不当零负载"的既定取舍，
  运维上离线实例应及时禁用。
- 覆盖窗口 90 秒是常量：若 `CT_LOG_POLL_INTERVAL_SECONDS` 调到 60 以上，
  标记会周期性过期，速率反复不可用。目前未与配置联动。
- 每次采集（含空闲）都产生一条 `metric_batches` 记录（此前空闲不产生），
  该表本就无清理；有持续流量的生产站点增长速率不变，空闲站点每天多约 2880 行。
- 引擎每 30 秒对旧 Agent 站点打一条 `current_rates failed` 日志，升级前会持续。
- 时钟：秒桶时间取 new-api 日志 `created_at`，覆盖标记取 Agent 时钟，查询窗取
  Server 时钟；三者偏差超过数十秒会让窗口错位或覆盖误判。

## 部署要点

- **本批动了 Agent**：Agent 新增秒级上报，需升级 Agent 才有实时负载与容量守卫。
- **部署顺序必须先 Server 后 Agent**：旧 Server 不识别 `channel_rate_second`，
  会把每秒一行的数据当普通指标写进 metric_1m / metric_5m（只是垃圾行，读侧
  按维度过滤不受影响，但会白占空间）。
- 065 迁移建新表 `channel_rate_seconds`，首启自动套用。
- Server 先升、Agent 未升期间：配了 RPM/TPM 上限的渠道暂停上调（其它渠道
  不受影响），页面提示"实时负载暂不可用"。
- rc91 不含本批（rc91 打在 40e87ad），上线需重打 rc92（server + 前端 + agent，
  065 迁移）。

## 追加：3c65c40 覆盖判定只要求日志采集实例（2026-09-04 复验通过，零缺陷）

- 只采服务器指标不采日志的 Agent（`CT_LOG_COLLECT_ENABLED` 关闭）不拥有渠道
  流量，却会因为永远没有覆盖标记把整站卡在"不可用"。修正：覆盖判定与速率
  求和共用同一来源范围——启用实例且有日志采集证据（agents 的
  source_latest_log_id / last_log_id > 0，或 log_offsets.last_log_id > 0）。
  证据取持久游标而非近期上报，所以**离线采集器仍然是必需来源**（上文 P3
  "离线未禁用实例拖住整站"是有意保留的取舍）。
- 复验：`go vet`、`go test ./...` 33 包全绿，含真库集成
  `TestRollingRatesIgnoreMetricsOnlyButRequireEveryCollector`（指标型实例不
  阻塞、第二个采集器缺覆盖必阻塞、秒桶过期后采集器仍必需、禁用即排除）。
  烟测栈：demo_1 无日志证据时带标记也 503；补 log_offsets 后 200 且求和正确。
- 部署提醒：新站点在采到第一条日志前不算来源，整站"不可用"，配上限渠道
  暂停上调；有流量后自愈。
- rc92 打在 cb97823 不含本笔，上线需重打 rc93（纯 server，无迁移，未动 agent）。

## 追加：3b38aca 覆盖标记改用采集前快照（2026-09-04 复验通过，零缺陷）

- 原实现在采集**之后**查 `MAX(id)` 算积压：繁忙站点采集与查询之间总有新日志
  落库，积压恒大于 0，覆盖标记永远发不出去，整站速率永久"不可用"。这是
  1e5b5d0 的实装缺陷，生产一旦有持续流量必现。修正：采集前先冻结上界，
  积压 = 上界 − 本次采到的最后 id；仍是每轮一次 `MAX(id)`。快照失败不阻断本地
  采集但不发标记；`CT_LOG_COLLECT_ENABLED=false` 的指标型 Agent 不发标记
  （与 3c65c40 服务端来源规则对齐）。`SnapshotKnown` 仅为本地判据，不进
  上报契约。
- 复验：`go vet`、agent 全部包测试通过（含 codex 新增的顺序/积压/失败三态表
  驱动测试）。**首次 Agent→Server 端到端实证**：在烟测库建最小 `logs` 表灌
  4 条消费 + 1 条错误日志，真跑 agent 一次——服务端秒桶按渠道按秒落 4 行、
  错误日志排除、标记落在上报时刻、agents/log_offsets 游标更新使 demo_1 成为
  来源；`rates_only=1` 返回按 60 秒窗求和且正确淘汰窗外行。再灌 5 条日志以
  `CT_LOG_BATCH_SIZE=2` 跑一轮：采到 2 条、backlog_estimate=3、**不发标记**；
  恢复批量再跑一轮：追平、backlog=0、标记出现、渠道 2 的 5 条计入。
- 部署：**本笔动了 Agent**，Agent 需升级到含本笔的构建，否则繁忙站点的实时
  负载与容量守卫不会生效。rc93 打在 f2675da 不含本笔，上线需重打 rc94
  （server 未动，agent 改动）。

## 追加：5b3ca3d 滚动窗口对齐覆盖水位（2026-09-04 复验通过，零缺陷）

- 原窗口以服务端当前时刻为终点取最近 60 秒：Agent 30 秒一报，两次上报之间
  窗口尾部尚未上报、头部持续过期，延迟被当成负载下降，最多低估约一半——
  与 09-02 沉淀桶修正针对的是同一类"未报完就当零"问题，只拦升的容量守卫
  会因此单向放行。修正：取所有必需来源最新覆盖标记的最小值 T，窗口为
  `[T−60s, T)`（标记所在秒可能只采了一半，不计入）；无新标记时窗口不动；
  任一必需来源超 90 秒无标记仍不可用。接口新增 `window_start` 与
  `delay_seconds`，页面显示统计区间与数据延迟；引擎与页面同口径。
  设计说明见 `docs/fix-rate-window-watermark-2026-09-04.md`。
- 复验：`go vet`、`go test ./...` 33 包全绿（真库：固定延迟完整计数、轮询间
  稳定、新标记推进、左闭右开边界、多来源取最慢、缺失/过期保护）；
  `pnpm typecheck`/`build` 通过。**Agent→Server 端到端**：灌 11 条每 7 秒一条
  的日志真跑 agent，标记 T=11:51:08，接口返回窗口 [11:50:08, 11:51:08) 内
  8 条（窗前 2 条与标记秒 1 条正确排除），`delay_seconds=1`；12 秒后无新上报
  再查，区间与数值不变、延迟变 13。页面 1366 截图：区间与延迟一行渲染正常。
- P3：常态下数据延迟在 0–35 秒间锯齿波动，这是"已覆盖窗口"语义的必然，
  页面已明示。多实例站点由最慢的采集器决定窗口终点。
- 部署：只改 server 与前端，无迁移；Agent 保持 rc94。rc94 打在 c3b9000
  不含本笔，上线需重打 rc95（server + 前端）。
