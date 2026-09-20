# 调权频率与 new-api 连接重置排查

- 标识：2026-09-21-tuning-write-frequency-diagnosis
- 更新时间：2026-09-21 07:58 +08:00
- 状态：CT 暂停后无法重试及观察权重读回缺陷已修复并完成独立交付验证，修复提交6107bb17已推送origin/main，未发布/部署；reset续查已收敛至CT到ALB的特定连接/链路方向，准确RST成因仍缺连接级证据。
- 目标与验收条件：核对 CT 实际调权频率、new-api 接口限流并定位 #199 写入失败；用户随后要求先修复已发现问题，验收覆盖数据库往返、旧空时间记录自愈及十分钟退避。
- 负责会话、分支和修改范围：本会话，main / 23468bae6d23aa736fed770fbc95e59af2c4e4bc；修改mysqlstore状态字段映射、tuning重试门控及对应测试、设计与此交接/当前总览，保留其他任务既有未提交改动。newApi 项目仅只读检查。
- 实际结果与关键决策：修复LastWriteFailureAt/LastObservedWeight读回；旧write_failed空时间记录经既有条件放行一次，失败后重新十分钟退避，无需DB迁移或手工修数。现有证据不支持默认内置限流为初始故障原因；未改15秒预算、正常30秒评估频率或生产配置。
- 验证：新增回归在旧代码复现失败；修复后针对性引擎回归、独立本地MySQL 8数据库往返和真实持久化引擎重试通过（1.071s），全量 `go vet ./...`、`go test ./...` 通过。全量命令未设置DB变量，所需实库回归单独明确执行。无Web改动，未做压测或生产管理写入。
- 未验证与阻塞：已读取ALB监听基本配置、访问日志开通状态及两节点资源/渠道日志；ALB当前未开启访问日志，CT旧日志无连接复用/握手/首字节阶段信息，无故障抓包。未读取运行中new-api环境变量、CT出口全部管理请求量或核验线上custom.1二进制对应源码，不能据现有证据指定RST发送方。
- 下一步：代码已推送，按用户后续发布/部署指令交付暂停恢复修复；若继续查reset，补CT httptrace连接信息并在故障复现时做CT侧抓包/两ALB入口对照，必要时用准确时间和五元组向云厂商查RST来源。ALB访问日志需另行评估开通（控制台提示SLS按量计费），本轮未开启。

## 已核实的 CT 行为

1. `server/internal/tuning/engine.go:32` 的 ticker 为 30 秒；站点、模型、渠道在同一 Engine 内串行处理。统计窗口按分钟截断不等于每分钟只评估一次。
2. `server/internal/tuning/continuous_engine.go:507`：auto 下整数拟执行权重不同于上次成功值，或确认有外部修改时就写；无普通写入死区或最小间隔。上调比例限制会使同一公式目标分多轮爬升。目标相同则去重。
3. `internal/channelcontrol/client.go:122,154`：每次正常成功操作先 GET `/api/channel/:id`，再 PUT `/api/channel/`；GET 失败立即终止，成功后没有额外 GET 回读。`server/internal/directcontrol/store.go:35,107` 共用 15 秒超时；成功同步只改 CT 自身状态。
4. 常规基数：N 个渠道每轮都变化且调度及时完成时，约 2N 次更新/分钟，即 4N 个应用层 HTTP 请求/分钟；整轮请求连续发送，无站点级 QPS 节流。多副本、探针、优先级纠偏、人工操作及底层透明重试另计，此公式不是网络请求绝对上限。
5. 引擎设计为普通调权连续失败 3 次后每 10 分钟放行一次，成功清除暂停；但本轮随后发现 MySQL 读取漏映射失败时间，真实存储路径无法按该设计重试，见下文补充。当前客户端使用默认 HTTP Transport，允许 keep-alive 复用；无额外应用层立即重试。
6. UI 的 30 秒刷新、5 秒负载刷新主要读 CT 状态，不能直接当作同等次数的 new-api 权重写入。

## 线上只读证据（2026-09-21，Asia/Shanghai）

