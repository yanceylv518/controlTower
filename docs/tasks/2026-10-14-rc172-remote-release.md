# rc172 请求监控与主线功能远程发布

- 日期：2026-10-14（Asia/Shanghai）。
- 授权：远程打包，包含本次请求监控改动和主线最新功能。
- 状态：已发布并核验正式产物；未部署，未执行生产迁移或重启。
- 标签：`v2.0.0-rc172`，固定完整提交 `6145749e6016ac6881bb36b011522a38d38869cf`；打标签前远端main与本地一致。

## 内容与包含关系

请求监控六档大小、最长24小时近期范围和北京时间自定义历史已随最新main远程发布，保留旧Server兼容判断、切换取消与历史自动刷新暂停，移除流量曲线。包含主线渠道定时启停、渠道名称复制、账单跨页选择与每日模型阶梯展示，并补齐迁移140三张计划表的显式排序规则。标签固定6145749e，祖先核验含rc160～rc171及此前生命线刷新、异常筛选、手动关闭隐藏、超时和停止状态修复。固定源码136项Web回归、远程CI/release、三正式附件和GHCR版本/latest核验通过。需Server/Web配套更新，含新增迁移140，无Agent业务改动；未部署。

- 请求监控：`6145749e`。六档为<5、5–10、10–20、20–30、30–40、≥40 MiB；近期与历史单次查询最长24小时，历史输入使用北京时间。旧/混合响应降级四档，长范围和历史不支持时明确提示；缓存仍为按配置与范围区分的单槽缓存。理论带宽、EIP实测不在此次实现内。
- 最新主线：`5b83f12e`渠道定时启停、`77fd778c`复制渠道名称、`9c739b84`账单批量状态筛选/跨页选择/月账单每日模型阶梯明细，以及`3852ac2c`交付记录均为本标签祖先。
- 迁移修复：`171a4ef2`为迁移140三张新表补齐utf8mb4_unicode_ci；不自动转换已经存在的表。
- 生命线：逐个核验rc160至rc171标签及`69c85608`、`90ba40d5`、`08fbb823`均为本提交祖先。保留刷新与异常筛选、手动关闭隐藏、当前状态/发送记录、页签保持、超时修复及移除独立正在停止探测分类。

## 验证与限制

本轮发布前导出固定提交源码，执行requestMonitor、channelSchedule、channelScheduleEditor、billingGenerationUsers、billingMonthlyTiers、channelLifeline、lifelineCurrent、lifelinePanels和tuningRefresh共136项Web测试，全部通过。紧接本轮的提交复查已执行Go全量vet/test及Web typecheck/build并通过；同一完整SHA的远程干净CI再次执行Go vet/test/build及Web typecheck/build通过。既有大chunk提示保留。

未配置CT_MYSQL_TEST_DSN，条件化外部MySQL用例不记作实际执行；本轮未做真实SLS、浏览器或生产升级验收。长窗口真实查询性能、SLS留存范围和新定时计划表生产迁移待部署后验证。

- [CI 38471505917](https://github.com/yanceylv518/controlTower/actions/runs/38471505917)：成功。
- [release 38471990497](https://github.com/yanceylv518/controlTower/actions/runs/38471990497)：成功。

[发布与下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc172)。

## 正式产物

三个Linux安装包SHA256、ELF架构/执行位、完整内嵌提交、vcs.modified=false、Agent版本和脚本LF均通过。142个迁移与标签源码逐字节一致，迁移140含三处显式排序规则。Web成品包含请求监控新字段与历史入口、定时启停、账单选择/阶梯、复制渠道名称及此前生命线功能标识，并确认没有独立正在停止探测标签。

- `control-tower-agent-v2.0.0-rc172-linux-amd64.tar.gz`：`7e2b2f37d84c3f5667e02862d7587b5fa608a33480b21d1a71e2dde297e3426f`。
- `control-tower-agent-v2.0.0-rc172-linux-arm64.tar.gz`：`0f0280b3cdbdf2e25ae8135dd73e2225b1d53916a0ae503e896015c486097d78`。
- `control-tower-server-v2.0.0-rc172-linux-amd64.tar.gz`：`9bdcebb70435c4e2cbf498c05519e8cb5845dc033357f437c674ac1dc754b1e5`。

GHCR `v2.0.0-rc172` 与 `latest` 摘要一致：`sha256:f75c3c7c364e87e24c45b13b23230e95be8039e6e891d8ae7d9e9f933b6afce9`，linux/amd64；仅核验manifest，未启动容器。

需配套升级Server/Web并应用迁移140；从更早版本升级还需既有迁移（包括139）。发布包含Agent安装包，但本次无Agent业务变更。不包含无关本地未提交规则、运维记录或预览脚本。发布事实另提交main，标签保持固定。核验报告在`local/releases/v2.0.0-rc172/remote/VALIDATION.json`。
