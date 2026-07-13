# Codex 任务：v1.1 批次 B1——慢返回窗口规则 + episode 事件持久化

上下文见 `codex-batches-plan.md` B1 节与 `design-v1.1-early-warning.md`（信号 F、第 8 节）。本任务只改 Agent 告警模块，**生产在跑，向后兼容是硬要求**：不新增配置时，现有错误窗口告警的行为必须与当前完全一致（新增慢返回规则默认启用是预期的功能新增，但错误规则的触发/提醒/衰减语义一字不变）。

## 背景速读

- 告警核心在 `agent/internal/erroralert/erroralert.go`：`Notifier` 持有 `states map[string]*dimensionState`；`outcome{isError bool, at time.Time}` 滑动窗口（按条数 `window` 截断 + 按 `windowMaxAge` 时间衰减）；`dimensionState` 持有 `alerted/episodeStartAt/episodeErrors/lastRemindAt`（episode 去重 + 定期提醒）；`evaluateLocked` 产出 `pendingMessage{key, content, kind, prevRemindAt}`，发送失败按 kind 回滚状态重试。
- 事件来源 `logcollector.Event`：含 `UseTime float64`（秒）、`IsStream bool`、`CreatedAt`。
- 配置模式见 `agent/internal/config/config.go`（env 键注册 + intOrDefault + 校验）；接线在 `agent/cmd/control-tower-agent/main.go` 的 `erroralert.New(...).WithXxx(...)` 链。
- 测试风格见 `erroralert_test.go`（httptest 假钉钉 + capture 计数 + 注入 `n.now`）。

## 硬性纪律

1. UTF-8 无 BOM、LF；**Go 源码中的中文消息字面量沿用现有写法**（消息前缀已是 `告警` 转义，新标题字符串与现有"渠道错误激增"同风格写中文即可，但不得改动既有字符串的编码方式）。
2. 零新依赖；只改 `agent/internal/erroralert/**`、`agent/internal/config/**`、`agent/cmd/control-tower-agent/main.go`、`agent/README.md`、`deploy/*.example`、`docs/development-progress.md`。
3. **事件日志必须 fail-safe**：写文件失败只用 `logf` 记一次警告，之后静默跳过，绝不影响告警评估与发送。
4. 每个行为配套测试；不删改现有测试的断言语义。

## 工作项

### 任务 1：episode 状态重构（为双规则做准备，行为不变）

把 `dimensionState` 里的 episode 字段抽成独立结构：

```go
type ruleState struct {
    alerted        bool
    episodeStartAt time.Time
    episodeTotal   int       // episode 期间累计命中数（原 episodeErrors 语义）
    lastRemindAt   time.Time
}
```

`dimensionState` 改为持有 `errorRule ruleState`（本任务再加 `slowRule ruleState`）。`outcome` 增加 `isSlow bool` 字段。此步完成后现有全部测试必须原样通过（纯重构）。

### 任务 2：慢返回规则

**判定**：`observeLocked` 中计算 `isSlow`——非流式请求 `event.UseTime >= slowSeconds`；流式请求（`event.IsStream`）用 `slowStreamSeconds`，其值为 0 时流式永不计慢。

**窗口共享**：窗口切片保留 `max(window, slowWindow)` 条；计数时错误取**最近 `window` 条**内的 `isError`，慢取**最近 `slowWindow` 条**内的 `isSlow`（各自从切片尾部数）。时间衰减对两者统一生效（现有 `windowMaxAge` 剪枝不变）。

**episode 语义**（与错误规则完全同构，作用在 `slowRule` 上）：
- 慢计数 ≥ `slowThreshold` 且未 alerted → 触发，消息标题"渠道慢返回激增"/"客户慢返回激增"，正文：`%s 最近 %d 条请求中 %d 条耗时超过 %d 秒`（不带错误摘要），末尾时间行与现有一致；前缀沿用 `[告警] 【Control Tower 告警】`。
- 提醒复用 `remindInterval`，标题按现有 `TrimSuffix("激增")+"持续"` 规则自然生成"渠道慢返回持续"，正文含 episode 起始时间、`episodeTotal` 累计慢条数、当前窗口计数。
- 低于阈值 → 重臂（rearm）；发送失败按 kind 回滚（`pendingMessage` 增加 `rule` 字段区分 error/slow，回滚时找对 ruleState）。
- 慢规则整体可关：`slowEnabled=false` 时不判定、不触发，错误规则不受影响。

**配置**（`config.go` 新增，含 env 键注册与校验）：

