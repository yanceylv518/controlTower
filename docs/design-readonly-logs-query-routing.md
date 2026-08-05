# 设计：使用日志列表查询选路优化（零 DDL）

日期：2026-08-05。状态：**设计评审中，待下文阻塞问题闭环后开工**（批次文件待写）。

## 1. 问题与约束

使用日志页（/passthrough/logs 直连 newapi logs 表）带筛选的列表查询慢，典型慢组合 = 时间区间 + 模型 + 类型。约束（用户拍板，2026-08-05）：

- **不动 logs 表**：高频写入表，不加索引、不做 DDL——表上已有 12+ 个二级索引，写放大已经很重，"按组合加索引"不可枚举也不可持续；
- 明细镜像方案（CT 自建裁剪明细表 + 按天分区 + 混合读缝合）**挂起**：本方案上线后若仍有痛点（如热门模型 × 整月这类"低选择性 × 大窗口"组合）再立项，讨论结论已在本文 §8 备档。

慢的根因：`WHERE created_at BETWEEN ? AND ? AND <等值条件> ORDER BY id DESC LIMIT ? OFFSET ?` 这种写法下，优化器常选"主键倒扫赌凑满 LIMIT"——筛选越准命中越稀疏，扫得越多（可能扫出时间窗直到全表穷尽）；或走时间索引圈进整窗行数再 filesort；OFFSET 深翻页再叠加一层浪费。

## 2. 生产索引清单（2026-08-05 用户提供 SHOW INDEX）

- `PRIMARY` (id)
- `idx_created_at_id` (created_at, id)、`idx_created_at_type` (created_at, type)
- `idx_user_id_id` (user_id, id)、`idx_logs_user_id` (user_id)
- 单列：`idx_logs_model_name`、`idx_logs_token_name`、`idx_logs_username`、`idx_logs_group`、`idx_logs_channel_id`、`idx_logs_ip`、`idx_logs_request_id`、`idx_logs_token_id`、`idx_logs_upstream_request_id`
- `index_username_model_name`（复合，**列顺序待确认**——贴的输出缺 Column_name 列；按 one-api 源码 gorm 标签推测为 (model_name, username)，开工时以 `SHOW INDEX` 完整输出为准）

结论：**每个筛选维度都已有索引**，问题纯粹是查询形态让索引用不上。

## 3. 核心机制：时间窗转 id 窗 + 二级索引的主键后缀

两个事实的组合：

1. logs 的 id 与 created_at 高度同序（自增 + 按时写入）；
2. InnoDB 二级索引叶子末尾都带主键——`idx_logs_model_name` 实际是 (model_name, **id**)，同一 model 内按 id 有序。

原方案计划先用两次查询把时间窗换算成 id 窗（走 `idx_created_at_id`，索引覆盖）：

```sql
SELECT MIN(id) FROM logs WHERE created_at >= ?;   -- lo
SELECT MAX(id) FROM logs WHERE created_at <= ?;   -- hi
```

这里有两个必须先验证的问题：

1. `idx_created_at_id` 的顺序是 `(created_at, id)`。当 `created_at` 是范围条件时，`MIN(id)` / `MAX(id)` 不一定能直接取索引端点，可能仍要扫描整个时间范围；尤其 `created_at <= end` 可能覆盖建表以来的大量记录。因此目前不能把这两条 SQL 视为“毫秒级”，必须先用生产数据执行 `EXPLAIN ANALYZE`。
2. `id` 与 `created_at` 只是高度同序，不是数据库约束。并发事务、延迟提交或补写历史日志都可能造成局部乱序。id 窗只能作为候选集裁剪，不能替代精确的 `created_at` 条件。

如果生产验证确认 id 边界获取成本可接受，之后可利用 InnoDB 二级索引携带的主键后缀，让等值条件走对应索引，再用 id 窗缩小候选范围。典型改写（时间+模型+类型）必须保留真实时间条件：

```sql
SELECT ... FROM logs FORCE INDEX (idx_logs_model_name)
WHERE model_name = ?
  AND id BETWEEN lo AND hi
  AND created_at >= ? AND created_at < ?
  AND type = ?
ORDER BY id DESC LIMIT 101
```

保留 `created_at` 后结果边界与旧接口等价；id 窗仅负责减少扫描候选。若边界 SQL 本身需要大范围扫描，或无法证明生成的 id 窗包含全部有效记录，则这条路径不能直接上线。

可作为对照基线先验证更简单且严格正确的查询形态：

```sql
SELECT ... FROM logs FORCE INDEX (idx_created_at_id)
WHERE created_at >= ? AND created_at < ?
  AND model_name = ? AND type = ?
ORDER BY created_at DESC, id DESC
LIMIT 101 OFFSET ?
```

