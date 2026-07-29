# Codex 任务：v2.9-B2——调权中心 + confirm 采纳链路

背景：调权闭环第二期（设计见 docs/design-v2.9-auto-dispatch.md §3 值班模型 + docs/design-v2.1-auto-tuning.md §7）。值班引擎（v2.9-B1）已按新规则产出建议，本批建 Web 调权中心并打通 confirm 档；**auto 档一律不做**（v2.9-B3）。**依赖：v2.9-B1 已合入。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### Server

- **模式放开到 confirm**：`tuning_handler.go` PUT policy 现硬拒 `mode != "observe"`——改为接受 `observe|confirm`，**auto 仍拒**（错误信息注明 v2.9-B3 提供）；模式与策略修改照旧记 UpdatedBy。
- **引擎 confirm 行为**（server/internal/tuning/engine.go）：站点策略 mode=confirm 时，**动作型建议**（demote/trial）`ModeAtCreation=confirm、Status=pending`（observe 仍 recorded；信息型建议 no_backup/ladder_exhausted/mixed_channel 永远 recorded 无按钮）；pending 超过 60 分钟未处理由引擎置 `expired`。值班判定/防抖/试岗退避逻辑（B1 产物）不动。
- **采纳/忽略 API**（增量，Dashboard API 只增不改）：
  - `POST /api/dashboard/tuning/recommendations/{id}/adopt`：校验状态为 pending → 复用既有渠道命令创建逻辑（channel.update 的 **priority** 路径——值班模型只动优先级，confirm 语义、created_by=操作人、目标实例=**该站点字典序第一台**——沿用 B4 约定）→ 建议置 adopted 并回写 command_id，**同步推进 tuning_dispatch_states**（demote 采纳→记 demoted/试岗排程；trial 采纳→按试岗结果留任或退避）；命令创建失败建议保持 pending 并返回错误。
  - `POST /api/dashboard/tuning/recommendations/{id}/dismiss`：pending → dismissed，记操作人。
  - 两接口均写 operation_audits（operation_type=tuning.adopt/tuning.dismiss，含建议 ID 与 before/after 摘要）。
- 事后回填对 adopted/dismissed/expired 照常执行（命中率统计不因状态中断）。

### Web（新页"调权中心"）

- 路由 `/tuning` + 菜单项（图标沿用 @element-plus/icons-vue 现有依赖）；页面跟随顶栏站点切换（站点→采集实例=字典序第一台成员，与既有维度页取数一致）。
- 五个区块：
  1. **模式与策略**：observe/confirm 二选一（auto 置灰注明 B3）+ 策略表单（B1 定稿的值班参数：窗口/样本/错误率线/熔断线/延迟倍数与下限/防抖/试岗退避三项/试岗观察/冷却/每日上限，校验规则同 server），保存提示下轮评估生效；
  2. **梯队总览**：按模型分组展示梯队——在岗/备胎/降级中（含下次试岗时间）状态签，数据来自 tuning_dispatch_states+渠道快照（需增量查询接口）；
  3. **建议流水**：时间线列表——rule 状态签（降级/试岗/无备胎/梯队用尽/混布）、证据一句话、当前值→建议值、事后徽标（命中 ✓/未命中 ✗/待回填/信息型）；pending 行带"采纳/忽略"（采纳弹既有危险操作确认框）；
  4. **命中率报表卡**：消费 `/api/dashboard/tuning/report`（7/30 天建议数、命中率、采纳率，口径=demote/trial），注明"命中率持续 ≥85% 才建议开 auto"；
  5. **说明区**：值班模型与三档模式各一段话解释（给未来的运维同学看）。

## 接线点（逐个核对，不得遗漏）

policy PUT 模式校验与测试、engine confirm 分支与 pending 过期（信息型建议除外）、adopt/dismiss 两接口+审计+命令复用+dispatch_states 推进、梯队总览查询接口、api-contracts.md 增量条目、路由/菜单、页面五区块、站点取数、报表卡。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck`；新增测试：mode=confirm 接受/auto 拒绝、confirm 下建议 pending、pending 60 分钟过期、adopt 建命令且回写 command_id、adopt 非 pending 状态拒绝、dismiss、审计落库。
2. 手工：策略切 confirm → 造一个高错误率渠道 → 建议以 pending 出现在页面 → 点采纳 → 渠道命令出现在渠道命令列表并被 agent 执行 → 建议变 adopted 带 command_id；点忽略 → dismissed；放置 60 分钟 → expired。
3. observe 模式回归：切回 observe 后行为与现版本一致（建议 recorded、无按钮）。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] auto 模式在 server 与 Web 双端均不可选（B3 边界守住）
- [ ] adopt 完整复用渠道命令链路（无旁路直调 agent/newapi）
- [ ] 建议状态机 recorded/pending/adopted/dismissed/expired 有测试；信息型建议无按钮不过期
- [ ] observe 回归：默认行为与 B1 版本逐项一致
- [ ] api-contracts.md 已更新；一个 commit：`feat(server,web): tuning center with confirm adoption flow (v2.9-B2)`

## 明确不做

auto 执行器/哨兵/回滚（B3）；agent 控制能力上报（记档可能项）；策略评分模型；模型级策略覆盖（记档）；探测并入；钉钉/企微建议提醒（通知接线随 B3 一起议）。
