# 运行监控与日志既有改动提交交付

- 目标：按用户指定范围提交并推送四项既有改动：运行监控历史、运行数据默认保留期、日志归档默认月份、Nginx 错误级别过滤。
- 状态：代码整理与验证完成，待提交推送；更新时间：2026-09-15。
- 工作目录：E:\projects\controlTower；隔离目录：.tmp/runtime-log-delivery；分支：codex/runtime-log-delivery；任务 ID：未取得。
- 范围：四项代码及相关测试、本记录、PROJECT_PROGRESS.md。共享 API 仅添加运行指标 offset 参数。
- 范围外：账单列表、电话预警、其他本地改动、发布打包及部署。
- 验收：基于最新 origin/main 整理准确差异，相关 Go/前端回归和类型检查构建通过，推送远端 main，保留原工作区改动。
- 初始化：已读取当前上下文和活跃任务，原工作区 main=87b788ff，存在大量混合改动；最新 origin/main=8d7e6506。Nginx 既有记录标注尚未提交，本次仅承担指定变更的交付。
- 实现：运行历史按实例及固定起止时间逐页读取，解决 6h/24h 仅返回最新 200 条；运行指标和 Docker 状态默认保留 1 天，数据库/环境显式设置仍优先；归档默认选择站点最新有记录月份，无记录回退当月，手动选择保留；Nginx 默认 error/crit/alert/emerg，显式条件避免命中旧低级别结果。
- 验证：隔离检出 9 个 Go 包测试通过（Agent containerlogs、internal containerlog/archivecontrol、Server 主程序/config/settings/dashboard/mysqlstore/httpapi）；14 项前端脚本回归通过；pnpm build 含类型检查通过，仅既有包体积提示；git diff --check 通过。
- 限制：未配置 CT_MYSQL_TEST_DSN，实库集成测试跳过；真实站点/浏览器未验收，未部署。无新增迁移；上线需同步 Server/前端及 Agent 或独立日志读取服务，已删除历史无法恢复。
- 下一步：提交并推送；生产升级与真实环境验收由后续发布安排。
