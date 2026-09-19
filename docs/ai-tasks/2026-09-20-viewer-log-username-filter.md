# 查看账号使用日志用户名筛选回归

- 标识：2026-09-20-viewer-log-username-filter
- 更新时间：2026-09-20 03:26 +08:00，Asia/Shanghai。
- 状态：本地修复及自动检查完成，待真实页面验收。
- 目标与验收条件：查看账号可以在使用日志页输入用户名查询；列表、总数和统计继续受其授权站点及用户 ID 限制。
- 负责会话、分支和修改范围：当前会话，主检出 main，基线 e06bc785；仅 ReadonlyLogsView.vue 的用户名输入框条件及本任务记录/当前总览。保留原有未跟踪文件。
- 实际结果与关键决策：移除用户名输入框的 `v-if="isAdmin"`，恢复 viewer 的原有入口。渠道及其他管理员字段的可见性保持原样。后端无须修改，无新迁移。
- 历史证据：2026-08-05 提交 `4eb6efd4` 已移除用户名输入、请求参数及深链的管理员限制；`229c9036`（2026-09-17，feat(logs): align readonly page with rc35）又给输入框加回 `v-if="isAdmin"`，其父提交没有此条件。这是页面改版回归，非本次新开放权限。历史验收也记载 viewer 用户名筛选已开放。
- 权限核查：`passthroughScope` 对 viewer 使用会话 ScopeSite/ScopeUserIDs；`parseReadonlyLogFilters` 将授权 `user_id IN (...)` 与用户名精确或显式 `%` 通配条件以 AND 组合。列表、统计和总数共用这些条件；用户名不能扩大查询范围。
- 验证：用 Vue 编译实际主筛选模板并 SSR 渲染，修复前 viewer 无用户名框、管理员有；修复后两种角色均有框且保持值绑定，渠道框仍仅管理员可见。`node --test --experimental-test-isolation=none tests/readonlyLogsColumns.test.mjs tests/fallbackRequestChain.test.mjs` 40/40 通过；`go test ./server/internal/auth ./server/internal/dashboard -run 'Test(Viewer|ReadonlyLogFilters|PassthroughAdminScope)' -count=1` 两包通过；在 webapp 执行 `pnpm typecheck`、`pnpm build` 通过，构建保留包体积提示。
- 环境说明：首次常规执行被 Windows 沙箱缓存权限/子进程限制阻止；Node 改为同进程测试执行，类型检查/构建及 Go 定向测试经自动审批后在沙箱外重跑通过。
- 未验证与阻塞：未使用真实 viewer 登录浏览器或连接真实只读 MySQL 验收；未运行 Go 全量 vet/test（无 Go 代码修改）；未提交、未打包发布、未部署线上。无实现阻塞。
- 下一步：安排前端发布后，用绑定多个用户的查看账号核对授权用户名、范围外用户名、清空筛选及列表/总数/统计一致性。
- 相关来源：[页面实现](../../webapp/packages/desktop/src/views/ReadonlyLogsView.vue)、[后端范围与筛选](../../server/internal/dashboard/passthrough_handler.go)、[历史开放记录](../review-readonly-log-rollup-2026-08-05.md)。

## 本地连接远程预览

- 按用户要求从当前主检出启动 Vite，地址 http://127.0.0.1:5192/readonly-logs，后台 PID 7300；仅进程环境 `CT_DEV_API_TARGET=http://124.220.201.99:8080`，未更改持久代理配置。
- 页面及修复后 Vue 模块返回 HTTP 200；代理 `/api/auth/me` 返回 HTTP 401 / unauthorized，确认远程认证接口可达。Codex 浏览器已打开并保留登录页，登录后返回使用日志。
- 待用户使用查看账号登录验收；未读取真实业务数据、未写入远程配置。运行日志与 PID 保存在忽略目录 `local/runtime/viewer-username-preview/`。
- 用户明确本问题不推送 Workspace，本轮沿用该选择。
