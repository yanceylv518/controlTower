# rc175 主线远程发布

- 用户授权包含本次修复及最新主线，未授权本轮生产部署。
- 标签v2.0.0-rc175固定e9ff05f87a5ecf6a5b38bb91c6e41f1a1f30d65c；发布前两次读取origin/main均为该提交，git merge-base核验rc174是祖先。包含rc174及主线已有功能，不包含无关本地未提交资料。
- [CI 38657929820](https://github.com/yanceylv518/controlTower/actions/runs/38657929820) Go/Web质量门均成功；[远程发布38658179577](https://github.com/yanceylv518/controlTower/actions/runs/38658179577)成功。
- [正式下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc175)：Server linux-amd64、Agent linux-amd64/arm64及SHA256SUMS齐全。逐一核对哈希、ELF架构和执行位、完整内嵌提交及vcs.modified=false、Agent版本、脚本LF；Server包147份迁移与固定源码逐字节一致，含144通知边界，既有Web功能标识检查通过。
- GHCR版本/latest均为sha256:50da03dbda8f76322a0a6bd3a6dba8ca5581b0e06d787ae01b8290e3d4f62e61，linux/amd64。核验报告local/releases/v2.0.0-rc175/remote/VALIDATION.json。
- 相对rc174：升级Server并执行144迁移，首次升级时固定历史熔断/恢复通知边界，既有历史不自动发送但保留记录；之后新事件沿用原通知流程。清理批次100、完整保护、错误隔离及所属实例轮转。Web/Agent无新增业务修改。
- 未部署、未改生产配置、未发送群测试或手动补发。当前生产不会因发布自动改变；MySQL8生产清理延迟待实际升级验收。通用机器人统一节流未在本次实现，新事件短时超过机器人额度仍可能沿原规则重试。
- 本次不推送Workspace，保留用户其他本地修改。
