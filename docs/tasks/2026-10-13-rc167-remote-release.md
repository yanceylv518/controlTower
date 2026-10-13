# rc167 生命线优化与探针渠道隐藏远程发布

- 日期：2026-10-13（Asia/Shanghai）
- 状态：远程打包发布完成，正式产物核验通过；未部署或重启生产。
- 固定标签：`v2.0.0-rc167`，代码提交 `90ba40d5085465f3cd71eb7f64e5f835d29a7659`。
- 用户授权：远程打包，并明确包含生命线刷新、异常筛选优化及本次手动关闭渠道隐藏。

## 包含关系与功能

发布前逐项 Git 祖先核验包含 rc160～rc166、生命线优化 `69c85608`、按渠道探针选择 `08fbb823` 及隐藏修复 `90ba40d5`。本地 HEAD 与远程 main 均为固定代码提交。未包含本地请求监控等其他未提交功能。

- 生命线刷新：默认手动，可选 30/60/120 秒自动概览；阅读记录及翻页时暂停，记录独立刷新并保留分页，后端复用汇总避免一次重复扫描。见[刷新优化](2026-10-12-lifeline-refresh-performance.md)。
- 生命线筛选：异常分类、渠道排序、点击异常次数进入对应模型及结果的逐条探针。见[异常筛选](2026-10-12-lifeline-filters.md)。
- 探针渠道选择：隐藏未选中的手动关闭/手动接管渠道，已选保留“不探测”标签并可移除；可选数量排除禁选项，自动关闭继续显示，手动重新启用后恢复可选。见[渠道选择交接](../ai-tasks/2026-10-13-routine-probe-channel-selection.md)。

rc166 包含的是生命线原有刷新与事件筛选；本版进一步包含后来补交的刷新和异常筛选优化，不修改旧版本安装包。

## 验证与远程执行

提交前在待发布源码上通过 86 项页面回归（调权 69、生命线 17）、类型检查、生产构建、全项目 Go vet/test；未配置测试 MySQL DSN，依赖实库跳过项不计为本轮实库验证。

- [固定提交 CI 38311669931](https://github.com/yanceylv518/controlTower/actions/runs/38311669931)：成功。
- [远程 release 38311778965](https://github.com/yanceylv518/controlTower/actions/runs/38311778965)：成功。
- [正式发布与下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc167)：三个 Linux 安装包及 SHA256SUMS。

## 升级范围

相对 rc166 无新增迁移、Agent 或 NewAPI 改动，需要更新 Server/Web；标准流程仍生成两种架构的 Agent 包。本次仅远程打包发布，未进行生产安装、迁移、重启或站点策略修改。

## 正式成品核验

三个安装包均通过 SHA256 清单、ELF 架构、执行位、Go 内嵌完整提交与 vcs.modified=false 校验；Agent 版本标识正确，脚本为 LF。Server 包含的 139 个迁移与固定标签逐字节一致。Web 产物检查通过：自动刷新概览及暂停/间隔、异常次数直达探针、隐藏手动关闭/移除旧选择、自动关闭标识及此前渠道选择功能。此核验确认功能已进入安装包，不替代生产环境验收。

- `control-tower-agent-v2.0.0-rc167-linux-amd64.tar.gz`：`502fb5677c6c30363c007404e439e8783910680c9536bd696b57eaddbc45d1ff`。
- `control-tower-agent-v2.0.0-rc167-linux-arm64.tar.gz`：`09373e2c8e6818cdd7f05c7da5d9f69f180fa1dc43571b1be519bc5c9c6a046f`。
- `control-tower-server-v2.0.0-rc167-linux-amd64.tar.gz`：`e65ef5d0bc9bd5857964a038bfbd58c005ed25854fcab4cbfa02466a7a37fb1f`。

GHCR 版本与 latest 摘要均为 `sha256:b7c47aca7cdea50dc73a14a8e99761851f6cf3fd51d801e2f2b70a64fc59e8d3`，确认 linux/amd64，未启动容器。

本地核验报告：`local/releases/v2.0.0-rc167/remote/VALIDATION.json`；复核脚本：`local/verify-rc167.py`。发布事实另提交 main，标签始终固定代码提交90ba40d5。
