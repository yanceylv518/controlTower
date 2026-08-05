# 设计：使用日志列表查询选路优化（零 DDL）

日期：2026-08-05。状态：**设计定稿，待开工令**（批次文件待写）。

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

于是先用两次毫秒级查询把时间窗换算成 id 窗（走 `idx_created_at_id`，索引覆盖）：

```sql
SELECT MIN(id) FROM logs WHERE created_at >= ?;   -- lo
SELECT MAX(id) FROM logs WHERE created_at <= ?;   -- hi
```

之后**所有单列索引自动升级为"该维度 + 时间"复合索引**：等值条件走它的索引定位，`id BETWEEN lo AND hi` 夹在主键后缀上收窗口，`ORDER BY id DESC` 与索引内序一致（零 filesort，倒走即时间倒序），其余条件回表过滤。典型改写（时间+模型+类型）：

```sql
SELECT ... FROM logs FORCE INDEX (idx_logs_model_name)
WHERE model_name = ? AND id BETWEEN lo AND hi AND type = ?
ORDER BY id DESC LIMIT 101
```

扫描量 = 该模型在窗口内的行数（冷门模型从最坏情况变最好情况），绝不出窗。

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

`FORCE INDEX` 按选路结果显式给出，防优化器按过期统计信息回退到灾难计划。

**组合完备性**：页面可用的筛选全集 = 时间（必填）× {username, token_name, model_name, group, channel_id, request_id, log_type} 任意子集 × viewer 的 user_id 集合。选路规则是**全序优先级的第一命中**（request_id > viewer-user > username > token > model > group > channel > 仅类型 > 仅时间），因此任意组合都有唯一确定的路径，不存在没规则可走的组合——未被选中的条件一律回表过滤，正确性与选哪个索引无关，选择只影响快慢。几个值得点名的组合：

- **username + model 同时在**：若 `index_username_model_name` 列顺序确认为 (model_name, username)，此组合可走该复合索引两列全等值 + id 窗，比单列更准（开工时按 §7.2 的确认结果决定是否启用这条特化路径，不启用则按优先级走 username 单列，正确性不受影响）；
- **viewer + 其他筛选**：固定走 `idx_user_id_id`（scope 集合通常很小，每用户一段范围最可控），model/token 等条件回表过滤——不按选择性和其他维度竞争，规则简单且防 viewer 场景被误路由到全表级索引；
- **静态优先级判错的代价**：只是多回表（比如某个 token 覆盖了全站流量、比 model 更不选择），不会退回灾难计划。若上线后发现某维度经验序普遍不符实际，可加每维度的粗粒度基数缓存（如 10 分钟一次 `COUNT DISTINCT` 采样）做动态排序——记档为可选演进，首版不做。

## 5. 配套改动

- **游标翻页替代 OFFSET**：下一页以"上页末行 id − 1"为新 hi，沿同一索引路径续走，深翻页与首页同价。前端已是 has_more 模型（rc28），语义顺接；`/logs/count` 精确总数端点不受影响，继续异步补数。
- **列表不拖大字段**：SELECT 去掉 content、other 两个 TEXT（延迟关联或直接砍列）；行展开时按 id 点查新详情端点（返回 content_summary 与 other 解析字段）。列表扫描行宽从 ~KB 级降到几十字节。
- viewer 多用户归并：每用户按页大小取 top-N 后在 Go 层按 id 归并截断——避免 `user_id IN (...) ORDER BY id DESC` 触发跨范围 filesort。

## 6. 边界（如实声明）

本方案把常见组合打到索引级（亚秒），把最坏情况从"无界"压成"窗口内该维度行数的一遍有序扫"。**天花板仍在**：热门维度值 × 整月窗口仍是秒级——那是镜像方案（§8）的活。选路表是静态经验序，不采样统计；错选的代价是多回表，不是灾难计划。

## 7. 验收要点（写批次时展开）

1. 生产 EXPLAIN 两条主路径：①`idx_logs_model_name` + id 窗（确认 key_len 覆盖主键后缀、Extra 无 filesort）；②`idx_created_at_type` 倒序范围扫。**"二级索引上按主键后缀做范围+排序"是本设计的承重墙，EXPLAIN 实证后才算数**；
2. 补齐 `index_username_model_name` 列顺序（完整 SHOW INDEX）；
3. id 窗换算两条查询的耗时与计划（应为索引覆盖毫秒级）；
4. 行为等价：选路各分支与旧查询结果一致（含 viewer scope 强制注入不回退）；游标翻页与 has_more/总数联动；
5. 超时护栏维持现状（120s 共用常量），选路后预期实际耗时大幅下降——若 count 回落路径仍频繁顶满，拆独立超时（rc28 验收记录已记档）。

## 8. 备档：挂起的明细镜像方案（讨论结论，2026-08-05）

若本方案上线后"低选择性 × 大窗口"仍痛，再立项以下方案（要点已议定）：

- CT 自建裁剪明细表：**搭聚合 runner 便车**（runner 本就逐行读日志，顺手批量写镜像，读侧零新增）；content 只存脱敏摘要、other 只抽前端实际使用的字段成窄列；
- **按天 RANGE 分区**，保留期 = 查询上限（31 天），过期 `DROP PARTITION`（瞬时、零删除抖动——吸取 channel_snapshots 无界增长的教训）；
- **混合读按游标缝合**：`id ≤ cursor` 走镜像、`id > cursor` 那一小口（约一分钟窗）直连补头——按 id 缝合零去重零遗漏，用户感知完全实时；runner 间隔可收紧到 10~15s；界面标注"数据截至"水位；
- 覆盖窗口外的老区间回落直连（低频冷路径，功能不减）。
- 前置数据：日均日志行数（定存储预算与保留期）。
