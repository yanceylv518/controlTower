# rc159 调权写入修复远程发布

- 版本：v2.0.0-rc159；固定提交：70a1a481956873b9bd3faab431d6b644df8615e8。
- main已提交推送。同步并保留已推送的权限预设绑定9af40f201；通过git merge-base祖先关系核实包含rc158/rc157/rc156，未用版本号推断包含关系。
- 仅提交本任务22个文件（含精确抽取的进度记录）。本地错误统计、请求监控渠道规则和其他未提交修改保持原状。
- 代码修复：禁用写NewAPI允许的2、恢复1；自动/手动归属由CT记录，外部启用撤销旧归属，control_revision阻止过期状态/恢复决策；写入连续失败5次结束旧任务并回到正常评估，不增加等待人工处理状态。已确认禁用保持新探测恢复，在途回执先结算。
- 正常评估后仍可产生新调权/熔断决策；不会无限保存和重放旧失败目标。完全未被快照采集观测到的外部启用再禁用仍无法识别。

## 验证

- 精确暂存树9e2fa4412e29a78d811d05696b31f155dd1e2761导出隔离候选；调权前端56项回归、类型检查通过。
- 隔离MySQL数据库ct_rc159_candidate_20261009175002使用精确发布迁移集合（含上游125权限预设和本次127）全量迁移，状态重载/旧失败任务结束/归属撤销/过期恢复拦截及直连集成通过。
- Windows候选go test ./...中既有TestTailerAppendRotationAndMissingFileRetry出现一次时序失败，单独-count=1复测通过；不将该次全量运行记为全部通过。
- [远程CI 37913932596](https://github.com/yanceylv518/controlTower/actions/runs/37913932596)全成功：Linux go vet/test/build及Web typecheck/build。
- [远程release 37914227610](https://github.com/yanceylv518/controlTower/actions/runs/37914227610)成功：Server amd64、Agent amd64/arm64安装包、SHA256SUMS及GHCR镜像发布。
- [发布下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc159)。
- 下载校验：三包SHA256/下载大小、ELF架构/可执行权限、内嵌Git提交、Agent版本、Shell LF通过；Server全部已提交迁移与固定提交逐字节一致，包含127和125_permission_preset_binding，不含097_error_statistics、125_error_statistics、126_request_monitor_rules。Web包含旧任务结束与重新评估提示，无人工等待状态。
- 校验脚本local/tuning-rc159/verify_release.py；证据local/releases/v2.0.0-rc159/remote/VALIDATION.json。

## 产物

- control-tower-agent-v2.0.0-rc159-linux-amd64.tar.gz：1b94ceed691512a4d059960311b41a20ca08181cf7a724c3a5b9e6e263eaf095
- control-tower-agent-v2.0.0-rc159-linux-arm64.tar.gz：4bccd4004c823a4a2781263dfcae8a83f042d89f9e79ce39cbe1a4454d66d41d
- control-tower-server-v2.0.0-rc159-linux-amd64.tar.gz：609dad0e4165807a5f16568334bccfd832d8bb339b0956e044d848badbbd8d67

GHCR rc159与latest摘要一致：sha256:3c602f477be4e3beb296650b0a254e260ae3cb4b3df4d3a6ead6e3309469f407

## 升级范围

需要更新Server/Web，正常迁移流程执行127_circuit_control_revision.sql；从rc158升级还会包含main此前已提交的125_permission_preset_binding.sql。保留现有密钥和配置。Agent无需为直连调权强制升级；配套Agent包提供共享channelcontrol客户端非法状态提前拦截。

已完成远程发布，不等于线上已生效：本次未生产部署或重启服务。发布文档后续提交不改变rc159固定代码提交。