- 公网 `/api/status` 返回 `success=true`，`version=v1.0.0-rc.35-custom.1`，`start_time=1789217312`。仅为该次请求命中的节点声明版本，不证明所有节点或环境变量一致。
- CT 站点 pinducloud-cn 的变更记录无模型、事件、渠道筛选；本次页面加载 200 条记录，分页只读核对。
- #199 最后可见成功：06:20:28，权重 140 → 92；06:22:28 记录安全暂停，拟执行 119。暂停证据原始错误为 `direct weight write: Get "https://pinducloud.com.cn/api/channel/199": ... read: connection reset by peer`。因此该次更新在 GET 阶段结束。
- 同一 06:22:28 评估时间戳，#191、#141 有成功更新记录；记录时间是评估时间，不等于请求完成时间。
- 暂停前 180 秒窗口 `[06:19:29,06:22:28]` 共看到 8 次成功调权，按 GET+PUT 估算 16 个请求；失败尝试及其他管理调用另计。
- 完整十分钟窗口 `[06:49:00,06:59:00)` 有 42 次成功调权，涉及 #141/#191/#146，估算 84 个请求（平均 8.4 次/分钟）。这只是已记录成功调权贡献，不是服务器全流量。
- #191 在 06:56:28、06:56:58、06:57:28 连续成功调权，实证 30 秒节奏。

## new-api 接口规则及开销

核对官方 `v1.0.0-rc.35` 提交 `bee45b58a3c0b77e8dc81e6b5aeb4474aa9058d1`，及本地 `D:/CodexProjects/codex/newApi/source/new-api-rc35-seedance-assets` 的对应实现。官方当前 main 的主要结论一致。

- 渠道 GET/PUT 走 `/api` 全局限流，按 `ClientIP()` 共享管理接口桶；默认启用，360 次/180 秒，可由 `GLOBAL_API_RATE_LIMIT_ENABLE`、`GLOBAL_API_RATE_LIMIT`、`GLOBAL_API_RATE_LIMIT_DURATION` 覆盖。
- 普通 GET `/api/channel/:id`、PUT `/api/channel/` 没有额外的 CriticalRateLimit；超限中间件明确返回 HTTP 429，并非直接发 TCP reset。
- 推理 `/v1/chat/completions` 属于独立 `/v1` 路由及 ModelRequestRateLimit；截图 RPM/TPM 不是上述管理接口桶的请求数。
- 每次成功 UpdateChannel 会更新数据库并重建该渠道的 abilities；启用内存缓存时还会读取全量渠道/能力重建缓存。频繁改权有数据库和缓存开销，但本轮无资源证据证明该开销造成断连。
- 本地历史部署材料记载 pinducloud 经 ALB、双节点共享数据库/Redis；模板仍为旧版本，不能据此认定当前部署配置。

相关源码：