```
CT_ALERT_SLOW_ENABLED=true            # bool
CT_ALERT_SLOW_SECONDS=120             # >0
CT_ALERT_SLOW_WINDOW=10               # 1..1000
CT_ALERT_SLOW_THRESHOLD=3             # 1..CT_ALERT_SLOW_WINDOW
CT_ALERT_SLOW_STREAM_SECONDS=300      # >=0，0=流式不计慢
```

**接线**：`Notifier` 新增链式方法 `WithSlowRule(seconds float64, window int, threshold int, streamSeconds float64)`（不调用即禁用，保证兼容）；`main.go` 在 `cfg.AlertSlowEnabled` 时挂上。

### 任务 3：episode 事件持久化（alert-events.jsonl）

新文件 `agent/internal/erroralert/eventlog.go`：

```go
type EventRecord struct {
    Time         time.Time `json:"time"`
    Dimension    string    `json:"dimension"`     // "channel:26" / "user:9"
    Label        string    `json:"label"`         // "渠道 26(xxx)"
    Rule         string    `json:"rule"`          // "error" | "slow"
    Kind         string    `json:"kind"`          // "alert" | "remind" | "rearm"
    WindowCount  int       `json:"window_count"`  // 窗口内命中数
    Threshold    int       `json:"threshold"`
    EpisodeStart time.Time `json:"episode_start,omitempty"`
    EpisodeTotal int       `json:"episode_total,omitempty"`
}
```

- `Notifier` 新增 `WithEventLog(path string)`；`evaluateLocked` 收集本轮全部状态变迁（alert/remind/**rearm**——rearm 指从 alerted 变回未 alerted 的那一刻，错误与慢规则各自记录），在锁外由 `Process` 追加写文件（一行一条 JSON）。
- 轮转：写入前检查文件大小，>5MB 时 rename 为 `<path>.1`（覆盖旧的 .1）后新开文件。
- fail-safe：任何写入/轮转错误只 `logf` 一次（进程生命周期内），后续静默跳过。
- `main.go` 接线：`WithEventLog(filepath.Join(cfg.DataDir, "alert-events.jsonl"))`，无条件启用（数据目录必然可写，preflight 已保证）。

### 任务 4：文档与样例

- `agent/README.md`：Agent-Side DingTalk Error Alert 一节补慢返回规则说明（含流式阈值语义）与 alert-events.jsonl 说明（字段、轮转、用途）。
- `deploy/agent.standalone.config.example`、`deploy/agent.config.example`：追加 5 个新配置项及注释。
- `docs/development-progress.md`：「Agent 采集与上报修复计划」表后的合适位置或 P6 表新增一行记录本批次（状态已完成、验证方式）。

## 测试要求（全部新增，现有测试不动）

1. 纯重构验证：任务 1 完成时点跑全量测试通过（可在提交信息中注明）。
2. 慢返回触发：10 条中 3 条 use_time=150 非流式 → 触发一次"渠道慢返回激增"；再喂慢事件不重复；8 条快成功把慢挤出窗口后再 3 条慢 → 第二次触发。
3. 流式阈值：use_time=150 的流式事件在 streamSeconds=300 时不计慢、在 streamSeconds=100 时计慢、在 streamSeconds=0 时永不计慢。
4. 双规则独立：同一渠道错误 episode 进行中，慢返回达到阈值 → 两条消息都发、互不干扰；各自 rearm 独立。
5. 慢规则提醒：remindInterval=1h，慢 episode 持续 61 分钟 → 收到"渠道慢返回持续"，含累计计数。
6. 发送失败回滚按 rule：慢告警被钉钉 errcode 拒绝 → 下轮重试，错误规则状态不受影响。
7. 事件日志：临时目录验证 alert/remind/rearm 三种记录的字段完整；rule 字段正确区分；>5MB 轮转（可临时调小阈值常量或写大记录模拟）；路径不可写时不 panic、告警照发。
8. 兼容：不调用 WithSlowRule/WithEventLog 时，现有行为逐项不变（靠现有测试保障）。

## 完成标准

1. `go vet ./...`、`go test ./...` 全部通过。
2. 新增配置全部有默认值，`config_test.go` 补默认值与校验用例（window/threshold 关系、streamSeconds 负数拒绝）。
3. 消息文案人工核对：慢返回告警/提醒的钉钉消息含"告警"关键词（现有前缀保证）、无乱码。
4. 提交信息：`feat(agent): slow-return window rule and episode event log (v1.1 B1)`，一个 commit，不夹带无关改动。

## 明确不做

- 探测、静默检测、恢复通知（B2/B3）。
- episode 事件上报 Server（v2.0 前只本地）。
- 不改 `server/**`、`web/**`、不动数据库。
