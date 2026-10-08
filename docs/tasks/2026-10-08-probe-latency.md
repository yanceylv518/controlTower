# 恢复探测30秒门槛与后台并发

> 后续提交整理：2026-10-08用户授权提交并推送。按main精确整理本任务及二次检查修复，排除独立错误统计等原有本地工作；提交/推送结果以Git记录为准。下文“未提交”描述各次实现/检查当时状态；本次不包含发布或部署。

提交前已从Git暂存区导出独立源码副本，8个相关Go包测试全部通过，确认不依赖未提交的独立错误统计等功能；暂存差异检查通过。

- 日期：2026-10-08，Asia/Shanghai；main基线3dd343002。
- 用户决定：主动探测请求满30秒未返回就计数，不等待返回；连续两次触发禁用。采用同渠道逐次、无额外5秒间隔，不同渠道有限后台并发。用户最终要求“按这个先实现”。
- 状态：本地源码与相关回归完成，最终检查结果见下；未提交、发布、迁移或部署。原独立错误统计等未提交工作保留。

## 现行行为

1. Client在认证完成后，对每次渠道测试HTTP设独立30秒deadline；到点取消客户端等待，输出Slow证据。及时返回但NewAPI报告耗时超过30秒也算慢，成功不进入恢复统计。认证失败、父级截止和进程取消不算慢探测。
2. Server与Agent复用同一轮次实现；每次结束立即开始下次，旧间隔配置不再执行。30秒内返回打断连续慢计数，连续2次慢时提前停止，无需完成原10次；既有快速失败计数及完整全失败禁用保留。
3. 本轮早前即使有成功请求，2次连续慢证据也优先触发status=2禁用。仍走既有真实回执链路，失败重试、静默及后续恢复不绕过。已经禁用的慢轮次不得被早前成功率错误启用。
4. Server直连在后台执行，新增4个共享探测名额。Agent常驻模式新增4工作者/64排队槽、命令ID去重和同渠道互斥，独立于默认约5秒采集pass；完成结果在后续正常报告中进入既有缓冲/重试链路。监控采集与其他渠道命令继续执行。
5. Agent后台任务随根运行context取消；RunOnce保留同步执行与父级预算，中断不作为慢渠道证据。后台结果尚未进入报告缓冲前若进程退出可能丢失，Server沿用探针丢失超时重新开轮，不把无回执当成功。
6. 新增probe_slow_streak协议/状态/事件证据和121迁移。SQL已完成轮次标记保护同时覆盖慢证据，旧tick不能抹掉结果，旧轮结果不能写入新轮。默认0兼容旧数据，但旧Agent不具备新规则，完整链路须配套升级。
7. Web移除可编辑“探测间隔”，替换为30秒/连续2次/后台并发说明；基础策略默认间隔0，旧保存策略继续可读，执行端忽略旧间隔。

两次无响应约60秒产生探测判断；实际禁用还受排队、Agent报告、Server评估（现有30秒）及禁用写入确认影响。取消客户端请求不保证NewAPI上游已停止处理。5分钟熔断静默仍保留，和本次去掉的5秒探测间隔是不同参数。

## 验证

- 新增/扩展测试：未返回HTTP到deadline立即返回（测试短deadline、服务端保持不返回）、迟到成功不计成功、两次慢提前停轮、快成功/快失败重置、父级取消不误禁用、早前成功不能放行、禁用等待回执、已禁用慢轮不恢复、后台任务不受采集取消影响、命令去重及最多4路并发。
- 8个相关Go包test通过：channelcontrol、Agent主程序/reporter、tuning、directcontrol、mysqlstore、ingest、agentgateway。
- `go test ./agent/... ./server/... ./internal/...`与相同范围go vet全部通过（正式源码完整范围）；53项调权Web回归通过；默认Git换行配置下diff检查通过。
- 新增SQL实库回归覆盖结果持久化、旧tick保护与新轮清零；未配置CT_MYSQL_TEST_DSN，按门控跳过。Docker daemon本轮不可用，未安装或启动数据库；121生产迁移耗时与真实NewAPI取消行为未验收。
- 根`go test/vet ./...`误扫描local/backups中的旧源码副本，因internal导入/不完整包失败；保留备份，改用三个正式源码目录执行完整范围检查，不将备份问题改动混入本任务。
- Web正式build在vue-tsc阶段被同步前已确认的权限预设两处done隐式any阻断（PermissionPresetManager.vue:171、UsersView.vue:302）；两文件本轮未改，不能报告完整build通过。
- 本轮未跑race、浏览器、生产持续流量或跨Server并发；未访问生产配置或写真实NewAPI。

## 升级与源码范围

### 二次检查（2026-10-08）

- 发现并修复：RunProbeRound在检查父级取消之前递增Attempts，导致配置1次或第10次恰好被取消时，可能被既有“完整全失败”规则误禁用。改为父级未取消才计入已完成请求，30秒独立请求超时仍正常计慢。
- 新增配置1次/10次、末次取消的回归测试，并修改首个请求取消的断言；修复前测试失败，修复后通过。
- 重新通过channelcontrol、Agent主程序/reporter、directcontrol、tuning、ingest、mysqlstore、agentgateway共8包test/vet。沙箱构建缓存权限阻断后，经工具审批正常运行；race因CGO未启用未执行。数据库门控、真实NewAPI和部署验证限制仍然适用。
- 二次检查未改其他业务功能，仍未提交、发布或部署。

先配套更新Server/Web及121迁移，再更新Agent。没有发布包或上线动作；生产应检查slow streak事件、status=2真实成功回执、后续探测与status=1软启动恢复，不能仅看命令“succeeded”判探测成功。

主要入口：internal/channelcontrol/client.go、probe.go；Agent probe_dispatcher.go、command_executor.go；Server directcontrol/store.go、tuning/continuous_engine.go、circuit_status.go、model.go、mysqlstore/tuning.go、ingest/service.go；双方contracts；Web ContinuousTuningView及共享状态类型；121迁移、回归与本文档/API文档。
