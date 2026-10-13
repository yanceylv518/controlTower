# rc169 生命线状态与详情页签修复远程发布

- 日期：2026-10-13（Asia/Shanghai）
- 用户授权：提交推送并远程打包。
- 状态：功能提交与远程发布完成，正式成品核验通过；未部署或生产操作。
- 标签：`v2.0.0-rc169`，固定完整提交`c59e82a20f7a81ecfe5f7432ecc2c1e3fa40f461`。

## 包含内容

1. 修复点击状态变更后被状态更新强制切回默认页签。真正切换站点/渠道/入口时才重置，保留当前页签、批次和查询。
2. 停止中改为正在停止探测；根据实际未结束恢复任务及在途请求判断，关闭且无任务、无有效恢复计划显示未探测，不再依赖旧恢复阶段。排队显示等待恢复，执行显示恢复探测；未结案旧任务且缺少近期确认仍不冒充停止。
3. 页面快照过期单独提示并保留上次状态，不再把整个列表改成待确认。

祖先核验包含rc160～rc168及此前生命线刷新、异常筛选、手动关闭隐藏修复。只提交本次11个文件/选定进度条目，保留请求监控等其他本地未提交内容。详见[实现交接](2026-10-13-lifeline-current-state-design.md)。

## 验证与远程执行

实现阶段全项目Go vet/test、关闭状态64组合及边界、临时MySQL四项集成、27项Web行为回归、类型检查/生产构建通过。本轮从固定提交导出的源码再次执行27项Web回归，通过；远程CI在干净提交上执行Go vet/test/build和Web typecheck/build，通过。未新增浏览器或生产验收，既有大包提示保留。

- [CI 38323532868](https://github.com/yanceylv518/controlTower/actions/runs/38323532868)：成功。
- [release 38323795167](https://github.com/yanceylv518/controlTower/actions/runs/38323795167)：成功。
- [发布与下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc169)。

## 正式成品与升级范围

三个Linux安装包SHA256、ELF架构/执行位、完整内嵌提交及vcs.modified=false、Agent版本、脚本LF核验通过。140个迁移与标签源码逐字节一致，与rc168无差异。Web含正在停止探测、独立过期提示、新生命线入口及原有刷新/异常筛选/隐藏手动关闭标识。

- `control-tower-agent-v2.0.0-rc169-linux-amd64.tar.gz`：`2fdb44d4cb0ae781394b19f2419b2d5063befa601410713c136b8ac23d2bd2dd`。
- `control-tower-agent-v2.0.0-rc169-linux-arm64.tar.gz`：`ee245eee69e966780cfc1caaa740d4fce9b47e88c6bb537a9e9c29fae5171349`。
- `control-tower-server-v2.0.0-rc169-linux-amd64.tar.gz`：`5eb83f9ffb97209fc23f9baed38d60f09c352753069a40a16f24f55bfbf7e23d`。

GHCR版本/latest摘要一致：`sha256:1bf47b09ad50c43621bbd43a172031ab36a3050bd68c6cd8cee1377954a4caf2`，linux/amd64；未启动容器。需更新Server/Web，无Agent代码或新增迁移。未生产部署、重启、策略修改或真实上游验证。

核验文件：`local/releases/v2.0.0-rc169/remote/VALIDATION.json`；脚本`local/verify-rc169.py`。发布事实另提交main，标签不移动。
