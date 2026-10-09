# ALB 请求监控实现（2026-10-09）

## 状态与范围

用户在使用页设计后明确“先实现一下”。本次已在 main 工作区实现正式使用页及只读 API；未提交、推送、发布、部署，保留其他会话未提交的错误统计及相关改动。现有 HEAD 为 de8ada349，线上截图的 rc157 不包含本功能。

入口：监控分析 → 请求监控（/request-monitor）。不加入 CPU/内存、客户关联、在途请求、排队疏通或自动限流。

## 页面

- 默认近30分钟，可选15/30/60分钟；自动30秒刷新，可关闭，隐藏页暂停定时查询，离开页面取消浏览器请求。
- 大请求三条曲线：5–10、10–20、≥20 MiB；<5 MiB单独纵轴。四档均统计，未知大小单列。
- Host筛选包含域名、IP及空Host，不限制主域名。Host表固定显示当前时间范围全部Host，点击只筛选ALB图表，不改变渠道数据。
- 滑杆/曲线点选分钟联动数量和字节摘要；图例显隐不改变总数。
- 请求和响应体字节分别使用实际 request_length/body_bytes_sent 聚合，不按档位估计，不代表网卡实时带宽、CDT账单或上游转发出流量。
- 渠道卡片最多5条，支持请求量/延迟排序和统一纵轴的分钟首响应趋势，包含实例名称避免跨站同名混淆。
- 未配置、无日志、SLS未完成/失败、日志时间落后、浏览器刷新失败/过期各有明确提示；ALB不可用时渠道仍可独立展示。
- 全局ALB及全局CT渠道；明确说明顶部站点选择不影响本页。暂无ALB到CT站点或业务渠道的映射。
- 设置页支持 /settings?tab=external 直达已实现的加密接入配置，保存说明同步更新。

## 数据与接口

### ALB

GET /api/dashboard/request-monitor

从数据库读取上一阶段的单个ALB配置，用同一专用RAM凭证访问公网SLS GetLogsV2。固定查询最近一小时，按分钟、Host聚合；浏览器时间/Host筛选不重复请求SLS。不查询原始日志或请求体内容，不新增日志副本或后台常驻采集任务。

- 四档边界使用5242880、10485760、20971520字节（MiB）；边界归入更大档。
- try_cast处理非法字段，缺失/负数大小独立统计；实际字节仅累计非负有效值。
- 总请求数必须等于四档加未知；校验非负/整数/JS安全范围、时间范围、重复分钟Host、最新日志时间。
- 单进程共享30秒缓存及单个在途查询；失败结果也短暂缓存，避免故障时放大查询。
- 缓存按完整连接字段隔离，修改数据源或凭证后不能复用旧源结果；单个查看者离开不取消共享云端查询，云端总超时15秒。
- 查询最多重试3次Incomplete，总超时不叠加；gzip解压后最多4MiB；返回超过6000个分钟Host组合时明确报错，不静默截断。
- 最近两个完整分钟、当前分钟和最新日志所在分钟暂缓展示；显示暂统计到哪一分钟，不声称日志完整。SLS查询Complete只是本次计算完成，不能证明日志投递完成。
- 缺分钟留空，不补零；每次重新查询滚动窗口，可回补迟到数据；最新日志落后超过180秒提示延迟。
- 不在页面打开期间不执行定时SLS查询；多个Server副本各自缓存，未实现跨副本缓存。
- 近60分钟查询属于有界的初版方案；大量Host超过上限时需后续服务端分页/持久聚合设计，而非呈现错误总量。

### 渠道

GET /api/dashboard/request-monitor/channels?sort=volume|latency

