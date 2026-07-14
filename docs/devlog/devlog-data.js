// Control Tower 开发日志数据。由代码 review / 发版工作流维护（Linux 侧、UTF-8）。
// type: release(发版) | bugfix(缺陷修复) | incident(生产事故) | review(代码评审) | decision(方案决策)
window.DEVLOG = [
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
