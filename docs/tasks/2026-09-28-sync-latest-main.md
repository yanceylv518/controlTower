# 同步最新主线

按用户要求先更新代码。fetch 后确认 origin/main 已从本地旧基线 d610eded5 前进至 e374be79c，共 17 个提交；其中 f762febfb 已发布缓存命中率、错误码趋势，4ba787936 修复错误图布局。此前回退使用旧 HEAD，未包含这些远程变更；当前同步纠正这一基线问题。

更新前用 `codex-before-update-20260928` stash 保存全部已跟踪修改和未跟踪文件，快进 main 后恢复；保留 stash，不删除主副图 ZIP 备份。进度文档保留双方记录，权限保留两个错误统计端点，流量面板合并 hideMode 与常驻勾选，DimensionView 保留远程模型双维、刷新与取消逻辑以及本地弹窗对象隔离。未恢复主副图布局。

验证：492 项前端回归、vue-tsc/生产构建通过（既有 chunk 提示）；Go auth/httpapi/server 命令包测试通过；无未解决冲突，diff 检查通过。浏览器刷新本地 5200 渠道页，确认 TPM/TTFT/OTPS/缓存命中率入口及流量分层可见；继续使用既有远程后端。

本次未部署或执行数据库迁移。原有本地错误统计开发保留，包含 `097_error_statistics.sql`；远程另有 `097_archive_read_connections.sql`，后续打包本地错误统计前应核对迁移编号和升级路径，本次不据此修改已发布迁移。未进行完整 Go/实库/生产验收。当前 main 与本次 fetch 的 origin/main 均为 e374be79c，本地修改未提交推送。
