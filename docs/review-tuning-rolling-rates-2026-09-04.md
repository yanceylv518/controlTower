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
