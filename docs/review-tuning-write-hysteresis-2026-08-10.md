# 验收记录：调权写入迟滞——死区+最小间隔（2026-08-10）

范围：2cc6f4c（codex 按批次 codex-task-tuning-write-hysteresis.md 交付,自查清单已贴进 commit）。验收环境：Linux,go vet+全量测试（server/agent/internal 三树）、直连真库集成（CT_MYSQL_TEST_DSN）、vue-tsc、desktop build 全绿。

## 结论：通过,零缺陷;一条 P3 记档（规格自身盲点,非交付缺陷）

## 核验

- **组合判据与语义**:常规 weight_write 须同时满足死区（|proposed−参照| ≥ max(1, base×%)，参照=LastWrittenWeight 否则 CurrentWeight）与间隔（now−LastWriteAt ≥ N 分钟,首写放行）;deadband=0 时阈值退化为 1 个单位=原精确去重语义,逃生门成立。
- **三豁免逐条落实且有测试证据**:熔断置零/软启动恢复写入——TestContinuousCircuitProbeAndSoftStart 特意钉 50%/60min 极端参数+新鲜 LastWriteAt,状态机写入照常执行;write_failed 慢速重试——自愈测试特意造"LastWriteAt=当下"证明重试旁路间隔。
- **暂停期变化蒸发直接清暂停**（批次 §4）:alreadyWritten 与落入死区两形态都测,零写入尝试、暂停+计数+错误全清;重试窗口未到时不提前清（retryingWriteFailure 门控在 writeAttemptAllowed 之内）。
- **observe 事件同享死区**;策略校验域 [0,50]/[1,60]、旧策略 JSON 兼容读默认 5/5 均有测试;设置页两参数+帮助抽屉参数详解与安全保护小节齐;**无迁移、未触碰 directcontrol**（契约恰好钉着）。

## 记档

- **P3（规格盲点,验收方自领）**:observe 事件死区的参照物按批次规格取"上一 tick 计算值",实为变化率过滤——缓慢漂移（如 1 单位/分钟）单步恒小于阈值,累计任意大也不记录 observe 事件。auto 写入无此问题（参照=上次已写值,水平过滤,累计漂移终触发）。修法=参照"上次已记录事件值"（状态需加一列),留待后续批次;总览页实时计算值不受影响,仅流水的观察记录有此盲区。
- 行为记档:软启动恢复写入后,向全权重的回攀写入属常规写入受间隔约束——软启动实际驻留时长从 1 个 tick 延长为约最小间隔（默认 5 分钟),方向偏保守,属可接受语义变化。

## 部署

无迁移。rc43 不含本批;含本批需打 rc44。参数即刻可在规则设置页调整（保存后下一分钟 tick 生效）。
