# rc166 探针渠道选择远程发布

- 日期：2026-10-13（Asia/Shanghai）
- 状态：远程打包发布完成，正式产物核验通过；未部署或重启生产。
- 固定标签：`v2.0.0-rc166`，代码提交 `08fbb823e989d9477626f6ace7777ebffda5124b`。
- 用户授权：在完成检查、提交推送后，明确要求“现在远程打包”。

## 包含关系与功能

发布前已通过 Git 祖先检查确认包含 rc160～rc165、归档修复 62fc9b36c、渠道生命线 16693b85、常态探针开关 0461572db、手动渠道状态 8238e2429。主线与远端当时均为 08fbb823；新标签固定该提交，不含本地请求监控及其他未提交内容。

新增常态探针的全部/指定渠道范围、模型→渠道两级多选和搜索；渠道项展示线上优先级/权重，支持整组选中。指定空列表表示全部停止常态探针；未选渠道退出定时和错误加速探针，普通评估排除其旧探针样本及基线，真实流量调权和熔断恢复保留。详见[实现与验证](../ai-tasks/2026-10-13-routine-probe-channel-selection.md)。

**渠道生命线原有页面、30 秒刷新、操作/决策/异常探针筛选以及手动关闭展示均包含。** 这些代码也已存在于 rc165 标签；此前历史文档“其他未提交刷新/筛选”备注不能解释为原有功能未打包。本轮已直接检查 rc166 成品 JavaScript 中的相关标识。没有重新承诺或实现未明确的额外展示优化。

## 远程执行

- [固定提交 CI 38307709151](https://github.com/yanceylv518/controlTower/actions/runs/38307709151)：完成，成功。
- [远程 release 38308474937](https://github.com/yanceylv518/controlTower/actions/runs/38308474937)：完成，成功，包含安装包构建、GHCR 推送和 GitHub Release 创建。
- [正式发布与下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc166)：3 个 Linux 安装包及 SHA256SUMS。

## 成品验证

三个安装包均下载至 `local/releases/v2.0.0-rc166/remote/` 并通过清单 SHA256 校验；所有 Server、Agent、Log Reader 二进制验证 ELF 架构、执行权限、内嵌提交为 08fbb823 且 vcs.modified=false，Agent 内嵌 rc166 版本；Shell 脚本为 LF。Server 包中的 139 个迁移与固定提交逐字节一致。

Web 成品检查包含 routine_probe_selected_only、routine_probe_channel_ids、常态探针渠道范围、模型两级选择提示、线上权重、渠道生命线、30 秒刷新首页、操作/决策/异常探针、全部事件及手动关闭渠道。此项验证证明代码打入安装包，不替代真实环境端到端验收。

GHCR `ghcr.io/yanceylv518/controltower-server:v2.0.0-rc166` 与 `latest` 摘要均为 `sha256:013f9d41478abdfc16b6b7f9a3e3edf942221a909c1e0e982a9d4dd3b645dd9c`，摘要计算及 linux/amd64 平台确认通过。没有启动容器。

完整报告：`local/releases/v2.0.0-rc166/remote/VALIDATION.json`，复核脚本：`local/verify-rc166.py`。

安装包 SHA256：

- Agent linux/amd64：`7b864354860f6252d27bcb09799a08047aa7889470170f2bc51eae2ea342cd95`
- Agent linux/arm64：`c496a7b80a2ede91b8d7a6551f813920f8c84d7c818f75c5fa3d804298fd9acb`
- Server linux/amd64：`a5105d8606b305ca692abb4baf8c9a55b0009696e4b519cdbe5148af0ba144cd`

## 升级范围与边界

相对 rc165，无新增迁移、Agent 或 NewAPI 改动，需要更新 Server/Web；标准发布流程仍生成两个架构的 Agent 包，但本功能不要求升级 Agent。未连接真实上游发探针，未修改站点策略，未执行生产迁移、安装或重启。发布记录另提交 main，已发布标签保持固定代码提交。
