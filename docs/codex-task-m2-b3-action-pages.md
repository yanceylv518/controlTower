# Codex 任务：M2-B3——操作页（告警中心 / 通知设置 / 实例管理 / 渠道命令）

M2 第三批，交互最重的一批：全部是**写操作页面**。纯前端批次，冻结契约（`docs/api-contracts.md`）不改，`server/**`、`agent/**`、旧静态页零改动；零新依赖；沿用 B1/B2 的通用件与风格。

**危险操作的交互纪律是本批的验收重点**（见各任务的 UX 硬要求）。文末自查清单粘贴进 commit message。

## 工作项

### 任务 1：shared 补 API

`alertEvents(id, limit)`、`alertAction({id, action, silence_minutes?, note?})`（B1 已有则复核）、`notificationChannels()`/`saveNotificationChannel(req)`/`notificationDeliveries(params)`/`resendDelivery(id)`、`createInstance(req)`/`updateInstance(id, req)`/`rotateInstanceToken(id)`、`createChannelCommand(channelID, req)`/`channelCommands(params)`/`operationAudits(params)`。类型逐字段对照契约。

### 任务 2：告警中心 `/alerts`

- 筛选行：status（全部/firing/acknowledged/silenced/resolved）、severity、active_only；列表沿用告警卡片式（severity 左边条着色，复用 StatusTag）。
- 行操作：**确认**（弹小窗可填备注）→ `action=acknowledge`；**静默**（时长选择 30m/1h/4h + 备注）→ `action=silence, silence_minutes`；resolved 的行无操作。
- **时间线抽屉**：点击行展开右侧 Drawer，调 `alerts/{id}/events`——按时间正序渲染事件流（event_type 图标/颜色区分 firing/refired/acknowledged/silenced/silence_expired/resolved，展示 actor 与 note）。
- 操作成功后局部刷新列表与抽屉；失败 ElMessage 显示错误码。

### 任务 3：通知设置 `/notifications`

- 渠道表单：名称、类型（通用 Webhook / 钉钉机器人）、URL、启用开关；**类型为钉钉时出现 secret 输入**（占位提示"加签密钥，留空为关键词模式"）；提交走 upsert。
- 渠道列表：类型 Tag、掩码 URL、`has_secret` 显示为"已加签/未加签"、启用状态；**任何位置不得展示 secret 明文**（后端本就不回，前端也不缓存表单值到列表）。
- 投递记录表：时间/状态（含 exhausted 死信红色）/HTTP/次数/下次重试/告警 ID/错误摘要；**failed 与 exhausted 行有"重发"按钮** → resend API，成功 toast 并刷新。

### 任务 4：实例管理 `/instances`

- 列表：实例 ID/名称/启用状态/创建时间 + agents 概要（在线 Tag、版本、积压、最后心跳）。
- **创建**：对话框（instance_id 正则 `^[a-z0-9-]{1,64}$` 前端预校验 + name）→ 成功弹 **Token 展示对话框**（UX 硬要求：等宽字体展示、复制按钮、醒目警告"Token 仅此一次显示，关闭后无法找回"、必须点"我已保存"才能关闭，关闭后不可再取）。
- **轮换 Token**：行按钮 → ElMessageBox 确认（文案说明旧 token 24 小时宽限）→ 成功后同款 Token 展示对话框 + 显示 grace_until。
- **停用/启用**：开关 + 确认弹窗（停用文案警告"该实例全部 Agent token 立即失效"）。409/404 错误码映射为可读文案。

### 任务 5：渠道命令（挂在渠道监控详情 + 新增审计页）

- `/channels` 详情面板底部新增"渠道操作"区块：
  1. **下发命令按钮** → 对话框：目标实例（默认当前筛选实例，必选）、可选字段 status（启用=1/禁用=2 选择器）/weight/priority（三者至少一项，前端校验）；**危险操作纪律**：对话框顶部醒目警告条"该操作将通过 Agent 调用 new-api 管理接口，直接影响线上渠道"；底部**确认勾选框**（"我确认要对渠道 #N 执行此变更"）——未勾选提交按钮禁用；勾选后提交携带 `confirm:true`。
  2. 成功后显示命令 id 与提示"等待 Agent 心跳认领（通常 ≤30 秒）"；区块下方展示该实例最近命令列表（状态 Tag：pending/delivered/succeeded/failed/expired，自动刷新跟踪状态流转）。
- 新增 `/audits` 页（导航"系统"组）：operation-audits 表（时间/类型/目标/操作人/摘要，实例筛选联动）。

### 任务 6：文档

`docs/development-progress.md` M2-B3 行；`webapp/README.md` 页面清单更新。

## 验证要求

1. `pnpm typecheck`、`pnpm build`、`go test ./...`、CI 双 job 全绿。
2. **手工验证逐项记录**（借助 e2e 环境造数据：跑一遍 `deploy/e2e-server.sh` 即可产出告警/命令/审计数据）：告警确认与静默落库且时间线抽屉展示 actor/note；通知渠道建钉钉（带 secret）后列表显示"已加签"且无明文；失败投递点重发状态变化；建实例走完 Token 一次性展示流程（截图或文字确认警告与复制存在）；轮换/停用流程；命令未勾选确认时按钮禁用、勾选下发后在列表看到状态流转（e2e 模拟 agent 可回传）；审计页有记录。无法验证项如实注明。
3. 复用自查：所有新页面用 AsyncPanel/useAutoRefresh/StatusTag/InstanceSelect，无复制粘贴布局。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 任务 1~6 逐节核对；shared 类型与契约一致
- [ ] 零新依赖；web/dist、node_modules 未提交；server/agent/旧静态页零改动
- [ ] 危险操作纪律落实：命令对话框警告条 + 确认勾选 + 未勾选禁用；Token 一次性展示对话框含警告与复制
- [ ] secret/token 前端无明文残留（列表/日志/console）
- [ ] 手工验证结果逐项记录
- [ ] 一个 commit：`feat(web): action pages for alerts, notifications, instances, commands (M2-B3)`

## 明确不做

`/next`→`/` 切换与旧页删除（B4）；暗色主题；用户管理页（只有改密，B4 设置页做）；vitest。
