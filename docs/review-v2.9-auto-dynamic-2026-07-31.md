# 验收记录：自动调度 + 动态配权（2026-07-31）

范围：63ae0b9（恢复流程页内说明）、24e541f（动态配权 observe）、84c5108（auto 档启用）。验收环境：Linux，Go 1.24.5 + Node 22。**注意：此三笔为未立项交付**——B3 原计划待 confirm 期命中率数据达标后开工，动态配权（rebalance）从未立项；本记录按实现质量验收，节奏偏离另见"流程记档"。

## 结论：通过，3 个必修缺陷已由验收方直接修复（16c7b88）

### 交付质量（好的部分）

- 动态配权方案设计合理：仅同模型同优先级、健康、非降级、样本充足且 ≥2 渠道参与；四因子（TTFT/归因错误率/缓存/OTPS）相乘 → 钳制 → 平滑 → 单轮限幅 → 权重下限 1；组内基线按请求数加权；evidence 全量留痕（含 raw/protected 倍率）。与值班降级分工清晰：固定阈值规则先行，异常渠道降级而非配权。
- auto 执行事务化：建议+命令+审计（tuning.auto_execute）原子提交，失败保持 pending 不虚报成功；三模式语义（observe 记录/confirm 待办/auto 执行）贯通 demote/trial/rebalance 三类动作。
- codex 自写两份方案文档（dynamic-weighting-observe/auto.md）质量可用；全量测试与 typecheck 绿。

### 验收修正（三处必修，已修）

1. **P2 auto 降级丢试岗退避**：AdoptRecommendation 事务写入 dispatch 行时恒为 attempts=0+初始试岗延迟，引擎算好的指数退避只进内存、每轮评估从库重读即丢——auto 下反复故障的渠道会每 60 分钟无限重试而非退避。修复：自动执行成功后引擎补 PutDispatchState 持久化正确状态。
2. **P2 配权与降级共用预算 → 降级可能被饿死**：rebalance 与 demote/trial 共享冷却与每日 6 次上限（交付文档视为特性），后果是日常配权可在 90 分钟内烧光预算，渠道真故障时降级被限流；pending 的配权建议也会挡住紧急降级（去重守卫同池）。修复：两类动作独立预算池（含 pending 去重、冷却、每日上限三处），补"配权历史不得饿死降级"回归测试。
3. **P2 命令过期哨兵缺失**：设计 §4 对 auto 的硬性护栏（控制机约定失效的兜底）未实现。修复：引擎每 Tick 检查 auto 模式站点是否存在"system:auto 创建且过期未执行"的命令，命中即回落 confirm（UpdatedBy=system:sentinel）、记 auto_paused 建议条目并日志；只统计策略自身 UpdatedAt 之后的过期，重新开 auto 不会被历史误伤。页面补"自动暂停"状态签。

### 流程与遗留记档

- **节奏偏离**：auto 未按约定等待命中率 ≥85% 判据即启用（用户主导，记录事实）；建议：生产先保持 observe/confirm，命中率达标再切 auto——代码已就绪不等于该立即打开。
- **人工优先 24h 静默未实现**（设计护栏之一）：auto 下引擎无法识别人工改过的权重/优先级并让路。实现需引擎侧跟踪"期望值 vs 快照"并区分自身命令效果，量级不小——**记为 auto 正式启用前必办事项**。
- auto 动作无企微通报（哨兵与动作仅落建议流水+审计+日志）；通知接线待议。
- rebalance 的命中率回填语义未定义（fillOutcomes 对 rebalance 标记 informational？需核对报表口径——目前报表只计 demote/trial，rebalance 不参与命中率，可接受但记档）。
- 交付 commit 均未贴自查清单（无对应批次文件，属未立项交付的连带问题）。

## B2.7 验收（2026-07-31 追加，1b0ec0a）

结论：**通过，零缺陷**。全量测试 + typecheck 绿，自查清单已贴。配权三态（off/observe/auto）与全局模式完全解耦——交叉矩阵有实测（全局 observe + 配权 auto：rebalance 自动执行、demote 不动）；`enabled` 旧字段兼容读（true→observe/false→off）有测试；哨兵扩展为双开关分别回落并各记 auto_paused（evidence 带 target/fallback，ID 加后缀防撞）；梯队行"权重 当前→目标"带 2×窗口过期判定与倍率悬浮；recommendations `rule` 参数进契约文档。顺带把配权指标（TTFT/缓存/OTPS）并入 QueryMetrics 单查询，无额外往返。

## 部署提示

rc19 打在 63ae0b9，**不含动态配权、auto、16c7b88 验收修正与 B2.7**；要发完整功能需新 tag。生产开启顺序建议：全局 observe + 配权 observe（各看一周数据）→ 降级切 confirm 用一阵 → 数据达标且"人工优先 24h 静默"补齐后再开两个 auto。
