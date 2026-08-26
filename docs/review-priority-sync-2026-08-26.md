# 验收记录：保存基础优先级同步线上（2026-08-26）

范围：36dd9a5。验收环境：Linux,vet+server 全量测试、直连真库集成、typecheck 全绿。

## 结论：通过,零缺陷

修真缺口：调权页改"基础优先级"此前只存锚点,线上优先级从不变（引擎只在熔断/恢复时写优先级）。现在保存基础值时逐渠道对比 saved BasePriority vs 线上 CurrentPriority,不一致即发 base_priority_sync 写（manual 口径,流水+审计带前后值）。

关键核验：

- **两条写路径都正确扩展**：直连=纯优先级 Update（不触权重）;命令路径 payload 仅含 priority,审计 before/after 记优先级;两处均前置校验 Current/Proposed 非空;
- **熔断/probing 渠道跳过同步**（其优先级归状态机管）,配套改动=恢复时优先级取**当前最新 BasePriority** 而非熔断时刻旧快照——熔断期间改的优先级在恢复时自动生效,闭环完整;
- agent 命令执行器天然兼容 priority-only payload（按存在字段设值);测试:handler 同步矩阵+directcontrol 纯优先级断言。

记档（P3 级以下）：对比基准 CurrentPriority 来自渠道快照（≤10min 陈旧）,极端时序下可能漏发或多发一次同值写,幂等无害。

无迁移。rc68 不含,下 tag 收。
