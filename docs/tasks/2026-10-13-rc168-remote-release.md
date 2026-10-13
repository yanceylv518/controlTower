# rc168 渠道生命线当前状态与恢复批次远程发布

- 日期：2026-10-13（Asia/Shanghai）
- 状态：远程打包发布、正式附件核验完成；未部署或重启生产。
- 用户授权：提交推送并远程打包。
- 标签：`v2.0.0-rc168`，固定提交 `c8637a5d1708fe088c2e64fa928873d56c46d808`。

## 发布内容与包含关系

默认当前渠道，独立展示渠道开关与探测状态、原因和下一步；补齐模型/开关/状态筛选、优先级与线上权重、当前恢复发送进度。跨渠道探针记录及渠道详情支持独立查询、分页，恢复检测批次按命令ID关联逐条请求；常态工作线程确认经持久化读取，过期状态不伪装为正在运行。

逐项Git祖先核验包含rc160～rc167、生命线刷新/异常筛选69c85608、渠道选择08fbb823和手动关闭隐藏90ba40d5，保留历史统计、趋势、异常筛选及自动刷新。仅提交本次31个文件/选定进度条目，未夹带本地请求监控等其他未提交功能。

## 验证

- 实现阶段全项目go vet/test、Web类型/构建、临时MySQL四项集成及桌面/390px合成API浏览器验收通过，具体见[实施交接](2026-10-13-lifeline-current-state-design.md)。未以合成验收替代真实站点测试。
- 本轮从固定提交导出源码快照，再执行25项生命线Web行为回归，通过；远程CI针对干净的固定提交执行Go vet/test/build及Web typecheck/build，通过。
- [CI 38318536545](https://github.com/yanceylv518/controlTower/actions/runs/38318536545)：成功。
- [release 38318744998](https://github.com/yanceylv518/controlTower/actions/runs/38318744998)：成功。
- [正式发布与下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc168)。

## 升级范围与边界

需Server/Web配套更新。迁移138新增运行状态留痕表、探针批次关联字段和索引；安装包共140个迁移，与标签源码逐字节一致。旧探针不会补造批次关联，Agent无逐条回执时仍明确只有已有任务报告。本次无Agent代码改动，标准流水线生成两架构Agent附件；未操作生产安装、迁移、重启、探针策略或真实上游。

## 成品核验

三个附件均通过SHA256、ELF架构/执行位、完整内嵌提交及vcs.modified=false、Agent版本、脚本LF校验。Server包迁移与源码一致，Web同时包含新的当前渠道/跨渠道记录/恢复检测批次入口和原刷新/异常筛选/手动关闭隐藏标识。

- `control-tower-agent-v2.0.0-rc168-linux-amd64.tar.gz`：`b772373dd0f488236de1835bbe7159c7ad4e58e361fbea8d93ea2bca315c1ac7`。
- `control-tower-agent-v2.0.0-rc168-linux-arm64.tar.gz`：`1e9e02a383508bacf02c7a9fbe8f74459cb73c9bc63456deb3b3776937ea3de0`。
- `control-tower-server-v2.0.0-rc168-linux-amd64.tar.gz`：`eb709dc7663002d35b768007879e709d7fddc887412a16c81899667481db3426`。

GHCR版本与latest摘要均为`sha256:c2136b97a0dc3f63773881bbbc0aa65c5a40e112002d85dac2193f603946253f`，linux/amd64，未启动容器。

本地核验报告：`local/releases/v2.0.0-rc168/remote/VALIDATION.json`；脚本：`local/verify-rc168.py`。发布记录另提交main，标签继续固定功能提交。
