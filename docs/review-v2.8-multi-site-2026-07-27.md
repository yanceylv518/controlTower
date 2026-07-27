# 验收记录：v2.8 多站点系列 B1/B2/B3（2026-07-27）

范围：397ac3a（B1 agent 采集开关）、b602c73（B2 站点数据层）、e091b9a（B3 站点视图）。验收环境：Linux，Go 1.24.5，Node 22（pnpm typecheck）。

## 结论：三批次全部通过，1 个 P2 待拍板清理，4 个 P3/记档

- `go vet ./...` + `go test ./...` 全绿（31 包）；`pnpm typecheck`（vue-tsc）无错误。
- B1 按规格：默认 true 零感知；三条 fail-fast 校验齐全且有矩阵测试（standalone/企微/显式快照）；快照仅在**未显式配置**时自动降级；关闭态 OpenMySQL 只在 LogCollectEnabled 分支内，全程零共享库连接；preflight 关闭态 mysql 项输出 skip。
- B2 按规格：014 纯增量 ADD COLUMN + 反向断言测试（禁 CREATE/DROP/RENAME/CHANGE/MODIFY）；siteOf 回退语义 server（dashboard/site.go）与 web（shared/site.ts）各一处且有测试；site_id 校验、List/Create/Update 接线完整；系统状态页站点分组；离线告警文案带站点。实例 ID 正则放宽 `[a-z0-9-]`→`[a-z0-9_-]`（命名约定 `<站点>_<序号>` 需要，属必要改动）。
- B3 按规格：overview/metrics/metric-history 增量 `site` 参数（log-samples 也接了，超规格但合理——server 端按实例扇出合并排序）；instance_id 优先级、空站点返回空均有测试；前缀历史走新 IN 查询（谓词形状与 008 索引一致），memory store 同步实现保持契约一致；SiteSelect 单站点隐藏、localStorage 记忆；api-contracts.md 已更新。维度页站点筛选实现为 web 端按站点成员扇出（dimension_key 内嵌实例 ID 所致），可接受。

## 待处理

- **P2（待拍板）**：`webapp/packages/desktop/src/views/InstancesView.vue` 成死文件——router 已改引 `InstancesSiteView.vue`（借旧名导入），旧文件零引用。与 07-21 验收清理的 /customers 影子路由同类，建议删除。
- **P3**：总览页告警小组件仍传 `filters.instance_id`（OverviewView.vue:128），未随站点切换——站点视图下可见他站告警；alerts 接口本无 site 参数（B3 明确不做告警站点化），但总览内的展示不一致，记档待议。
- **P3**：DimensionDetailView 仍按 `filters.instance_id` 过滤（:90）——顶栏实例选择器与站点选择器并存，从站点视图钻入详情时若实例选择器指向非采集实例，详情可能为空。混合过滤模型记档待议。
- **P3**：离线告警文案用 instance.ID，规格要求"有 display_name 用 display_name"（instances.name），未实现。
- **记档**："实例维度页业务区加全站汇总标注"一项无落点——不存在独立实例维度页（实例维度仅总览图表），codex 跳过合理，规格假设有误。

## B4 验收（2026-07-27 追加，e2212fa）

结论：**通过，1 个验收修正已由验收方直接修复**。`go vet` + 全量测试 + `pnpm typecheck` 全绿；全仓 `filters.instance_id`/`InstanceSelect`/`CT_DEFAULT_INSTANCE_ID` 前端零残余；六消费方 + 三修复逐项核对通过。客户端按 `item.site_id` 直比是安全的——B2 的 List 接口在 server 侧已用 siteOf 预解析，site_id 永不为空。

- **验收修正（已修）**：SettingsView 移除"默认实例"设置区后，server settings 注册表仍下发 `CT_DEFAULT_INSTANCE_ID`，该键落入 `Number()` 分支——空值变 0、旧存值变 NaN，每次保存设置都会把脏值写回 system_settings。修复：从注册表移除该键（const/defaults/Keys/校验），原校验测试替换为"该键不得回归注册表"的守卫测试。库中已存的旧值成为孤儿行，无消费方，无害。
- **记档**：告警中心改为单次拉 200 条客户端分页，超 200 条的旧告警不可见（静默上限；告警清理功能在，实际难触顶）；"全部站点"筛选项未实现（规格标注"可加"，属可选项）；SettingsView 删除默认实例设置区为合理的超范围改动（键已无语义）。

## 部署后必做（本机无 MySQL，无法实证）

1. `EXPLAIN` 验证 `QueryMetricHistoryPrefixForInstances` 的 IN 查询走 `idx_metric_1m_dim_bucket`（1m/5m 两表）。
2. 014 迁移双重启动验证幂等（1060 容忍路径）。
3. 顺序照旧：先 Server（014 自动迁移）后 Agent；B 机 agent 用 B1 开关部署（配置要点见 design-v2.8-multi-site.md 第 6 节）。
