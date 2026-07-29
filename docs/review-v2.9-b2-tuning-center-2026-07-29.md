# 验收记录：v2.9-B2 调权中心 + confirm 采纳链路（2026-07-29）

范围：11abf8a。验收环境：Linux，Go 1.24.5 + Node 22（vue-tsc）。

## 结论：通过，1 个 P2 验收修正已由验收方直接修复（63bf610）

- `go vet` + 全量测试 + `pnpm typecheck` 全绿。
- **采纳链路端到端核验**：adopt 事务（pending 守卫+RowsAffected 复核）→ 目标实例=同站点启用成员按 id 字典序第一台（SQL 内联 siteOf 回退语义）→ 插入 channel.update 命令（payload `{"priority":N}`）→ ingest 心跳下发解析 Priority → agent channelcontrol 写 newapi priority——全链路每一环实测存在；审计（tuning.adopt/dismiss）、dispatch 状态推进（demote 采纳排试岗、trial 采纳置试岗中）、60 分钟 pending 过期均符合批次文件。
- **016 迁移**：纯增量 ADD COLUMN×2+ADD INDEX，单语句重放靠 1060 容忍，反向断言测试在。
- **页面**：五区块齐全（模式+13 参数策略表单/梯队总览按模型分组含试岗状态/建议流水含 rule 状态签与采纳按钮/7-30 天报表/说明），站点接线正确，auto 双端置灰拒绝（`mode_not_supported_until_v2.9-B3`）。api-contracts.md 已补 v2.9-B2 章节。

## 验收修正（P2，已修）

**confirm 模式 pending 重复堆叠**：评估窗口（15min）大于冷却（10min），持续劣化的渠道每个窗口新产一条 pending（旧的 60 分钟才过期）——页面同挂多条重复建议，且每日 6 次动作预算约 90 分钟内被同一决策的重复烧光，之后正当动作被限流。修复：actionAllowed 增加"同渠道存在 pending 动作建议即不再产出"守卫（引擎类型断言模式+mysqlstore EXISTS 查询），补两次评估只产一条的回归测试。

## 顺手修正（P3）

TuningView 内联 `site_id || instance_id` 回退，改走 shared siteOf（v2.8 唯一实现规则）。

## 记档

- **流程违规**：交付 commit message 未粘贴批次文件要求的自查清单（批次文件明文要求，历史各批均执行）——记录在案，下批注意。
- dismiss 后渠道若持续劣化，下个窗口会再次产出建议（受冷却与每日上限约束）——"忽略≠静默"是有意语义，页面说明区未明说，B3 时补文案。
- 采纳 trial 后的试岗观察由引擎 trialHealthyWindows 自动收敛（状态删除=留任），confirm 模式下无需人工二次确认留任——合理，记档明确语义。

## 部署提示

先 Server（016 自动迁移）；confirm 档开启前建议先在 observe 下看几天新引擎的建议质量。
