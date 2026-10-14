# 熔断事件通知停滞排查

- 日期：2026-10-15，北京时间；只读生产检查，未发送群消息、未修改配置或数据库。
- 用户报告：渠道生命线存在 06:05:08 权重 500→0，但群未收到预警。
- 已定位：pinducloud-cn 渠道 331 alphaic_kimi-k3；决策 tun-1792015508472853937-pinducloud-cn-331-circuit_opened，rule=circuit_opened，auto_executed；命令 08c2960bc0822d36d216b7807c94160e 为 succeeded，实例所属站点正确且未删除。
- 通知配置：该站点预警接收渠道启用，订阅 channel_circuit_opened 与 channel_circuit_recovered。未读取 webhook 或密钥。
- 决策没有对应 circuit_alert_events 及 SHA1(circuit:决策ID) 告警；不是已生成后投递失败。熔断事件最新 UTC 2026-10-12 09:00:04.134；发送记录最新 UTC 2026-10-12 09:00:49.216643。该存量时间不能单独证明中间每次执行均失败。
- rc173 日志 23:22:05、23:23:07、23:24:09 UTC 连续出现 MySQL i/o timeout 和 alert notification pass failed: invalid connection。
- 现场 PROCESSLIST 捕获 SyncCircuitAlerts 的事件导入查询 executing 86 秒；查询从 __activation__（2026-09-14）开始反关联查找未导入事件。EXPLAIN 含 Using temporary / Using filesort，缺少按熔断规则及状态范围筛选的专用索引证据。
- 代码：server/internal/dashboard/notification_runner.go 的 RunOnce 在 SyncCircuitAlerts 返回错误后终止，后续通知派发不执行。server/internal/mysqlstore/circuit_alert.go 使用 context.Background 查询，无独立服务端执行期限。
- 结论：本次符合通知条件，但告警事件未生成；当前事件导入慢查询与读超时阻断了通知轮次。不能声称 rc173 首次引入，因为目标事件早于本次升级且存量发送早已停滞。
- 下一步：优化事件导入候选查询及索引、限制查询生命周期，验证分页/幂等/迟到回执/恢复顺序与通知隔离；修复恢复时明确积压通知处理策略，不能擅自补发历史群消息。本轮尚未修改实现或运行测试。

## 修复与验证（后续授权）

用户授权同时修复通知查询、渠道历史清理超时，检查后提交推送并远程打包；追加要求解释为何突然发生。以下更新替代上文仅排查状态。

根因证据：生产表统计估计调权决策约58万条、命令约51万条、审计约45万条；检查时 data_lock_waits=0。通知导入从9月启用边界反复查找，普通调权占多数但原查询没有熔断筛选组合索引。清理执行计划估计扫描约22万条，LIMIT应用在保护条件之后，不能限制实际候选检查数。现场同时观察通知查询47秒、清理查询8秒。rc172到rc173通知导入SELECT未改变，只有告警写入增加总览证据字段；清理属于rc173新增。数据积累与扫描方式造成性能脆弱性已有证据，首次恶化时刻、统计计划变化或缓存/磁盘影响尚不能确认，不能归因为用户操作或宣称首次由rc173引入通知故障。

实现：

- 新增143_circuit_import_index.sql，以rule/mode/status/created_at/id组合索引分别读取熔断、恢复事件；固定从决策范围驱动主键关联，每种最多500条，合并时间和ID排序后每轮最多500条，保留事件账本幂等、迟到回执与恢复顺序。
- 导入事务15秒上下文、候选查询3秒服务器执行期限；已生成事件待发送查询5秒上下文及3秒执行期限。事件导入失败仍继续处理其他已生成告警，错误注明导入/查询/发送阶段。
- 清理先按范围索引锁定最多1000个候选，SKIP LOCKED避免等待别的任务持有的候选锁，再强制按主键对这批候选执行全部原保护条件，最后仅删除通过检查的主键。每条候选/保护查询1秒服务器期限，外层仍受5秒worker预算约束。
- 内存游标按表/所属实例/命令状态轮转，保护记录也推进游标，扫到尾部重置；提交失败不推进，重启从头重新扫描，不改变删除条件或保留天数。近期完成/关联、在途、当前任务和非渠道审计保护保持。
- 不修改群配置，不手动补发、删除生产记录或部署。部署后按既有规则恢复积压事件处理，未新增过期丢弃策略。

验证：隔离本机MySQL9.7库ct_notification_fix_20261015，已通过全部ChannelHistoryRetention专项、熔断生命周期与迟到/幂等回归；新增1000条关联受保护审计之后的过期记录可继续清理，解除保护后旧记录可被重扫清理。新增10000条普通调权加501个交错熔断/恢复事件，两轮处理不漏不重，第一轮500个事件约0.43秒（仅本机合成，不代表生产性能）。通知导入失败后已生成恢复消息仍可投递的本机HTTP回归通过，未调用真实群机器人。正式源码快照全量Go检查结果及发布事实在后续补充。

正式源码快照 go vet ./...、go test ./... 全部通过；未提供DSN的全量运行中外部条件测试仍跳过，专项实库结果以上述隔离验证为准。无Web产品改动，远端CI仍执行Web全量门禁。生产MySQL8新增索引耗时及真实负载下延迟需部署后验收。

追加只读性能证据：用户指出几十万条数据不应直接等同于超时，补查确认生产 innodb_buffer_pool_size=134217728（128 MiB），调权表及索引约1072 MiB，命令约185 MiB、审计约327 MiB（information_schema统计估计）。vmstat两次一秒采样读取约43872/43968 KiB/s、iowait均24%；关联ID/command_id/event_id/correlation_id均为utf8mb4_unicode_ci，无排序规则不一致证据。缓存偏小和现场I/O压力是重要放大因素，尚无历史采样证明首次恶化的触发点；全局累计物理读数不能当作本次查询独占耗时。未更改生产MySQL缓存或连接参数。

提交8757ac168a6f9f6397819fa804f3646390c63694已推送main；对应远端CI38655187005成功，包含Go及Web质量门。核验正式Go/SQL文件与本机已验证源码快照一致。本轮专用隔离数据库已删除，其他数据库未动。

## rc174 发布完成

- 标签v2.0.0-rc174固定8757ac168a6f9f6397819fa804f3646390c63694，核验rc173为祖先；未夹带本地规则、历史文档或产物。
- [CI38655187005](https://github.com/yanceylv518/controlTower/actions/runs/38655187005)与[release38655460741](https://github.com/yanceylv518/controlTower/actions/runs/38655460741)均成功。
- [发布与下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc174)。三个正式安装包SHA256、ELF架构/执行位、完整提交及vcs.modified=false、Agent版本、脚本LF核验通过；146份迁移与固定提交逐字节一致，既有Web功能标识核验通过。
- GHCR版本/latest均为sha256:3a7672bd5e3ed28dfc6b340f165cabccdc2fef11932ec0b67931cf873dec5920，linux/amd64。核验报告local/releases/v2.0.0-rc174/remote/VALIDATION.json。
- 需升级Server并执行143索引迁移；Web/Agent产品代码未变。生产仍由用户决定部署，未执行远程重启、迁移、数据库清理、缓存调整或群消息补发。生产性能与索引创建耗时待升级验收。
