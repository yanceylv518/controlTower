# Codex 任务：v2.9-B2.7——动态配权独立开关 + 梯队权重展示

背景：用户拍板（2026-07-31）——①动态配权与降级调度解耦：配权自带**关闭/观察/自动**三态开关，与全局 observe/confirm/auto 各管各的（降级可停在人工确认档、配权先观察攒数据）；②梯队总览每渠道要看到**优先级、当前权重、方案算出的目标权重**。**依赖：16c7b88（auto 护栏验收修正）已合入。v2.9 未上生产，schema 改动零迁移成本。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### 一、配权独立三态开关

- **schema**：`dynamic_weighting.enabled`（bool）替换为 `dynamic_weighting.mode ∈ {"off","observe","auto"}`，默认 `"observe"`；DecodePolicyJSON 旧 `enabled` 字段兼容读（true→observe、false→off，读到即转写新字段）；校验拒绝其他值。
- **引擎**：rebalance 评估在 mode != off 时运行；建议的状态与执行由**配权自身 mode** 决定（observe→recorded 只记录、auto→pending+事务执行），不再看全局 mode。全局 mode 只管 demote/trial。evidence 的 `observe_only` 字段语义同步（= 配权 mode=="observe"）。
- **哨兵扩展**：命令过期哨兵现只在全局 auto 时回落 confirm；扩展为——过期未执行信号命中时，全局 auto→confirm **且** 配权 auto→observe（各自独立回落、各记一条 auto_paused，evidence 注明哪个开关被降；只统计策略 UpdatedAt 之后的过期，语义与 16c7b88 一致）。
- **每日预算**：配权预算池维持 16c7b88 的独立池不变。
- **页面**：模式与策略区的配权小节加三态单选（关闭/观察/自动），文案注明"独立于上方运行模式"；auto 选项说明"建议生成即自动执行并可回滚"。

### 二、梯队总览权重展示

- 每渠道行从 `名称 + P{优先级} + 状态签` 扩展为：`名称 + P{优先级} + 权重 {当前} → {目标} + 状态签`。
- **目标权重来源**：该渠道最近一条 rebalance 建议（任何状态）的 proposed_weight；建议时间超过 2× 评估窗口视为过期不显示箭头（只显示当前权重）；无建议同理。悬浮提示显示建议时间与 raw/protected 倍率（evidence 里有）。
- **数据获取**：`GET /api/dashboard/tuning/recommendations` 增加可选 `rule` 过滤参数（增量，RecommendationQuery 加 Rule 字段，SQL 相应 WHERE）；梯队区单独按 `rule=rebalance&limit=100` 拉取后按渠道取最新，不与建议流水列表共用数据（流水有自己的 limit 语义）。api-contracts.md 更新。

## 接线点（逐个核对，不得遗漏）

schema 替换+enabled 兼容读+校验、引擎 rebalance 按自身 mode 分支、哨兵双开关回落、页面配权三态单选、梯队行权重展示+过期判定+悬浮、recommendations rule 参数（handler/store/memory store/契约文档）、既有测试同步（rebalance 相关测试改从配权 mode 驱动）。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck`；新增测试：三态各自行为（off 不评估/observe 只记录/auto 执行）、全局与配权模式交叉（全局 observe+配权 auto 会执行 rebalance 且不执行 demote；全局 auto+配权 off 反之）、enabled 兼容读、哨兵分别回落两开关、rule 过滤参数。
2. 手工：配权 observe 下梯队出现"权重 100 → 108"展示且不执行；切 auto 后同建议自动执行、渠道命令可见；建议超时后箭头消失。
3. observe 回归：全局三档对 demote/trial 的行为与 16c7b88 版本一致。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 配权三态与全局模式完全解耦，交叉矩阵有测试
- [ ] enabled 旧字段兼容读有测试；写回一律新字段
- [ ] 哨兵对两个 auto 开关分别回落且各有测试
- [ ] 梯队权重展示含过期判定；rule 参数进 api-contracts.md
- [ ] 一个 commit：`feat(server,web): independent dynamic-weighting mode and ladder weight display (v2.9-B2.7)`

## 明确不做

配权 confirm 档（用户拍板只要观察/自动）；人工优先 24h 静默（独立必办事项，另立批次）；auto 企微通报（另议）；rebalance 命中率回填（报表口径维持 demote/trial）。
