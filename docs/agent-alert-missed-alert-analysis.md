# Agent 渠道告警未触发问题分析

## 1. 问题概述

2026-07-12 17:56-17:57，new-api 的渠道 26 连续产生多条 `type=5` 错误日志。按 Agent 当前规则，渠道或客户最近 10 条请求中错误数达到 3 条时应发送钉钉告警，但实际没有收到告警。

本次分析基于用户提供的 `Untitled.csv`，以及服务器上的 Agent 状态和 systemd 日志。

## 2. 已确认的服务器状态

服务器上的 Agent systemd 日志显示：

```text
2026-07-11T22:10:33+0800 Started Control Tower Agent (new-api monitoring).
2026-07-11T22:10:33+0800 control tower standalone mode: starting from current log id 4450243
```

服务器状态文件显示：

```json
{
  "last_log_id": 4577489,
  "last_success_report_at": "2026-07-12T10:13:00.743295656Z",
  "consecutive_report_failures": 0
}
```

告警配置显示：

```text
CT_ALERT_ERROR_WINDOW=10
CT_ALERT_ERROR_THRESHOLD=3
CT_LOG_POLL_INTERVAL_SECONDS=30
```

因此可以确认：

- Agent 在错误发生前已经启动；
- 初始游标 `4450243` 小于本次错误日志 ID；
- Agent 后续游标已经推进到 `4577489`；
- 轮询间隔、窗口大小和阈值配置符合预期；
- 不能用“Agent 启动后跳过历史日志”解释本次问题。

## 3. 用户提供的数据

CSV 共 96 条记录，ID 范围为 `4577041-4577138`。

渠道 26 的错误记录如下：

| 日志 ID | 时间 | type | user_id | channel_id | model |
|---:|---|---:|---:|---:|---|
| 4577044 | 17:56:03 | 5 | 9 | 26 | deepseek-v4-pro |
| 4577056 | 17:56:12 | 5 | 9 | 26 | deepseek-v4-pro |
| 4577081 | 17:57:18 | 5 | 9 | 26 | deepseek-v4-pro |

这 3 条记录都是渠道 26，且全部为错误。

CSV 中渠道 26 在这个导出范围内没有其他正常请求，因此在处理 `4577081` 时，渠道 26 窗口至少是：

```text
window_count=3
error_count=3
threshold=3
```

按当前规则，渠道 26 此时应该触发告警。

## 4. 中间成功请求的影响

CSV 中确实存在大量成功请求，但它们属于其他渠道，例如 24、25、62、78。

这些请求不会进入 `channel:26` 的窗口。Agent 内部是分别维护：

```text
channel:26
channel:24
channel:78
user:9
```

因此：

- 其他渠道成功请求会影响用户 9 的全局用户窗口；
- 其他渠道成功请求不会影响渠道 26 的渠道窗口；
- 渠道 26 的 3 条错误仍然满足渠道告警条件。

## 5. 全局 last_log_id 的处理逻辑

Agent 只有一个全局日志游标：

```json
{
  "last_log_id": 4577489
}
```

查询逻辑是：

```sql
SELECT ...
FROM logs
WHERE id > ?
  AND type IN (2, 5)
ORDER BY id ASC
LIMIT ?
```

Agent 不会在读取每一行后立即更新游标，而是：

1. 查询一批全局日志；
2. 按 ID 顺序把整批事件送入告警模块；
3. 取本批最大 ID；
4. 保存新的 `last_log_id`。

所以中间的其他渠道成功记录不会导致后面的渠道 26 错误被正常情况下跳过。例如同一批中出现：

```text
4577044  channel=26  error
4577045  channel=24  success
4577056  channel=26  error
```

这一批处理完成后游标可能直接变成 `4577056`，但 `4577044` 已经在本批中进入了渠道 26 窗口。

## 6. 发现的 fallback 关系

数据中存在 new-api 的 fallback：

```text
4577044  type=5  channel=26  request_id=...GcGPvJRu
4577051  type=2  channel=78  request_id=...GcGPvJRu
```

以及：

```text
4577081  type=5  channel=26  request_id=...3uKR7owk
4577137  type=2  channel=78  request_id=...3uKR7owk
```

这表示渠道 26 失败后，new-api 使用渠道 78 重试并成功。

但 fallback 不会抵消当前 Agent 的渠道告警，因为当前实现按 `channel_id` 统计。对 Agent 来说，渠道 26 已经发生了错误，渠道 78 的成功属于另一个渠道窗口。

因此 fallback 可以解释“最终用户请求可能成功”，但不能解释“渠道 26 告警为什么没有触发”。

