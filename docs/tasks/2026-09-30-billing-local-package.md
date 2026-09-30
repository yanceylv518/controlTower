# 账单优化本地打包（2026-09-30）

用户要求打包，本次生成可安装的本地版本 `v2.0.0-rc149-local.20260930`。没有提交、Git 标签、GitHub/GHCR 发布或生产部署；主工作区和运行中的本地后端未替换。

## 产物

目录：`local/releases/v2.0.0-rc149-local.20260930/packages/`

| 文件 | 字节数 | SHA256 |
| --- | ---: | --- |
| control-tower-server-v2.0.0-rc149-local.20260930-linux-amd64.tar.gz | 7569247 | 98114c4b37937edea0dd93b9daceec484331de208608a93dd16b8296d7da54b1 |
| control-tower-agent-v2.0.0-rc149-local.20260930-linux-amd64.tar.gz | 6795938 | 7e1f7816054e10dddab2a380976fcb6fdf77e1fcc8face8be0c9384326672997 |
| control-tower-agent-v2.0.0-rc149-local.20260930-linux-arm64.tar.gz | 6117079 | 3e4876f6458406f769bd26775365fa7d5c84d5f2546278b221748719016f29d7 |

同目录提供 SHA256SUMS、SOURCE_MANIFEST.json、VALIDATION.json 和 README.txt。Server 包包含生产 Web、完整迁移及新增 115 模型单价迁移。升级不会自动重生成历史账单或回填旧单价；此前用户 #5 的补价是经过逐条验证的本地单独修复。

## 来源与范围

- fetch 后 origin/main 为 `f7c627f756baf3943d738ac3e158ce34b04636d0`；`git merge-base --is-ancestor v2.0.0-rc148 origin/main` 成功，包含已发布修复。
- 工作区 HEAD 仍为 `d36ab2748`，有混合未提交工作。使用 git archive 导出远程最新版本到 `local/releases/v2.0.0-rc149-local.20260930/source`，按白名单叠加账单性能、部分月刷新、简明单价、令牌 ID 展示相关实现与测试/文档，共 55 个文件。每个覆盖文件都核对 HEAD 与远程基线文件一致，避免覆盖远端独立修复。main 仅加入 SetBillingSourceReadPause 配置；mux/auth/Agent 沿用远程版，保留之前明确排除的主副图/独立错误统计边界。
- 完整源码清单含 1,521 个文件，清单摘要 `a59bc1d540d0fbf5f1f34dd564276d858097070e94c5cb60c97491c33b838818`。构建后再次逐文件核验，源文件未被构建/测试修改。Go 设置 buildvcs=false，避免嵌套源码导出目录错误记录父仓库旧 HEAD；实际来源由清单明确记录。
- 组装与校验脚本在 `outputs/assemble-billing-package.py`、`outputs/verify-billing-package.py`，前者拒绝覆盖已有源码快照。

## 验证结果

- 隔离源码 `go vet ./...`、`go test ./... -count=1` 全部通过。默认全量测试中的需数据库项不代表已执行，随后显式设置 CT_MYSQL_TEST_DSN 执行下列专项。
- 真 MySQL：模型筛选 SQL 完整字段对照、用户/渠道/令牌分页、简明单价聚合、用户和上游日账单复用生成月账单、部分月刷新均通过。使用本地 CT 测试库唯一测试数据，已清理；未写源业务日志。
- 527 项前端测试、vue-tsc 类型检查及 Vite 生产构建通过，保留既有大 chunk 提示。
- 使用项目 deploy/package.sh。第一次 Git Bash 缺失工具 PATH 失败于构建前；仅本次进程内补 Git `/usr/bin`、`/mingw64/bin` 和既有 pnpm 10.28.1 shim 后成功，未更改全局环境。
- Windows 交叉编译产物权限为 0644，交付前重新封包规范为 ELF/安装脚本 0755、普通文件 0644，并重新生成 SHA256SUMS；source/dist/release 中间目录也同步为已修正包。
- 三包逐一验证 SHA256、ELF 64 位/目标机器架构、Agent 内嵌版本、脚本 LF、执行权限；Server 包所有迁移和 Web 文件与构建源码/产物逐字节相同，包含 097_archive_read_connections、114 和 115，不含 097_error_statistics。

原始构建与测试日志保存在该版本目录的 go-vet.log、go-test.log、web-tests.log、mysql-billing-tests.log、package.log。Linux 实机部署和生产大数据量压力验收不属于此次已完成验证。