这条基线不解决深 OFFSET，也可能在“冷门模型 × 大时间窗”下扫描较多时间索引记录，但不依赖 id/时间严格同序，且不改变现有分页契约。生产 `EXPLAIN ANALYZE` 应同时对比旧 SQL、该基线和 id 窗选路 SQL，再决定首版实现范围。

## 4. 应用层选路表（静态优先级，不赌优化器）

| 条件组合 | 索引 | 说明 |
|---|---|---|
| 带 request_id | `idx_logs_request_id` | 点查，无需 id 窗 |
| 带 username | `idx_logs_username` + id 窗 | 经验选择性：username≈token > model > group |
| 带 token_name | `idx_logs_token_name` + id 窗 | |
| 带 model_name | `idx_logs_model_name` + id 窗 | 本案高频慢组合的主路径 |
| 带 group | `idx_logs_group` + id 窗 | |
| 带 channel_id | `idx_logs_channel_id` + id 窗 | |
| viewer（必带 user_id 集合） | `idx_user_id_id` | 每用户一段 (user_id, id窗) 范围，应用层归并取页 |
| 多个等值条件同时在 | 按上表优先级取**一个**索引，其余回表过滤 | |
| 只有时间+类型 | `idx_created_at_type` 倒序范围扫 | type 在索引内过滤 |
| 只有时间 | 主键 id 窗倒扫 | |

`FORCE INDEX` 可按选路结果显式给出，防优化器按过期统计信息回退到灾难计划。但索引名称来自外部 newapi 数据库，不同站点或版本可能不同；开工前必须决定是启动时探测索引能力并缓存，还是在缺少目标索引时自动回落到时间索引路径，不能让硬编码索引名导致接口直接 502。

另外，“二级索引隐式携带主键”不等于所有 MySQL 版本与配置都会把隐式主键后缀用于本查询的范围和排序。需要确认 `optimizer_switch` 中 `use_index_extensions` 的实际状态，并以生产 `EXPLAIN ANALYZE` 的 `key`、扫描行数和 `Using filesort` 为准。

**组合完备性**：页面可用的筛选全集 = 时间（必填）× {username, token_name, model_name, group, channel_id, request_id, log_type} 任意子集 × viewer 的 user_id 集合。选路规则是**全序优先级的第一命中**（request_id > viewer-user > username > token > model > group > channel > 仅类型 > 仅时间），因此任意组合都有唯一确定的路径，不存在没规则可走的组合——未被选中的条件一律回表过滤，正确性与选哪个索引无关，选择只影响快慢。几个值得点名的组合：

- **username + model 同时在**：若 `index_username_model_name` 列顺序确认为 (model_name, username)，此组合可走该复合索引两列全等值 + id 窗，比单列更准（开工时按 §7.2 的确认结果决定是否启用这条特化路径，不启用则按优先级走 username 单列，正确性不受影响）；
- **viewer + 其他筛选**：固定走 `idx_user_id_id`（scope 集合通常很小，每用户一段范围最可控），model/token 等条件回表过滤——不按选择性和其他维度竞争，规则简单且防 viewer 场景被误路由到全表级索引；
- **静态优先级判错的代价**：只是多回表（比如某个 token 覆盖了全站流量、比 model 更不选择），不会退回灾难计划。若上线后发现某维度经验序普遍不符实际，可加每维度的粗粒度基数缓存（如 10 分钟一次 `COUNT DISTINCT` 采样）做动态排序——记档为可选演进，首版不做。

## 5. 配套改动

- **游标翻页不能直接替代现有 OFFSET**：当前前端仍通过 `offset = (page - 1) * limit` 支持点击任意页码；`has_more` 只是总数请求未完成时的兜底信息，不代表已经采用游标协议。改为游标后只能可靠地顺序上一页/下一页，无法无成本跳到任意页。是否接受交互变化需要产品确认；首版若必须保留页码，应继续使用 OFFSET，并把游标分页拆成独立改造。
- **列表大字段需要先梳理依赖再裁剪**：当前页面直接从 `other` 解析 TTFT、缓存 Token、倍率、流式状态等展示数据，`content` 用于生成 `content_summary`。不能只从 SELECT 删除这两列。若采用详情懒加载，必须先提供按 id 查询的详情端点，并明确列表首屏仍需返回哪些由 `other` 派生的字段，避免功能回退或展开时产生未受控的 N+1 请求。
- viewer 多用户归并：每用户按页大小取 top-N 后在 Go 层按 id 归并截断——避免 `user_id IN (...) ORDER BY id DESC` 触发跨范围 filesort。

## 6. 边界（如实声明）

