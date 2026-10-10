# rc162 上游URL优先、定时同步及合并删除远程发布

- 版本：v2.0.0-rc162，固定提交6cd3b0d8563d9e7ab9c1cc179f5257507bc05b2a。
- git merge-base 已核验包含rc161及其此前修复，不以版本号推断。仅从标签对应提交构建，未混入本地未提交工作。
- 内容：先收集已有绑定URL再匹配，URL优先、前缀兜底并补充URL；后台立即/每30秒同步；错误上游合并删除、历史归档、折扣/版本冲突保护与死锁有限重试；页面明确前缀原归属及一并转入。
- [发布下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc162)

## 验证

- [固定提交CI 38031254812](https://github.com/yanceylv518/controlTower/actions/runs/38031254812)成功。
- [远程发布38031396468](https://github.com/yanceylv518/controlTower/actions/runs/38031396468)成功；三Linux安装包、SHA256SUMS与GHCR镜像已发布。
- 下载核验大小/SHA256、ELF架构/执行权限、内嵌提交及Agent版本、Shell LF。Server完整迁移集合与固定提交逐字节一致，包含132且不含097实验迁移。
- Server核验后台同步与删除保护标识；Web核验URL优先、每30秒同步、合并删除，并保留rc159–161相关功能标识。
- 验证脚本：local/upstream-rc162/verify_release.py；证据：local/releases/v2.0.0-rc162/remote/VALIDATION.json。

## 产物

- control-tower-agent-v2.0.0-rc162-linux-amd64.tar.gz：`f0926643e0fd31eb14bb0015cc1a969419af77aab781c86855660fa5e1341dfe`
- control-tower-agent-v2.0.0-rc162-linux-arm64.tar.gz：`2c08c71d74bfca651cffd31ff68085ef2a823c3ae81da5692e81768bb3fc050c`
- control-tower-server-v2.0.0-rc162-linux-amd64.tar.gz：`edc7720c838ef3e10aaa3a6be68f68d78ceb269e41b3fd589c7b666768819932`

版本镜像与latest摘要一致：`sha256:466605066628c16bee6795f61352ebe7cae3abf39d7a8c46fdbe321b000f9f22`。

## 升级范围

相对rc161更新Server/Web并执行132_billing_upstream_archive.sql；Agent无新增业务变更，不必为本次上游修复单独升级。若从rc160或更旧版本升级，仍需配套rc161的采集Agent与125_error_statistics、128–131迁移。迁移按完整文件名登记。

后台同步不会抢占已有归属；既有重复上游由用户选择正确目标“合并并删除”，有历史时保留ID并隐藏。源渠道和旧账单不删除，旧账需要修正时仍主动覆盖生成。

未部署/重启生产、未操作实际合并删除或账单覆盖。发布记录提交不改变标签固定代码。
