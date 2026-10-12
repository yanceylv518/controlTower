# 常态探针启停开关

2026-10-12，用户要求可以关闭常规探针，关闭后不再定时发送，探针也不参与调权。基于 main / be258d42d 实现，按用户要求提交推送 main；提交范围仅含本功能及配套测试/记录，结果以 Git 记录为准。未发布或部署。

## 行为与范围

- 新增站点规则 `continuous.routine_probe_enabled`，默认 `true`。旧 JSON 缺少字段时默认开启，显式 `false` 保存、读取后保持关闭，不需要数据库迁移。
- 页面入口：调权中心 → 规则设置 → 运行规则 → 探针与恢复 → 启用常态探针，点击保存更改生效。
- 关闭后取消该站点所有常态任务，定时探针和错误后加速探针都停止；调度器下一轮配置检查处理，现有检查周期为 5 秒，数据库等耗时另计。取消在途请求使用原有 canceled 结算，不作为慢请求、渠道错误或调权样本。
- 已启动的常态任务若尝试触发熔断，须再次核验最新策略；已关闭时拒绝旧结果触发熔断。关闭不撤销已经确认的熔断，也不抹除历史记录。
- 后续普通评估不再查询探针速度样本、不建立探针基线，探针系数为 1，探针证据不构成普通调权的最低样本资格；综合倍率函数也独立排除旧缓存探针系数。真实流量的速度、缓存、输出和错误系数继续按原规则计算。
- 下一轮普通评估使用新配置；不因关闭开关直接重置线上权重。历史评估仍按其参数快照解释，渠道覆盖提示按已保存开关显示“常态探针已关闭”，未保存草稿不改变运行状态显示。
- 熔断后的恢复探测与恢复初始权重流程不受此开关影响。重新开启后按原有错峰、频率、有效期和最少样本规则运行；历史记录仍可查看。
- 本次没有新增 max_tokens 参数，没有 Agent、NewAPI 或数据库结构改动，需要配套更新 CT Server 与 Web。

## 验证

- 新增 `routine_toggle_test.go` 六项回归：缺字段兼容及 false 序列化；关闭时不启动和拒绝迟到故障；排除已有样本但真实流量继续升权；缓存探针系数与基线排除；熔断恢复完成；运行中的真实调度循环在关闭后取消请求、记录 canceled 且不再发送或误熔断。
- `go test ./server/internal/tuning -run 'Test(RoutineProbeToggle|DisabledRoutineProbe|RoutineSchedulerCancels)' -count=1`：通过。
- `go test ./server/internal/tuning ./server/internal/dashboard ./server/internal/mysqlstore ./internal/channelcontrol`：通过。本轮未使用实库连接执行集成套件。
- `node --test packages/desktop/tests/tuningRefresh.test.mjs`：58 项通过，包括新增旧配置兼容/保存/重载/重新开启、关闭快照排除旧系数和探针独立样本资格。
- `pnpm build`：Vue 类型检查与生产构建通过，保留既有大包提示。
- 本地只读示例浏览器检查：开关默认开启，关闭后显示未保存更改，取消后恢复开启；没有提交示例表单或修改实际站点。截图 `.tmp/lifeline-refresh-integration/routine-toggle.png`。
- 本次修改文件 `git diff --check` 通过。未连接生产验证或改变生产配置。

## 交付边界

本轮文件：`server/internal/tuning/{business_rules,continuous_engine,probe_speed,routine_probe}.go`、新增 `routine_toggle_test.go`、`webapp/packages/shared/src/api/dashboard.ts`、`webapp/packages/desktop/src/views/ContinuousTuningView.vue`、`webapp/packages/desktop/tests/tuningRefresh.test.mjs` 及本记录/进度。

工作区另有尚未提交的渠道生命线刷新优化、异常筛选及其他既有改动，应精确区分。当前已发布 rc164 不含此开关。

## 提交核对

2026-10-12 已获取远程 main，提交前 HEAD 与 origin/main 均为 be258d42d，无需合并。仅选择上列 10 个文件；`PROJECT_PROGRESS.md` 只暂存本开关条目，不带入同文件其他待交付内容。代码与上一轮已通过测试/构建的版本一致，未重复运行不变的检查。渠道生命线刷新/筛选、实验迁移 097 及其他未提交工作保持本地。
