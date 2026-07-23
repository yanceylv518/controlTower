# Codex 任务：v2.8-B1——Agent 日志采集开关（CT_LOG_COLLECT_ENABLED）

背景：pinducloud_cn 上 ALB 双机，B 机需部署一个**只跑心跳/健康探测/资源/docker/nginx、不碰共享库**的 agent（共享库数据每站点仅一份采集者，见 docs/design-v2.8-multi-site.md 第 2 节）。现状 `LogDSN` 必填且日志采集无开关（DockerEnabled/ChannelSnapshotEnabled 都有，唯独它没有），此形态部署不起来。**依赖：无；与 server 侧 v2.8-B2 并行。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试。**

## 设计

- **新配置键** `CT_LOG_COLLECT_ENABLED`，bool，**默认 true**——现有部署零感知，config example 与 agent/README 同步补充。
- **关闭后的行为**（`agent/internal/config/config.go` + `agent/cmd/control-tower-agent/main.go`）：
  - `LogDSN` 不再必填，全程不打开共享库连接；
  - 跳过日志采集、backlog 统计、样本上报；心跳/report 中 backlog 相关字段报零值（server 侧兼容零值，不得 panic/报错）；
  - 渠道快照强制不跑（快照走同一共享库）；
  - 心跳、健康探测（NewAPIStatusURL）、系统指标、docker、nginx timing **全部照常**。
- **校验规则**（fail fast，非静默失效）：
  - `!LogCollectEnabled && ServerURL == ""` → 配置错误（独立告警模式的唯一职责就是采日志发企微，关采集即无意义）；
  - `!LogCollectEnabled && WeComWebhookURL != ""` → 配置错误（企微错误告警由日志驱动，关采集后 notifier 永不触发，禁止静默无效配置）；
  - `!LogCollectEnabled && ChannelSnapshotEnabled` 显式为 true → 配置错误，错误信息提示两者关系（默认 true 时自动降级为关，不报错——避免逼用户多写一行）。
- **preflight**（`agent/internal/preflight`）：关闭时跳过 DB 类检查（连库/logs 表权限/channels 表权限），输出 skip 状态而非 fail；其余检查照常。

## 接线点（逐个核对，不得遗漏）

config 校验矩阵、main.go 的 collectAndReportFullPass（无 DB 路径）、独立告警模式入口（拒绝）、preflight、心跳 backlog 零值、config example、agent/README 配置表。

## 验证要求

1. 全量测试绿；新增校验矩阵单测（上述三条拒绝 + 默认 true 不变 + 关闭且无 LogDSN 通过）。
2. 手工（可在 A 机旁跑第二份进程模拟）：`CT_LOG_COLLECT_ENABLED=false` + 不配 LogDSN 启动 → server 出现新实例心跳/健康/资源/docker 数据、无任何日志事件上报；journal 无连库尝试。
3. preflight `--preflight` 关闭态输出 skip 而非 fail。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 默认 true 时行为与现版本逐项一致（回归测试覆盖）
- [ ] 关闭态全程零共享库连接（含渠道快照、preflight）
- [ ] 三条 fail-fast 校验有测试；快照默认值自动降级不报错
- [ ] config example 与 README 已更新
- [ ] 一个 commit：`feat(agent): log collection switch for heartbeat-only deployments (v2.8-B1)`

## 明确不做

站点概念（server 侧 v2.8-B2）；采集角色自动切换/选主（单点采集者是约定，不做高可用）；nginx 采集开关变化（维持现状：配了路径就采）。
