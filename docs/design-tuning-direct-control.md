# 调权直连 new-api 设计与实施记录（2026-08-10）

用户发起讨论并拍板;实施人:验收方本人(非 codex)。本文既是讨论定稿也是 B1 交付说明。

## 1. 背景与动机

调权写路径(auto 权重写入/熔断置零/恢复探针/auto 预检)此前全部经 Agent 命令队列:server 造 channel_commands → Agent 心跳领取(默认 30s 周期)→ Agent 调本机 newapi 管理 API → 结果随下趟上报回传。代价:写延迟一个心跳周期、恢复探针轮被心跳量化、server-agent 版本耦合(2ceccc3 预检即活例)、命令过期哨兵等为异步性而建的机制。

newapi 管理 API 本质是纯 HTTP(Bearer access token + `New-Api-User` 头),且已通过 ALB 对浏览器暴露——CT server 直调不新增暴露面。**源码实证**(scratchpad/newapi-src):路由选渠道读 `abilities` 表,渠道更新走应用层才会重建 abilities 并维护内存缓存,故**直写 DB 不可行,直调 HTTP API 是唯一正确形态**;渠道列表返回 `data` 为对象 `{items,total,...}`。

## 2. 用户拍板的决策

1. **按站点渐进,配置驱动**:站点配了直连三件套走直连,没配回落 Agent 命令队列;全部站点切换稳定后另开清理批次退役命令写路径。
2. **探针同批迁**:避免写直连、探针走队列的两套并存。
3. **Agent 端清理不同批**:B1 期间 agent 零改动(回落路径用其既有能力),清理垫后(B2)。
4. **URL 走 ALB 域名**,与业务流量同一入口,不开内网特殊通路。

## 3. 站点直连配置(三件套)

`instances` 表新增(041 增量迁移,单条 ALTER):

- `control_api_url`——newapi 管理 API 地址(ALB 域名,明文存);
- `control_api_token`——管理员 access token,**AES-256-GCM 加密落库**(同 logs_readonly_dsn,密钥 CT_SECRET_KEY);
- `control_admin_user_id`——token 属主用户 ID(`New-Api-User` 头校验用)。

读取按站点取首个非空行、更新按站点整体写(与 DSN 完全同构)。配置界面在实例管理页"调权直连"对话框:保存即测试连接(**只读校验**:GET /api/channel/ 渠道列表,验 URL/token/权限但不写任何东西),失败不落库;token 不回显;清除配置回落命令队列。viewer 角色在实例列表中看不到直连配置字段。

## 4. 直连执行层(directcontrol 包)

`directcontrol.Store` 内嵌 `mysqlstore.Store`,仅覆写三个方法,引擎与前端**零改动**:

| 方法 | 直连行为 | 回落 |
| --- | --- | --- |
| CreateContinuousWeightChange | 同步 HTTP 写权重(15s 超时;circuit 规则连带优先级),成功后落与命令路径同构的三件套:tuning_recommendations + channel_commands(**终态 succeeded**)+ operation_audits(带 direct:true 标记);失败返回错误,引擎按既有写失败逻辑处理 | 原样入队 pending 命令 |
| CreateContinuousProbe | 落 **delivered** 状态命令行+probe_started 流水,后台 goroutine 跑探针轮(count×interval,整轮 8 分钟封顶——低于引擎 10 分钟探针丢失兜底),完毕 CompleteChannelCommand+审计+RecordContinuousProbeResult,状态机语义与 agent 回传完全一致 | 原样入队 |
| CreateTuningPreflight | **同步**执行无变更 GET+PUT,结果落终态 channel.verify 行;前端首轮轮询即得结果,45s 轮询代码不变 | 原样入队等 agent |

命令行状态设计要点:直连行只用 succeeded/failed/delivered,**永不 pending**——agent 领取(pending only)、过期哨兵清扫(pending only)、auto_paused 哨兵(system:auto+expired)天然全部绕开,无需改任何消费方。

channelcontrol 客户端从 `agent/internal/` 移至共享 `internal/`(agent 侧 FileTokenStore 拆回 `agent/internal/channeltoken`),新增只读 `Check`;加解密助手抽为 `server/internal/secrets` 共享包。

## 5. 语义变化与不变量

- 直连站点:auto 写权重从"最坏一个心跳周期"变为 tick 内同步秒级;探针轮不再被心跳量化;预检从异步 45s 轮询变为首轮即得(前端无感)。
- 人工优先/对账不变:检测仍读 agent 采集的 channel_current 快照 vs LastWrittenWeight。
- 多机站点缓存语义不变:PUT 落一台,另一台等 newapi 内存缓存同步周期——与 agent 打本机时完全一致。
- 直连写失败即时可见(引擎既有失败分支处理),不再有"命令没人领"的静默形态。
- 残余风险记档:HTTP 写成功但落库失败时 newapi 已变更而 CT 无痕——人工优先对账回路会捕获漂移并暂停;与 agent 路径"命令执行了但结果丢失"同类,未新增风险等级。

