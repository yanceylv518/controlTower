# rc153 远程打包发布

- 标识：2026-10-04-rc153-remote-release
- 更新时间：2026-10-04（Asia/Shanghai）
- 目标与验收条件：按用户要求远程打包当前已提交main；发布标签固定，检查流水线、正式附件与镜像。
- 负责会话与范围：本会话；main，仅发布已提交源码，保留本地其他未提交改动。
- 状态：远程发布与附件核验完成，未生产部署或验收。

## 版本与验证

- `v2.0.0-rc153`固定`2a04135820cb31d915ad14da9b6103dbd2691f22`，远端标签回读一致；`git merge-base --is-ancestor v2.0.0-rc152 HEAD`通过，包含此前rc152完整历史。新增包含505c63a3报表恢复/站点队列、7c1f5967调权可靠性/延迟及cebc1338归档页面请求优化。
- [CI 37204175366](https://github.com/yanceylv518/controlTower/actions/runs/37204175366)成功后触发标签；[release 37204397932](https://github.com/yanceylv518/controlTower/actions/runs/37204397932)全部成功。
- [正式发布与附件](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc153)：Server/Web Linux amd64、Agent Linux amd64/arm64、SHA256SUMS齐全。
- 下载至`local/releases/v2.0.0-rc153/remote/`，三包SHA256与清单一致；ELF架构、执行位、内嵌提交、Agent版本和安装脚本LF检查通过；Server全部迁移逐字节匹配发布提交，含118和119迁移，Web入口齐全。脚本和结果见上级verify.py及VALIDATION.json。
- `docker buildx imagetools inspect`确认GHCR版本/latest摘要一致：`sha256:f82ac573b54fbd5f0e3968fe6f828874d2aea2e59ec6af0dccbeeba93b974437`，运行平台linux/amd64。

## 边界与下一步

- 需Server/Web/Agent配套更新，使用118和119迁移；本轮未重跑数据库专项，沿用对应修复记录，不将远程打包成功等同生产验收。
- 未部署生产、未重启本地服务。真实调权延迟、报表大规模负载和归档首屏速度仍待升级后验收。发布标签不随后续文档提交移动。
