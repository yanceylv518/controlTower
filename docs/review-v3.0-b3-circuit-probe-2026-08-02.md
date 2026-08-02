# 验收记录：v3.0-B3 熔断与探针恢复（2026-08-02）

范围：e9264c3。验收环境：Linux，Go 1.24.5 + Node 22。

## 结论：Commit 1（熔断功能）通过，1 个 P1 验收修正已修（a927513）；**Commit 2（值班遗留清理）未交付，待补**

### 通过项（对照批次文件 Commit 1）

- 020 迁移（状态列扩展 + probe_results 表）+ 反向断言；四态状态机（normal/circuit/probing/soft_start）齐全，含批次要求的关键细节：熔断/恢复写入绕过去抖并更新对账值、熔断写 `{weight:0,priority:0}` 恢复写回 original_priority（payload priority 正确限定在两类熔断规则）、探针命令防重（ProbeCommandID 在途不重发）、**探针命令丢失/过期 10 分钟兜底回熔断态**（超出批次要求的自愈细节，好）、写失败回退重试语义；agent `channel.probe` 串行执行+整轮聚合上报（含测试）、契约三处同步、probe 结果落表；B2 验收修正（去抖/人工接管/K_error 游标/混布）全部保留完好；observe 档只记事件；页面状态签/事件/计数点亮。全量测试 + typecheck 绿。

### 验收修正（P1，已修）

**恢复后再熔断死循环**：探针通过仅切软启动，K_error 仍是熔断前被打残的值（0.001~0.1）；软启动后的首个正常周期重算 M ≈ K_error < 熔断线，低流量渠道（真实流量不足以靠 1.08^成功 快速爬升）会陷入 熔断→探针通过→软启动→再熔断 的无限循环，每轮 ~8 分钟并持续烧探针。修复：探针通过时将 K_error 下限抬至恢复线（10 次成功探针即成功证据），回归测试走完 熔断→恢复→软启动→稳定 normal 全程。

### 记档

- **偏离**：探针 M_new 用 成功率×速度因子（batch 规格为 0.8^失败次数×速度因子）——两者在阈值 0.2 附近严格度相当，接受并记档；探针速度因子用 sqrt(基线P95/均值) 简化式（规格为三元组 speedFactor），探针只有单一延迟观测，简化合理。
- **Commit 2 未交付**：值班引擎死代码、Policy 旧字段、adopt/dismiss、旧 TuningView.vue、页面全局模式隐藏——批次明确的第二 commit 整体缺失，**需补交后 v3.0 才算完结、方可打 rc20**。
- 交付 commit 又未贴自查清单（连续第三次，记档）。

## Commit 2 补交验收（2026-08-02 追加，9343e0c）

结论：**通过，零缺陷**（-2552 行）。值班符号（evaluateActive/evaluateDynamicWeights/findBackup/scheduleTrials/criteriaFor 等）非测试代码零命中；duty_engine_test 与旧 TuningView.vue 删除；adopt/dismiss 与 ladders 路由移除；Policy 保留 window/min_samples/sparse/dispatch_modes/continuous、旧字段解码容忍；哨兵简化为按模型 auto 开关；tuning_dispatch_states 表与历史保留；验收方全部修正与回归测试（含 K_error 恢复下限）存活。全量测试 + typecheck 绿。**v3.0 全系列就此完结。**

## 站点化收尾验收（2026-08-02 追加，dce5091 + ac744fe）

结论：**通过，零缺陷**。连续调度整体从按实例改为按站点运转（ListEnabledSites、基础值/指标/快照查询按站点成员展开、页面直接以 site_id 为上下文）——渠道本是站点属性，这是正确的最终归位。**亮点：controlInstanceForSite 以"渠道快照最新 → 心跳最新 → id 字典序"三级证据选择控制机，取代 B4 以来的 `_1` 命名约定——那个记档已久的口子就此闭合**（命令与探针均路由至证据选出的控制机；哨兵同步按站点成员展开查询，无失明）。探针结果回写以 command_id 唯一匹配，安全。全量测试 + typecheck 绿。

记档：未立项交付、无自查清单（惯例记录）；main 历史含一次重复清理提交与两次 merge（codex 分支基线陈旧所致，净变更正确，未 force push 合规）；遗留远端分支 codex/site-scoped-tuning 可删。

## 部署提示

**rc20（428ea1c）不含站点化收尾两笔**——调度状态/策略的键位语义在这两笔中从实例改为站点，若 rc20 尚未部署，建议直接打 rc21 从站点化版本起步（避免部署 rc20 后产生按实例键位的状态残留）。上线开启顺序：先一键同步基础值 → 全模型 observe 观察因子与熔断事件 → 逐模型开 auto。部署时确认生产 new-api 版本的渠道测试接口路径与 agent channelcontrol.Probe 一致（交付说明缺失，记档）。
