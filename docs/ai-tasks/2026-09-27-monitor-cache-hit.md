# 监控缓存命中率

- 标识：2026-09-27-monitor-cache-hit
- 更新时间：2026-09-27T20:31:40.965720+08:00
- 状态：本地完成，待发布验收
- 目标与验收条件：各监控页面OTPS后增加缓存命中率。
- 负责会话、分支和范围：当前会话，main；DimensionView、CustomerMonitorView、DimensionDetailView及CustomerCompareChart。
- 实际结果与关键决策：**监控缓存命中率入口（2026-09-27，本地未发布）**：渠道/模型/客户监控在OTPS后新增缓存命中率切换及趋势图，复用既有cache_hit_rate（Server映射CacheTokenRate）与已加载时序；空值保留，百分比轴0–100%。共享详情页OTPS后补数值。仅Web修改，无Agent/Server协议变化；typecheck/build及36项相关回归通过，未真实浏览器/生产验收，待提交发布。
- 验证：pnpm --dir webapp typecheck/build通过；customerTooltip/customerTraffic/customerTrafficRefresh/monitorBuckets共36项通过，日志local/monitor-cache-tests.log；保留既有大chunk提示。
- 未验证与阻塞：未浏览器交互或生产数据验收；无阻塞。
- 下一步：用户确认交付后提交发布，仅需Server/Web包更新。
- 证据：server/internal/dashboard/metric_handler.go中CacheHitRate与CacheTokenRate同源；复用接口聚合比例，不在前端平均百分比。
