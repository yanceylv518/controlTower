# 验收记录：调权中心渠道即时同步（2026-09-05）

- 范围：`f19d3d7 fix(tuning): sync live channel state after refresh and writes`
- 结论：**通过，零阻塞缺陷**。两点体验问题记档（P3，其一涉及设计取舍，建议
  用户拍板）。设计说明见 `docs/tuning-channel-live-sync.md`，与实现一致。

## 变更

- **进入/刷新页面先拉最新渠道**：`POST tuning/channels/refresh` 由 Server 直连
  new-api 分页读取全部渠道（缺 items/total、分页中途变化、分页不完整一律拒绝，
  绝不用残缺列表当全量快照），写入采集实例的 channel_current。未配置直连不回退
  Agent，返回 502 与中文提示；失败时旧缓存不当作同步成功。
- **写入即回写**：Server 直连调权成功后立即把实际写入字段回写 channel_current
  （`ApplyChannelWrite`，只动确认写入的字段，保留锚点等其它元数据）；Agent
  `channel.update` 命令只在成功回执时回写；待执行/失败不提前显示目标值。
- **长轮询通知**：`GET tuning/channels/changes?after=` 最长 25 秒等待进程内
  广播（任何持久化的渠道变更都会 `Notify`），页面收到即重读基础值，保留未保存
  编辑；30 秒运行时轮询与 10 分钟快照作兜底。
- **旧快照保护**：channel_current 写入按 captured_at 单调（晚到的旧快照跳过、
  `GREATEST` 合并）；旧全量快照不删除更新的行及其基础值；多实例同 captured_at
  按 instance_id 去重。
- **067 迁移**：`tuning_recommendations.rule` 16→64 字符。18 字符的
  `base_priority_sync` 在 STRICT 模式下写入报 1406，08-26 上线的"保存基础值时同步
  线上优先级"因此在生产从未成功过（保存基础值即报错），本笔顺手修复。

## 实证

- `go vet ./...`、`go test ./...` 全绿，含真库 `TestChannelSyncPreservesAnchorsAndRejectsOldSnapshots`
  与 `TestDirectRefreshAndWriteImmediatelyUpdateDashboard`；分页/拒绝残缺/通知唤醒
  单测通过。`pnpm typecheck`、`pnpm build` 通过。
- 烟测栈换新二进制：067 首启套用（`rule` 列已为 varchar(64)）；refresh 端点在
  未配置直连时 502 并返回提示文案；changes 端点无 `after` 立即返回，`after=当前`
  时挂起，真跑 agent 上报渠道快照后 3.4 秒唤醒并推进 revision；快照按 captured_at
  落库，基础值锚点保留。
- 1366 截图：顶部横幅、"刷新渠道信息"按钮、模型标题行状态均正常渲染。

## 记档（P3）

- **未配置直连的站点每次打开页面必先触发一次注定失败的刷新并挂出警告**（顶部
  横幅 + 模型标题行"刷新失败"两处同文案）。对只用 Agent 指令控制的站点，这是合法
  配置却被当作错误提示，且每次进入都出现。建议：无直连配置时跳过自动刷新，仅在
  点击"刷新渠道信息"时提示；或提示改为中性说明。设计文档写明"不回退到 Agent"，
  是否调整由用户定。
- 通知是进程内广播、全站点共享：任一站点的渠道变更会唤醒所有打开的页面重读
  一次基础值；多 Server 进程部署时跨进程不通知，靠 30 秒轮询兜底。
- 页面打开先等直连拉取渠道（超时 45 秒）再加载，new-api 慢时首屏相应变慢。
- 时钟：Agent 快照 captured_at 用 Agent 时钟、直连写入用 Server 时钟，Agent 时钟
  偏快时快照后数秒内的直连写入会被 `captured_at<=at` 条件跳过，直到下次快照。
- 067 编号跳过 066（不存在），迁移按文件名顺序套用，无影响。

## 部署要点

- 只改 Server 与前端，067 迁移首启自动套用，未动 Agent（Agent 保持 rc94）。
- 部署后"保存基础值同步优先级"功能才真正可用。
- rc95 打在 86f1286 不含本批，上线需重打 rc96。

## 追加：P3 修复批（2026-09-05，用户令）

修了记档中的四项，另修一项验收时漏掉的功能回归：

- **无直连站点的自动刷新**：refresh 端点对未配置直连的站点返回 409
  `direct_control_not_configured`（`tuning.ErrDirectControlNotConfigured`），
  页面自动刷新静默跳过、不挂横幅，只在点击"刷新渠道信息"时提示"未配置直连"。
  真正的直连失败仍 502 并挂横幅。
- **漏掉的回归（P2）**："初始化/刷新基础值"在无直连站点会因先调 refresh 抛错而
  整个失败。改为无直连时直接用 Agent 快照继续，直连失败才中止。
- **重复提示**：渠道同步错误改用独立的 `channelSyncError` 只显示顶部横幅；模型
  标题行的"刷新失败"仅保留给运行时轮询错误。
- **首屏不等直连**：`load(true)` 把直连刷新与页面数据加载并行，刷新完成后合并
  基础值（变更通知也会再校准）。
- **通知按站点分发**：`channelupdates.Listen/Notify` 以站点为键，一站点的写入不再
  唤醒其它站点页面；写入侧只有实例 ID 时解析站点，解析失败退化为全站唤醒。
  跨进程仍不通知（需换机制，未动）。
- **确认写入不受 Agent 时钟偏快影响**：`ApplyChannelWrite` 与 Agent 成功回执的
  回写去掉 `captured_at<=at` 守卫，改为无条件应用并 `GREATEST` 推进 captured_at。
  已确认的写入在其发生时刻就是权威值，旧快照保护仍只作用于快照路径。
- 测试：hub 按站点作用域单测；真库 `TestApplyChannelWriteIgnoresFutureSnapshotClock`
  （快照 captured_at 超前 20 秒后直连写与 Agent 回执写均生效、captured_at 不倒退）；
  handler 409 单测。`go test ./...` 全绿，`pnpm typecheck`/`build` 通过。
- 烟测：demo（无直连）打开页面无横幅、标题行无"刷新失败"，refresh 返回 409；
  改配不可达直连地址后 refresh 502、页面仅顶部一条横幅；已恢复。
- 部署：server + 前端，无迁移，未动 agent；随本批一起进 rc96。
