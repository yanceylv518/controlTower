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

## B2.5 验收（2026-07-30 追加，d478fb5）

结论：**通过，零缺陷**。`go vet` + 全量测试 + `pnpm typecheck` 全绿。策略拆为 scheduling/criteria/assignments 三段；DecodePolicyJSON 旧平面 JSON 整体回退默认值（有测试）；criteriaFor 指派/回退有测试；evidence 带 criteria_name；表单两区块带说明文案；校验错误路径分组前缀。行为等价硬证据成立——引擎测试的判定断言零修改，仅有的断言改动是批次自身要求的校验路径前缀与新增 criteria_name 断言。自查清单本批已按要求贴入 commit message（上批违规未再犯）。

## 页内标准说明验收（2026-07-30 追加，b12a595）

调权中心新增降级标准说明卡（判定公式动态引用当前配置值）+ 参数注释 + 归因口径说明。逐条对照引擎行为核验：常规/延迟/持续窗口重计/归因公式/0.15=15% 标注均准确；响应式布局与 typecheck 通过。**验收修正（bb93def）**：熔断条目原文"无需等待持续窗口立即触发"漏说最少样本闸同样生效（不足 20 条不评估），已补——运维误读风险（少量请求全失败≠触发熔断）。

## 部署提示

先 Server（016 自动迁移）；confirm 档开启前建议先在 observe 下看几天新引擎的建议质量。
