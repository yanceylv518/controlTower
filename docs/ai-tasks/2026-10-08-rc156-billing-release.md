# rc156 账单过滤远程发布

- 日期：2026-10-08，Asia/Shanghai。
- 功能提交：96852530c2e10b6a4aa1d4c0a7321b655c6fe302，已推送 main。标签 v2.0.0-rc156 固定该提交。
- 标签前以 merge-base 核验包含 rc155、rc154；精确提交22个文件，未包含主副图、独立错误统计及其他本地工作。
- 暂存内容的独立源码三包 Go test/vet 通过；独立 MySQL8规则持久化、自动目标重叠、任务历史专项通过。前端10项回归、类型检查及构建通过。
- [CI 37738578857](https://github.com/yanceylv518/controlTower/actions/runs/37738578857)与[release 37738606527](https://github.com/yanceylv518/controlTower/actions/runs/37738606527)均成功。
- [正式下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc156)包含 Server amd64、Agent amd64/arm64、SHA256SUMS。
- 下载至 local/releases/v2.0.0-rc156/remote，三包 SHA256、ELF与执行权限、内嵌提交号、脚本LF均核验通过；Server全部迁移与标签源码逐字节一致，含122，不含未提交097_error_statistics；Web入口存在。VALIDATION.json保留本地。
- GHCR rc156/latest 摘要一致：sha256:28e075e4c4edecab4e20223a3af88b6e8a733e58d061a7461a00ce5525639e0a。

相对 rc155 升级需 Server/Web 和迁移122；Agent没有功能改动，流水线仍生成配套Agent包。已本地启动，未生产部署或验收。更早版本升级仍需包含其间迁移与Agent修复。
