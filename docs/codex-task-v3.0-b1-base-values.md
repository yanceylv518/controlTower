# Codex 任务：v3.0-B1——渠道基础值配置与一键同步

背景：v3.0 连续配权调度第一期（设计见 docs/design-v3.0-continuous-dispatch.md）。本批只建数据层与交互：渠道基础权重/基础优先级由 CT 持有、每模型三态开关、一键同步回填。**引擎不动**（v2.9 行为原样，v3.0-B2 才切换）。**依赖：d563169（B2.8）已合入。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### 数据层（018 迁移）

`channel_base_values(instance_id VARCHAR(64), channel_id BIGINT, model_name VARCHAR(255), base_weight BIGINT NOT NULL, base_priority BIGINT NOT NULL, updated_at DATETIME(6), updated_by VARCHAR(128), PRIMARY KEY(instance_id, channel_id))`——纯增量 CREATE TABLE IF NOT EXISTS，钉 ENGINE/CHARSET，反向断言测试照例。

每模型开关存 policy JSON：`dispatch_modes: map[model]("off"|"observe"|"auto")`，缺省 `"off"`（未配置的模型不受 v3.0 引擎影响——B2 切换后的安全缺省）；DecodePolicyJSON 兼容（无字段=空 map）。

### API（增量）

- `GET /api/dashboard/tuning/base-values?instance_id=&model=`：返回渠道基础值列表（联渠道快照带出名称/当前 newapi 权重与优先级，便于对照展示）；
- `PUT /api/dashboard/tuning/base-values`：批量保存（校验 base_weight ≥0、base_priority ≥0，写审计 tuning.base_update，含 before/after）；
- `POST /api/dashboard/tuning/base-values/sync`：一键同步——body 指定模型列表（多选），server 读渠道快照当前值写入 base 表（**回填即保存**由前端两步操作保证：接口只返回回填数据，不落库；前端填入表单，用户点保存才走 PUT）；
- policy PUT 接受 `dispatch_modes`（校验值域与模型名非空）。

### Web（调权中心新增"基础值"区块）

- 按模型分组表格：渠道名 / newapi 当前权重→基础权重（可编辑）/ 当前优先级→基础优先级（可编辑）/ 该模型三态开关；
- 工具栏：模型多选 + "一键同步权重"、"一键同步优先级"（调 sync 接口回填表单，标黄未保存态，点"保存"才 PUT）；
- 区块说明文案：三态含义（off=不参与 v3.0 调度 / observe=B2 后算而不写 / auto=B2 后自动写入），注明当前引擎仍为 v2.9、开关在 B2 上线后生效。

## 接线点（逐个核对，不得遗漏）

018+反向断言、base 表 store CRUD（mysqlstore+memory store）、三个 API+审计+校验、dispatch_modes schema/兼容/校验、Web 区块（分组表格/同步回填/未保存态/保存）、api-contracts.md、测试同步。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck`；新增测试：base CRUD、sync 只回填不落库、批量 PUT 校验与审计、dispatch_modes 值域与兼容缺省 off。
2. 手工：同步→表单标黄→保存→重载一致；不保存离开无副作用；开关保存后 policy JSON 可见。
3. 回归：引擎行为与 B2.8 版本零变化（本批不接引擎）。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 018 纯增量+反向断言；sync 接口零落库副作用
- [ ] dispatch_modes 缺省 off 且兼容旧 JSON
- [ ] 批量保存有校验与审计；引擎零改动回归通过
- [ ] api-contracts.md 已更新
- [ ] 一个 commit：`feat(server,web): channel base values with sync and per-model dispatch modes (v3.0-B1)`

## 明确不做

引擎切换（B2）；熔断/探针（B3）；值班模型退役清理（随 B2）；基础值历史版本（审计可查即可）。
