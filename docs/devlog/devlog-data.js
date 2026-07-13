// Control Tower 开发日志数据。由代码 review / 发版工作流维护（Linux 侧、UTF-8）。
// type: release(发版) | bugfix(缺陷修复) | incident(生产事故) | review(代码评审) | decision(方案决策)
window.DEVLOG = [
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