- 来源既有 CT metric_1m 的 instance_channel 维度，不通过ALB upstream_addr冒认渠道。
- 窗口为服务器当前分钟前一完整分钟作为排他结束边界，向前5分钟；SQL限定上下界、维度、最多10001条，超过10000明确报错；查询超时5秒，支持取消。
- 按实例ID+渠道键分组；P95从5分钟TTFTBuckets V2合并计算，绝不平均分钟P95或把旧版MAX作为真实P95。
- 缺TTFT计数/直方图、计数不一致、负数、重复分钟等渠道不排名，页面显示排除数量。
- 固定首版门槛：5分钟请求≥100、有效首响应样本≥20、P95≥10秒；不足5条不补足，真实负载验收后可调整。
- 近似分位数落入开放尾桶时显示≥90秒，避免伪装为精确值。分钟趋势仍为直方图插值估计。
- 每条显示最新指标分钟，整个窗口无指标不声称渠道健康；返回查询时间和最新指标时间，不与ALB日志更新时间混用。
- 仅加载筛选后至多5条卡片；不会把大请求数量关联为某渠道的请求量。

## 权限和交付

新增独立 monitor.requests 权限（说明包含全局ALB及重点渠道），接入凭证配置仍是 settings.manage。两个查询API要求真实管理员会话，拒绝普通用户和旧Bearer；菜单、权限树和前后端路由一致。保持原默认登录页/移动端默认入口顺序。

需更新Server和Web；沿用上一阶段124_alb_access_log_config.sql，不增加额外迁移，Agent无本次修改。已有CT_SECRET_KEY需稳定；真实SLS统计需app_lb_id、host、request_length、body_bytes_sent支持字段统计。手动连接测试只验证较小字段集，监控首次查询仍需实际验收。

## 验证

已通过：
- Go test：alblog、dashboard、mysqlstore、ingest、httpapi、auth及Server主入口。
- Go vet：alblog、dashboard、mysqlstore、ingest、httpapi、auth。
- 关键回归：完整/延迟/未完成、非法数值/总数/时间/重复结果、返回体有界、并发缓存合并、改数据源隔离、管理权限、无配置，以及合并直方图P95反例与最多5条排名。
- 本机MySQL8新建 ct_request_monitor_test_ 随机临时数据库：全部迁移、接入配置持久化/版本冲突，以及跨实例渠道读取、开始包含/结束排除、其他维度过滤、直方图持久化和取消；通过后删除该次临时库，未改业务库。
- Web8项行为测试（含原接入配置4项）：四档/未知分母、实际字节、空缺分钟、IP/空Host筛选、时间窗口、刷新合并、失败保留和乱序/卸载保护。
- pnpm typecheck / pnpm build通过，保留既有大chunk提示。
- 生产构建Chrome模拟API验证：3渠道、Host不触发渠道查询、排序/分钟、五种状态、配置直达、1440/1024/540/390/320布局无横向溢出，无页面脚本异常。
- git diff --check通过（已有换行提示）。

浏览器脚本及模拟截图：outputs/request-monitor/browser-check.cjs、desktop.png、mobile.png。截图数据为模拟，已检查桌面和手机效果。

仍待：
- 真实SLS凭证/索引/SQL执行与投递时延验收，未向用户索取或读取真实AccessKey。
- 生产流量性能、采集滞后、渠道门槛与实际直方图覆盖率验证。
- 提交、发布、部署及线上验收；本次未重启任何业务服务。