若 id 窗边界成本、索引扩展能力和执行计划均通过生产验证，本方案有望把常见组合压成“窗口内该维度候选行的一遍有序扫”。当前不能预先承诺亚秒。**天花板仍在**：热门维度值 × 整月窗口仍可能是秒级甚至超时——那是镜像方案（§8）的适用场景。选路表是静态经验序，不采样统计；错选可能造成大量回表，仍应通过超时、慢查询指标和回落路径保护，不能假设一定不会出现灾难计划。

## 7. 验收要点（写批次时展开）

1. 生产 `EXPLAIN ANALYZE` 三条路径：①旧 SQL；②`idx_created_at_id` + `ORDER BY created_at DESC,id DESC` 基线；③`idx_logs_model_name` + id 窗。记录实际扫描行数、回表数、排序方式和总耗时。**“二级索引上按主键后缀做范围+排序”是本设计的承重墙，实证后才算数**；
2. 补齐 `index_username_model_name` 列顺序（完整 SHOW INDEX）；
3. 单独测量 id 窗换算两条查询的耗时、扫描行数与计划；若不是稳定小成本，禁止每次列表请求同步执行；
4. 构造并发写入、延迟提交和补写历史时间的测试数据，证明保留 `created_at` 后行为与旧查询一致，并验证 id 窗不会遗漏有效记录；
5. 行为等价：选路各分支与旧查询结果一致（含 viewer scope 强制注入不回退）；
6. 分页契约明确二选一：保留页码 + OFFSET，或改成游标 + 仅顺序翻页。若改游标，前后端接口、返回上一页能力和筛选重置行为必须一起验收；
7. 删除 `content` / `other` 前，为页面所有派生字段建立契约测试，并验证详情端点不会产生批量 N+1；
8. 缺少某个硬编码索引、关闭 `use_index_extensions` 或执行计划不满足预期时，接口必须回落到严格正确的时间索引路径；
9. 超时护栏维持现状（120s 共用常量），选路后预期实际耗时下降——若 count 回落路径仍频繁顶满，拆独立超时（rc28 验收记录已记档）。

## 8. 备档：挂起的明细镜像方案（讨论结论，2026-08-05）

若本方案上线后"低选择性 × 大窗口"仍痛，再立项以下方案（要点已议定）：

- CT 自建裁剪明细表：**搭聚合 runner 便车**（runner 本就逐行读日志，顺手批量写镜像，读侧零新增）；content 只存脱敏摘要、other 只抽前端实际使用的字段成窄列；
- **按天 RANGE 分区**，保留期 = 查询上限（31 天），过期 `DROP PARTITION`（瞬时、零删除抖动——吸取 channel_snapshots 无界增长的教训）；
- **混合读按游标缝合**：`id ≤ cursor` 走镜像、`id > cursor` 那一小口（约一分钟窗）直连补头——按 id 缝合零去重零遗漏，用户感知完全实时；runner 间隔可收紧到 10~15s；界面标注"数据截至"水位；
- 覆盖窗口外的老区间回落直连（低频冷路径，功能不减）。
- 前置数据：日均日志行数（定存储预算与保留期）。

## 9. 评审问题与建议实施顺序（2026-08-05）

以下问题闭环前，本文状态保持“设计评审中”：

1. 生产数据上的 `MIN(id)` / `MAX(id)` 边界查询实际扫描多少行、耗时多少？是否会把原来的一条慢查询变成两条边界慢查询加一条列表查询？
2. newapi 是否对 `id` 与 `created_at` 的严格单调关系有可依赖的约束？如果没有，如何证明 id 窗不会漏掉并发乱序或补写数据？无论答案如何，最终列表 SQL 都必须保留 `created_at` 精确条件。
3. 各站点的索引名称、列顺序、MySQL 版本及 `use_index_extensions` 是否一致？索引能力探测和安全回落由哪一层负责？
4. 产品是否接受从“可点击任意页码”改为“仅上一页/下一页”的游标交互？如果不接受，游标分页不进入首版。
5. `other` 与 `content` 的首屏字段依赖如何拆分？详情接口返回什么、何时请求、失败时如何降级？

建议分两阶段推进：

- **阶段 A（低风险基线）**：仅将时间条件改为半开区间，排序改为 `created_at DESC,id DESC`，显式验证 `idx_created_at_id`，保留现有页码和返回字段；先取得生产耗时基线。
- **阶段 B（验证后增强）**：只有当阶段 A 对“冷门维度 × 大窗口”仍不够快，并且上述 id 窗承重假设通过生产实证时，再加入应用层选路；游标分页和详情懒加载分别作为独立变更验收，避免一次修改同时改变 SQL 计划、接口契约和页面交互。
