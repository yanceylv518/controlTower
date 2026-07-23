# Codex 任务：v2.8-B3——站点视图（总览切换器 + 维度页站点筛选）

背景：站点显式化第二期（见 docs/design-v2.8-multi-site.md 第 3/4 节）。用户拍板：**总览一次只看一个站点**（切换器，非分块）。**依赖：v2.8-B2 已合入。可能与在途 web 批次撞文件，冲突 rebase 手工合并。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试。**

## 设计

### API（Dashboard API v1 只增不改——一律增量可选参数，缺省行为与现版本完全一致）

- `GET /api/dashboard/overview`、`/metrics`、`/metric-history` 增加可选 `site` 参数；
- server 端展开：`site` → 该站点成员实例 ID 集合（用 B2 的 siteOf 回退语义，从 instances 表算），按集合过滤指标。落点：
  - metric_handler 的 `filterMetricItems` 从单 instanceID 扩展为集合匹配（现签名已是变长参数，注意兼容既有调用）；
  - `QueryMetricHistoryPrefix` 的 instance_id 下推从单值扩展为 `IN (...)`（沿用 008 idx_metric_1m_dim_bucket，EXPLAIN 验证不退化；空集合=站点无实例时返回空结果而非全量）；
  - `Latest1m/5mMetricsForInstance` 同理扩展或在 handler 层聚合，取改动小者；
  - overview.go 汇总前按集合过滤。
- `site` 与 `instance_id` 同时传时 `instance_id` 优先（更具体的过滤赢），写进 api-contracts.md。

### Web

- **全局站点切换器**：顶栏组件，选项=全部实例经 siteOf 归并后的站点列表；选择存 localStorage，默认取列表第一个；**只有一个站点时隐藏切换器**（当前生产升级后视觉零变化）。
- 总览、渠道/模型/客户维度页、样本/日志页的请求统一带 `site` 参数（走现有请求层，别每页各拼一遍）。
- **实例维度页语义修正**：业务指标区加"全站汇总（挂采集实例名下）"说明标签——分实例业务量在数据源中不存在（logs 表无实例字段），不得假装能展示；机器面内容（如有）不受影响。
- 延迟分诊页暂不接站点参数（nginx 数据天然按实例走，且 nginx 去留待定——见设计文档第 6 节）。

## 接线点（逐个核对，不得遗漏）

三个 API 的 site 参数与优先级、集合下推 SQL + EXPLAIN、空站点空结果、切换器组件与 localStorage、各页请求接线、实例维度页标签、api-contracts.md 增量条目。

## 验证要求

1. 全量测试绿；site 展开/优先级/空集合单测；`QueryMetricHistoryPrefix` IN 版本 EXPLAIN 走 idx_metric_1m_dim_bucket。
2. 手工：不传 site → 各接口响应与现版本一致（回归）；单站点环境切换器隐藏、页面与升级前等价；造两个站点（改 site_id）→ 切换器出现，总览/维度页数据随切换正确隔离。
3. 交付说明含 api-contracts.md 的新参数条目。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 缺省 site 时三接口响应与旧版逐字段一致（回归测试）
- [ ] IN 下推 EXPLAIN 不退化；空站点返回空而非全量
- [ ] 单站点时切换器隐藏，升级前后页面等价
- [ ] site/instance_id 优先级有测试并写入 api-contracts.md
- [ ] 一个 commit：`feat(server,web): per-site overview and dimension filtering (v2.8-B3)`

## 明确不做

分实例业务量展示（数据源天花板）；延迟分诊页站点化（nginx 去留定了再议）；告警规则按站点分组聚合（文案已在 B2 带站点，规则粒度不变）；总览多站点分块布局（用户已拍板切换器）。
