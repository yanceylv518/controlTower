# 上游账单每日明细下载修复

- 目标：每日入口唯一，下载包含当天全部已登记明细，完整 ZIP 无重名覆盖。
- 状态：代码 34604fa06 已快进推送 origin/main，未发布/部署；更新时间：2026-09-15。
- 任务 ID：01a0a421-0d9e-7230-96e7-d6b7ab628aaf
- 工作目录：E:/projects/controlTower/.tmp/billing-daily-download
- 分支：codex/billing-daily-download，基于 origin/main 92f359f1e。
- 文件范围：账单结果处理器、下载回归、本记录、PROJECT_PROGRESS.md。
- 范围外：计费规则、源明细生成、其他页面改动、发布部署。
- 根因：上游每日按用户存多份文件；列表使用相同日期/名称，下载只返回首份，整包使用重名 ZIP 条目。
- 验收：多用户跨日预览去重；单日 ZIP 包含全部且唯一命名；单文件兼容；缺失文件明确失败；完整 ZIP 不重名；相关 Go 回归。
- 实现：API 按业务日期去重排序；单文件保持 XLSX，多文件下载当天 ZIP；上游 ZIP 成员名附用户 ID，完整 ZIP 同步修正。按天 ZIP 在临时磁盘生成完成后返回，任一源文件缺失即 404，路径越界拒绝，不在内存累积大账单。既有账单无需重生成，Server 升级即可生效，无迁移或 Agent 改动。
- 验证：原处理器运行新增回归失败，复现同日同名列表；修复版专项通过，覆盖跨日/UTC 表示、多用户全部字节保留、完整 ZIP 唯一名称、单文件兼容、缺失文件失败；dashboard、billing、mysqlstore、xlsxwriter 四包 go test 与 dashboard go vet 通过，git diff --check 通过。默认 Go 缓存权限不足，改用工作区 .gocache 后通过。
- 限制：实库集成、真实站点下载及浏览器验收未运行；未收到原始 Excel，未核验其中计费数值。原完整 ZIP 对缺失文件的跳过行为未改，本次新增按天 ZIP 严格检查。
- 2026-09-15 提交：用户授权提交并推送。fetch origin 成功，origin/main 仍 92f359f1e，无新增提交或合并冲突；暂存核对及 diff --check 通过，仅提交三个账单实现/回归文件，提交为 34604fa0。本轮代码未变，沿用上轮四包 Go 测试及 dashboard vet 结果，未重复测试。
- 推送阻塞：自动审批拒绝 git push origin HEAD:main，理由是缺少对具体目标仓库/默认分支的明确授权，且远端尚未被其认定为可信证据验证。目标为 https://github.com/yanceylv518/controlTower.git 的 main；未执行成功、未改用其他途径绕过。
- 推送结果：用户明确确认目的地后，git push origin HEAD:main 成功，远端由 92f359f1e 快进至 34604fa06；上面的审批阻塞已解除。交付文档随单独文档提交推送，未打包/部署。
- 下一步：按用户安排发布及真实站点验收；旧账单无需重新生成，核对差异继续计费。主工作区已有改动保留，本记录同步至隔离目录供交付。
