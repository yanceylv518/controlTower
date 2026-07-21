# 验收记录：客户维度监控页 + OTPS 指标（2026-07-21）

**范围**：上次验收（`7a748af`）之后未经评审的四个功能提交：

| commit | 内容 |
| --- | --- |
| `9e38ac1` | 总览页等待实例初始化，修复首刷竞态 |
| `c57c7c8` | 监控列表 instance_id 下推 + latest 按实例查询 + 实例初始化请求去重 |
| `2d9fe01` | 总览突出 TPM 与成功率趋势（纯前端，复用已有 history） |
| `05775c3` | 客户监控独立页、渠道/模型页图表化改版、OTPS 指标全链路（013 迁移） |

（07-17 的 `bbbe146`/`5a10d9a`/`313ef21`/`4507b4e` 已有当日 devlog 记录，属上一轮工作，不在本次范围。）

## 验证结论：测试面全绿，无 P1 阻断

- Go 1.24.5（Linux）`go build ./...` + `go test ./...` 31 包全部通过；
- 前端 `vue-tsc --noEmit` 通过，生产构建通过（1.7MB 单 chunk 为既有状态，非本批引入）；
- gofmt 检查：本批改动文件全部合规（另有 9 个历史文件不合规，与本批无关）。

### 核对通过的关键点

- **013 迁移幂等**：两条 `ALTER TABLE ... ADD COLUMN`，重放时以 1060 被 ApplySQL 容忍、快速失败，无 010 式全表重建地雷；`otps_migration_test.go` 断言 additive 形态。
- **OTPS 语义正确且"准确"成立**：Agent 侧仅对 `IsStream && FirstResponseMs != nil && LogType=="consume" && CompletionTokens>0 && generation>0` 的事件累计 `(输出token, 生成秒数)` 两个可加累加器；MergeMetric/rollup/SQL merge 全部线性求和；读侧 `otps()` 在分母/分子非正时返回 null（前端显示 —）。跨窗口聚合出的 OTPS 是真实的 token 加权值，不是分桶平均的平均。存量 5m 行默认 0 → null，不产生假数据。
- **契约只增不改**：Agent payload 新增两字段带 `omitempty`；Dashboard `MetricItem` 新增 `otps`/`otps_sample_tokens`。旧 Agent→新 Server 视为 0；新 Agent→旧 Server 忽略字段，双向兼容。metricArgs 73、batch args 146、占位符 33 三处对齐且有测试锁定。
- **`c57c7c8` 的按实例 latest 查询**与 008 索引 `(dimension_type, instance_id, dimension_key, bucket_time)` 前缀完全匹配，带 SQL 结构回归测试；`loadInstances` 单飞去重实现正确（并发首刷只发一次实例+设置请求）。
- **前端前缀拼接**（`inst:user:` / `inst:channel:` / `inst:model:`）与聚合器维度 key 格式一致，`dimension_type` 等值过滤挡住了 `instance_user_model` 等同前缀维度的串数据。

## 待处理发现（不阻断合入，打 tag 前应拍板）

1. **P2 · 路由重复注册**：`router.ts` 中 `/customers` 注册了两次（新 `CustomerMonitorView` 与旧 `DimensionView kind=customers`），仅靠注册顺序令新页面生效。旧行成死代码，DimensionView 内 customers 分支（title 映射、prefix 回退）同为死代码。应删除旧路由行并清理死分支，避免后续改动被影子路由坑到。
2. **P2 · 前缀历史查询未下推 instance_id**：三个监控页改为每次加载（且每 30s 自动刷新）对每实例发 2 次 `QueryMetricHistoryPrefix`，SQL 仅有 `dimension_type = ? AND dimension_key LIKE ? AND bucket_time >= ?`。008 索引的 `instance_id` 夹在中间，前缀 LIKE 无法走满索引，依赖 MySQL skip-scan 或 001 的 bucket_time 索引兜底。当前 2 实例规模大概率无恙，但这与"latest 10s""CPU 99%"是同族形状问题，且 handler 已收到 `instance_id` 参数。建议补 `AND instance_id = ?`（一行 SQL + 一个参数），部署后 EXPLAIN 实证。
3. **P2 · 渠道/模型列表可见性收窄（需用户确认是否有意）**：列表数据源由 latest（24h 活跃视野）改为所选时窗聚合（默认 1 小时）。超时窗无流量的渠道——尤其是被禁用的渠道——从页面完全消失，"无流量/已禁用"筛选签与计数随之失真，v2.3-B2"渠道清晰化"的禁用折叠/健康墙行为被实质改变。若非有意决策，需为渠道页保留快照兜底行（无指标也列出，标记无流量）。
4. **P3 · 排名页模板内 O(N×M) 计算**：峰值 TPM 与 sparkline 每行渲染都对全量 history 数组 filter（客户页每行还双次 `pointsFor`）。客户/渠道数上百、24h 窗口时排名页会卡；建议按 dimension_key 预建 Map。
5. **P3 · OTPS 精度备忘**：`use_time` 为整秒粒度，短请求的生成时长分母误差偏大（已有 `generation>0` 门槛挡住非正值）。记档即可，无需行动。

## 结论

四个提交**验收通过、保留在 main**；发现项 1/2 建议随下批小修一并处理，发现项 3 需用户拍板后决定是否补快照兜底。处理完毕再打新 tag 部署（013 迁移随 Server 启动自动应用，先 Server 后 Agent 的既有顺序不变）。
