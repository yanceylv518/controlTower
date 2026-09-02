# 验收记录：渠道 RPM / TPM 上限封顶加权（2026-09-02）

- 范围：`e114fcc feat(tuning): cap weight increases by channel load`
- 结论：**通过，验收修正两处前端缺陷（P2，`4c69f2e`）**，后端零缺陷。

## 功能

- `channel_base_values` 新增 `max_rpm` / `max_tpm`（0 = 不限制），调权中心
  “渠道 RPM / TPM 上限”折叠面板可逐渠道编辑，随基础值一起保存并入审计
  before/after。
- 连续评估每轮用 `metric_1m` 的 `instance_channel` 维度按评估窗口折算
  RPM = 请求数 / 窗口分钟，TPM = token 总量 / 窗口分钟，写入
  `tuning_continuous_states.metric_rpm/metric_tpm/capacity_limited`。
- 任一上限达到（`>=`）即 `capacity_limited`：单向守卫，性能系数只能维持
  或下调权重，不能上调；上界优先取本系统最近一次成功写入，其次取渠道快照
  当前权重。熔断归零、探测、软启动路径不受影响。

## 后端核对

- `tpm` 列语义：aggregator 按桶累加 `TotalTokens`，是 1 分钟桶的 token 总量
  而非速率，`SUM(tpm)/窗口分钟` 折算正确；请求数同口径。
- 守卫位置在 `ProposedWeight` 计算之后、熔断判定之前，只在正常评估分支生效；
  混合渠道、零基准权重两条早退分支在守卫前已 `continue`，无除零风险。
- 无指标行的渠道 `m` 为零值 → RPM/TPM = 0 → 不判限升，不会沿用上一轮状态。
- 观察模式 `weight_observed` 事件的死区锚点取封顶后的 `ProposedWeight`，
  自动模式的写入去重同样比对封顶后值，达上限时不产生多余写入。
- 快照同步 `channelBaseValueSnapshotUpsertSQL` 不触碰 `max_rpm/max_tpm`，
  “初始化/刷新基础值”不会清掉已配置的上限。
- 处理器校验 `max_rpm/max_tpm < 0` → 400（实测）；List/Put 状态列增删对称。
- 064 迁移只 ADD COLUMN 带默认值，走台账一次应用；集成测试双重放为空操作。

## 缺陷与修正（前端）

1. **脏状态下 30 秒自动刷新丢弃未保存的上限编辑（P2）**：`refreshRuntime`
   合并本地编辑时只保留 `base_weight/base_priority`，用户改了上限尚未保存，
   下一次刷新即被服务端旧值覆盖。修正：合并时同时保留 `max_rpm/max_tpm`。
2. **1366 宽度下上限面板溢出（P2，截图实证）**：`capacity-row` 使用固定像素
   列（110/130/210/220/80）总宽超过内容区，“最大 TPM”输入框被截断，“已限升”
   标签与“0 表示不限制”提示完全不可见。修正：列改为 `minmax` 弹性列、间距
   8px、输入框 100px；1366/1920 两档复验完整显示。

## 记档（P3，不阻塞）

- `PutContinuousState` 用主 upsert + 追加 UPDATE 两条语句写新列，每渠道每轮
  多一次往返；建议并入主 upsert。
- 上界优先取 `LastWrittenWeight`：自动模式下若外部把权重调低且渠道达上限，
  下一轮“确认外部变更”会把权重写回上次写入值（权威模式既有取舍，注释已说明）。
- 新增测试一例同时触发 RPM 与 TPM 两条件，未单独覆盖“优先取上次写入”分支。
- 关闭模式下仍计算并展示 RPM/TPM 与限升标记，仅作证据展示。

## 测试与实证

- `go vet ./...`、`go test ./...` 33 包全绿（含 `CT_MYSQL_TEST_DSN` 真库集成）。
- `pnpm typecheck`、`pnpm build` 通过。
- 烟测栈：新二进制启动套用 064；API 往返 PUT `max_rpm=200` → GET 读回、
  负值 400；`continuous-states` 带出 `metric_rpm/metric_tpm/capacity_limited`。
- UI 截图 1366×768 / 1920×1080：面板完整、“已限升”标签与提示可见。

## 部署要点

- 较 rc89 增量：064 迁移（5 列 ADD COLUMN）+ server + 前端；**未动 agent**。
- rc89 不含本批，上线需重打 rc90。
