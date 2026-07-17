// Control Tower 开发日志数据。由代码 review / 发版工作流维护（Linux 侧、UTF-8）。
// type: release(发版) | bugfix(缺陷修复) | incident(生产事故) | review(代码评审) | decision(方案决策)
window.DEVLOG = [
  {
    date: "2026-07-17",
    type: "release",
    version: "",
    title: "渠道操作移入渠道列表",
    summary: "用户反馈:操作不该藏在详情子页。渠道列表行尾新增『操作』按钮(阻止行跳转冒泡),弹抽屉承载完整的下发命令+命令历史;详情子页的『操作』Tab 移除——分诊看到坏渠道当场处置,不再多一次导航。",
    docs: [],
    commits: []
  },
  {
    date: "2026-07-17",
    type: "release",
    version: "",
    title: "样本↔延时分诊双向关联 + 延时分诊页内说明（Claude 直接实现）",
    summary: "①样本分析行点开详情抽屉:业务账(use_time/token/错误摘要) + 网关账(RT/UHT/URT/传输段/客户端段/归因结论)按 request_id 对齐——『这条慢请求慢在哪一段』一击可查;网关未采样或无 request_id 时如实说明。nginx 慢样本接口增量 request_id 过滤(索引现成)。②延时分诊慢样本行加『业务明细›』跳样本页(带 request_id 过滤)。③延时分诊页顶栏『说明』抽屉:三个计时字段、两个派生段、组合读法三步骤、归因卡与关联状态解释、multiple=内部重试的提示、覆盖率局限——页面自带使用手册。全部测试与构建绿。",
    docs: [],
    commits: []
  },
  {
    date: "2026-07-17",
    type: "release",
    version: "",
    title: "告警保留清理 + 一键清理按钮（Claude 直接实现）",
    summary: "用户发现真实缺口：alerts/alert_events/notification_deliveries 三表不在保留清理范围,告警无限累积。①设置中心新增『已解决告警保留天数』(默认30):清理器只删已解决且超期的告警(firing/已确认/已静默不受年龄影响),时间线与投递记录随删;②告警中心新增『清理告警』下拉:按已确认/已静默/已解决/全部非活动一键删除(确认框+审计友好);③告警中心默认只显示活动告警。顺手修一个真 bug:defaultAlertSettings 手抄默认值 map,新增设置键后 Parse 半途失败致阈值归零(内存70%也触发 critical)——改为 settings.DefaultValue() 权威来源,根除手抄。全部测试与构建绿。",
    docs: [],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "",
    title: "维度页拆分为子页面 + Quota 货币化 + 用户名根因修复（Claude 直接实现）",
    summary: "①维度页按方案 C 拆分:列表页纯分诊表格(720px 内滚,行点击进详情);详情子页 /channels|customers|models/:key——面包屑返回、‹上一个/下一个›巡检、四卡常驻、五 Tab(趋势/模型或客户交叉表/慢样本/告警带角标/操作),旧 ?key= 链接自动跳转;告警中心跳转改子路由。②「模型」Tab 用上一直在采集的 user×model/channel×model/model×user 交叉维度,零采集改动。③Quota 货币化:CT_QUOTA_PER_UNIT(默认50万)+CT_CURRENCY_SYMBOL(默认¥)进设置中心,按站点符号直显不做汇率换算;交叉表与用量页生效。④Token 超百万显示 M 单位。⑤用户名不显示的根因修复:UserNames 批量解析原来只查 log_events——生产聚合模式该表为空,与 v2.4-B1 同坑;改为 log_samples UNION log_events。全部构建与测试绿。",
    docs: [],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "",
    title: "布局重构第二阶段 + 直方图工程交付（Claude 直接实现）",
    summary: "①分页器重做：紧凑 上/下页 + 诚实页码指示,弃用伪总数分页条;②系统状态页：每实例机器卡(状态行+六格资源仪表带色条+趋势行+折叠原始表),支撑表三列网格;③通知设置：表单收进弹窗,修复多余 '>' 文本节点;④设置页：分区卡片+紧凑字段网格+来源标签,保存收进顶栏。⑤直方图工程全栈：统一 15 桶 V2 桶界(V1 严格超集,尾部加密 8/12/20/45/90s),use_time 与 TTFT 共用;Agent 逐事件累积双 V2 直方图+TTFT P50/P90/P95 精确值,V1 桶继续填充保混布兼容;012 迁移(可空,无重建);SQL 合并 V2 相加(NULL 毒化=双方都有才合并)、TTFT 保守 GREATEST;读侧 精确值>V2 插值>V1 插值 三级回退;维度页 TTFT 图改 P50/P90/P95 三线。部署顺序:先 Server(012)后 Agent。全部 Go 测试与 web 构建绿。",
    docs: [],
    commits: ["7787b51", "34c3416", "234f2a8"]
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "",
    title: "布局重构第一阶段（Claude 直接实现）：单行工具栏 + 列表即表格",
    summary: "用户反馈页面乱、不专业,且明确布局优先、由 Claude 直接实现。诊断:9 个逐批 CSS 补丁、40 色、12 档间距,无设计语言。本阶段:①新增 design.css 设计系统(12 色彩角色/6 档间距/5 档字阶/Element Plus 变量对齐),加载于 b*.css 之后逐步取代;②AppShell 重构——52px 单行工具栏承载页标题+页级控件插槽(#tools)+实例/用户,页面内不再有第二行工具条;③维度页(客户/渠道/模型)重建为『列表即表格』——全宽多指标可排序表格(名称/请求/错误率带微条/成功率/P95/TTFT/缓存命中/Token),异常置顶,搜索与状态签在顶栏,行点击后详情紧跟表格,模型页不再有空白区;DimensionWorkspace 组件删除;④8 个视图的过滤条全部迁入顶栏插槽(告警/样本/用量/延时/系统状态/实例/总览)。构建+类型检查通过。视觉稿见 artifact『方案B-布局优先』。后续:走查反馈后微调,再统一并删 b*.css。",
    docs: [],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "bugfix",
    version: "",
    title: "v2.7-B4 批次下发：稀疏曲线珠串问题",
    summary: "rc7 部署后用户反馈 TTFT/缓存命中率曲线变成一串点。诊断：本地 SSR 实证 ECharts 6.0.0 connectNulls 正常画线,排除库与配置;真因是 B3 sparse 模式给所有点开符号,而繁忙维度这两条序列每分钟有值——密集符号连成珠串盖住线。B3 的设计盲区:救孤立点的符号不该套在密集段上。修法:逐点符号——仅左右邻居均为空的孤立点画圆点,连续段纯线;connectNulls 保持。",
    docs: ["docs/codex-task-v2.7-b4-sparse-symbols.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "v2.0.0-rc7",
    title: "rc7 发布：可靠性 + 设置中心 + 体验修正全量集",
    summary: "自 rc6 后新增：v2.6-B1（心跳解耦——CT Server 故障不再阻断 Agent 本地企微告警;instance_offline 规则;Server 通知中心 wecom 渠道）、v2.7-B1（display_name/指标重排/7 页分页/latency 自动选实例/维度页布局）、v2.7-B2（设置中心:011 迁移,库>env>默认,免重启生效,15 可配项）、v2.7-B3（稀疏曲线渲染+TTFT P95 合并 MAX 兜底）。部署:先 Server(011 迁移)后 Agent;部署后需在通知设置页创建 wecom 渠道以打通 Server 侧告警推送,建议与 Agent 直发分群。",
    docs: [],
    commits: ["de37f8d", "70a5917", "aba7738", "9fe87ee"]
  },
  {
    date: "2026-07-16",
    type: "review",
    version: "",
    title: "v2.6-B1 验收通过：可靠性三件套,P1 心跳解耦清账",
    summary: "核实：①pass 重排——本地段（读日志+企微告警）先行,Server 段任何失败走 bufferFailedPass（nginx 字段不入缓冲的既有语义保留、游标按既有规则推进、状态落盘）,不再提前 return;游标晚一轮对齐的取舍写进注释;三种故障模式有单测。②instance_offline:启用+曾接入+7天退役窗+阈值走 settings provider,多 Agent 取最新心跳;离线 Agent 的积压告警抑制联动。③wecom 渠道:与 dingtalk 共用 errcode 校验分支,类型白名单扩展,复用既有退避重试;交付说明含双链路分群建议。清单进 commit、无 force push,Linux 全量测试绿。零返工。至此:账本 P1 心跳解耦、v1.1.2 企微遗留、离线无人喊三项全部清账;v2.6/v2.7 全系列完成,待打 rc7。",
    docs: ["docs/v2.6-b1-delivery.md"],
    commits: ["de37f8d"]
  },
  {
    date: "2026-07-16",
    type: "feature",
    version: "v2.6-B1",
    title: "可靠性三件套：Agent 心跳解耦、实例离线告警与企业微信渠道",
    summary: "Agent 先执行日志采集和本地企业微信告警，再进入 Server 补传、心跳与上报，Server 故障不再让本地预警停摆；Server 新增基于动态离线阈值的 instance_offline 告警，并过滤从未接入和超过 7 天的退役实例；通知中心新增 wecom 类型，校验机器人 errcode 并沿用退避重试。",
    docs: ["docs/codex-task-v2.6-b1-reliability.md", "docs/v2.6-b1-delivery.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "review",
    version: "",
    title: "v2.7-B2 验收通过：设置中心",
    summary: "核实：011 迁移干净（无重建语句）;provider 三级回退（db>env>默认）代码逐层确认,60s 缓存+保存即失效,改配置不重启生效;15 个可配项含数据保留三档/离线秒数/资源与错误率与 P95 阈值/通知总开关;校验带字段错误,修改写 operation_audits;API 响应带 source/default;.prettierrc.json 按验收要求落地锁定前端风格;清单进 commit、无 force push——上两次流程问题都改了。Linux 全量测试绿。协调项：离线阈值默认统一为 120s（B2 已落地）,v2.6-B1 任务书已更新为从 provider 取值、资源阈值 env 化任务改为复核接线。",
    docs: ["docs/v2.7-b2-delivery.md", "docs/codex-task-v2.6-b1-reliability.md"],
    commits: ["70a5917"]
  },
  {
    date: "2026-07-16",
    type: "feature",
    version: "v2.7-B2",
    title: "系统设置中心支持运行时动态配置",
    summary: "新增 system_settings 存储、DB > env > default 三级回退与 60 秒缓存；设置页可调整数据保留、离线时间、资源/错误率/P95 阈值及通知总开关，并展示配置来源。保留清理、离线判定、资源告警、业务指标告警和通知派发均在每轮读取生效值，保存无需重启；修改会写入操作审计。",
    docs: ["docs/codex-task-v2.7-b2-settings-center.md", "docs/v2.7-b2-delivery.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "review",
    version: "",
    title: "v2.7-B1 与 B3 验收通过（附一项流程违规记录）",
    summary: "B1 六项全落地：display_name 增量字段（display_key 未动,注释写明兼容原因）、指标重排、7 页统一 ListPager 分页（默认20,过滤重置页码）、/latency 在线优先自动选实例、维度页左列独立滚动+吸顶+默认50项。B3 根因修复：MergeMetric 与 5m rollup 的 TTFT P95 改 NULL-safe MAX（上界数学写进注释,三分支+部分NULL测试齐）;TrendChart sparse 模式（connectNulls/showSymbol 按序列开关,密集序列未变）;use_time 合并降级为插值的现状已如实写进交付说明。codex 本机无法跑 Linux 已如实注明,我在 Linux 全量测试绿——此关键项由验收侧兜住。违规记录：B1 把全前端从压缩单行重排为 prettier 多行风格,属未经确认的无关改动混入功能提交（diff 膨胀 10 倍,blame 污染）;功能既成且新风格客观更可维护,决定保留,但下批须加 prettier 配置锁定风格,禁止再次整仓重排。",
    docs: ["docs/v2.7-b1-delivery.md", "docs/v2.7-b3-delivery.md"],
    commits: ["aba7738", "9fe87ee"]
  },
  {
    date: "2026-07-16",
    type: "bugfix",
    version: "v2.7-B3",
    title: "稀疏 TTFT 与缓存命中率曲线修正完成",
    summary: "TrendChart 增加 sparse 序列模式，TTFT 平均/P95 与缓存命中率启用跨空值连线和孤立点符号，密集序列保持原样；修复同一 1m 桶多次部分聚合把 TTFT P95 清空的根因，1m 合并与 5m 汇总均用非空 P95 的 MAX 作为保守上界，NULL 三分支及 MySQL 增量合并均有测试；核对确认 big_input_count/hits 在 5m 正确相加并由读侧计算命中率。",
    docs: ["docs/codex-task-v2.7-b3-sparse-series.md", "docs/v2.7-b3-delivery.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "v2.7-B1",
    title: "Web 体验修正完成：纯名称、指标排序、七处分页与维度布局",
    summary: "指标 API 增量提供 display_name，客户/渠道/模型、健康墙及告警以纯名称为主，原始 ID 退居 tooltip；维度指标、总览 KPI 与趋势图按业务优先级重排；告警、样本、用量、审计、通知、渠道命令和慢样本统一默认 20 条分页；延时分诊自动选择在线优先实例；维度页完成 300px 左列独立滚动、筛选吸顶、前 50 项渐进展开和窄屏布局。既有 display_key 未变，不涉及 Agent 或数据库迁移。",
    docs: ["docs/codex-task-v2.7-b1-ux-fixes.md", "docs/v2.7-b1-delivery.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "",
    title: "v2.7-B3 批次下发：TTFT 稀疏曲线断裂修正",
    summary: "机制：TTFT 仅流式分钟有值(无流量分钟 NULL 是正确语义,不画 0);断裂被两点放大——①connectNulls:false+showSymbol:false 让孤立数据点完全隐形;②5m 桶 ttft_p95 为 NULL,≥6h 视图 P95 线整条消失。修法：TrendChart 加 sparse 序列模式(连线+显示符号,TTFT/缓存命中率启用);5m rollup 以 MAX(1m p95) 合成——数学上是合并集 P95 的保守上界,比线消失诚实;use_time 的 p50/p99 有插值回退不动。",
    docs: ["docs/codex-task-v2.7-b3-sparse-series.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "",
    title: "v2.7-B1/B2 批次下发：七项走查反馈",
    summary: "用户走查提出七项：①客户监控不应直显用户 ID（新增 display_name 纯名称字段,ID 退居 tooltip）;②指标按重要性重排（请求数/错误率/成功率/P95 第一排）;③⑥全站列表分页（prev/next+每页20,零 Server 改动,7 个页面）;④延时分诊自动选实例不再默认空白;⑤维度页左列独立滚动+吸顶+默认50项,禁双滚动条——以上并入 v2.7-B1。⑦设置中心（v2.7-B2）：system_settings 表(011)+三级回退(库>env>默认)+60s provider 改配置免重启,第一期覆盖数据保留三档/离线秒数/资源与错误率与 P95 阈值(顺手解决 5s/10s 偏敏感)/通知总开关,修改写审计,API 带 source 标签。",
    docs: ["docs/codex-task-v2.7-b1-ux-fixes.md", "docs/codex-task-v2.7-b2-settings-center.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "v2.0.0-rc6",
    title: "rc6 出产物 + v2.6-B1 可靠性批次下发",
    summary: "rc6 一次带上 v2.3 系列（名称/图标/健康墙/性能/静默刷新）、v2.4-B1（request_id 关联）、v2.5-B1（精确分位数/缓存命中率/TTFT/快照 group+priority）,部署顺序先 Server（009/010 迁移）后 Agent。同时下发 v2.6-B1 可靠性三件套：①心跳解耦——pass 重排为『先本地采集告警、后 Server RPC』,CT Server 故障不再阻断企微告警（账本 P1 清账）;②instance_offline 合成规则（默认 300s,曾接入才告警,7 天退役不刷屏,资源阈值顺手 env 化）;③Server 通知中心补 wecom 渠道类型（v1.1.2 遗留清账,离线/资源告警自此可推企微群）。",
    docs: ["docs/codex-task-v2.6-b1-reliability.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "",
    title: "v2.4-B1 完成：Request ID 精确关联延时诊断",
    summary: "Agent 从 Nginx timing 日志解析并上报慢样本 request_id；Server 新增可空字段与组合索引，批量合并 log_samples 和 log_events 并去重，明确返回 matched、unmatched、multiple；延时分诊页新增用户、渠道、模型、令牌、Request ID、筛选、复制和维度跳转，同时澄清 RT/UHT/URT 语义。旧 Agent 和采样截断安全降级，不影响企业微信错误提醒。Go 全量测试、vet、前端类型检查与生产构建通过。",
    docs: ["docs/codex-task-v2.4-b1-request-linked-latency.md", "docs/v2.4-b1-delivery.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "review",
    version: "",
    title: "v2.5-B1 验收通过（吸收 v2.4-B2;修正一处每次启动全表重建的迁移缺陷）",
    summary: "核实：frt 解析防御（≤0/>1h 视为缺失）、512 严格边界、TTFT 仅流式、原始值数组 10000 上限、精确 P50/95/99（1m 精确,5m NULL 回退插值——插值实现一并交付,等于吸收 v2.4-B2,验收补了单调性/边界测试）、快照 group/priority 反引号+COALESCE、契约/API 只增不改、前端两卡两图,Linux 全量测试绿。验收修正 P1：010 迁移末尾三条 ALTER TABLE ...(引擎/排序规则重钉)——ApplyDir 每次启动重放全部迁移,该语句每次成功执行并强制全表重建,表越大启动越慢还带锁;已删除并把『迁移文件不得含重建语句』写成反向断言测试（codex 原测试反而把有害语句断言为必需项,一并修正）。部署顺序：先 Server 后 Agent。",
    docs: ["docs/codex-delivery-v2.5-b1-agent-data-plane.md"],
    commits: ["f62e0cc"]
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "",
    title: "v2.5-B1 完成：Agent 精确分位数、缓存命中率与 TTFT 全链路",
    summary: "已完成 Agent→契约→010 迁移→Server→API→维度页：1m 精确 P50/P95/P99，大输入（prompt>512）缓存命中率，流式 frt TTFT avg/P95，渠道 group/priority；5m 精确列保持 NULL 并回退桶内插值，页面明确近似语义。旧 Agent 新字段为 NULL，新 Agent 未知字段可被旧 Server 忽略；部署必须先 Server 后 Agent。Go 全量测试与前端类型检查通过。",
    docs: ["docs/codex-task-v2.5-b1-agent-data-plane.md", "docs/codex-delivery-v2.5-b1-agent-data-plane.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "release",
    version: "",
    title: "v2.4-B2 批次下发：延迟分位数桶内插值",
    summary: "用户反馈维度页延迟图不合理。根因：latencyhist.Quantile 返回桶上界,P50/P95/P99 只取 10 个离散值——曲线方波阶梯、8s→11s 显示成 10→30、超 60s 顶格 120 拉飞纵轴压扁其余曲线。批次：分位数改桶内线性插值（histogram_quantile 标准做法）,纯读侧、历史数据即刻受益;要求核对全部调用点（生产 p95 列需换 Agent 二进制才精细,交付说明须声明）+ 改造前后曲线对比。精确分位数直报（Agent 新列）与渠道 group/priority 补采同批,留待下次 Agent 升级。",
    docs: ["docs/codex-task-v2.4-b2-latency-quantiles.md"],
    commits: []
  },
  {
    date: "2026-07-16",
    type: "review",
    version: "",
    title: "v2.4-B1 计划验收：通过,修正一处会导致零命中的关联源",
    summary: "计划质量高：生产先行验证（X-Oneapi-Request-Id 与使用日志一致、排除通用 X-Request-Id、request_id=- 归为未关联）、只按 instance_id+request_id 精确关联禁止时间猜测、multiple 状态诚实处理内部重试、uht 不冒充精确 TTFT、只加不改+失效安全齐全。验收修正一处关键错误：任务 3 原以 log_events 为关联源——生产 aggregate_with_samples 模式下该表为空,命中率会趋零;已改为 log_samples 主源（字段全齐+现成索引,慢阈值 10s 与 nginx 慢样本对齐）、log_events 次源,并写明采样截断导致的 unmatched 属设计内行为。rc5 产物核verified（release 绿,四产物齐）。",
    docs: ["docs/codex-task-v2.4-b1-request-linked-latency.md"],
    commits: ["68c4b56", "b3a2fdc"]
  },
  {
    date: "2026-07-15",
    type: "release",
    version: "v2.0.0-rc5",
    title: "重新打包发布 rc5",
    summary: "基于最新 main 重新发布 Agent 双架构包与包含完整 Control Tower Web 的 Server 包。相较 rc4，包含 Nginx 延时分诊、维度查询性能优化、告警可读化与现场跳转、渠道健康墙、区间汇总指标及全站静默刷新。",
    docs: ["docs/iteration-log.md", "docs/v2.3-b2-delivery.md", "docs/v2.3-b3-delivery.md"],
    commits: ["5056a2d", "e5704f8"]
  },
  {
    date: "2026-07-15",
    type: "review",
    version: "",
    title: "v2.3-B2 验收通过（附一处 CSS 文案 hack 修正 + 流程提醒）",
    summary: "五项全部落地并核实：①告警可读化——AlertItem 增 dimension_type/dimension_key,标题摘要带维度名,P95 顶格改『≥60s（超出直方图量程）』,告警中心与总览一键跳维度页并 ?key= 选中;②静默刷新——useAsyncData 区分首次/后台,后台失败保留旧数据,历史图原地更新;③渠道清晰化——搜索/五状态签/异常置顶/无流量禁用折叠/健康墙(localStorage 记忆,窄屏回退);④样本/用量/通知格式统一;⑤详情卡升级为区间聚合(新增 aggregate=true,复用 MergeMetric)。Linux 全量测试绿。验收修正:详情卡标题被 CSS font-size:0 + ::after 硬改文案(模板就在手边却用 CSS 注入文字)——已改回模板文本并删除 hack。流程提醒:本批 commit message 未贴自查清单,内容在交付说明里——清单进 commit 的纪律不能丢。",
    docs: ["docs/v2.3-b2-delivery.md"],
    commits: ["5056a2d"]
  },
  {
    date: "2026-07-15",
    type: "release",
    version: "v2.3-B2",
    title: "Web 告警可读化与渠道健康墙收尾",
    summary: "告警补齐可读维度名称、现场跳转和 P95 量程说明；维度卡片按 1h/6h/24h 展示区间聚合总数；渠道页新增搜索、五类状态过滤、无流量/禁用折叠和可记忆健康墙；全站后台刷新静默化，失败时保留旧数据。",
    docs: ["docs/v2.3-b2-delivery.md", "docs/codex-task-v2.3-b2-web-followups.md"],
    commits: []
  },
  {
    date: "2026-07-15",
    type: "review",
    version: "",
    title: "v2.3-B3 验收通过：维度页性能优化",
    summary: "核实：008 索引迁移钉名（与生产手工止血索引同名兼容）;latest 查询改写为 24h 活跃集合分组自联结,dimension_type 下推,API 字段零变化,语义变化（超 24h 无流量维度不出现在 latest）已声明并有内存店同步;nameResolver 批量预载有 100 键单批查询测试;维度页首屏不再等历史曲线;gzip 中间件只包 dashboard JSON（Vary/协商/池化,Agent 网关未接入）。Linux 全量测试绿。诚实留白:120 万行实测因 codex 机器无测试库凭据未做,perf-seed.sql 已交付——分析层面 EXPLAIN 推理成立,最终以部署后生产 API 实测收尾（验收条件:latest 接口 <100ms）。",
    docs: ["docs/v2.3-b3-delivery.md"],
    commits: ["0acb7d5"]
  },
  {
    date: "2026-07-15",
    type: "release",
    version: "v2.3-B3",
    title: "维度页 latest 查询与首屏性能优化",
    summary: "新增 metric_1m/5m 复合索引，latest 查询改为 24 小时活跃维度分组自联结并下推 dimension_type；名称解析按实例批量预载，维度列表不再等待历史曲线，Dashboard JSON 支持 gzip，并提供 120 万行基准造数与 EXPLAIN 脚本。",
    docs: ["docs/codex-task-v2.3-b3-perf.md", "docs/v2.3-b3-delivery.md"],
    commits: []
  },
  {
    date: "2026-07-15",
    type: "release",
    version: "",
    title: "v2.3-B3 批次下发：维度页加载性能优化",
    summary: "用户反馈三个维度页打开很慢。定位：latest 指标查询用相关子查询逐行 MAX 扫 metric_1m 全表,且现有索引 bucket_time 打头对子查询完全不可用,随表增长线性恶化;次因:前端首屏等三个请求串完、B1 名称解析未命中时逐渠道单查。批次内容:008 索引迁移(dimension_type,instance_id,dimension_key,bucket_time)、latest 改写为分组自联结+dimension_type/24h 活跃视野下推(超24h无流量维度不再出现在最新列表,语义变化已声明)、nameResolver 整批预载、维度页首屏与趋势图加载拆分;要求 120 万行造数验证+EXPLAIN 前后对比,目标 <100ms。",
    docs: ["docs/codex-task-v2.3-b3-perf.md"],
    commits: []
  },
  {
    date: "2026-07-15",
    type: "review",
    version: "",
    title: "v2.3-B1 验收：名称解析/图标/维度页通过,三处收尾移交 B2",
    summary: "通过:nameResolver 实现正确(渠道名取最新快照、用户名取最近日志、60s 缓存、回退,均有测试),API 只增不改,维度页/总览/审计前端已接线,零 Agent 改动,Server 测试 Linux 全绿。收尾缺口:告警中心/样本页前端未接新字段与统一格式,系统状态图表化追加晚了没赶上。已并同渠道清晰化需求(搜索/状态分组/无流量禁用折叠/健康墙,纯前端)下发 v2.3-B2。供应商分组依赖渠道 group 字段,与 priority 同归下次 Agent 快照升级。",
    docs: ["docs/codex-task-v2.3-b2-web-followups.md", "docs/v2.3-b1-delivery.md"],
    commits: ["6fb831a"]
  },
  {
    date: "2026-07-15",
    type: "release",
    version: "v2.3-B1",
    title: "系统状态页指标图表化",
    summary: "按实例展示 CPU、内存、磁盘、负载和网络最新值，加入阈值色与两分钟数据陈旧提示；新增 1h/6h/24h 的 CPU/内存、磁盘、网络三张趋势图，原始采样默认折叠保留排障能力。",
    docs: ["docs/codex-task-v2.3-b1-web-polish.md", "docs/v2.3-b1-delivery.md"],
    commits: []
  },
  {
    date: "2026-07-15",
    type: "release",
    version: "v2.3-B1",
    title: "Web 名称化、图标与指标视觉打磨",
    summary: "新增 60 秒缓存的实例/渠道/用户名称解析，Dashboard API 以附加字段保持兼容；侧栏 13 个入口加入图标，维度页加入状态点、排序和指标标签，总览 KPI 增加颜色语义与千分位，并统一前端格式化工具和 b7 样式。",
    docs: ["docs/codex-task-v2.3-b1-web-polish.md", "docs/v2.3-b1-delivery.md"],
    commits: []
  },
  {
    date: "2026-07-15",
    type: "decision",
    version: "v2.0.0-rc4",
    title: "生产端到端部署与数据呈现手册",
    summary: "新增从 Compose 初始化 MySQL、Server/Web 首启、实例 Token 签发、new-api 只读账号、Agent 双模式接入，到总览、维度、样本、用量、运行态、告警、延时分诊和渠道命令完整验收的逐步手册，并补充首次游标、持久化、备份、升级、回滚与故障排查。",
    docs: ["docs/control-tower-end-to-end-deployment.md"],
    commits: []
  },
  {
    date: "2026-07-15",
    type: "bugfix",
    version: "v2.0.0-rc4",
    title: "渠道命令按钮文字对比度修复",
    summary: "修正 panel-title 通用 span 规则对 Element Plus 按钮内部文字节点的覆盖，并为渠道危险操作按钮约束默认、悬停和按下状态的白色文字，解决红底灰蓝字、文字难以辨认的问题。",
    docs: [],
  },
  {
    date: "2026-07-15",
type: "release",
    version: "",
    title: "v2.3-B1 批次下发：Web 体验打磨",
    summary: "用户反馈页面粗糙：菜单无图标、裸 ID 遍布、指标缺视觉语义。批次要点：①Server 集中名称解析（nameResolver + 60s 缓存：渠道名取快照、用户名取 log_events、实例名取 instances），display_key 升级为『名称 (ID)』且找不到回退旧文案，API 只增不改；②菜单 13 项全部配官方图标（唯一新增依赖 @element-plus/icons-vue）；③维度页左列加请求量+错误率色条+红橙绿状态点；④Overview 卡片颜色语义化；⑤全站逐页 sweep 清单（时间格式/千分位/RateBar/StatusTag/空态引导），格式化函数集中 utils/format.ts。最终判定靠用户浏览器走查。",
    docs: ["docs/codex-task-v2.3-b1-web-polish.md"],
    commits: []
  },
  {
    date: "2026-07-14",
    type: "review",
    version: "v1.1.2",
    title: "v1.1.2 验收通过：Agent 告警切企业微信（附 rc4 产物补发）",
    summary: "核实：企微与钉钉文本机器人载荷同构，send 逻辑复用成立；配置硬切换（旧 CT_DINGTALK_WEBHOOK_URL 不再读取并有拒读测试，防迁移期双群重复发送）；告警规则/episode/提醒零改动；安装脚本、示例、runbook、账本同步。验收发现并修复：runbook 引用 v2.0.0-rc4 但 tag 不存在（照文档部署会 404）——已从当前 HEAD 补打 rc4，release 流水线绿，四产物齐。注意：Server 通知中心仍是钉钉类型（账本已列遗留）；生产升级顺序＝先建企微机器人直测，再换配置键，最后换二进制重启。",
    docs: ["docs/iteration-log.md", "docs/v2-deploy-runbook.md"],
    commits: ["399dcee"]
  },
  {
    date: "2026-07-14",
    type: "release",
    version: "v1.1.2",
    title: "Agent 直发告警切换为企业微信机器人",
    summary: "Agent 告警通道由钉钉整体切换到企业微信群机器人，配置改为 CT_WECOM_WEBHOOK_URL；文本载荷继续校验 errcode，失败保持下轮重试。告警规则、episode、提醒与缓存失效检测不变。旧钉钉变量不再读取，避免迁移期间双发；安装脚本、配置示例与部署手册已同步。Server 通知中心不在本次范围内。",
    docs: ["docs/iteration-log.md", "docs/deployment-error-alert.md", "docs/v2-deploy-runbook.md"],
    commits: []
  },
  {
    date: "2026-07-14",
    type: "release",
    version: "v1.1.1",
    title: "告警 Agent v1.1.1：移除慢返回告警，新增缓存失效预警",
    summary: "生产反馈：慢返回告警噪音大于价值，整条规则与配置移除。新增渠道级缓存失效检测：最近 10 条输入 >512 tokens 的成功请求全部未命中缓存（other.cache_tokens 缺失或 0）→ 钉钉告警，任一命中即重臂，episode/衰减/提醒复用既有骨架。已知限制：不支持缓存的渠道会告警（调高下限或关规则）。runbook 4.1 钉钉验收改为 webhook 直测。全测试绿；待重建二进制部署生产。",
    docs: ["docs/iteration-log.md", "docs/design-v1.1-early-warning.md"],
    commits: []
  },
  {
    date: "2026-07-14",
    type: "review",
    version: "",
    title: "v2.1-B1 验收通过（附命中率口径修正）",
    summary: "零动作核实：全批无渠道命令创建、无 Agent 改动、无 new-api 访问；006 迁移钉扎、weight_adjustments 旧表未动；加权错误率 SQL 正确（SUM(error)/SUM(request)）；防抖/冷却/权重下限/恢复封顶原值/仅对有 degrade 前科渠道模拟（evidence 标 simulated）均有测试；PUT policy 拒绝 mode≠observe 与危险值；回填三分支覆盖；渠道状态匹配 agent normalizeStatus。验收修正一处口径：hit_rate 分母原为已回填数（含样本不足 hit=NULL 的行），会拉低命中率、干扰 ≥85% 切 auto 判据——已改为已判定数（hit 非 NULL），响应新增 judged 字段。观察期自此开始积累数据。",
    docs: ["docs/codex-task-v2.1-b1-tuning-observe.md", "docs/design-v2.1-auto-tuning.md"],
    commits: ["cab97ab"]
  },
  {
    date: "2026-07-14",
    type: "feature",
    version: "v2.1-B1",
    title: "渠道调权评估引擎进入 observe 观察模式",
    summary: "Server 新增按实例策略、加权错误率评估、持续窗口与冷却控制，仅生成 recorded 建议；recover 只模拟有 degrade 前科的渠道并封顶原始权重。建议在 30 分钟后回填走势与命中结果，Dashboard 提供策略、建议流水和 7/30 天命中率 API。全批次不创建渠道命令、不访问 new-api、不修改 Agent。",
    docs: ["docs/codex-task-v2.1-b1-tuning-observe.md", "docs/design-v2.1-auto-tuning.md"],
    commits: []
  },
  {
    date: "2026-07-14",
    type: "review",
    version: "",
    title: "v2.2-B1-fix 验收通过（附一处竞态补丁）",
    summary: "三项返工全部修复且各有测试：残行进 pending 缓冲（64KB 上限、轮转清空）；firstOpen/reopenAtStart/续读三分支消除全量回放（注入读错误的测试验证不重复计数）；开放桶 map + 5s 宽限期 + closedThrough/forcedClosed 双防线保证同一分钟只入队一次。验收发现一处遗留竞态：发现轮转后还要空等一个 1s flush 周期才重开新文件，Linux 下轮转测试 ~50% 失败（codex 在 Windows 只跑了 truncate 分支）；已由 Claude 补一行 continue 立即重开，8 连跑稳定，全仓测试绿。v2.2-B1 至此整体验收通过。",
    docs: ["docs/codex-task-v2.2-b1-fix-tailer.md"],
    commits: ["162d88b"]
  },
  {
    date: "2026-07-14",
    type: "review",
    version: "",
    title: "v2.2-B1 验收：整体通过，tailer 三项数据正确性缺陷返工",
    summary: "通过项：零消息推送、失效安全（缺文件重试有测试+复核）、独立模式 WARN、007 迁移钉扎、桶 upsert 幂等、慢样本唯一键防重、retention 并入、API 走 protect 不泄漏存储结构体、Web 延时分诊页完整（归因卡+三图+慢样本表+空态）、全测试绿。返工项（P1 已写复现测试实证）：① EOF 残行被当完整行解析，rt 截断值入桶且误判慢请求归因；② 非 EOF 读错误后重开从头回放整个文件，覆盖 Server 历史桶；③ 分钟边界乱序使同分钟桶分裂，upsert 后写覆盖先写导致该分钟缩水。修复单 codex-task-v2.2-b1-fix-tailer.md，仅动 nginxtiming 包。",
    docs: ["docs/codex-task-v2.2-b1-fix-tailer.md", "docs/codex-task-v2.2-b1-nginx-timing-analytics.md"],
    commits: ["ea466c4"]
  },
  {
    date: "2026-07-14",
    type: "feature",
    version: "v2.2-B1",
    title: "Nginx timing 延时分诊分析链路",
    summary: "Agent 以失效安全方式只读 tail Nginx timed 日志，剥离 query 后按 UTC 分钟聚合 TTFT、传输段、5xx/504 与 Top5 慢样本；Server 通过 007 迁移幂等入库并提供受保护的增量 API；Web 新增延时分诊页展示归因卡、三张趋势图和慢样本表。该模块为纯分析数据，零钉钉、零 webhook、零告警事件。",
    docs: ["docs/codex-task-v2.2-b1-nginx-timing-analytics.md", "docs/latency-diagnosis.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "decision",
    version: "",
    title: "Nginx timing 转分析线：v1.1-B5 作废，改下发 v2.2-B1",
    summary: "用户决策：timing 数据是分析型的，不发钉钉，在 Control Tower 里看。原 v1.1-B5（钉钉告警）指令作废，重写为 v2.2-B1：Agent tail + 分钟桶聚合（TTFT/传输段分位数、5xx/504、慢请求归因计数）+ 每桶 Top5 慢样本 → 既有上报链路入库（007 迁移，编号避开在途的 006 tuning）→ Web 新增「延时分诊」页（归因卡 + 三趋势图 + 慢样本表）。失效安全仍是第一验收项；独立模式不支持（无处上报）。",
    docs: ["docs/codex-task-v2.2-b1-nginx-timing-analytics.md", "docs/latency-diagnosis.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "",
    title: "v2.1-B1 批次下发：调权评估引擎（observe 模式）",
    summary: "codex 指令就绪：006 迁移（tuning_policies + tuning_recommendations，钉 COLLATE）、策略 API（默认值照设计 §3，mode 仅接受 observe）、评估引擎 runner（加权错误率 + sustained_windows + cooldown + weight_floor）、30 分钟事后回填与命中率口径、建议流水与报表 API。硬性纪律：本批零动作——不建渠道命令不碰 new-api。发现并写明：channel_snapshots 无 priority 字段，severe/priority_drop 规则预留不实现，待 Agent 快照升级补采。",
    docs: ["docs/codex-task-v2.1-b1-tuning-observe.md", "docs/design-v2.1-auto-tuning.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "",
    title: "[已作废→v2.2-B1] v1.1-B5 批次下发：Nginx timing 日志告警（信号 E）",
    summary: "codex 指令就绪：新包 nginxtiming（timed 格式解析 + tail + 轮转检测），三条规则（504 即时 / 5xx 窗口 / TTFT 窗口带分段归因文案），钉钉发送提取为共用 dingtalk 包。第一验收项是失效安全：未配置零启动、文件缺失只 WARN 并重试、脏行静默跳过，任何故障不伤主告警链路。不做：网关开销分解探测（归 v1.1 探测批次）、渠道/客户归因、指标上报 Server。",
    docs: ["docs/codex-task-v1.1-b5-nginx-timing.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "decision",
    version: "",
    title: "延时分诊体系：Nginx timing 日志启用 + 信号 E 升级",
    summary: "针对『new-api 记录延时大而上游自报很小』的对账问题：确认根因候选（内部重试掩盖/跨境传输段/本机瓶颈/DB 收尾），用户在两台 new-api 的 Nginx 启用 timed 日志格式（rt/uct/uht/urt/bytes）。产出 latency-diagnosis.md（分诊公式+现场命令+SOP）；v1.1 信号 E 升级为 TTFT 告警与分段归因，配套网关开销分解探测（无 key 握手基线，不违边界）。",
    docs: ["docs/latency-diagnosis.md", "docs/design-v1.1-early-warning.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "decision",
    version: "",
    title: "v2.1 自动调权设计定稿：三档模式 + 命中率自证",
    summary: "按渠道质量自动调整权重/优先级的完整设计：observe（默认，只记录建议+30 分钟事后回填）→ confirm（人工一键采纳，走既有命令链路）→ auto（护栏约束+可回滚）。标准全部可配（阈值型规则保持可解释），护栏含最小可用集/步长/冷却/人工优先/双端开关。关键机制：关闭自动时持续产出建议记录与命中率报表（≥85% 为切 auto 判据），用数据回答『开自动是否合理』。执行全部复用 M1-B4 命令闭环与审计。排期 v2.0 上线后三批。",
    docs: ["docs/design-v2.1-auto-tuning.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 v2.0-B1（部署编排 + 发布流水线）：通过",
    summary: "Dockerfile 三阶段布局精确满足默认相对路径、非 root 1001 运行；Compose MySQL 显式钉 utf8mb4_unicode_ci（M1 事故的部署层保险）+ healthcheck 依赖链 + 日志上限；package.sh 本地/CI 共用；发布演练真实完成——v2.0.0-rc1 Release 挂出 3 个 tar.gz + SHA256SUMS，GHCR 镜像推送成功，release workflow 1m33s 绿。Codex 本机还实测了 compose up 全链路（healthz/登录页/uid 1001）。业务代码仅 agentVersion const→var。下一步 v2.0-B2 生产上线。",
    docs: ["docs/codex-task-v2-b1-deploy.md"],
    commits: ["b045519"]
  },
  {
    date: "2026-07-14",
    type: "release",
    version: "v2.0-B1",
    title: "Docker Compose 部署与发布流水线完成",
    summary: "完成 Server 多阶段非 root 镜像、MySQL 8 持久化 Compose 与中文部署手册；共享 package.sh 生成 amd64/arm64 Agent、amd64 Server 和 SHA256SUMS，tag 流水线同时发布 GitHub Release 与 GHCR 镜像。Agent 版本支持 ldflags 注入并在启动时打印，业务行为无变化；本地 Compose 实测 MySQL healthy、健康接口和登录页均为 200。",
    docs: ["docs/codex-task-v2-b1-deploy.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M2-B5（维度页趋势图）：通过",
    summary: "用户需求增强：客户/渠道/模型详情从单桶快照升级为 KPI 行 + 2×2 趋势网格（请求与错误/成功错误率/P50-P95-P99 延迟/Token 进出）。审计全绿：四图共用 TrendChart、单次 metric-history 请求派生四组序列、≥6h 自动切 5m 桶、零新依赖零新 ECharts 模块、server/agent 零改动。种子数据实测曲线吻合。下一步回到 v2.0-B1 部署编排。",
    docs: ["docs/codex-task-m2-b5-dimension-trends.md"],
    commits: ["b2d72aa"]
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "M2-B5",
    title: "维度监控页补齐重要指标趋势图",
    summary: "客户、渠道、模型详情共用 TrendChart，一次历史请求驱动请求/错误、成功率/错误率、P50/P95/P99 延迟和 Token 入/出四张曲线；1h 使用 1m 桶，6h/24h 自动切换 5m 桶，加载期间保留旧图。",
    docs: ["docs/codex-task-m2-b5-dimension-trends.md", "docs/development-progress.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "M2",
    title: "M2 阶段验证 PASS：Web 管理端完成",
    summary: "两轮走查闭环：首轮零功能失败，第二轮用种子数据补齐空库引导、双实例隔离（OpenAI/Claude 演示数据肉眼可辨不串）、渠道快照展示、样本筛选分页、通知重发、命令与审计全链路；三项高成本重复项正式豁免（有单测/前批实测覆盖）。Vue3 管理端 13 页接管根路径，旧静态页退役。M2 关闭，进入 v2.0 发布准备：最小部署编排 + 生产 Agent 双模式接入。",
    docs: ["docs/m2-stage-verification.md"],
    commits: ["c9b8ef9"]
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "M2 阶段第二轮验证通过，M2 正式关闭",
    summary: "在全新 control_tower_test 上验证空库引导，并用 seed-demo-data.sh 补齐双实例、48 条维度指标、渠道快照、样本、运行态、告警与失败通知。双实例隔离、快照名称/权重/状态/chips、样本组合筛选、通知重发 attempts 重置、命令 pending→delivered→succeeded 与 admin 审计全部通过；三项按聚焦清单由既有手工/单测覆盖而豁免。M2 阶段结论由 PARTIAL 更新为 PASS。",
    docs: ["docs/m2-stage-verification.md", "docs/m2-stage-checklist.md"],
    commits: ["b654f5a"]
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "M2 阶段首轮走查 PARTIAL 处置：3 项豁免 + 6 项种子数据补验",
    summary: "首轮已执行项零功能失败（认证/切换/错误态恢复/告警确认落库/实例 token 全流程/命令确认纪律均实测通过），9 个未闭环项全部为环境数据依赖。处置：锁定/自动刷新/静默过期 3 项豁免（自动化已覆盖或纯时间等待）；新增 deploy/seed-demo-data.sh 一键产出双实例 12 桶指标、渠道快照、错误慢样本、告警与必失败通知渠道，支撑余下 6 项 20 分钟聚焦复验。清单已更新第二轮章节。",
    docs: ["docs/m2-stage-verification.md", "docs/m2-stage-checklist.md", "deploy/seed-demo-data.sh"],
    commits: ["c935922"]
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "M2 阶段走查：已执行项通过，仍有环境依赖项待闭环",
    summary: "提交 a9fb16f 的 typecheck/build、Go vet/test、CI、入口认证、全路由、深链/404、断服错误态恢复、告警确认落库、钉钉 secret 不回显、实例 Token 轮换与停用 401、渠道危险操作保护均通过。多实例完整数据、渠道快照、静默时序、通知重发、命令经 Agent 全状态流转等 9 项尚未闭环，结论为 PARTIAL，不能正式关闭 M2。",
    docs: ["docs/m2-stage-verification.md", "docs/m2-stage-checklist.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M2-B4（切换转正）：通过，M2 开发完成",
    summary: "切换语义六条全部落实并有 mux 测试：SPA 接管 /、/next 301 兼容、深链 fallback、未构建 503 带构建提示、/api 与 /healthz 保护、旧静态页删除（-933 行，git ls-files 确认为零）。设置页改密强制重登、404/标题/favicon/空环境引导齐备。25 包全绿、CI 双 job 绿。服役自 P6 骨架的旧静态页正式退役。M2 四批开发全部完成，阶段点走查清单已生成（m2-stage-checklist.md），待用户浏览器走查。",
    docs: ["docs/codex-task-m2-b4-cutover.md", "docs/m2-stage-checklist.md"],
    commits: ["6aa9a39"]
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "M2-B4",
    title: "Vue 前端转正并完成 M2 收官",
    summary: "新增账户设置、404、动态标题、内联 favicon 与空实例引导；Vue SPA 从 /next/ 切换到根路径，保留旧书签 301 兼容并正式删除旧静态页。",
    docs: ["docs/codex-task-m2-b4-cutover.md", "docs/development-progress.md", "webapp/README.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M2-B3（操作页四件套）：通过",
    summary: "危险操作纪律逐项落实：命令对话框警告条+确认勾选未勾禁用+confirm:true+状态流转自动跟踪；Token 一次性对话框（警告/复制/必须确认保存）并被创建与轮换两处复用；secret 仅表单存在、列表只显 has_secret。告警时间线抽屉/备注/静默时长、通知死信重发、实例正则预校验与停用警告全到位。边界零改动、零新依赖、CI 双 job 绿、25 包 Go 全绿。Codex 手工验证覆盖 UI 层（含 Token 流程实测），写操作数据流留待 M2 阶段点 e2e 数据环境统一验证——判断合理。",
    docs: ["docs/codex-task-m2-b3-action-pages.md"],
    commits: ["e1714e4"]
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "M2-B3",
    title: "Web 操作页与危险操作保护完成",
    summary: "新增告警中心、通知设置、实例管理、操作审计及渠道命令交互；落实告警时间线、通知重发、Token 一次性展示、停用与轮换确认、线上渠道命令警告和确认勾选。",
    docs: ["docs/codex-task-m2-b3-action-pages.md", "docs/development-progress.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M2-B2（通用组件 + 六只读页）：通过",
    summary: "复用达标且超出预期：三个维度页共用一个 DimensionView（单组件 + 路由参数，而非三个薄壳）；useAutoRefresh 抽出并回灌 Overview；九个通用件齐全。功能审计全绿：维度详情接 metric-history 历史趋势（P1-1 缺失的完整兑现）、渠道快照联动、P50/P95/P99、样本分页与四筛选、runtime 网络列、用量排行、全局实例筛选。边界零改动（server/agent/旧页/依赖），CI 双 job 绿。自查清单诚实标注两项未验证（单实例环境无法验证多实例切换；停 Server 后会话中断未点到重试按钮）——留 M2 阶段点覆盖。",
    docs: ["docs/codex-task-m2-b2-readonly-pages.md"],
    commits: ["97885c0"]
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "M2-B2",
    title: "Web 六个只读监控页与通用组件完成",
    summary: "补齐实例、渠道快照、样本、运行态和用量 typed API；沉淀自动刷新、异步三态、状态标签、比率条、迷你指标、维度工作台、时间与实例筛选组件；新增客户、渠道、模型、样本、系统状态、用量六个只读页。",
    docs: ["docs/codex-task-m2-b2-readonly-pages.md", "docs/development-progress.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M2-B1（Web 行走骨架）：通过",
    summary: "依赖与批准清单逐项一致零越界；API client 语义正确（非 GET 自动 CSRF 头、401 统一回调跳登录、错误码归一）；路由守卫与 redirect 回跳、ECharts 按需引入、总览四要素齐全（KPI/趋势/告警/30 秒可见性刷新）；Go 托管 /next/ 带路径穿越防护与未构建 503 诊断、目录可注入可测；旧静态页零改动；CI 双 job 绿。一个流程事故：node_modules 曾被整体提交（8312 文件）随即移除，仓库历史 +~9MB（可接受），.gitignore 已补 webapp/**/node_modules 防复发。",
    docs: ["docs/codex-task-m2-b1-skeleton.md"],
    commits: ["f75996d", "3f5d732", "c359ffa"]
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "M2-B1",
    title: "Vue3 Web 行走骨架打通",
    summary: "建立 pnpm workspace（固定 Node 20 兼容的 pnpm 10）、typed API client、Session+CSRF 登录守卫和总览页面；总览包含 KPI、ECharts 趋势、当前告警及可见性暂停的 30 秒刷新。Go Server 新增 /next/ 托管与 SPA fallback，旧静态页继续在 / 共存；CI 增加独立前端 typecheck/build 质量门。",
    docs: ["docs/codex-task-m2-b1-skeleton.md", "docs/development-progress.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "decision",
    version: "",
    title: "前端依赖批准，M2 Web 启动并定稿四批次",
    summary: "用户批准 Node≥20+pnpm、Vue3/Vite/TypeScript/Router/Pinia、Element Plus、ECharts、Vant4（M3 用）。M2 拆四批：B1 行走骨架（脚手架+API client+登录+总览+Go 托管 /next/+CI 前端门）→ B2 通用组件+只读页 → B3 操作页 → B4 收尾切换删旧静态页。开发期间新旧前端共存，M2 阶段点浏览器过全部页面。",
    docs: ["docs/codex-batches-plan.md", "docs/codex-task-m2-b1-skeleton.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "release",
    version: "M1",
    title: "M1 阶段点验证 PASS：Server 产品化完成",
    summary: "第三轮验证在真实 MySQL 9.7 上全链路通过：迁移与管理员引导、e2e 全部步骤（认证→实例/token→心跳/错配 403→轮换宽限→告警触发/确认/时间线→通知→命令下发/认领/回传/审计）、数据库四项抽查全部符合预期。三轮验证累计抓出两个发布级缺陷（迁移重复列被幂等容错掩盖、002~005 排序规则未钉导致 token 查询 1267 且被网关吞错）——均已修复并带防复发测试。M1 四批次正式关闭，Dashboard API v1 契约冻结生效，下一步 M2 Web（待前端依赖审批）。",
    docs: ["docs/m1-stage-verification.md", "docs/api-contracts.md"],
    commits: ["e527e25"]
  },
  {
    date: "2026-07-13",
    type: "bugfix",
    version: "",
    title: "M1 阶段验证第二轮 FAIL 定位与修复：排序规则冲突 + 网关吞错",
    summary: "heartbeat 401 且 Server 零日志。根因：M1 新增的 002~005 迁移表未钉 COLLATE，在 MySQL 8/9 上继承默认 utf8mb4_0900_ai_ci，与 001 表的 unicode_ci 在 instance_tokens JOIN instances 的 token 查询中触发 1267 排序规则冲突；网关 authenticate 将查询错误静默当作 401。修复：002~005 全部 CREATE TABLE 钉 ENGINE/CHARSET/COLLATE 与 001 一致；网关 token 查询出错必打日志；迁移体检测试新增排序规则强制项。需 DROP 重建测试库后第三次验证。",
    docs: ["docs/m1-stage-verification.md"],
    commits: ["9b94eff"]
  },
  {
    date: "2026-07-13",
    type: "bugfix",
    version: "",
    title: "M1 阶段验证 FAIL 定位与修复：迁移重复列 + 迁移器吞错漏洞",
    summary: "阶段验证在全新空库首跑即抓到发布级缺陷：metric_1m/metric_5m 建表语句中 10 个延迟直方图列被重复粘贴，CREATE TABLE 报 1060 被迁移器的幂等容错吞掉，表未建成导致后续 1146 启动失败（上次空库跑迁移还是 P4 时期，之后验证全在内存存储上——正是阶段点存在的意义）。修复：去重列；迁移器改为 CREATE TABLE 的错误绝不忽略（1060/1061 仅对 ALTER/INDEX 幂等重放豁免）；新增迁移文件重复列扫描测试防复发。待 Codex 重跑验证。",
    docs: ["docs/m1-stage-verification.md", "docs/codex-task-m1-stage-verify.md"],
    commits: ["ef68327"]
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M1-B4（渠道命令闭环 + 硬化 + 契约冻结）：一次通过，M1 开发完成",
    summary: "27 文件 +1152 行零返工：命令认领用 SELECT FOR UPDATE 行锁保证原子性、先过期后认领、终态命令不重复审计（幂等）、IP 限流明确忽略 XFF 并注释原因、数据保留每日清理三组可配、契约冻结横幅入档、e2e 补全命令五步断言（含审计 actor）。每个领域都带测试（25 包全绿），自查清单如实粘贴进 commit message——三批打磨出的交付纪律定型。M1 四批次全部关闭，进入阶段点人工验证。",
    docs: ["docs/codex-task-m1-b4-commands-freeze.md"],
    commits: ["8040bff"]
  },
  {
    date: "2026-07-13",
    type: "feature",
    version: "M1-B4",
    title: "M1 收官：渠道命令闭环、服务硬化与 API 契约冻结",
    summary: "新增渠道命令 pending→delivered→succeeded/failed/expired 闭环及操作审计；登录增加 IP 滑动窗口限流，明细/指标/运行态数据分层保留；Dashboard API v1 完整编目并冻结字段语义。",
    docs: ["docs/codex-task-m1-b4-commands-freeze.md", "docs/api-contracts.md", "docs/development-progress.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "feature",
    version: "v1.0.7",
    title: "告警工具：禁用渠道不再检测",
    summary: "渠道被禁用（status != 1）后立即退出渠道级监控：事件不入窗口、进行中 episode 静默关闭（事件日志 kind=disposed）、重新启用从零开始；客户维度不受影响。状态随名字缓存每 10 分钟刷新（禁用到静默最长 10 分钟滞后）。用户插队需求，主线 M1-B4 前直接实现。",
    docs: ["docs/iteration-log.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "M1-B3 二次返工验收：通过，批次关闭",
    summary: "R1 MySQL 三个状态机方法改为同事务先查后写（firing/refired/resolved/silence_expired 全部落事件，空 IN 正确处理，附源码契约测试）；R2 e2e 生长完整（report 触发告警→确认带 note→时间线断言 actor/note→通知重发尽力断言）；R3 nil 守卫 + 持续 firing 负断言 + 双通道 actor 全链路测试。自查清单首次被真实执行并粘贴进 commit message（含诚实注明 e2e 未能本地跑通的原因）。24 包全绿，CI 绿。",
    docs: ["docs/codex-task-m1-b3-rework.md"],
    commits: ["7aecc7b"]
  },
  {
    date: "2026-07-13",
    type: "bugfix",
    version: "M1-B3",
    title: "M1-B3 验收返工：MySQL 系统事件与 E2E 时间线",
    summary: "补齐 MySQL firing/refired/resolved/silence_expired 事件的事务内先查后写，保证与 MemoryStore 一致；Server E2E 增加错误 report、告警确认及 actor/note 时间线断言；时间线 handler 增加 Store 空值保护。",
    docs: ["docs/codex-task-m1-b3-rework.md", "deploy/e2e-server.sh"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M1-B3（告警时间线 + 通知强化）：部分通过，3 项返工",
    summary: "通过：actor context 贯通、确定性抖动指数退避、exhausted 死信与 resolved 释放归零、手动重发、钉钉加签（has_secret 掩码）、时间线 API、MemoryStore 全部转换事件。返工：R1 致命——MySQL 侧三个状态机方法未写系统事件（生产时间线将只有用户动作）；R2 e2e 生长再次缺失；R3 nil 守卫与两组规格测试。两处遗漏均为自查清单明列项，返工要求把填好的清单粘贴进 commit message。",
    docs: ["docs/codex-task-m1-b3-rework.md"],
    commits: ["b712086"]
  },
  {
    date: "2026-07-13",
    type: "feature",
    version: "M1-B3",
    title: "告警生命周期时间线与通知强化",
    summary: "新增告警事件表、时间线 API、操作者与动作备注；通知支持最大尝试次数、指数退避/死信、手动重发，以及钉钉 HMAC 加签。渠道列表仅返回 has_secret，永不回显 secret。",
    docs: ["docs/codex-task-m1-b3-timeline-notify.md", "docs/api-contracts.md", "docs/development-progress.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "M1-B2 返工验收：通过，review 补三组测试",
    summary: "R1 多实例过滤（metrics/usage/overview 补齐，runtime 原生支持）、R2 网关五场景测试（含解析前拒绝断言）、R3 snake_case DTO + agents 概要、R4 认证→解析→实例匹配三段式重构、R5 错误返回与停用实例 409——全部到位。review 补齐：两实例互不串（metrics/agents/server-metrics）、DTO 字段名断言、mux 实例路由断言。24 包全绿。M1-B2 关闭。",
    docs: ["docs/codex-task-m1-b2-rework.md"],
    commits: ["6de2fff"]
  },
  {
    date: "2026-07-13",
    type: "bugfix",
    version: "M1-B2",
    title: "M1-B2 验收返工：过滤、DTO 与鉴权顺序",
    summary: "补齐 Dashboard 多实例过滤、实例列表 snake_case DTO 与 Agent 摘要；Agent Token 改为请求体解析前完成认证，避免无效凭证触发大体积解压；补全网关生命周期测试，并修复实例更新/轮换吞错与停用实例仍可轮换问题。",
    docs: ["docs/codex-task-m1-b2-rework.md", "docs/api-contracts.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M1-B2（实例管理 + 按实例 Token）：部分通过，5 项返工",
    summary: "通过项：003 迁移、存储双实现、token 只显示一次且列表无泄漏、instance_mismatch 403、24h 轮换宽限、e2e-server.sh 完整起步。返工项：R1 任务 4 多实例过滤整体缺失；R2 网关五场景零测试；R3 实例列表缺 agents 概要且裸序列化 storage 结构体（PascalCase 与全 API snake_case 相悖）；R4 安全回归——鉴权完成前解析 gzip 请求体（旧代码先验 token）；R5 Update/Rotate 吞掉 store 错误。返工清单：codex-task-m1-b2-rework.md。",
    docs: ["docs/codex-task-m1-b2-rework.md"],
    commits: ["24ada7a"]
  },
  {
    date: "2026-07-13",
    type: "feature",
    version: "M1-B2",
    title: "实例管理与按实例 Agent Token",
    summary: "新增实例管理 API、随机 Agent Token 一次性回显与哈希存储、24 小时轮换宽限、实例停用即时失效；Agent 网关校验 Token 绑定的 instance_id，同时保留全局 Token 兼容通道，并建立 Server E2E 脚本。",
    docs: ["docs/codex-task-m1-b2-instances.md", "docs/api-contracts.md", "deploy/e2e-server.sh"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M1-B1（Server 认证体系）：通过，review 补一处加固与缺失测试",
    summary: "实现核对：PBKDF2-600k（Go 1.24 标准库，零新依赖）、登录锁定（锁定期连正确密码也拒绝）、session 生命周期、双通道中间件（Cookie 写操作强制 CSRF 头、token 通道豁免）、config 层挡住半配置引导、多文件迁移加载、参数化 SQL。review 补：中间件空 token 守卫（防误配空 CT_DASHBOARD_TOKEN 时无凭证放行的潜在越权）、迭代数字面量去重、CSRF 通过路径/无凭证 401/handler 级 me-logout-改密-429 锁定/mux 路由等缺失测试。24 包全绿，CI 绿。",
    docs: ["docs/codex-task-m1-b1-auth.md"],
    commits: ["793191b"]
  },
  {
    date: "2026-07-13",
    type: "feature",
    version: "M1-B1",
    title: "Server Session 用户认证与旧 Token 兼容",
    summary: "新增 PBKDF2 密码哈希、用户与 Session 持久化、登录限速、认证 API；Dashboard 支持 Session Cookie 与旧 Bearer Token 双通道，Cookie 写请求增加 CSRF 头校验。迁移按目录顺序执行，并支持首次管理员引导。",
    docs: ["docs/codex-task-m1-b1-auth.md", "docs/api-contracts.md", "docs/development-progress.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 M0-lite（CI 质量门 + Makefile）：通过",
    summary: "Makefile 四目标与 ci.yml 完全符合规格（1.24.x + 缓存 + 并发取消，只做质量门不传产物）；本地 make test/build 通过，GitHub Actions 已真实跑绿两次（57s 首跑 / 16s 缓存跑）。从此每次 push 自动跑 vet + 23 包测试 + 双端构建，M1 起的批次验收多一道机器信号。",
    docs: ["docs/codex-task-m0-lite-ci.md"],
    commits: ["59529cb"]
  },
  {
    date: "2026-07-13",
    type: "feature",
    version: "M0-lite",
    title: "GitHub Actions 质量门与 Makefile",
    summary: "新增仓库级 make test/make build：统一执行 vet、全量测试，并交叉编译 Linux Agent amd64/arm64 与 Server amd64；GitHub Actions 在 main push 和所有 PR 上运行测试与构建，并启用同分支并发取消。发布打包、版本注入和 Agent 重构仍按计划挂起。",
    docs: ["docs/codex-task-m0-lite-ci.md", "docs/development-progress.md", "README.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "decision",
    version: "",
    title: "方向修正：主线回归监控系统产品，告警线挂起",
    summary: "用户决策：钉钉告警 v1.0.6 生产运行良好，需要升级时再深化。B1（慢返回+事件留痕）已合入主干不单独部署，B2/B3 挂起（设计保留）。执行顺序改为 M0-lite CI → M1 Server 四批次 → M2 Web → v2.0 发布（Agent 届时一次性升级接入双模式）。",
    docs: ["docs/codex-batches-plan.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 v1.1 B1（慢返回规则 + 事件持久化）：逻辑全对，补齐 3 组回归测试后通过",
    summary: "实现核对无误：ruleState 双规则重构、窗口共享按各自尾部计数、流式独立阈值、rearm 前先记录事件、fail-safe 事件日志（一次告警式禁用）、23 包测试全绿。缺口：双规则独立性、慢规则提醒、慢告警失败按 rule 回滚三组测试未写，review 时补齐。小项：慢消息对流式触发也显示非流式阈值秒数（措辞瑕疵，记入 B2 顺带）。",
    docs: ["docs/codex-task-v1.1-b1.md"],
    commits: ["ed0fe7e"]
  },
  {
    date: "2026-07-13",
    type: "feature",
    version: "v1.1-B1",
    title: "慢返回窗口规则与 episode 事件持久化",
    summary: "Agent 新增与错误告警相互独立的慢返回窗口：非流式与流式分别配置阈值，支持触发、持续提醒、重臂和发送失败重试；全部 episode 状态变迁写入 alert-events.jsonl，5 MiB 轮转保留一个旧文件，写入失败不影响告警链路。",
    docs: ["docs/codex-task-v1.1-b1.md", "docs/development-progress.md", "agent/README.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "decision",
    version: "",
    title: "Codex 批次执行计划定稿：v1.1 四批次 + M1 四批次",
    summary: "后续开发切成 Codex 可独立执行的批次：B1 慢返回+事件持久化 → B2 证据驱动探测 → B3 静默确认+正向恢复+episode 收尾 → B4 CI/发布打包+快照常驻化 → v1.1 上线观察一周 → M1 四批次。每批次含开发思路、review 验收和明确的人工验证点（做什么/预期/耗时）；一批一个任务文件，上一批通过才生成下一批。B1 任务文件已就绪。",
    docs: ["docs/codex-batches-plan.md", "docs/codex-task-v1.1-b1.md"],
    commits: []
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "验收 Codex 的 Web 监控 P1 批次修复：通过",
    summary: "7 个工作项全部正确实现：指标历史 API（参数化 SQL、升序、时窗校验）+ latest 模式（安静维度不再消失）、乱码分隔符修复、30 秒自动刷新、网络列、P50/P99（空直方图安全返回 null）、用量统计视图（聚合 SQL + 排行表）、趋势图双线/图例/时间轴。纪律全守：agent 目录未动、零新依赖、全部 \\u 转义、LF、escapeHTML 覆盖、23 包测试与 vet 通过。",
    docs: ["docs/review-web-monitoring-2026-07-13.md", "docs/codex-task-web-monitoring-fixes.md"],
    commits: ["7dfa567"]
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "Web 监控界面与设计文档 review，生成 P1 修复批次交 Codex",
    summary: "发现 1 个结构性缺失（指标历史 API 只返回全维度最近 200 行，趋势只有几分钟、安静维度从页面消失）、生产 Agent 告警在 Web 不可见、延迟直方图未用、缺用量/成本视角、乱码分隔符、无自动刷新等。P1 共 8 项列入 Codex 任务，P2/P3 归入 M1/M2。",
    docs: ["docs/review-web-monitoring-2026-07-13.md", "docs/codex-task-web-monitoring-fixes.md"],
    commits: ["f15e35b"]
  },
  {
    date: "2026-07-13",
    type: "decision",
    version: "",
    title: "v1.2 暂缓：先用现有数据源做监控，不改 new-api",
    summary: "确立 new-api 维护模式（将来若改：固定版本 + ct-patch 分支打补丁、CI 出镜像、fail-safe、不动库），但当前决定暂不引入中间件；v1.1 上线跑一段时间后按真实盲区数据再评估在途请求检测的必要性。",
    docs: ["docs/development-plan.md"],
    commits: ["932fee9", "a35a66e"]
  },
  {
    date: "2026-07-13",
    type: "review",
    version: "",
    title: "review NULL 字段防护提交（f6c81f0），发现 created_at 兜底缺陷",
    summary: "COALESCE 修复本身正确（19 列对齐、id/type 不包裹的理由成立），但 NULL created_at 变成 Unix 0 后会被告警窗口 60 分钟衰减立即清出，错误静默漏计。按用户建议将修复放在采集边界 scanLogRow（一处修好全部下游），告警层保留兜底。",
    docs: ["docs/iteration-log.md"],
    commits: ["d51cd4e", "5aaa9f1"]
  },
  {
    date: "2026-07-13",
    type: "bugfix",
    version: "v1.0.6",
    title: "logs 采集 NULL 字段防护",
    summary: "采集 SQL 全部可空列 COALESCE，消除“源表 NULL 行导致 Scan 报错、游标不推进、采集永久停摆”的 P0 风险；created_at 为 NULL 时在采集边界用采集时间代替，避免 1970 时间戳污染告警窗口、指标桶和上报数据。",
    docs: ["docs/iteration-log.md"],
    commits: ["f6c81f0", "d51cd4e", "5aaa9f1"]
  },
  {
    date: "2026-07-12",
    type: "bugfix",
    version: "v1.0.5",
    title: "episode 去重导致预警丢失的修复：窗口时间衰减 + 持续提醒",
    summary: "新增 CT_ALERT_WINDOW_MAX_AGE_MINUTES=60（超龄事件滑出窗口，稀疏渠道可重臂）与 CT_ALERT_REMIND_MINUTES=60（episode 持续 firing 每小时提醒，附起始时间与累计错误数）；触发/提醒输出按维度审计日志。",
    docs: ["docs/iteration-log.md", "docs/agent-alert-missed-alert-analysis.md"],
    commits: ["7846f8f", "8e96c26"]
  },
  {
    date: "2026-07-12",
    type: "incident",
    version: "",
    title: "渠道 26 预警丢失事故调查与定案",
    summary: "17:56 渠道 26 连续 3 条错误未触发钉钉告警。定案：当天 03:02 已触发过告警（钉钉群历史为证），该渠道成功请求极少、窗口错误数从未降到阈值以下，episode 永不重臂——“同一故障只发一次”的设计缺陷。18:08 的关键词编码修复与本次丢失无关。",
    docs: ["docs/agent-alert-missed-alert-analysis.md"],
    commits: ["5ddc2b3", "f1d4510"]
  },
  {
    date: "2026-07-12",
    type: "bugfix",
    version: "v1.0.4",
    title: "钉钉告警关键词编码修复",
    summary: "告警消息前缀改为 Unicode 转义 [告警]，防止源文件在 Windows 侧被重编码后关键词变乱码、被钉钉机器人 errcode 拒收；同时增加消息必须包含关键词的自动化测试（编码金丝雀）。",
    docs: ["docs/iteration-log.md"],
    commits: ["f77d495", "4d3bf68"]
  },
  {
    date: "2026-07-11",
    type: "bugfix",
    version: "v1.0.3",
    title: "安装脚本升级不重启修复",
    summary: "install-agent.sh 原用 systemctl enable --now，服务已运行时升级只换了磁盘二进制、旧进程继续跑。改为 enable + restart，升级真正生效。",
    docs: ["docs/iteration-log.md"],
    commits: ["f7d3df1", "4c41456"]
  },
  {
    date: "2026-07-11",
    type: "release",
    version: "v1.0",
    title: "错误预警版发布：第一个生产版本",
    summary: "Agent 独立运行（不依赖 Server），按渠道/客户维护最近 10 条请求滑动窗口，错误 ≥ 3 条直发钉钉群；episode 防刷屏、失败重试、首启不回放历史、一键安装（install-agent.sh + systemd）。部署于 Ubuntu 生产机。",
    docs: ["docs/iteration-log.md", "docs/deployment-error-alert.md"],
    commits: ["63b31fc", "155126e"]
  },
  {
    date: "2026-07-11",
    type: "decision",
    version: "",
    title: "双轨迭代路径定稿",
    summary: "告警 Agent 线（v1.x 小步快发）与产品主线（M0-M5：CI/Server/Web/App）并行推进；v2.0 = Web 上线 + Agent 双模式汇合（钉钉直发保留为独立冗余链路），v3.0 = PWA App。",
    docs: ["docs/development-plan.md", "docs/design-v1.1-early-warning.md"],
    commits: ["901bcd1"]
  }
];
