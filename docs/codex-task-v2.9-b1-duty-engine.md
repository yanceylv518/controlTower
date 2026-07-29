# Codex 任务：v2.9-B1——值班调度引擎（observe 静默运行）

背景：调权引擎按 docs/design-v2.9-auto-dispatch.md §3 定稿的**值班模型**重写。本批纯 server 侧、**只跑 observe**（产建议不产动作），页面与 confirm/auto 后续批次接。**依赖：无。原 v2.1 渠道级规则的引擎逻辑整体替换，旧 observe 数据作废（保留表数据不迁移，新旧建议靠 rule 值区分）。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试。**

## 设计

### 策略 schema 重定义（tuning.Policy 整体替换，页面 B2 才接，字段名此批定稿）

`window_minutes:15, min_samples:20, error_rate_threshold:0.15, severe_threshold:0.50, latency_multiplier:2.0, latency_floor_seconds:10, sustained_windows:2, trial_initial_minutes:60, trial_backoff_factor:2, trial_max_minutes:1440, trial_windows:2, cooldown_minutes:10, daily_action_limit:6`。校验：比例 0<x≤1、倍数 ≥1、分钟 1~2880、样本 1~1000、阈值 error<severe。旧字段（weight_step_ratio 等）删除；PolicyRecord 仍按实例（=站点采集者）存取，mode 校验暂维持只收 observe。

### 梯队构建（每轮评估先算）

- 数据源：最新渠道快照（models/priority/status）+ 站点 instance_channel 指标。
- 渠道 models 清单长度 >1 → 标记混布、跳过并产出一条 rule=`mixed_channel` 的记录型建议（去重：同渠道存在未过期同类记录不重复产出）。
- 同模型启用渠道按 priority 降序排梯队；**在岗 = 最高优先级的全部渠道**（并列时逐个评估、逐个判定）。

### 判定（只评在岗者；窗口/样本/防抖按策略）

1. `degrade`：错误率 ≥ error_rate_threshold 连续 sustained_windows 周期；
2. `severe`：错误率 ≥ severe_threshold，1 周期；
3. `latency_degrade`：P95 ≥ 自身 24h 基线 × latency_multiplier 且 ≥ latency_floor_seconds，连续 sustained_windows 周期。
   - **24h 基线**：取该渠道最近 24h 的 metric_1m P95 中位数（有效桶 = 样本 ≥min_samples 的桶；有效桶 <8 个则本条判定跳过）；每渠道基线值缓存 10 分钟。

### 决策与建议产出（observe：全部只记录）

- 备胎存在性：梯队中存在未被本引擎降过且启用的更低优先级渠道 → 产出 rule=`demote`（proposed_priority=全梯队最低值-1，evidence 含判定依据+接岗备胎名）；不存在 → rule=`no_backup`（记录型，去重同 mixed）；梯队全部被降过一轮 → rule=`ladder_exhausted`（记录型）。
- 试岗：处于"已降级"状态的渠道到达 next_trial_at → 产出 rule=`trial`（proposed_priority=原值）；observe 下试岗判定用"假设时间线"记录（不实际动过就没有真实试岗，evidence 注明 hypothetical）。
- 冷却/每日上限在决策层生效（超限→不产出动作型建议，理由入日志）。

### 调度状态存储（015 迁移，新表 tuning_dispatch_states）

`instance_id, channel_id (联合 PK), model_name, original_priority, demoted_at, trial_attempts, next_trial_at, updated_at`。纯增量 CREATE TABLE IF NOT EXISTS，钉 ENGINE/CHARSET，反向断言测试照 014 模式。observe 模式也维护该状态（记录"假如执行了"的状态机，供命中率与 B3 复用），**引擎重启从表恢复**。

### 事后回填（30 分钟，语义随新规则）

- `demote` 命中 = 回看时该渠道错误率/延迟仍满足其触发判定（当时降是对的）；
- `trial` 命中 = 回看时该渠道指标恢复正常（当时回是对的）；
- `no_backup`/`ladder_exhausted`/`mixed_channel` 为信息型，不参与命中率；报表接口的命中率统计只计 demote/trial。

## 接线点（逐个核对，不得遗漏）

Policy schema+校验+默认值、梯队构建（含并列在岗与混布）、三判定（含基线计算与缓存）、决策闸（备胎/冷却/上限）、015 迁移+反向断言、状态机含重启恢复、回填新语义、报表统计口径更新、旧规则代码与旧字段清理（不留死分支）。

## 验证要求

1. `go test ./...` 全绿；单测覆盖：三判定各自触发/防抖/样本不足跳过、基线不足跳过、并列在岗、备胎存在性三分支、试岗退避序列（60/120/240 封顶）、冷却与每日上限、状态重启恢复、回填两种命中语义。
2. 手工（生产或验证环境 observe 静默跑）：造高错误率渠道 → 建议流水出现 demote 且 evidence 可读；唯一渠道模型 → no_backup；混布渠道 → mixed_channel 且不重复刷屏。
3. 部署后 SQL 抽查 tuning_dispatch_states 状态推进符合时间线。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] observe 下引擎零动作（不创建任何渠道命令）
- [ ] 015 纯增量+反向断言；状态机重启恢复有测试
- [ ] 旧渠道级规则代码与旧策略字段零残留
- [ ] 信息型建议去重不刷屏；命中率只计 demote/trial
- [ ] 一个 commit：`feat(server): duty-rotation dispatch engine, observe-only (v2.9-B1)`

## 明确不做

confirm/auto 执行（B2/B3）；页面（B2）；权重调整（设计已出列）；模型级策略覆盖（记档）；延迟基线的独立存储表（用 metric_1m 现查+缓存，不够用再议）。
