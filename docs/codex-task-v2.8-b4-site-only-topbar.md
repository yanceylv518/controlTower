# Codex 任务：v2.8-B4——顶栏站点化收尾（退役全局实例上下文）

背景：v2.8-B3 验收后用户拍板——顶栏下拉不应再是实例列表，应是站点列表；全局"当前实例"上下文整体退役，实例选择只在业务上真正需要实例的页面内部出现。同时并入 B3 验收记档的三处修复（docs/review-v2.8-multi-site-2026-07-27.md）。**依赖：B1~B3 已合入（e091b9a 后）。纯 web + 一处 server 文案，无迁移。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### 顶栏（AppShell.vue）

- 删除 `<InstanceSelect />`，只留 `<SiteSelect />`；`components/InstanceSelect.vue` 随之成死文件，**一并删除**（勿留影子）。
- filters store：`instance_id` 不再作为全局上下文被顶栏写入；仍被延时分诊用作页内状态的，改为该页局部 state（见下），store 里彻底移除 `instance_id` 与 `CT_DEFAULT_INSTANCE_ID` 持久化（搜全仓确认无残余消费方后再删，别删出运行时错误）。

### 各消费方改造（六处，逐个核对）

1. **AlertsView（告警中心）**：拉取不传 instance_id、limit 放大（200 已是现值则维持），客户端按当前站点成员实例集合过滤；筛选器区域可加"全部站点"选项保留全局排障视角（默认=当前站点）。
2. **AuditsView（操作审计）**：同 1，客户端按站点成员过滤，默认当前站点。
3. **UsageView(用量)**：业务数据=站点级——按当前站点成员扇出请求并合并（同 DimensionView 模式），切站点触发刷新。
4. **RuntimeView（系统状态）**：只显示当前站点的分组（与总览"一次只看一个站点"一致；单站点时等价现状）；查询仍全量拉、客户端滤即可。
5. **LatencyView（延时分诊）**：加**页内**实例选择器，候选=当前站点成员；沿用现有"自动选在线实例"逻辑（loaded 后无选中则选在线优先），切站点时重置并重选；跳转链接的 key 拼接改用页内选中实例。
6. **ChannelOperations（渠道操作）**：目标实例默认=当前站点成员**字典序第一个**（`<站点>_1` 命名约定即采集/控制机），表单内保留实例下拉（候选限当前站点）可改；下发逻辑不变。

### 并入的三处修复

7. **总览告警小组件**（OverviewView.vue:128 一带）：拉取放大 limit（5→50）后按当前站点成员过滤、取前 5；alerts 接口不加参数（维持 B3 边界）。
8. **DimensionDetailView**（:90 一带）：过滤条件从 `filters.instance_id` 改为 key 内嵌的 `instancePart`——详情页天然被 key 锁定实例，与全局上下文彻底解耦；"上一个/下一个"、快速切换下拉随之在双站点下恢复正常。
9. **离线告警文案 display_name**（server/internal/dashboard/alert_handler.go appendInstanceOfflineAlerts）：节点名 `instance.Name` 非空用 Name、空回退 ID；站点名不变。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck` 无错误；alert 文案单测更新（Name 优先）。
2. 手工（单站点环境）：顶栏无实例下拉且无站点下拉（单站点隐藏）；总览/告警/审计/用量/系统状态/延时分诊全部与改前等价；渠道操作可正常下发。
3. 手工（造双站点）：切站点后六个页面数据全部随之切换；延时分诊页内选择器只列本站实例；详情页从任一站点钻入，上一个/下一个与快速切换正常。
4. 全仓 grep 确认 `filters.instance_id`、`InstanceSelect`、`CT_DEFAULT_INSTANCE_ID` 零残余。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 顶栏只剩 SiteSelect；InstanceSelect.vue 已删除，无死文件
- [ ] 六个消费方逐一改造完毕，全仓无 filters.instance_id 残余
- [ ] 三处并入修复完成，alert 文案测试更新
- [ ] 单站点环境行为与改前等价（回归）
- [ ] 一个 commit：`feat(web): retire global instance context in favor of site context (v2.8-B4)`

## 明确不做

alerts/audits/usage 接口加 site 参数（客户端过滤够用，接口边界维持 B3 决策）；渠道控制"哪台是控制机"的显式建模（依赖 `_1` 命名约定，出问题再议）；LatencyView 站点级聚合（nginx 数据按机器走是设计语义）。
