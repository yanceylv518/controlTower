# 验收记录：auto 模式控制链路预检 + 取消未保存编辑（2026-08-10）

范围：befc737（取消未保存编辑,纯前端）+ 2ceccc3（开 auto 前验证控制链路,server+agent+前端）。codex 交付,无批次文件。验收环境：Linux,server+agent 全量 vet+test、vue-tsc、desktop build 绿;新增三测试单跑通过。

## 结论:通过,零代码缺陷;一条 P2 级行为待用户确认,部署硬耦合一条必读

## 2ceccc3 控制链路预检

机制:前端把模型翻到 auto 并保存时,先 POST `/api/dashboard/tuning/preflight` 造一条 `channel.verify` 命令(复用 channel_commands,payload 空)→ Agent 领取后执行**无变更 Update**(GET 渠道→原样 PUT,delete key;真实走通认证读写链路)→ 前端轮询 45s;策略 PUT 带 preflight_command_id,server 端强制:**新开 auto 的保存必须携带 5 分钟内 succeeded 的 verify 命令**,否则 409 tuning_preflight_required(存量已 auto 的模型不受影响)。

核验:

- 无变更 Update 与 auto 写权重同一条 channelcontrol 链路——预检通过=写权重必然可用,语义成立;agent 测试断言 verify 调 Update 且三字段全 nil;
- server 端 409 闸有测试(无预检拒绝/succeeded 放行);比较基准是库里当前策略,server 权威,并发改动最多多跑一次预检;
- 命令过期哨兵按 `status='pending'` 全类型清扫,verify 命令不会滞留;**auto_paused 哨兵只认 created_by='system:auto',预检命令过期不会误触发 auto 暂停**;
- 结果回传走通用 CompleteChannelCommand+审计落库,类型无关;probing 特殊路径不受扰;
- viewer 白名单默认拒绝,新端点对 viewer 自动 403;POST 走 ApiClient 全局 X-Requested-With,CSRF 闸兼容;
- 无迁移(channel_commands command_type 自由文本),前端失败/超时/过期三态错误透出。

**⚠️ 部署硬耦合:rc22 agent 不认识 channel.verify**,会回 "unsupported command type" → 预检必失败 → **server 升级后要新开任何模型的 auto,agent 必须同步升级到含本笔的版本**。存量已处于 auto 的模型不受影响;失败原因会显示在界面上,不是静默。这是 agent 自 rc22 以来第一笔改动。

## befc737 取消未保存编辑

保存态快照(load/save/sync 后捕获;30s 轮询仅在无未保存编辑时刷新快照)+"取消更改"按钮还原;轮询在编辑中会保留已编辑行的 base 值合并新数据(不丢编辑);行加 row-key。

- **P2 待用户确认:切换模型会静默丢弃未保存编辑**(selectModel 里 dirty 时 cancelChanges(false),无提示无确认弹窗)——跨模型批量编辑后一次保存的用法从此不可能,误点模型导航即丢编辑。若是用户指示的预期行为请确认;否则建议改为确认弹窗或保留编辑。
- P3:渠道表排序从基础值改为按线上当前值(current_priority/current_weight)——auto 运行时 30s 轮询可能在编辑中途重排行,输入焦点易错位;
- P3:取消更改会把 current_weight/current_priority 一并回退到旧快照的值,最长 30s 后被轮询纠正(显示层)。

## 部署

无迁移。rc42/rc43(若已按上批打)均不含;**发布本批需 server+agent 同版新 tag,agent 部署要点回归**(A 机升级,B 机 CT_LOG_COLLECT_ENABLED=false)。

## 补充(同日,用户问询预检交互后核实)

预检为异步:POST 立即 202 返回 command_id,前端每秒轮询最多 45 次,期间仅保存按钮 loading+toast 提示;成功才继续 PUT 策略。Agent 侧同一趟上报内闭环(Heartbeat 领命令→立即执行→结果附在同趟 Report 回传),默认 30s 周期下端到端平均 ~15s/最坏 ~31s,45s 预算覆盖。**P3 记档:轮询预算写死 45s,若站点把 CT_LOG_POLL_INTERVAL_SECONDS 配到 ~40s 以上预检必假超时**(命令实际稍后成功但保存已中止);生产默认 30s 不受影响。
