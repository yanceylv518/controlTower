# rc155 探测保护远程打包发布

- 日期：2026-10-08，Asia/Shanghai。
- 用户授权远程打包；main，保留其他未提交工作。
- v2.0.0-rc155 固定 bfc81badfd98d322f11bbc82463daf641ae9613b，包含探测修复16a8fb006及两处权限弹窗关闭回调类型修复。标签创建前通过merge-base核验包含rc154、rc153、7c1f5967e调权修复和16a8fb006。
- 类型修复解决此前TS7006打包阻断，仅两行类型注解；本地Web生产构建通过，保留既有chunk体积提示。
- [CI 37733808492](https://github.com/yanceylv518/controlTower/actions/runs/37733808492)和[release 37733827246](https://github.com/yanceylv518/controlTower/actions/runs/37733827246)均success。
- [正式附件](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc155)：Server Linux amd64、Agent Linux amd64/arm64及SHA256SUMS齐全。
- 三包下载至local/releases/v2.0.0-rc155/remote；SHA256、ELF架构/执行位、二进制提交、Agent版本、脚本LF、全部迁移逐字节及Web入口验证通过。含121_tuning_probe_latency.sql，不含未提交097_error_statistics.sql。脚本verify.py及VALIDATION.json保留本地。
- GHCR rc155/latest摘要一致：sha256:ead186340621635f92907e0ed6d3af0c34760c42174d7258de8f46e6e2dbf85b。

相对rc154同时包含已提交的权限预设与渠道搜索修复，因此升级需Server/Web/Agent及120、121迁移；已有120时只补121。未生产部署、重启或验收。探测规则、真实NewAPI取消行为和数据库验证边界见[实现交接](../tasks/2026-10-08-probe-latency.md)。
