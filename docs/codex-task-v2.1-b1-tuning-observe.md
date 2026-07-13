# Codex 任务：v2.1-B1——渠道调权评估引擎（仅 observe 模式）

自动调权三批次的第一批：Server 侧评估引擎 + 策略存储 + 建议记录 + 30 分钟事后回填。**本批不产生任何动作**（不建渠道命令、不碰 new-api），只积累"如果当时调了会怎样"的决策记录与命中率数据。设计依据：`design-v2.1-auto-tuning.md`（策略字段、命中率口径、护栏原则以它为准）。

**文末自查清单粘贴进 commit message。**

## 背景速读

- 数据源全部现成：`metric_1m` 表（`dimension_type='instance_channel'`，含 request_count/error_rate/p95_use_time，读法参考 `server/internal/mysqlstore/metrics.go`）；`channel_snapshots` 表（每渠道最新一条 = 当前 weight/status/name）。
- 后台任务模式参考 `server/cmd/control-tower-server/main.go` 的 `startAggregationRunner` / `startRetentionRunner`（goroutine + ticker）。
- 迁移：新建 `server/migrations/006_tuning.sql`，**所有 CREATE TABLE 必须钉 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`**（`migration_sanity_test.go` 会强制检查）。
- API 挂载在 `server/internal/httpapi/mux.go`，dashboard 路由用 `protect(...)` 包（session/token 双通道）。响应字段命名与既有 dashboard API 一致，**不得把存储层结构体直接序列化出去**（M1-B2 返工教训）。
- `001_init.sql` 里有个早期占位表 `weight_adjustments`——**不用、不动、不迁移数据**，本批新建 `tuning_recommendations`（字段更全：priority、mode、outcome）。
- `channel_snapshots` 无 priority 字段 → 本批 severe/priority_drop 规则**预留字段但不实现**（见"明确不做"）。

## 硬性纪律

- 本批**绝不创建渠道命令、绝不写任何会被 Agent 执行的东西**——observe 模式是零风险批次，这是它存在的意义。
- 策略校验必须拒绝危险值（阈值出 [0,1]、step_ratio ≤0 或 >1（degrade）、窗口/样本数 ≤0、cooldown <1 分钟）。
- 零新增依赖；迁移幂等可重跑；`make test` 全绿。

## 工作项

### 任务 1：迁移 `006_tuning.sql`

```sql
tuning_policies (
  instance_id VARCHAR(64) PK,
  policy_json TEXT NOT NULL,          -- 结构见任务 2，Go 侧校验
  mode VARCHAR(16) NOT NULL,          -- observe|confirm|auto，本批只允许 observe
  updated_at DATETIME(6), updated_by VARCHAR(128)
)
tuning_recommendations (
  id VARCHAR(64) PK, instance_id, channel_id, channel_name, created_at,
  rule VARCHAR(16),                   -- degrade|recover（severe 预留）
  evidence_json TEXT,                 -- 触发时快照：error_rate/样本数/p95/连续周期数/窗口起止
  current_weight BIGINT, proposed_weight BIGINT,
  current_priority BIGINT NULL, proposed_priority BIGINT NULL,   -- 预留，本批恒 NULL
  mode_at_creation VARCHAR(16),
  status VARCHAR(16),                 -- 本批只产 recorded；枚举含 pending|adopted|auto_executed|dismissed|expired|rolled_back 备后批
  command_id VARCHAR(64) NULL,        -- 备后批
  outcome_json TEXT NULL, outcome_at DATETIME(6) NULL,
  hit TINYINT NULL                    -- 1 命中 / 0 未命中 / NULL 未回填
)
+ 索引：(instance_id, created_at)、(instance_id, channel_id, created_at)、(outcome_at)（回填扫描用）
```

### 任务 2：策略存储与 API（`server/internal/tuning` 新包 + mysqlstore）

- 策略结构与默认值照抄设计文档 §3（evaluation_window_minutes=15、min_samples=20、degrade{0.15/2 周期/×0.5/floor 1}、recover{0.02/4 周期/×2.0 封顶原值}、cooldown_minutes=10）。severe 段解析但忽略（注释注明本批不实现）。
- `GET /api/dashboard/tuning/policy?instance_id=` → 有记录返回记录，无记录返回默认值（标 `"isDefault": true`）；`PUT` 同路径写入（校验失败 400 带字段级错误；`mode` 非 `observe` 一律 400 `mode_not_supported`——confirm/auto 是 B2/B3 的事，现在接受了就是给未来埋雷）。

### 任务 3：评估引擎 runner

- `startTuningRunner`：每分钟 tick；对每个启用实例，若距上次评估 ≥ 该实例策略的评估窗口，跑一次评估。
- 单次评估（每实例）：
  1. 取窗口内 `instance_channel` 维度的 metric_1m 行，按渠道聚合出 error_rate（错误数/请求数加权，不是逐行平均）、样本数、P95（取窗口内最大值即可，注明近似）；
  2. 取每渠道最新快照（status 非启用的跳过；无快照的跳过并 debug 日志）；
  3. **degrade**：样本 ≥ min_samples 且 error_rate ≥ 阈值 → 该渠道"连续满足计数"+1，达 sustained_windows 且不在 cooldown 内 → 产建议：proposed_weight = max(floor, floor(current×step_ratio))；否则计数清零；
  4. **recover**：仅对"此前有 degrade 建议且未被 recover 建议对冲"的渠道模拟（observe 下没真降过，恢复建议是决策序列的完整模拟，evidence 注明 `simulated: true`）：error_rate ≤ 恢复阈值持续 sustained_windows → proposed_weight = min(原始值, current×2)；"原始值"取该渠道首条 degrade 建议里的 current_weight；
  5. 建议落库 status=recorded，evidence_json 带全部触发证据；同渠道进入 cooldown。
- 连续计数与 cooldown 状态存内存（重启清零，注释注明；持久化等真实动作批次再说）。
- 每次评估打一行 info 日志：实例、评估渠道数、产出建议数。

### 任务 4：事后回填 runner（与任务 3 同 goroutine，每 tick 顺带跑）

- 扫 `outcome_at IS NULL AND created_at <= now-30min` 的建议（LIMIT 100/次）：计算建议后 30 分钟窗口的该渠道 error_rate/样本数/P95 → 写 outcome_json + outcome_at + hit。
- 命中口径（设计 §5）：degrade 建议命中 = 后续 30 分钟 error_rate 仍 ≥ degrade 阈值；recover 命中 = 仍 ≤ recover 阈值。后续窗口样本数 < 5 → hit 记 NULL、outcome_json 注明 `insufficient_samples`（没数据不硬判对错）。

### 任务 5：读 API

- `GET /api/dashboard/tuning/recommendations?instance_id=&limit=&before=`：倒序分页，返回建议全字段（evidence/outcome 解析成对象再出，不透传原始字符串）。
- `GET /api/dashboard/tuning/report?instance_id=&days=7|30`：建议总数、按 rule 分组数、已回填数、命中数、命中率；响应里带一行固定说明字段 `autoCriteria: "观察期命中率持续 ≥85% 且无最小可用集险情，才建议切 auto"`。

### 任务 6：文档

`docs/development-progress.md` 记一行；`deploy/server.env.example` 无新变量则不动（评估间隔走策略不走 env）。

## 验证要求

1. `make test` 全绿（迁移 sanity 会自动覆盖新 SQL 的 COLLATE 钉扎）。
2. 引擎单测（fake store）：min_samples 拦截小样本；sustained_windows 断续不触发/连续触发；cooldown 生效；weight_floor 不破；recover 只对有 degrade 前科的渠道产建议且封顶原值；加权 error_rate 计算正确。
3. 回填单测：命中/未命中/样本不足三分支。
4. API 测试：policy 默认值返回、校验拒绝表、mode=auto 被 400；recommendations 分页；report 命中率计算。
5. 手工冒烟：本地 MySQL 起 server，用 `deploy/seed-demo-data.sh` 的数据（或手插几行 metric_1m 高错误率数据）观察 2 个评估周期产出 degrade 建议、30 分钟后回填（可临时调小窗口验证，恢复默认后提交），过程记入交付说明。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 全批次零动作：无渠道命令创建、无 Agent 侧改动、无 new-api 访问
- [ ] 006 迁移钉 ENGINE/CHARSET/COLLATE；weight_adjustments 旧表未动
- [ ] PUT policy 拒绝 mode≠observe 与全部危险值
- [ ] recover 仅对有 degrade 前科渠道模拟且 evidence 标 simulated
- [ ] 回填三分支（命中/未命中/样本不足）有测试
- [ ] API 不泄漏存储结构体，命名与既有 dashboard 风格一致
- [ ] 一个 commit：`feat(server): tuning recommendation engine in observe mode (v2.1-B1)`

## 明确不做

confirm/auto 模式与采纳链路（B2/B3）；Web 调权中心页面（B2）；severe/priority_drop 规则（channel_snapshots 无 priority 字段，待 Agent 快照升级补采后启用，字段已预留）；探测数据并入；跨实例联动；new-api 权重/优先级语义实测（用户在验证环境做，B2 采纳链路上线前完成即可——B1 纯观察不受影响）。
