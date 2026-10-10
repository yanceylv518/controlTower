# 归档计费用量与起始日期设置 rc163 交付

- 本任务代码提交：62fc9b36c3cd29a2697b8ebcdbbc1aa13f3e9d43，已推送 main，仅提交本任务20个文件/进度条目，其他未提交改动未夹带。
- 固定发布标签 v2.0.0-rc163：9c4b903dbff680e43ea0fb485482beb34ff2d5a2。并行账单任务已创建该标签，核验包含归档修复及 rc162 后沿用；没有重写标签或另外发布较旧代码。
- 本任务独立索引快照 go vet ./...、go test ./...、前端生产构建与10项行为测试、Linux Server/amd64及arm64 Agent 构建通过。MySQL归档/读取回归通过；181MiB专项仍受本地64MiB配置限制，未宣称已通过。
- 初始提交CI因后续main提交被并发规则取消，并非失败；[发布提交CI](https://github.com/yanceylv518/controlTower/actions/runs/38044173801)全通过。
- [发布流程](https://github.com/yanceylv518/controlTower/actions/runs/38044333118)成功。
- [安装包下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc163)。

## 实物核验

已下载三个Linux安装包及SHA256SUMS，核验大小/摘要、ELF架构/执行位、内嵌提交、Agent版本和Shell LF。Agent二进制包含summary_from_date、cache_write_unclassified_tokens、usage_issue_rows及版本保护；Server前端包含新统计起始日期、此前旧统计不重建的说明。Server迁移集合与固定提交逐字节一致，包含本次合并账单功能133，不含实验097迁移。

- control-tower-agent-v2.0.0-rc163-linux-amd64.tar.gz：`12153e5787f1a4fdb5e0dcc6877d3fc18b33e029ae460e07420d21b4ab562f34`
- control-tower-agent-v2.0.0-rc163-linux-arm64.tar.gz：`2483084aafcc0e2be69fc463d39f883b6193f86064e2449463e96bbd5bfd138e`
- control-tower-server-v2.0.0-rc163-linux-amd64.tar.gz：`852946c06828d058bd36a288b25bdd52b5400f64726fa55b8de77a948ae72efe`

版本镜像及latest摘要一致：`sha256:d226eb14cdfe7aa9ae968e860a98513b40c57631ef9cecbaa88565ad7050b59b`。

验证脚本：local/archive-summary-rc163/verify_release.py；证据：local/archive-summary-rc163/remote/VALIDATION.json。

## 部署边界

已提交、推送、发布并验证产物。未部署/重启生产，也未选择生产起始日期或触发生产历史重建。启用新设置须配套升级Server/Web与Agent；默认首次新版运行的北京时间日期，指定更早日期将纳入更多历史统计。此前的旧统计不因升级自动重建。
