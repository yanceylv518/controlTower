# 验收记录：渠道日账单明细 + 账单异常单计数（2026-08-05）

范围：2445cf8（渠道日明细）+ 8cddac5（异常单计数），codex 自主交付，无批次文件。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 全绿。

## 结论：通过，零缺陷

## 2445cf8 渠道日账单明细

- `/billing/channels` 带 channel_id 时增返 `details`（日×模型×分组明细）：rows 在 store 层即按 channelID 过滤（QueryBillingChannelAggregatesForJob 核过），复用 BuildDetails——抽屉不会混入他渠道数据；
- V4 页行点击/“日账单”按钮开抽屉，折扣价按渠道 discount 换算显示；行内按钮补 @click.stop 防误触发抽屉，正确；
- **回退计价 CreateCacheRatio 默认 1→1.25**：对齐 new-api GetCreateCacheRatio 的默认值——与今日生产样本 `cache_creation_ratio:1.25` 互证。回退价为查询时现算不落库，无需重生成账单；
- 计价页新增 `cache_write_price_configured`：标注 newapi 是否显式配置了该模型的 CreateCacheRatio，辅助导入判断（多一次 options 快照读取，admin 低频页可接受）；
- 记档：vite.config 开发代理默认端口 8080→18081——codex 本地环境私货，可用 CT_DEV_API_TARGET 覆盖，不影响构建产物。

## 8cddac5 账单异常单计数

- SQL 按 job 聚合（GROUP BY user/channel/日/模型/分组）；`CONVERT_TZ(created_at,'+00:00','+08:00')` 取上海日——写入侧 `time.Unix` 在 UTC 容器下存 UTC 已核，偏移量写法不依赖时区表且上海无夏令时，数值正确（风格上与 BusinessLocation 约定不一致，P3 记档，与既有 time.Local 四处残留同类）；
- 用户 summary / 渠道 summary / 明细三处接线 + CSV 均加"异常订单数"列；明细多档位行只标一次（有测试）；过滤条件运算符优先级核过（&& 高于 ||，语义正确）；
- **合计行改用 SummarizeUsers(items) 重算**：求和字段与原内联合计一致（含 Quota），Amount 从"精确和后取整"变"逐行取整后求和"——合计从此与页面各行严格对得上（改善），与旧值差 ≤ 每行 5e-7，记档；
- 缓存 Put 在计数并入之后，异常单按 job 不可变、jobID 空守卫在——缓存一致性正确；明细只在 job complete 时并入计数；
- P3 记档：计数查询失败会 500 整个 summary/detail 响应——异常计数属附属信息，可降级为不显示而非阻塞主表，后续顺手改。

## 部署

纯 server + 前端，无迁移、无新配置；仍属 rc29 待打范围（rc28 之后累计：缓存语义 c998218+守卫 3d2c172+样本测试 232bf52+本批两笔）。