- [CT 调度](../../server/internal/tuning/engine.go)、[写入条件与失败处理](../../server/internal/tuning/continuous_engine.go)、[GET/PUT 客户端](../../internal/channelcontrol/client.go)。
- [new-api rc35 默认限流](https://github.com/QuantumNous/new-api/blob/bee45b58a3c0b77e8dc81e6b5aeb4474aa9058d1/common/init.go#L123-L125)。
- [限流响应](https://github.com/QuantumNous/new-api/blob/bee45b58a3c0b77e8dc81e6b5aeb4474aa9058d1/middleware/rate-limit.go)、[API 路由](https://github.com/QuantumNous/new-api/blob/bee45b58a3c0b77e8dc81e6b5aeb4474aa9058d1/router/api-router.go)。
- [渠道更新](https://github.com/QuantumNous/new-api/blob/bee45b58a3c0b77e8dc81e6b5aeb4474aa9058d1/controller/channel.go)、[模型层更新](https://github.com/QuantumNous/new-api/blob/bee45b58a3c0b77e8dc81e6b5aeb4474aa9058d1/model/channel.go)、[能力重建](https://github.com/QuantumNous/new-api/blob/bee45b58a3c0b77e8dc81e6b5aeb4474aa9058d1/model/ability.go)。

## 用户服务器日志补充与 CT 重试缺陷（07:14）

- 用户截图确认 CT 容器 `compose-server-1`，镜像 `ghcr.io/yanceylv518/controltower-server:v2.0.0-rc127`。本会话未远程执行该截图中的命令，仅解读用户提供输出。
- CT 日志使用 UTC；2026-09-20 22:xx 对应北京时间 2026-09-21 06:xx。06:20:29–30 多渠道成功（约0.2–0.3秒）；06:21:44–06:22:59，#199/#191/#146 的 GET 接连达到15秒期限；06:23:12 #199 GET reset；06:23:13 起其他渠道恢复成功。该证据说明当时多个渠道有共同访问异常，不能只按页面残留状态推断单渠道网络故障。
- `context deadline exceeded` 来自 CT 单次调权整体15秒 context 预算，GET 耗尽预算即返回，没有执行后续 PUT；不能只凭该错误区分链路、TLS、入口或后端耗时。
- 页面事件06:22:28为评估时刻，第三次失败的实际日志完成时刻为06:23:12；后续服务端日志应覆盖06:21–06:24。
- 用户随后查询最近1小时 `channel=199` 日志，末条仍是06:23:12，未显示后续预期慢速重试；与此前页面长期保留同一个 reset 错误一致。
- **确定性代码缺陷**：`server/internal/mysqlstore/tuning.go:309,312` 将 `last_write_failure_at` 扫描到局部 `writeFailAt`，却在返回前漏掉赋值 `v.LastWriteFailureAt`；`server/internal/tuning/continuous_engine.go:771` 要求这个指针非nil才放行重试，所以真实DB读取后的write_failed状态不能重试。后续整行 `PutContinuousState` 又会把nil写回数据库；只修读回字段不能自动修复所有历史已空的时间。
- 同段也扫描了 `last_observed_weight` 到 `observed` 而未回填 `LastObservedWeight`；属于邻近字段映射遗漏，应在修复时一并核验其影响，尚未实施。
- 既有 `TestWriteFailureStreakPausesThenSelfHeals` 使用内存fake，未经过该MySQL读回路径。此次仅静态核对代码及用户日志，未新增/运行测试，未修改业务代码或数据库。
- 目前需分别追踪：①首次短时超时/reset的环境原因（未定）；②网络恢复后CT暂停不自愈的代码原因（已定位）。

## 本地修复与验证（07:26）

- `server/internal/mysqlstore/tuning.go`：将扫描所得失败时间和观察权重回填状态；保留NULL语义，观察权重0不会误当未设置。防止下一轮整行写回丢失失败时间和观察事件锚点。
- `server/internal/tuning/continuous_engine.go`：对 `write_failed` 且失败时间缺失的历史状态允许下一次合格尝试。仍遵守自动模式、样本/基线、多模型隔离等条件；失败立即建立新的失败时间，十分钟内不再尝试；成功或目标已无需变化时清除暂停。不使用每轮变化的UpdatedAt作为退避起点。
- 新增引擎测试覆盖旧空时间的成功、无需写入、无证据、多模型保护、再次失败后每30秒评估仍保持十分钟退避、边界重试及不重复暂停事件。
- 新增 `tuning_state_integration_test.go`，独立本地MySQL 8执行真实Put/List往返，覆盖失败时间、观察权重非零/零/NULL；Engine.Tick经真实状态持久化验证正常暂停与旧空时间记录的失败、等待、到期成功。仅指标和外部写入响应使用确定性替身，未访问new-api。
- 旧代码验证：引擎无法恢复旧暂停、真实MySQL读回丢失字段均复现；修复后上述测试及全量Go质量门通过。临时测试容器只承载本任务空库，验证后移除。
- 本次修复解决网络恢复后CT不再尝试的问题，不证明最初TCP reset原因已解决；需要部署Server后才会影响线上渠道。无新增数据库迁移，无Agent/Web变更。

## reset只读续查（07:36）

用户确认当前是ALB后多节点部署。本轮通过已登录Chrome读取CT容器日志、系统状态及阿里云ALB控制台；未通过SSH执行命令，未开启日志收费服务、调整监听、修改权重或重启容器。

### 真实环境及配置

- ALB：`pinducloud-newapi-alb / alb-j56kbginhhag3rc6hr`，杭州；B区EIP `47.111.13.226`、VIP `172.16.145.72`，I区EIP `47.97.46.32`、VIP `172.16.50.53`。成功访问的Nginx前一跳分别为 `172.16.145.73` 和 `172.16.50.54`；后者在控制台Local IP栏直接可见。
- HTTPS监听 `lsn-m98n9u2ng75oa7neps`：HTTP2已开启，连接空闲超时3600秒、连接请求超时3600秒，监听ACL未开启，实例WAF防护未开启。此为读取时配置，未以操作历史验证故障瞬间完全一致。
- 后端HTTP、加权轮询、会话保持关闭，当前健康检查正常。ALB访问日志页仅显示“创建访问日志”，未配置可查的访问日志；事件中心最近30天显示0条。事件列表为空不证明不存在瞬时连接故障。

### 日志证据

CT按Asia/Shanghai查询2026-09-21 06:20:00–06:25:00、关键词 `/api/channel/`：

- 应用日志两个来源完成扫描，均0匹配；不能以应用日志缺失独立推断请求未到后端。
- Nginx访问日志四个来源均完成，各14行，总56行。每节点有JSON `new-api-access.log` 与 `newapi-timing.log` 两份重复记录，去重后为28笔请求：节点2的14笔GET、节点1的14笔PUT，全部200。
- 故障前06:20:29，GET `/api/channel/199` 的 `forwarded_for=124.220.201.99`、`remote_addr=172.16.145.73`、`request_time=0.034`、`upstream_status=200`；同轮191/141/146 GET耗时0.026/0.027/0.036秒。PUT约0.134–0.196秒。
- 从该轮结束至06:23:12恢复前，四来源中没有CT截图里连续失败的GET记录，也未见该路径429/5xx。该负面证据限定于这些来源、时间和路径；日志遗漏或延后完成不能完全排除。
- 06:23:12 GET191成功，`remote_addr=172.16.50.54`、耗时0.035秒；随后PUT191于06:23:13成功。恢复后GET为0.025–0.038秒，PUT约0.125–0.180秒。两后端角色与故障前一致，变化的是ALB转发来源。
- 两个Nginx错误日志来源完成扫描，在相同窗口/渠道关键词下均无匹配。
- 为区分B区整体故障和CT特定连接问题，另查节点2的JSON访问日志，06:21:30–06:21:40、关键词 `172.16.145.73`：扫描完成38行、均200（读取2.68MiB）。包含按结束时间减request_time估算在该窗口内新发起并成功的推理请求，以及06:21:34完成、耗时0.031秒的GET `/v1/models`。不记录其他客户IP/完整日志内容到交接。

### 资源与ALB监控

- CT六小时原始采样中，06:20–06:25节点1 CPU约5.6–7.3%、内存4.0–4.4%；节点2 CPU约11.6–15.6%、内存3.8–4.4%，采样连续，网络仍有流量。没有CPU/内存整体耗尽证据，但这些采样不能排除短时尖峰、进程内锁或单连接问题。
- ALB B区VIP三小时图表覆盖故障窗口；06:21–06:23附近未见明显2xx流量中断或5xx集中上升。仅为图表趋势观察，未导出秒级原始点，不能证明该条TCP连接正常。

### 当前推断与证据边界

“CT到ALB B区的一条连接/特定公网路径异常，等待期间请求没有形成可见后端访问记录，连接reset后经I区恢复”最符合目前证据。CT采用默认共享HTTP Transport，访问日志UA为Go-http-client/2.0且ALB启用HTTP2，使HTTP2长连接复用异常成为待验证假设；旧日志没有每次请求local/remote端点及reused字段，不能确认所有超时都复用同一连接，也不能将切换前一跳等同于已证明客户端远端EIP切换。

现有证据不支持“权重写频繁触发new-api限流”“15秒配置直接产生reset”“B区或new-api整体不可用”作为已确认根因。实际RST可能来自ALB、链路中间设备或连接状态异常处理，尚未定位发送者。

### 下一次故障需补的证据

1. CT出站GET/PUT记录 `httptrace.GotConn` 的local/remote端点、Reused/WasIdle/IdleTime、TLS协商协议，以及DNS/TCP/TLS、WroteRequest和首字节时序。只记录方法/路径及时间，不记录Token、请求头/体。
2. 在CT服务器同一容器网络环境中，对两个ALB EIP用相同域名/SNI做低频GET对照，比较HTTP1.1/HTTP2；公共 `/api/status` 只能验证基本网络，不能代表鉴权渠道接口全链路。不得通过反复PUT调权做压力复现。
3. 复现时在CT侧限定两ALB EIP及443抓包，结合TCP重传、RST方向与ALB/运营商支持查明原因；没有历史pcap无法事后还原故障包。
4. ALB访问日志可补 `client_ip`、`upstream_addr/status`、`request_time` 等入口证据，但尚未启用，本轮未擅自创建SLS计费资源；也不能回补故障时历史日志。

参考：[ALB实例控制台](https://slb.console.aliyun.com/alb/cn-hangzhou/albs/alb-j56kbginhhag3rc6hr)、[ALB访问日志字段](https://www.alibabacloud.com/help/zh/slb/application-load-balancer/access-logs)、[Go httptrace](https://pkg.go.dev/net/http/httptrace)。本轮只读诊断与文档，无新业务代码，未重跑此前修复测试。

## 独立提交验证（07:58）

- 用户要求先提交推送刚才的修复；仅纳入本任务四个Go文件、调权设计、本交接及当前总览中的本任务条目，其他归档/账单/电话等改动保留。
- 从与origin/main一致的23468bae建立隔离检出，仅复制这次修复，重新运行 `go vet ./...`、`go test ./...` 均通过，确认不依赖其他未提交功能。
- 在隔离检出和独立临时MySQL 8空库重新运行 `TestContinuousStateRetryFieldsMySQLIntegration`，通过（8.375s，含迁移）；未连接生产数据库或new-api。
- 用户后续提供的新-api日志中的reset目标为推理上游，与CT访问ALB的管理连接不同；展示的新-api节点内核过滤无匹配，用户报告CT内核检查也无相关记录。不能据此确定RST发送方，尚未实现httptrace或抓包。
- 本次不修改15秒超时、HTTP2或正常写入频率，不创建发布标签或部署。

Workspace 总结仍待用户本次明确确认，未上传。
