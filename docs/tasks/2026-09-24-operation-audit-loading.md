# 操作审计加载慢排查

## rc136打包发布（2026-09-24）

- 用户先要求打包；自动审批认为远程标签会触发公开Release/GHCR而拒绝，已向用户说明。用户随后明确回复“确认”，据此创建并推送v2.0.0-rc136，固定功能提交e02737fbd7e47ea079a60dbd2a7c0958b04e1b07。
- CI [35960442242](https://github.com/yanceylv518/controlTower/actions/runs/35960442242)成功；release [35960671835](https://github.com/yanceylv518/controlTower/actions/runs/35960671835)成功，包括安装包构建、GHCR版本/latest镜像推送、GitHub Release创建。
- [正式Release](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc136)非草稿，三个安装包及SHA256SUMS均uploaded。已下载到dist/releases/v2.0.0-rc136；逐包校验SHA256一致、ELF架构与可执行权限正确，Agent包含rc136版本及LF安装脚本，Server含前端和095迁移。本地此前交叉编译也通过，最终交付以CI附件为准。
- 本版还包含cb6e207c1熔断禁用恢复及bcda77166命令清理/Agent诊断补修。升级Server需093–095，审计前后端随包配套；Agent诊断增强需升级Agent，单独审计修复不要求Agent更新。未执行生产部署、生产DDL或Linux实机安装。
- 发布记录作为后续文档提交保存，不改变已发布标签的功能提交。

## 提交验收（2026-09-24）

用户明确授权提交并推送。提交前fetch确认main与origin/main均为bcda77166，本次仅纳入审计优化及配套测试、095迁移和文档，共17个文件。最终前端类型/生产构建及diff检查通过（既有chunk提示），此前本轮Go五包、15项前端、独立MySQL和Chrome验证见下文，未重复生产或手机验收。提交与远程接收结果以Git记录为准；下方“未提交”保留阶段历史。本次不发布版本或部署，仍需Server/Web及095配套升级。

## 已实现与验证（2026-09-24）

用户确认默认当天，并要求一并完成其他优化。已本地实现，未提交、发布或部署；下方只读诊断是先前阶段记录。

- 页面默认本地当天，日期与全筛选重置均回到当天首屏；接口仍使用UTC左闭右开时间范围。
- 列表请求 `list_only=true`，读取limit+1并返回has_more；总数独立异步 `count_only=true`，失败可单独重试、不遮挡列表。前后端30秒缓存避免翻页和频繁刷新反复计数；服务端每Store缓存128项、最多一个计数在途，等待继承取消。
- 翻页改为上一页/下一页游标，保留服务端微秒时间及ID，页数/筛选变化清空游标，旧列表/计数响应隔离。原API默认offset+total行为保留兼容，新页面不再提供任意深页跳转。
- 新增095：按原SQL人工审计判断定义STORED生成列，建立 `(is_manual_audit,created_at,id)` 及 `request_id` 索引；不删除或重分类历史事实。既有同名system/agent人工账号、模块类型与转义筛选实库回归通过。
- 查询继承HTTP context，列表/计数8秒预算，操作人候选5秒；候选查询也限定已应用日期，切换日期取消旧候选。无站点条件时不再JOIN instances。
- 增加“请求ID精确”检索模式；普通内容搜索保持模糊语义。

验证：

- 相关五包（dashboard/ingest/mysqlstore/httpapi/storage）Go test与vet通过。默认缓存目录访问被拒后改用工作区GOCACHE，没有改变业务代码规避。
- 15项审计前端回归及类型检查通过，生产构建通过（既有chunk体积提示）。覆盖异步计数不阻塞、游标微秒、末页、筛选/页大小竞态、取消、计数失败重试及本地日期边界。末次候选取消补充后复跑专项和类型检查，浏览器/构建使用此前同版主流程。
- 独立本机MySQL8.0.46（127.0.0.1:33429，ct_audit_perf）执行全部迁移，原审计实库筛选回归、同微秒ID分页/翻页间新增记录及连接池等待取消通过，未连接生产。
- 10万条合成记录（20%人工、80%自动）实测：当天列表1.816ms；当天2720条人工记录计数1.044ms；旧人工筛选同日计数94.362ms；缓存计数低于本次计时显示精度。EXPLAIN确认列表使用manual_created索引且无filesort，请求回填使用request索引；全夹具新旧人工条件零差异。这是开发机合成数据结果，不是生产SLA。
- Chrome本地合成API验证默认当天、列表先出/统计后补、20→20→5分页/末页、重置首屏及精确请求ID返回1条；桌面布局已检查，console无error。未做手机/生产验收。

部署：需配套升级Server及Web并执行095，不需升级Agent。STORED列与索引首次构建涉及历史表处理，应预留迁移时间，生产耗时尚未测量。现有审计数据保留；未执行生产DDL或发布。其他任务在本轮期间提交了熔断/死锁修复，最终基线已到bcda77166，未覆盖它们。

复现：隔离库设置CT_MYSQL_TEST_DSN后运行 `go test ./server/internal/mysqlstore -run 'TestQueryOperationAuditsTypesAndTimeRangeMySQLIntegration|TestOperationAuditCursorCancellationMySQLIntegration' -count=1`；大数据夹具另设置CT_AUDIT_PERF_TEST=1并运行TestOperationAuditPerformanceMySQLIntegration，自动只清理自身前缀数据。

2026-09-24，main（检查时 HEAD 9bf446e86）。本次为诊断，未修改业务代码、数据库或生产配置；保留其他任务的未提交改动。

## 现场与代码证据

- 通过已登录 Chrome 只读打开生产 `/audits`，初始列表尚未返回，随后显示 80,294 条、4,015 页、每页20条。一次手动查询观察到按钮禁用后恢复；跨工具调用观测完成上界约7.3秒，包含工具调度间隔，不能作为准确HTTP/SQL耗时。浏览器运行时不能读取Resource Timing，未取得网络分段耗时。
- `AuditsView.vue` 默认时间范围为空，列表请求不传站点/实例条件；顶部选中站点不限制此全局审计列表。首页每30秒可见时刷新，翻页/展开详情时暂停定时刷新。
- `mysqlstore/command_store.go` 的 `QueryOperationAudits` 先执行精确 `COUNT(*)`，成功后才查20条。计数和列表都应用人工审计的多字段 COALESCE、否定条件与 OR；默认没有时间边界。LIMIT不能限制前置计数工作量。
- 列表按 `created_at DESC,id DESC` 排序。仓库迁移仅有 `(instance_id,created_at)`、`(actor_id,created_at)`、`(correlation_id,created_at)` 和单列 `operation_type` 索引，缺少以全局时间排序为前缀的索引，也缺少覆盖人工筛选的索引。代码所声明结构支持大范围扫描/额外排序的判断，但生产实际索引、行数及执行计划未读取。
- 列表与计数使用 `context.Background()`，未承接HTTP请求取消；前端Abort不能终止对应SQL。操作人候选查询虽有5秒超时，也不随HTTP取消。
- 附带发现 `UpdateOperationAuditHTTPStatus` 按 `request_id` 更新，但仓库没有该列索引；中间件在部分人工操作响应提交前同步执行该更新。这是同表扫描/锁开销风险，尚无证据证明它占据本次列表耗时的具体比例。
- 当前 `PruneBefore` 支持表列表不含 operation_audits，未发现此路径的审计保留清理；历史规模可能持续增加，不能擅自删除审计记录。

## 结论与后续

优先优化审计读路径：让首屏列表不被全历史精确计数阻塞；为全局时间排序和人工审计筛选设计可用索引，保留旧记录/同名人工账号的识别语义；补 request_id 索引并贯通请求context。默认时间范围属于产品行为，若调整必须在界面明确展示。只新增时间索引不能消除全历史COUNT成本。

上线前应通过独立MySQL大数据夹具比较计数/列表/回填更新的EXPLAIN和耗时，再验证权限、筛选、分页、取消和历史人工记录兼容。生产还需只读SHOW INDEX、表规模、慢SQL或EXPLAIN确认实际瓶颈；当前不宣称已证实某条SQL的生产耗时或已修复。

本次没有代码变更，未运行单元测试或实库压测，未提交、发布、部署。早期development-progress阶段表与当前代码存在历史差异（Vue根路由、Linux发布及自动调权已实现），本次按最新代码/交接判读。
