# rc161 上游管理、账单保护、调权与错误统计远程发布

- 版本：v2.0.0-rc161，固定提交 4be16076b656bf6f2d04dedbe930430cede6eea5。
- 已核验 rc160 为该提交祖先，保留此前发布修复；未打包本地未提交工作。
- 包含：Agent 分钟错误统计与 Server 持久化、调权容量降幅配置与渠道筛选、临时账单入口修复、上游多前缀/手动转移/URL资料/折扣编辑及覆盖月账完整性保护。
- [发布下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc161)

## 验证

- [固定提交 CI 38025840419](https://github.com/yanceylv518/controlTower/actions/runs/38025840419)成功。
- [远程发布 38026210759](https://github.com/yanceylv518/controlTower/actions/runs/38026210759)成功，三个安装包、SHA256SUMS及GHCR镜像已发布。
- 下载核验大小与SHA256、ELF架构/执行权限、内嵌提交及Agent版本；Server迁移集合与固定提交逐字节一致，包含125_error_statistics和128–131，不含097实验迁移。
- Web保留rc159/rc160功能标识，并核验前缀/转移、错误统计、容量降幅与上游信息标识。
- 验证脚本：local/upstream-rc161/verify_release.py；证据：local/releases/v2.0.0-rc161/remote/VALIDATION.json。

## 产物

- control-tower-agent-v2.0.0-rc161-linux-amd64.tar.gz：`3976ad9c76add07af6d90f9775eee242e14335df5b6ffc689ea8957b76125de4`
- control-tower-agent-v2.0.0-rc161-linux-arm64.tar.gz：`1383da1768bc273ac195744954c6893ddb51f08ebeda8b29825975ccb999f836`
- control-tower-server-v2.0.0-rc161-linux-amd64.tar.gz：`849d3459ae37083840420248617968ad0c1024e68bd7097ae9bc8aad8df295ed`

版本镜像与latest摘要一致：`sha256:1fee162ac7d812388483665d38a11aed9bb2f24c2a5c8a7e6f9edba9d5d07341`。

## 升级范围

完整使用新功能需更新Server/Web及负责日志采集的Agent，执行125_error_statistics.sql及128–131迁移；按完整文件名登记，不以编号大小判断是否已应用。首次启用错误统计不回填历史；已有账单不自动重算，修正归属后需选择覆盖生成。覆盖按当前归属和配置重算，原渠道金额也可能变化。

发布成功不等于生产已部署。本轮未部署或重启生产、未执行真实账单覆盖。记录提交不会改变标签固定代码。
