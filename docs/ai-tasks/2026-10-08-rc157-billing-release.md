# rc157 账单修复发布

- 版本：v2.0.0-rc157；固定提交 e0e06ed572fa0734846bc5ab79ca431a7a70d835，main已推送。
- 已按Git祖先关系确认包含rc156、rc155，无版本号推断。
- 范围：上游日明细按渠道XLSX、所有账单下载准备反馈；排除零输出仍保留页面统计；已生成账单标注包含/排除/混合来源。
- 仅提交本次账单功能与文档，其他错误统计、监控/图表及未提交工作保留。
- 本地及精确暂存快照：billing/mysqlstore/dashboard测试通过，Server/Agent编译通过；工作区Go vet、Web 38项回归、类型检查与构建通过；独立MySQL验证统计分离与月快照通过。本地Server已重启，healthz为200。
- 远程CI [37746576421](https://github.com/yanceylv518/controlTower/actions/runs/37746576421)、release [37746581175](https://github.com/yanceylv518/controlTower/actions/runs/37746581175)成功。
- [Release下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc157)：Server amd64、Agent amd64/arm64三包SHA256、ELF/可执行权限、内嵌提交、Shell LF、Web、全部已提交迁移逐字核验通过；未包含未提交097_error_statistics.sql。
- 初次Agent amd64下载被截断，重新下载后长度与SHA256均匹配，发布产物本身正确。
- GHCR rc157/latest均为sha256:ac545e9a5ead2c2ee05a3a9ac8b4b9f8d16a1b0e3975fadc3a27708fcc55a3a0。
- 校验产物位于local/releases/v2.0.0-rc157/remote，VALIDATION.json记录各包哈希。

## 升级与边界
更新Server/Web并执行123_billing_excluded_output_stats.sql（通过正常迁移启动流程；仍保留此前迁移）。本次无新增Agent逻辑，已更新rc156 Agent的无需为本次特性再次更新Agent。生产尚未部署或验收。
历史排除账单未保存的零输出统计不能凭空恢复，需显式覆盖重生成；旧上游按用户拆分文件同样需重生成才能改为渠道文件。不会自动重扫源库或修改已签发文件。