## 6. 验证

- 单测:executeWeightUpdate 字段映射/优先级只随熔断规则/失败传播;executeProbeRound 整轮计数与取消;channelcontrol.Check 只读+失败透出(fixture 按源码实证的对象形态);实例 handler 校验矩阵(协议头/空 token/缺 user id/连接测试失败不落库/清除免测试/token 加密往返/响应不回显 token)。
- SQL 契约:直连行终态/delivered 断言、控制配置站点化 SQL、041 单语句三列。
- **真库集成测试**(`CT_MYSQL_TEST_DSN` 门控,scratchpad MariaDB 实跑通过):041 双重放→配置加密落库→直连预检(无变更 PUT 且不漏 key)→权重直写(假 newapi 收到 25,命令终态+审计在库)→探针轮 3×1s 服务端跑完(命令 succeeded,状态 3/3 回填,挂账清空)→无配置站点回落 pending。

## 7. B2 清理批次(待全站点切换稳定后开工)

退役:agent 命令执行器与 channelcontrol 引用、CreateContinuous* 的队列分支、命令过期哨兵(简化为连续写失败→暂停+告警)、异步预检机制(改纯同步"测试连接")。届时 agent 需发一版;在那之前 **agent 零改动**。

## 8. 残余风险清单(2026-08-10 用户问询后系统盘点)

- **P3(B2 修,2026-08-10 与用户核对后从 P2 降级):直连写失败是哑的**。准确差异(代码核实):旧路径命令入队即乐观记账 LastWrittenWeight,agent 执行失败后快照与记账不符→2 分钟内触发 manual_override 暂停+事件——**失败一次就停且可见**(标签不准但有效);直连失败引擎不记账,快照恒与记账一致,人工接管检测不触发→**每分钟静默重试(每次烧 15s 超时),无事件无暂停**。现实触发面窄:基本只有"newapi 侧删除/重生成 access token 而未同步 CT 配置"一种(ALB 挡 CT=挡所有用户,不成立;newapi 挂则流量先报警)。间接可见性:写入流水停更、计算权重与当前权重持续不符。B2 以"连续写失败→暂停+告警"闭环。
- P3:同一失败场景下引擎 tick 会被拖慢——写失败每渠道烧满 15s 超时且站点内串行,N 个待写渠道≈N×15s/tick;ticker 丢过期 tick 自愈,只降调权节奏不积压。
- P3:探针/预检的 delivered 命令行在 server 重启(goroutine 被杀)后永久停在 delivered——状态机由引擎 10 分钟兜底恢复,仅留一行 DB 残迹;与 agent 死在执行中的既有形态同类。
- P3:HTTP 写成功但落库失败→newapi 已变而 CT 无痕,由人工优先对账回路捕获;与 agent 路径"执行了但结果丢失"同类,未新增风险等级。
- 记档:token 在 newapi 侧轮换后需到实例管理页重新保存直连配置(无自动探测,失败表现即上条 P2);直连探针轮完成早于引擎落 ProbeCommandID 的理论竞态在 interval≥1s 下窗口为亚毫秒级,不构成实际风险。
- 已闭环(398a4f7):引擎 ContinuousStore 由运行时断言改为编译期钉死——wrapper 签名漂移从"调权静默停摆"变为编译失败。

## 9. 写失败一等公民处理(2026-08-10 用户点破"直连应有更好方案"后实施)

用户判断正确:失败信号就在 CT 手里,不需要复刻旧路径"乐观记账+快照对账"的间接机制。落地(042 增量迁移三列 write_failure_streak/last_write_failure_at/last_write_error):

- **连续 3 次写失败 → 暂停**:paused_reason=write_failed,流水落一条 auto_paused 事件(evidence 带 reason+完整传输错误),只发一次不重复;
- **慢速自愈重试**:暂停后每 10 分钟放行一次写尝试(不再每 tick 烧 15s 超时),成功即自动解除暂停、清零计数,失败刷新计时继续暂停;
- **三个写入点全覆盖**(常规 weight_write/熔断置零/探针恢复),熔断在暂停期间退化为 observe 式记录(事件照发,不写);页面状态签显示"写入 new-api 失败已暂停"+具体错误,帮助抽屉同步;
- 引擎层实现,**命令队列路径同等受益**(入队失败同样计数),路径无关。

同时闭环 §8 前两条 P3(哑火+tick 被拖慢)。测试:三连败暂停+单事件+间隔内不重试+到点重试成功自愈、重试再失败保持暂停不重复告警;迁移契约;真库集成含 042 双重放复跑通过。
