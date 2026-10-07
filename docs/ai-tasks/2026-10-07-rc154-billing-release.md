# rc154 账单修复远程打包发布

- 标识：2026-10-07-rc154-billing-release
- 更新时间：2026-10-07（Asia/Shanghai）
- 目标与验收条件：提交推送本轮账单修复及页面优化，远程打包并核验正式产物。
- 负责会话、分支与范围：当前会话，main；仅本轮账单代码、测试和文档，保留其他本地修改。
- 状态：已提交、推送、远程发布和附件核验；未生产部署或验收。

## 发布与验证

- 修复提交及标签 v2.0.0-rc154 固定 d6cbc7280955373ea65a0cc6287a68409eebe852，远端标签回读一致；merge-base 已确认包含 rc153 完整历史。
- 内容：用户/上游旧账单替换主键锁范围、月创建站点互斥、1213 有界事务重试；生成月份可选、对象日/月状态预览、紧凑筛选、去掉重复横栏、默认不选中首个对象。
- 本地 Go vet/test、15 项账单 Web 回归通过；Web typecheck/build 通过，保留既有 chunk 体积提示。数据库专项证据见死锁交接（独立 MySQL 9.7，非生产 MySQL 8）。
- [CI 37567311813](https://github.com/yanceylv518/controlTower/actions/runs/37567311813)、[release 37567505266](https://github.com/yanceylv518/controlTower/actions/runs/37567505266) 均 success。
- [Release 正式附件](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc154)：Server Linux amd64、Agent Linux amd64/arm64、SHA256SUMS 齐全。
- 下载至 local/releases/v2.0.0-rc154/remote；三包 SHA256 匹配，ELF 架构/执行位、内嵌提交、Agent 版本、脚本 LF、全部迁移逐字节和 Web 入口核验通过。验证脚本 verify.py、结果 remote/VALIDATION.json 保留本地。
- GHCR rc154 与 latest 摘要一致：sha256:1f2c1bd5572d580994857bc3b1f0db1ad22a93df319f0b58192f7296e9211bed，运行平台 linux/amd64。

## 边界与下一步

相对 rc153 无 Agent 代码或数据库迁移变化，只需升级 Server/Web。流水线按既有规则仍生成 Agent 包，不表示 Agent 有业务改动。未生产部署、未重启运行中的本地服务；生产失败账单需升级后补生成，真实 MySQL 8 大批量负载及页面业务数据仍待验收。
