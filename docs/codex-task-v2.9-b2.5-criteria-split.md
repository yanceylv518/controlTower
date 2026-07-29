# Codex 任务：v2.9-B2.5——降级标准与调度参数分离

背景：用户拍板（2026-07-29）——策略里"什么算坏"（质量判断，天然因模型而异）与"怎么运转"（引擎机制，全站一套）性质不同，须在数据模型层分离，为将来按模型放宽/收紧标准预埋结构。**趁 v2.9 未部署改 schema，零迁移成本。依赖：v2.9-B2 已合入（含验收修正 63bf610）。本批结构分离到位、界面保持最简：只有一套"默认标准"，不做多套管理与模型指派界面。**

**文末自查清单粘贴进 commit message（上批漏贴已记违规，勿再犯）；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### 策略 schema 拆分（tuning.Policy → 两实体）

```
SchedulingParams（每站点一套）:
  window_minutes, min_samples, trial_initial_minutes, trial_backoff_factor,
  trial_max_minutes, trial_windows, cooldown_minutes, daily_action_limit
DegradeCriteria（命名实体,可多套;本批恒一套 name="default"）:
  name, error_rate_threshold, severe_threshold,
  latency_multiplier, latency_floor_seconds, sustained_windows
模型指派: assignments map[model]criteria_name（本批恒空=全走 default）
```

- 存储仍在 tuning_policies.policy_json 一个 JSON 里（结构化为 `{scheduling:{...}, criteria:[{name:"default",...}], assignments:{}}`），**不新增表**——多套标准的量级用 JSON 足够，真到需要独立表再说；
- 兼容：GetPolicy 现有"decode 在默认值之上"的模式保留——旧平面 JSON（生产 v2.1 遗留与未部署的 v2.9 平面结构）解不出新结构时整体回退默认值即可（v2.9 从未上过生产，无真实数据需迁移）；写回一律新结构；
- 校验规则不变，按新归属分组报错（字段错误路径带 `scheduling.`/`criteria[default].` 前缀）。

### 引擎（server/internal/tuning）

- `criteriaFor(policy, model)`：查 assignments，无指派回退 name="default" 的标准；判定逻辑改从 criteria 取阈值、机制参数从 scheduling 取——**行为与现版本逐项一致**（本批 assignments 恒空）；
- evidence 快照里补 `criteria_name` 字段（回填与页面可见用了哪套标准——多套时代的审计基础）。

### API 与页面

- policy GET/PUT 直接过渡到新结构（**v2.9 tuning API 未上过生产，无兼容负担**，api-contracts.md 的 v2.9-B2 章节就地更新为新结构示例）；
- 调权中心策略表单拆两个区块：**"降级标准（默认标准）"**（5 项）与**"调度参数"**（8 项），字段与校验一一对应；说明文案注明"标准将来可按模型定义多套，当前全部模型使用默认标准"。

## 接线点（逐个核对，不得遗漏）

Policy 结构与校验分组、GetPolicy 回退路径（旧平面 JSON→默认值）、criteriaFor 与引擎取值改造、evidence 带 criteria_name、handler GET/PUT、表单分区、api-contracts 更新、engine/handler/页面测试同步。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck`；新增测试：旧平面 JSON 回退默认、criteriaFor 无指派回退 default、有指派取指派（结构预埋的行为先测住）、校验错误路径前缀。
2. 行为回归：默认策略下引擎判定/建议与 B2 版本逐项一致（复用既有引擎测试全部不改断言即为证）。
3. 手工：表单两区块保存→重新载入结构正确；旧库若有 v2.1 平面策略行，页面载入显示默认值不报错。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 既有引擎测试断言零修改全部通过（行为等价的硬证据）
- [ ] 旧平面 JSON 回退默认值有测试；写回一律新结构
- [ ] criteriaFor 指派/回退有测试；evidence 含 criteria_name
- [ ] 表单两区块、校验错误路径分组正确
- [ ] 一个 commit：`refactor(server,web): split degrade criteria from scheduling params (v2.9-B2.5)`

## 明确不做

多套标准管理界面与模型指派界面（结构已预埋，需求出现再做）；独立标准表（JSON 内嵌够用）；auto（B3）。