参考：[GetLogsV2](https://help.aliyun.com/en/sls/developer-reference/api-sls-2020-12-30-getlogsv2)、[类型转换](https://help.aliyun.com/en/sls/data-type-conversion-functions)、[ALB日志字段](https://help.aliyun.com/zh/sls/log-fields-10)。

## 本地运行

2026-10-09按用户“本地运行”要求已启动当前工作区：
- Web：http://127.0.0.1:5173/request-monitor
- Server：http://127.0.0.1:18081
- 本机MySQL既有control_tower_test，既有本地加密主密钥；未改生产部署。
- 启动包装：local/runtime/start-request-monitor.ps1；启动进程PID保存在request-monitor-server.pid / request-monitor-web.pid。
- 日志：local/runtime/request-monitor-server.out.log / .err.log、request-monitor-web.out.log / .err.log。
- 后端沿用deploy/start-server-local.ps1 -APIOnly $true；该模式关闭调权/通知等运行器，但按既有实现仍启动账单worker及登记目标调度。启动日志已明确此行为。
- 已核验/healthz HTTP200、Web路由HTTP200、代理API未登录401；使用原有本地账号登录。
- 前端连接本地新版Server，不是先前的远程API代理。真实SLS查询仍待配置凭证验收。


## SLS 响应解压兼容修复（2026-10-09）

用户真实配置的连接测试返回 alb_invalid_response，配置仍为未保存草稿。核对官方 Python SDK 的 aliyun/log/compress.py，确认其对 x-log-compresstype=gzip/deflate 使用 zlib.decompress；原 CT 只调用 gzip.NewReader，无法读取这种 SLS 返回格式。此次未读取用户密钥或真实响应正文，因此尚不能证明用户该次失败一定属于这个分支。

- 连接测试和监控查询共用读取器增加 zlib 流识别，保留标准 gzip 与未压缩 JSON；压缩前、解压后均有大小上限，损坏/超限/不支持编码继续拒绝。
- 连接测试增加固定阶段日志（读取解压、JSON、进度/行数、计数、时间），不记录凭证、SQL或响应正文。
- 新增 gzip/zlib/deflate、损坏/解压超限、不支持编码及 Probe/Monitor 共用链路回归。go test alblog/dashboard/httpapi 和 go vet alblog 通过。
- 已仅重启本地 18081 后端；18081/healthz 和 5173/request-monitor 均 HTTP200。未改前端，用户可保留草稿直接重测。
- 真实 SLS 重测/保存与统计页面验收仍待用户操作；未提交、发布或部署生产。

依据：https://github.com/aliyun/aliyun-log-python-sdk/blob/master/aliyun/log/compress.py


## 分钟滑块与页面精简（2026-10-09）

用户发现滑块最右端可选到尚未展示数据的分钟，摘要全显示“—”，并要求移除 Host 和无用提示。

- 滑块范围截止当前窗口最后有展示数据的分钟；右端与“回到最新”跟随新数据，手动选择历史分钟保持固定。
- 图表点击末尾待展示区域同样限制到最新可查看分钟；历史内部缺口保留 null，空窗口禁用滑块，不伪造零请求。
- 移除页面 Host 筛选和 Host 汇总，统一全站聚合；服务端仍沿用现有按 Host/分钟聚合接口，未改查询或权限。
- 移除长篇水位、直方图、CDT、全局选择等技术说明，保留已结束请求口径、大小含请求头/请求体、数据更新分钟及必要失败/延迟/缺数据提示。字节趋势改为整行。
- 请求监控6项行为测试、Vue/TypeScript检查通过；隔离Chrome模拟接口验证最右端有数据、Host控件移除、5种状态及1440/1024/540/390/320布局无溢出、无脚本异常。
- 本地Vite已热更新，无后端变更/重启，未提交发布部署。浏览器模拟验证：outputs/request-monitor/simplified-check.cjs 与 simplified-desktop.png / simplified-mobile.png。


## 提交交付（2026-10-09）

按用户要求整理并推送 main，接入配置、请求监控、SLS zlib兼容、分钟滑块及页面精简一起交付；新增124迁移，需Server/Web配套升级，Agent不变。用户后续截图已展示ALB请求趋势；生产负载、投递时延、真实渠道覆盖率仍待验收。此前阶段记录中的“未提交/未实现”是对应时点状态，当前以此记录和Git为准。

远程main新增5项提交已先快进整合；独立错误统计和其他本地任务未纳入。远端调权测试已有自动禁用状态2/3断言不一致的记录，本次不改变调权业务。推送结果以Git操作记录为准，本次不打标签、发布或部署。

提交前精确暂存副本验证：alblog/dashboard/httpapi/auth/ingest/mysqlstore/Server入口七包 Go 测试通过，前端10项回归、类型检查及生产构建通过（保留既有大chunk提示）。本轮未提供测试DSN，实库测试沿用前阶段结果，未重跑。
