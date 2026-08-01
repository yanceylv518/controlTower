# Codex 任务：v3.0-B3——熔断、探针恢复与软启动 + 值班遗留清理

背景：v3.0 收官批次（设计见 docs/design-v3.0-continuous-dispatch.md §2/§6；B2 验收修正 ab4f184/13f8151 已合入，**在其之上开发**）。交付分**两个 commit**：①熔断功能；②值班模型遗留清理。

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## Commit 1：熔断 + 探针 + 软启动

### 数据（020 迁移，纯增量单文件）

- `ALTER TABLE tuning_continuous_states ADD COLUMN circuit_state VARCHAR(16) NOT NULL DEFAULT 'normal', ADD COLUMN circuit_since DATETIME(6) NULL, ADD COLUMN original_priority BIGINT NULL, ADD COLUMN next_probe_at DATETIME(6) NULL`（1060 容忍；circuit_state ∈ normal/circuit/probing/soft_start）；
- `CREATE TABLE IF NOT EXISTS tuning_probe_results(command_id VARCHAR(64) PRIMARY KEY, instance_id VARCHAR(64), channel_id BIGINT, model_name VARCHAR(255), success_count INT, total_count INT, p50_ms BIGINT, p90_ms BIGINT, p95_ms BIGINT, reported_at DATETIME(6))`。
- 反向断言测试照例（无重建语句）。

### Agent 探针

- 新命令类型 `channel.probe`，payload `{model, probe_count, interval_seconds}`；agent 认领后串行执行 probe_count 次（间隔 interval_seconds 秒）newapi **渠道测试接口**（channelcontrol 增 `TestChannel(ctx, channelID, model)`，复用既有 admin 登录/token；接口路径以 new-api 实际版本为准并在交付说明记录）；
- 结果经 report 上报新 payload 字段 `probe_results: [{command_id, channel_id, success_count, total_count, p50_ms, p90_ms, p95_ms}]`（agent/gateway/ingest 契约三处同步），server 落 tuning_probe_results；命令执行完成回报沿用既有 result 语义；
- agent 端护栏：单命令探测总时长上限 2×count×interval 秒兜底退出；无 channelcontrol 凭证（CT_NEW_API_CONTROL_ENABLED=false）拒绝执行并回报错误——探针命令与写权命令同走控制机约定。

### 引擎状态机（每分钟评估内）

仅 `dispatch_modes[model]=="auto"` 的渠道执行动作；observe 档全部转为记录型事件（`circuit_would_open` 等），不动作。人工接管/混布暂停中的渠道不进入熔断流程。

1. **normal → circuit**：M < circuit_threshold(0.1) → 记 original_priority=基础优先级，经命令链路写 `{weight:0, priority:0}`（channel.update 支持双字段，agent 已支持 Priority），事件 `circuit_open`，circuit_since=now；
2. **circuit → probing**：now ≥ circuit_since + silent_minutes(5) → 下发 channel.probe，事件 `probe_started`，next_probe_at 防重（探针命令未回报前不重发；命令过期由哨兵覆盖）；
3. **probing 判定**：读 tuning_probe_results —— `M_new = K_error_probe × K_speed_probe`，K_error_probe = clamp(0.8^失败次数, 0.001, 1)；K_speed_probe = speedFactor(探针 P50/P90/P95 vs 组当前基线)，基线不可用（组内可比渠道<2）时取 1；
   - M_new < recovery_threshold(0.2) → 事件 `probe_failed`，回到 circuit 态重新静默 5 分钟循环；
   - M_new ≥ 0.2 → **soft_start**：写 `{priority:original, weight:round(base×soft_start_multiplier(0.2)) 最低 1}`，事件 `circuit_recovered`；
4. **soft_start → normal**：下一轮评估按正常公式计算并恢复写入，清 soft_start。
- 熔断/恢复写入不受 LastWrittenWeight 去抖阻挡（状态迁移必写），写后同步更新 LastWrittenWeight/LastWriteAt 保持人工接管检测正确。

### 页面点亮（ContinuousTuningView）

状态签补熔断中（含下次探测时间）/探测中/软启动；事件名补 circuit_open/probe_started/probe_failed/circuit_recovered/circuit_would_open；计数卡补 7/30 天熔断次数与探针成功率。api-contracts.md 增量更新（probe 契约字段、事件类型）。

Commit 1：`feat(server,agent,web): circuit breaker with probe recovery and soft start (v3.0-B3)`

## Commit 2：值班遗留清理（chore）

- 删除死代码路径：值班引擎（evaluateActive/scheduleTrials/findBackup/试岗/延迟基线判定）、旧 rebalance（evaluateDynamicWeights）、与其配套的死路径测试；
- Policy 清理：criteria/assignments、DynamicWeighting、scheduling 中试岗与冷却/每日上限字段删除（**保留** window_minutes/min_samples/sparse 两项——连续引擎在用）；DecodePolicyJSON 对旧字段维持"忽略不报错"；
- adopt/dismiss 接口与 pending/expire 机制、tuning_dispatch_states 的引擎侧读写退役（表与历史数据保留不删）；报表接口口径改为事件计数（或暂返回空结构，页面已不消费命中率）；
- 删除零引用的旧 `webapp/.../TuningView.vue`；页面隐藏全局 observe/confirm/auto 模式选择（policy.mode 字段保留 API 兼容，记档）；
- 哨兵保留（改为只看 dispatch_modes 的 auto 与全局 auto 兼容判断可简化为前者）。

Commit 2：`chore(server,web): retire duty-rotation engine leftovers`

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck`；新增测试：状态机四迁移各自触发与防重、observe 只记事件、探针 M_new 判定两分支、软启动首轮 0.2 次轮恢复、熔断写入绕过去抖且更新对账值、agent 探针执行与超时兜底、契约三处序列化、020 反向断言；清理后全量测试绿且无死代码残留（grep evaluateActive 等零命中）。
2. 手工：造 M<0.1（如连续错误压 K_error）→ 渠道权重/优先级归零 → 5 分钟后探针命令出现并被 agent 执行 → 探针失败继续熔断 / 探针成功恢复+软启动 → 下轮回正常；observe 档同场景只出事件。
3. 交付说明记录 new-api 渠道测试接口的实际路径与响应格式。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 状态机四态迁移+防重发有测试；observe 零动作
- [ ] 熔断写入绕过去抖并更新 LastWrittenWeight；恢复写回 original_priority
- [ ] 探针走控制机约定，无凭证拒绝执行；命令过期由哨兵覆盖已验证
- [ ] 清理后 grep 值班符号零命中；保留字段清单核对（window/min_samples/sparse/dispatch 表）
- [ ] 两个 commit 按规定命名；api-contracts.md 已更新

## 明确不做

优先级的常态自动调整（设计定死：仅熔断联动）；探针结果入指标管道（只服务恢复判定）；多模型渠道熔断（混布保险丝已挡）；tuning_dispatch_states 表删除（历史数据保留）。
