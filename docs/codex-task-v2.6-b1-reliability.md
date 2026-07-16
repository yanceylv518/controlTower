# Codex 任务：v2.6-B1——可靠性三件套（心跳解耦 / 实例离线告警 / 企微通知渠道）

监控系统自身的可靠性补课：①CT Server 生病时 Agent 的本地告警不许哑（账本 P1"心跳解耦"）;②Agent 所在机器死了要有人喊（实例离线告警,当前无此规则）;③Server 通知中心补企业微信渠道类型（v1.1.2 遗留,离线/资源告警推送的前提）。

**文末自查清单粘贴进 commit message（清单进 commit,不是只进交付说明;禁止 force push,推送前自查）。**

## 背景速读

- 双模式 pass 现有顺序（`agent/cmd/control-tower-agent/main.go` `collectAndReportFullPass`）：补传缓冲 → 心跳（领命令/对游标）→ 读 logs → 本地告警(企微) → 上报。**前两步任一失败直接 return,走不到读日志和告警**——CT Server 超载/宕机时每轮都死在开头,本地告警随之停摆。
- Server 告警链路：`AlertNotificationRunner.RunOnce` 每周期把 `currentAlerts()`（含错误率/P95/CPU/内存/磁盘等合成规则）持久化 → 解决消失项 → 对 firing 告警走 `dispatchAlertNotifications` 投递到通知渠道（带退避重试/exhausted/手动重发,设施齐全）。**缺的只是"实例离线"这条规则和企微渠道类型。**
- Agent 在线状态数据已有：runtime 侧每实例 last-seen（`seconds_since_seen` 已在 API 暴露）。
- 企微群机器人协议与钉钉同构：POST `{"msgtype":"text","text":{"content":...}}`,响应校验 `errcode`（参考 `agent/internal/erroralert/erroralert.go` 的 send;注意 Server 侧通知已有自己的投递/重试骨架,只需新增一种 channel_type 的发送实现）。

## 工作项

### 任务 1：心跳解耦（Agent,本批核心）

重排 `collectAndReportFullPass`：

```
新顺序：读 logs → 本地告警(企微) → [Server 侧] 补传缓冲 → 心跳+执行命令 → 上报
```

- **本地段（读日志+告警）只依赖源库与企微,任何 Server RPC 失败不得阻断它**;
- Server 段任何一步失败：本轮事件按既有语义进本地缓冲、`ConsecutiveReportFailures++`、游标照常推进并保存,**函数不再因心跳/补传失败而提前 return 跳过后续本地逻辑**（错误仍记日志与审计行）;
- 心跳的 `ServerLastLogID` 游标对齐从"采集前"变为"下一轮生效"——行为差异写进注释与交付说明（正常运行无影响,仅补传恢复场景晚一轮对齐）;
- 命令执行（channel.update）依赖心跳成功,失败即本轮无命令,语义不变;
- 单测：模拟心跳失败/补传失败/上报失败三种,断言本地告警照常触发、缓冲追加、游标推进;既有测试原样通过。

### 任务 2：实例离线告警（Server 合成规则）

`currentAlerts()` 增加规则 `instance_offline`：

- 触发：实例启用且**曾经有过心跳**,`seconds_since_seen > CT_OFFLINE_ALERT_SECONDS`（新 env,默认 300）→ critical,Title"实例离线",Summary 带"最后心跳于 X 分钟前";
- 防噪：从未接入过的实例不告警;最后心跳早于 7 天的不再重复出现在 firing（视为退役,自然 resolve）;
- 恢复：心跳恢复后规则不再产出 → 既有 ResolveMissingAlerts 自动闭环并可通知恢复（沿用现状语义）;
- 顺手：CPU/内存/磁盘阈值（现硬编码 80/90、80/90、85/95）改为 env 可配（`CT_ALERT_CPU_WARN/CRIT` 等六个,默认值不变）;
- 单测：离线触发/从未接入不触发/恢复闭环/退役不刷屏/阈值 env 覆盖。

### 任务 3：企微通知渠道类型（Server + Web）

- 通知渠道新增 `channel_type: "wecom"`：投递实现 POST 企微文本消息（内容 = 告警 Title + Summary + 实例名 + 时间,带 `[告警]` 前缀）,HTTP 2xx 且 `errcode==0` 才算成功,失败走既有退避重试;
- 既有 dingtalk/webhook 类型行为零改动;
- Web 通知设置页类型下拉加"企业微信",表单只需 webhook URL（无加签）;
- 单测：成功/errcode 非零判失败/重试计数;e2e 脚本（`deploy/e2e-server.sh`）加 wecom 假渠道走一遍投递失败-重试路径;
- 交付说明注明：**Server 侧告警（离线/资源/错误率/P95）自此可推企微群**——与 Agent 直发告警是两条独立链路,建议分开两个群或消息前缀区分（运维决策,文档给出建议即可）。

## 硬性纪律

- Agent 改动仅限 pass 重排与其测试;不动告警规则/采集/缓冲数据结构;
- API/契约只增不改;无迁移（不需要新表列;channel_type 是字符串值）;
- 部署顺序无强约束,但**任务 1 的 Agent 与任务 2/3 的 Server 独立生效**,交付说明分别写明各自的验证方法;
- Linux 语义自查：pass 重排涉及时序,交付前在 Linux 跑全量测试（历史上两次 Windows-only 验证漏 Linux 行为）。

## 验证要求

1. `go test ./...`、`go vet`、`pnpm build`、`pnpm test` 全绿;
2. 手工验证任务 1：本地起 Agent（配假 CT_SERVER_URL 指向不存在地址）→ 观察每轮日志:采集与告警照常、上报失败进缓冲、进程不退出;恢复真 Server 后缓冲补传;
3. 手工验证任务 2/3：停掉一个 Agent ≥5 分钟 → Web 告警中心出现"实例离线"（critical）→ 配置企微渠道收到推送;重启 Agent → 告警 resolve;
4. 全过程截图/日志记入交付说明。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 心跳/补传/上报三种 Server 故障下,本地告警照常、缓冲追加、游标推进（各有单测）
- [ ] 游标对齐"晚一轮"的行为差异已注释并写进交付说明
- [ ] instance_offline：触发/从未接入/恢复/退役四分支有测试;阈值 env 化默认值不变
- [ ] wecom 渠道类型 errcode 校验与重试有测试;dingtalk/webhook 零改动
- [ ] 交付说明含两条链路（Agent 直发 vs Server 通知）的分群建议
- [ ] 一个 commit：`feat: decouple agent alerting from server and notify offline instances (v2.6-B1)`

## 明确不做

Agent 直发告警链路的任何改动;通知渠道模板化/静默时段;P95 告警阈值调整（另行观察）;CT Server 自身的自监控（看门狗,记档);K8s/HA。
