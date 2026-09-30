# rc149 远程打包与发布核验（2026-09-30）

按用户“远程也打包一下”要求，精确提交本次已验证的账单单价、源库减压、部分月刷新及月账单展示改动，推送 main，并完成远程发布。

## 固定版本与范围

- 发布标签：`v2.0.0-rc149`。
- 固定提交：`84551f6e388932e6a1b2ce31af0111c83ee29460`，父提交 `f7c627f756baf3943d738ac3e158ce34b04636d0`。
- 使用本聊天已有的干净交付 worktree，不另建功能分支；57 个文件精确暂存，未带主副图、独立错误统计、运行数据、凭据或构建产物。对照先前打包源码清单，1,520 个源文件等价，Git 规范化了其中 1,146 个文件的 CRLF；补充项目进度及打包记录不改变程序。
- 明确检查了 v2.0.0-rc148、v2.0.0-rc147、0418042a9 大记录归档、671baaf78 通知时区、7e32f4814 通知后缀、f7c627f75 归档页面的祖先包含关系，全部通过。标签不会随后续验证文档提交移动。
- 主工作区旧 HEAD 与混合未提交工作保留。本次账单改动已经交付，后续同步需要核对，避免把现有工作区差异再次重复提交。

## 远程流水线

- [CI 36688201304](https://github.com/yanceylv518/controlTower/actions/runs/36688201304)：Go vet/全量测试、Linux 构建、前端类型检查与生产构建全部通过，确认通过后才推送发布标签。
- [release 36688476919](https://github.com/yanceylv518/controlTower/actions/runs/36688476919)：安装包、Docker 镜像构建推送和 GitHub Release 创建全部成功，head_sha 与固定提交一致。
- [GitHub Release](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc149)。
- GHCR：`ghcr.io/yanceylv518/controltower-server:v2.0.0-rc149` 和 `latest` 由发布工作流推送。对版本标签额外执行只读 imagetools inspect，OCI 索引摘要为 `sha256:a649b70476d4ce621c96c5587cda515d11381e27c027ef20cb612d2ae29e318d`，运行镜像平台 linux/amd64，并附 provenance attestation。

## 正式附件核验

已下载到 `local/releases/v2.0.0-rc149/remote/`，同目录保留 SHA256SUMS 和 VALIDATION.json。正式远程附件与本地预览包使用不同工具链和版本标记，不要求二者二进制哈希相同。

| 文件 | 字节数 | SHA256 |
| --- | ---: | --- |
| control-tower-server-v2.0.0-rc149-linux-amd64.tar.gz | 7258703 | a0da9918a2da59d0e28c2043c36589f5a9203afe887ef2e0987b7731a51f81a1 |
| control-tower-agent-v2.0.0-rc149-linux-amd64.tar.gz | 6078584 | e534ae4b502c84f6f0fb5b5c69d298bc1ef6998128f55988e1037b94381ded14 |
| control-tower-agent-v2.0.0-rc149-linux-arm64.tar.gz | 5559138 | 9494bd089008d8b3d658c17eafe5cb87803ded9258946368619346fb89742f2c |

三包校验和与清单一致；二进制 ELF 64 位架构正确、执行位有效，Agent 内嵌版本和三个程序内嵌 vcs.revision 均与发布提交一致。安装脚本为 LF：install-agent.sh 为 0755；既有 install-log-reader.sh 按仓库保持 0644，按 docs/container-log-queries.md 使用 `sudo bash ./install-log-reader.sh`，无需改发布包。

Server 包包含 Web 和完整迁移集合；全部迁移逐字节对照发布提交通过，含 097_archive_read_connections、114_persistent_settlement_reports 和 115_billing_unit_prices，不含 097_error_statistics。

验证脚本：outputs/rc149-release-api.py、outputs/verify-rc149-remote.py。凭据只由本机 Git credential helper 在进程中读取，用于 GitHub 标准认证，没有写入包、仓库或日志。

本次未部署生产。新账单保存模型单价，历史账单不会仅凭升级自动重生成或回填；此前用户 #5 的单价修复只针对经过逐条验证的本地指定账单。生产大数据量的 CPU/IO 收益仍待运行观察。