同时，这暴露出一个规则设计问题：

- 如果目标是监控渠道健康，渠道 26 的失败应该告警；
- 如果目标是监控用户最终请求失败，则应按 `request_id` 关联 fallback，只在整个请求链最终失败时告警。

## 7. 当前问题的严谨结论

从 CSV 数据和当前规则推导：

```text
渠道 26：3 条错误 / 最近 3 条请求
阈值：3 条
预期：应该触发渠道告警
```

因此以下解释均不能成立：

- 错误间隔超过 30 秒；
- 中间有其他渠道的成功请求；
- Agent 首次启动时跳过了这批错误；
- fallback 成功自动抵消了渠道 26 错误。

当前历史数据无法证明告警实际在哪一步失败，原因是旧版本 Agent 没有保存以下审计信息：

- 本轮实际读取的日志 ID 范围；
- 渠道 26 是否收到这 3 条事件；
- 渠道 26 窗口中的事件数量和错误数量；
- 是否生成了 pending alert；
- DingTalk 是否返回成功或错误码。

因此目前能确认的是：

> 数据和告警规则均满足触发条件，但旧版本运行日志不足以还原“读取、窗口计算、生成消息、发送钉钉”中的具体失败环节。

## 8. 已完成的改进

提交 `f1d4510 feat(agent): add alert evaluation audit logs` 已增加每轮统计日志，记录：

```text
after_log_id
last_log_id
events
errors
channel_dimensions
user_dimensions
alerts_triggered
alerts_sent
alerts_failed
```

该版本可以确认 Agent 是否读取到事件，以及是否生成和发送告警。

## 9. 仍需补充的审计信息

仅记录总数还不够定位单个渠道。后续建议增加维度明细，例如：

```text
channel=26
window_ids=4577044,4577056,4577081
window_errors=3
threshold=3
triggered=true
sent=true
```

这样可以直接确认渠道 26 是否进入阈值判断，而不是只看到全局 `errors=3`。

## 10. 确认的根因（2026-07-12 定案）

后续调查在钉钉群历史中找到确凿证据：**当天早上 03:02 已有一条渠道 26 的告警**。据此还原完整链条：

1. 03:02 渠道 26 首次凑满窗口阈值，告警正常触发并送达（episode 开始，`alerted=true`）。
2. 渠道 26 失败率极高、成功请求极少（几乎每次都靠 fallback 到渠道 78 救回），它的窗口始终被错误填满，**永远达不到"错误 < 3"的重臂条件**。
3. 17:56-17:57 的三条新错误进入窗口时条件满足，但 `alerted` 仍为 true → 按"同一故障期间只发送一次"的设计被静默。

**这不是代码 bug，是设计缺陷**：episode 去重对"永远好不了的渠道"意味着"只在第一次坏时说一声，之后无限沉默"。第 7 节列出的四个排除项之外，漏掉了第五个假设——"这次不是第一次触发"。18:08 的关键词编码修复（f77d495）与本次丢失无关（同期其他告警正常送达可证），但作为加固保留。

**修复**（同日实现）：

- **窗口时间衰减** `CT_ALERT_WINDOW_MAX_AGE_MINUTES`（默认 60）：超龄的旧事件滑出窗口，稀疏渠道的陈年错误不再永久占位，错误清空后 episode 自然重臂，下一波故障产生新告警。
- **持续告警定期重提醒** `CT_ALERT_REMIND_MINUTES`（默认 240）：episode 持续 firing 超过该时长即再发提醒，附故障起始时间与累计错误数（"渠道 26 自 03:02 起持续异常，累计 N 条错误"），长期故障不再沉默。
- **按维度触发审计日志**（第 9 节要求）：每次触发/提醒输出 `dimension=channel:26 kind=alert window=3 errors=3`，下次调查可直接定位。

按本次事故重放：03:02 首告警 → 07:02 起每 4 小时收到"持续异常"提醒 → 17:56 的错误爆发时值班者早已在处理中；若渠道 26 曾有一小时无错误，窗口衰减清空 → 17:56 触发的是全新告警。两条路径都不再丢失。

## 11. 后续规则决策

需要明确 Agent 的告警目标：

1. **渠道故障告警**：保留当前按渠道统计的逻辑，fallback 成功也仍然提示渠道 26 失败；
2. **最终请求失败告警**：按 `request_id` 关联同一次请求的 fallback 结果，只对最终失败的请求计数；
3. **两种告警同时保留**：分别展示“渠道尝试失败”和“用户最终请求失败”。

在规则没有明确前，不应直接用 fallback 成功覆盖渠道 26 的失败记录。
