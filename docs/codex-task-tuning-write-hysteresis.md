# Codex 任务：调权常规写入加死区+最小间隔（write hysteresis）

背景：auto 模式下计算权重 = round(基础权重 × 四因子倍率)，因子基于 15 分钟滑动窗口每分钟重算，倍率天然抖动 1%~3%。现行写入闸只有"取整后精确相等去抖"（`alreadyWritten` + `proposed != current`），**大基础权重（≥100）渠道 1 个单位 = ≤1% 变化，分钟级抖动足以每分钟触发一次真实写入**：newapi 每分钟被 PUT（事务重建 abilities 行），CT 变更流水被单渠道 ±1 事件刷满（300 条上限，熔断/恢复事件被淹没）。用户拍板方案：**死区 + 最小写入间隔组合（与语义），事件驱动写入豁免**。设计讨论记录在 design-tuning-direct-control.md 会话延伸（2026-08-10）。

**无新迁移**（两参数进 tuning_policies.policy_json；引擎所需 LastWriteAt/LastWrittenWeight 状态字段已存在）。**不碰 directcontrol 包**（本批全部在引擎与策略层，直连/命令队列两路径自然同享）。

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量 `go test ./...` + `pnpm -r typecheck` + `pnpm -r build`。**

## 设计

### 1. 策略参数（tuning.Policy continuous 段）

- `write_deadband_percent` float，默认 **5**，合法域 [0,50]，0=关闭死区；
- `min_write_interval_minutes` int，默认 **5**，合法域 [1,60]，1≈事实关闭。

要求：Validate 校验范围；DecodePolicyJSON 对旧策略 JSON（无这两键）回退默认值（既有兼容读模式，参照 sparse 参数的处理）；两参数进策略保存/回显全链路。

### 2. 引擎：常规 weight_write 闸追加两条件（与）

仅动 `continuous_engine.go` 常规写入点（`alreadyWritten` 所在处），在现有 `mode=="auto" && writeAttemptAllowed && !alreadyWritten && proposed != current` 之上追加：

- **死区**：`|proposed − reference| ≥ max(1, round(baseWeight × deadband% / 100))`，reference = LastWrittenWeight（非 nil 时）否则 CurrentWeight；
- **间隔**：LastWriteAt 为 nil（从未写过）直接放行；否则 `now − LastWriteAt ≥ min_write_interval`。

### 3. 豁免（必须逐条落实，不可漏）

以下写入是状态机转移，**不受死区与间隔约束**：

1. 熔断置零写入（circuit_opened 路径）；
2. 探针通过后的软启动恢复写入（circuit_recovered 路径）；
3. **write_failed 暂停后的慢速重试写入**（即 `writeAttemptAllowed` 因重试窗口放行的那次）——若被死区/间隔挡住，暂停将永远无法经"重试成功"解除。

### 4. 堵一个既有隐患：暂停期间变化蒸发 → 暂停卡死

write_failed 暂停期间若计算权重漂回（`alreadyWritten` 成立或落入死区），重试窗口到达时无写可试，暂停永久滞留（解除唯一路径是成功写入）。要求：**重试窗口到达且已无需写入时，直接清除 write_failed 暂停**（调 noteWriteSuccess 语义：清暂停+清计数）——路径是否康复无从验证，但已无写入诉求；下次真实变化自然重新尝试，路径仍坏会重新计数暂停。补测试钉住。

### 5. observe 事件同享死区

weight_observed 现按 `previous.ProposedWeight != state.ProposedWeight` 记录，同样被 ±1 抖动刷屏。改为：`|state.ProposedWeight − previous.ProposedWeight| ≥ 死区阈值`（同 §2 公式，reference=previous.ProposedWeight）才记录；首条（!exists）照旧记录。

### 6. 前端

- 规则设置页参数区加两项：**"写入死区（%）"**（说明：计算权重变化不足基础权重此百分比时不写入 new-api，0 为关闭）与 **"最小写入间隔（分钟）"**（说明：同渠道两次常规调权写入的最小间隔；熔断与恢复不受限）；
- shared TuningPolicy 类型补两字段；
- 帮助抽屉"安全保护"/写入相关小节补一条组合语义说明（死区与间隔同时满足才写、熔断/恢复/暂停重试豁免）。

## 测试要求（引擎测试用现有 continuousFake 模式）

1. 死区：base=100、deadband=5%，proposed 相对上次已写变化 4 单位不写、6 单位写；deadband=0 时 1 单位即写；
2. 间隔：变化超死区但距上次写入 <5min 不写，≥5min 写；LastWriteAt nil 首写放行；
3. 组合：只满足其一不写；
4. 豁免：熔断置零与恢复写入在死区/间隔均不满足时照常执行；write_failed 重试窗口写入不被死区/间隔阻挡；
5. §4：暂停期间变化蒸发，重试窗口到达 → 暂停清除、零写入尝试；
6. observe 事件死区过滤（小变化不追加事件、大变化追加）；
7. 策略校验域测试 + 旧 JSON 兼容读默认值测试。

## 明确不做

- 不加迁移、不动 directcontrol 包与 channel_commands 语义；
- 不做每模型级参数覆盖（全局参数即可，记档）；
- 不动熔断/探针/恢复判据本身；
- 不做写入合并/批量（每渠道独立判据）。

## 自查清单（粘贴进 commit message）

- [ ] go test ./... 全绿（Linux）
- [ ] pnpm -r typecheck && pnpm -r build 全绿
- [ ] 死区/间隔/组合/三豁免/暂停蒸发清除/observe 过滤/校验域/兼容读测试全部存在且通过
- [ ] 未新增迁移文件；未改 directcontrol 包
- [ ] 设置页两参数可存可读可回显，帮助抽屉已更新
