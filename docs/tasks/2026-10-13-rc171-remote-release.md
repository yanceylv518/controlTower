# rc171 移除停止分类远程发布

- 日期：2026-10-13（Asia/Shanghai）
- 授权：修复后复查，无问题则提交推送并远程打包。
- 状态：代码推送及远程发布完成，正式成品核验通过；未部署。
- 标签：`v2.0.0-rc171`，固定完整提交 `7e03ec0c8a85159c95a64a953972750337613a2d`。

## 内容与范围

移除生命线独立的正在停止探测分类，停止条件成立显示未探测；旧未结案命令不再冒充当前停止/恢复进度，可信在途数量和近期任务仅作补充。标签固定7e03ec0c，祖先核验包含rc160～rc170及此前刷新、异常筛选、手动关闭隐藏和加载超时修复。固定源码29项Web回归、远程CI/release、三正式附件及GHCR版本/latest核验通过。需Server/Web配套更新，无新增迁移或Agent业务修改；未部署。

只读核对pinducloud-cn渠道214：旧任务创建/更新时间均为9月11日，状态delivered且计划10次，没有当前在途证据。未改写、清理或取消生产历史任务。本次修正的是展示判断，不改变探针执行、熔断、恢复和自动开启规则。详见[实现交接](2026-10-13-lifeline-current-state-design.md#移除正在停止探测分类2026-10-13)。

精确提交本次7个文件/选定进度条目，其他本地请求监控等工作保留。分类删除后仍有可信在途请求时，保留数量、原因和恢复进度，避免误称请求已取消。

## 验证

实现阶段Go全项目vet/test、45组停止条件×命令状态×新鲜度组合及在途/旧线程边界、29项Web回归、typecheck/build通过。实际Web构建配本地合成API确认旧任务回放为未探测、无停止分类，另有在途请求的渠道仍显示待结果。没有新增SQL，本轮未重跑外部MySQL集成。发布复查确认常态和恢复在途统计来源独立；固定提交导出源码29项Web回归通过。远程干净提交CI执行Go vet/test/build及Web typecheck/build通过，既有大包提示保留。

- [CI 38327325871](https://github.com/yanceylv518/controlTower/actions/runs/38327325871)：成功。
- [release 38327568575](https://github.com/yanceylv518/controlTower/actions/runs/38327568575)：成功。
- [发布与下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc171)。

## 产物与升级

三个Linux安装包SHA256、ELF架构/执行位、完整内嵌提交、vcs.modified=false、Agent版本和脚本LF核验通过。141个迁移与标签源码逐字节一致，包含rc170迁移139索引。本次无新增迁移。Web成品不含正在停止探测标签，并包含未探测及此前超时、刷新、筛选、手动关闭隐藏功能；Server成品包含本次在途补充说明。

- `control-tower-agent-v2.0.0-rc171-linux-amd64.tar.gz`：`5beb6466a922562a2c3670e0af3f2839ad58b35624c52201e78f83a948b74947`。
- `control-tower-agent-v2.0.0-rc171-linux-arm64.tar.gz`：`fba5f1fd7c97b38dce01eace8553b051f576f1cfbb5bffae7c8c6d130682f6ef`。
- `control-tower-server-v2.0.0-rc171-linux-amd64.tar.gz`：`3bb18160ee6fcd00b8a7d3d8e211133066f0a8d24aeac50a1d288c2e869bfdfa`。

GHCR版本/latest摘要一致：`sha256:92135cb0229cd1e412aac83cb1f91120109ee0fc08fb4560439d5fd4ddfad2a4`，linux/amd64；未启动容器。需更新Server/Web，从rc170以前升级需包含迁移139；无Agent业务变化。未生产部署、重启或执行生产迁移；真实站点上线验收待部署后进行。

核验记录：`local/releases/v2.0.0-rc171/remote/VALIDATION.json`。发布事实另提交main，标签不移动。
