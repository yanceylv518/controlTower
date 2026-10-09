# rc160 请求监控远程发布

- 版本：v2.0.0-rc160；固定提交：f050938f8471c4aa2ad1429d6c2332b798813188。
- 通过 git merge-base 核实包含 rc159、rc158、rc157 已发布提交。仅基于已推送代码构建，未混入本地错误统计、Agent 等未提交工作。
- 本次内容：ALB 渠道固定绑定一个业务站点；首响应 P95、总耗时 P95、错误率按可配置规则任一触发；独立有效样本与待补充渠道，避免一项缺失排除整条渠道。
- [发布下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc160)

## 验证

- [CI 37917450740](https://github.com/yanceylv518/controlTower/actions/runs/37917450740)全部通过：Go质量检查/测试/构建与Web类型/构建。
- [release 37917724525](https://github.com/yanceylv518/controlTower/actions/runs/37917724525)成功，安装包与GHCR镜像已发布。
- 下载后的三包SHA256/下载大小、ELF架构与可执行权限、内嵌提交、Agent版本、Shell LF全部通过。
- Server包的完整迁移集合与固定提交一致且逐字节核对，包含126_request_monitor_rules、125_permission_preset_binding及127_circuit_control_revision；不含未提交错误统计迁移。
- Web包含ALB站点绑定、判断规则、总耗时与错误率，以及rc159调权修复的提示。
- 脚本：local/request-monitor-rc160/verify_release.py；证据：local/releases/v2.0.0-rc160/remote/VALIDATION.json。

## 产物

- Agent linux-amd64：a1fa32ee14555eee355952f92f52b5533bd01a0c800732bf27f8fe83736207c3
- Agent linux-arm64：83dcb0004bf723706beb353b3c073a376325d68208eed4c5b9e931f889bb5911
- Server linux-amd64：d535585572305b707868d3c61b4d43705bdb05498abb98270c79b5aaf8d2b7f0

镜像 ghcr.io/yanceylv518/controltower-server:v2.0.0-rc160 与 latest 核对摘要一致：
sha256:02cd9654917473ce56435508a60c07146c3708a60dba98f9a5fbfd11ab6e680c

## 升级范围

更新Server/Web，按正常迁移流程执行新增126_request_monitor_rules.sql。迁移按完整文件名登记，即使已升级rc159并执行127，也会补执行126。
本次无新增Agent逻辑，Agent无需因本功能升级。首次使用到“请求监控 → 判断规则”选择实际ALB对应站点并保存，未绑定时不会全局查询。

远程发布完成不等于生产已更新；本次未部署或重启线上服务。后续发布记录提交不改变rc160标签固定代码。
