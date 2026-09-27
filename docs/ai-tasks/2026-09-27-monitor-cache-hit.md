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


## 发布结果

**rc144监控优化远程发布完成（2026-09-27）**：f762febfb4818f425c9aa15528d56ddb83ff4a0f已推送main，v2.0.0-rc144固定同一提交；CI36321082647与release36321085845成功，三Linux附件、SHA256SUMS及GHCR镜像已发布。正式包下载release/v2.0.0-rc144，校验和、ELF架构/执行位、脚本LF、版本与Server错误码接口/前端内容检查通过；Go vet/test、Web typecheck/build及489项前端测试通过。本次仅更新Server/Web，Agent保持现有版本；rc143大记录归档修复尚在独立分支，rc144不包含该修复，发布说明已明确不可替换现有rc143 Agent。未生产部署，真实错误码曲线与源库性能待验收。
