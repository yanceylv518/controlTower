# 客户 TPM 电话预警

实现日期：2026-09-05。默认关闭；未配置语音凭据不会发起外呼。

## 规则和数据口径

- 客户以 `站点 ID + NewAPI 用户 ID` 隔离，跨渠道合计输入 Token 与输出 Token，与渠道 TPM 使用相同口径。
- Agent 复用每 30 秒采集的消费日志，按客户、秒汇总；不新增 NewAPI 查询。`user_rate_second` 走原 metric batch 事务去重，分流到 `user_rate_seconds`，不混入分钟和五分钟指标。
- 用户 0 是客户数据专用覆盖标记。旧 Agent 的渠道标记不能替代它。有积压、采集前上界查询失败、指标型 Agent 均不发送客户覆盖标记。
- 以所有必需日志采集器最新覆盖时间的最小值 T 为终点，计算 T−300、T−270、……、T 共 11 个采样点；每个 TPM 是该点前 60 秒的 Token 总量，窗口左闭右开。
- 因此最早一个 TPM 需要 T−360 的数据，首次升级需约 6 分钟预热。所有来源必须完整覆盖六分钟；标记间隔超过 60 秒，或最新标记超过 90 秒，停止判断。真实无流量可以是零，缺数据不能视为零。
- 默认同时满足 `max(TPM)-min(TPM) > 10000000` 和 `(max-min)/min*100 > 50` 才触发。最低值为零时，只要绝对差值超阈值即触发。严格“大于”，等于阈值不触发。上涨、下降均包括。
- **1000 万解释为最高与最低 TPM 的差值，不是当前 TPM**。设置页明确标注这一口径，可以调整差值与比例。
- 上报全部保存后通过非阻塞信号唤醒后台；每 5 秒兜底检查，不等待分钟结束。正常数据发现延迟约 0–30 秒，兜底另加最多 5 秒；长请求落日志、采集积压、多个电话串行投递和运营商拨号不包含在此估算内。

## 阿里云接口依据

实现对照以下官方文档，使用 Go 标准库实现 RPC HTTPS POST，不新增第三方依赖：

- [SingleCallByTts](https://help.aliyun.com/zh/vms/developer-reference/api-dyvmsapi-2017-05-25-singlecallbytts)
- [HTTP 协议及签名](https://help.aliyun.com/zh/vms/the-http-protocol-and-signature)
- [地域及服务接入点](https://help.aliyun.com/en/vms/developer-reference/api-dyvmsapi-2017-05-25-endpoint)

参数约定：

| 参数 | 使用方式 |
| --- | --- |
| Endpoint | `https://dyvmsapi.aliyuncs.com/` |
| Action / Version | `SingleCallByTts` / `2017-05-25` |
| RegionId / Format | `cn-hangzhou` / `JSON` |
| CalledNumber | 单个国内接听号码 |
| CalledShowNumber | 公共模式完全省略；专属模式填写真实号码或服务实例 ID，模板模式必须对应 |
| TtsCode | 同一阿里云账号已审核的语音通知模板 ID |
| TtsParam | JSON 字符串，传入 `customer` 和 `direction`；前者为设置中的 NewAPI 用户名，后者为按窗口极值时间顺序确定的“上涨”或“下降” |
| OutId | 14 字符随机业务 ID，用于回执关联；不是幂等参数 |
| PlayTimes | 2 |
| 鉴权 | HMAC-SHA1 RPC 签名、唯一 nonce、UTC Timestamp；支持 STS SecurityToken |

建议申请的模板：`您好，监控系统检测到客户${customer}的接口调用流量出现异常${direction}，请及时登录系统查看并处理。`

方向取触发窗口内最高、最低 TPM 采样点的时间顺序：最高点晚于最低点为“上涨”，否则为“下降”；重复极值使用最近一次出现的位置。阿里云模板必须同时声明 `${customer}` 和 `${direction}`，变量数量及名称需与 API 参数完全一致。

阿里云返回 `Code=OK` 且包含 `CallId` 才记为 **accepted（已受理）**，不等同接听成功。当前不接收最终通话回执；可使用记录中的 CallId 在阿里云查询。网络超时、无效响应、缺 CallId 均记为 unknown；不自动重试这次外呼。业务错误记录为 rejected，保留错误码供排查。

## 冷却、并发和频控

- `voice_dispatch_guard` 单行锁保护客户与接听号码组合的冷却检查、号码频控检查和拨号记录插入，跨进程有效。
- 发请求前先提交 unknown 记录。进程崩溃或结果未知时，这条记录继续占用冷却和号码额度，避免重复拨号。
- 同站点、同客户、同接听号码至少 10 分钟不重复外呼。不同订阅号码分别接收同一客户的预警；预留 15 秒网络安全余量，最终结果落库时保证至少再保留 10 分钟冷却。
- 同一号码跨客户、跨站点合并计算尝试次数：每分钟 1 次、每小时 5 次、24 小时 20 次。失败和未知也计入。阿里云账号中其他系统发起的电话不在 CT 本地计数中，仍可能触发阿里云频控。
- 持续异常在冷却结束且号码额度可用时可再次拨号。没有“失败立即重拨”，没有手工强制绕过冷却入口。
- 电话预警使用独立启用开关；主线已退役的 `CT_NOTIFICATIONS_ENABLED`、`CT_NOTIFY_BALANCE_ONLY` 不参与电话判断。
- API-only 模式不运行电话后台。配置页显示该状态。主进程与其他 worker 一起停止。

## 部署和启用

1. 先部署 Server 和 Web，执行 `077_user_tpm_voice.sql`（整合主线后避开已有 066 权限迁移）；再升级所有日志采集 Agent。不要先把新 Agent 接入不支持客户秒桶的旧 Server。
2. 阿里云完成企业资质和语音通知模板审核。公共模式无需显号；专属模式还需要匹配号码/服务实例。
3. 服务端环境变量配置：

   ```text
   CT_VOICE_ACCESS_KEY_ID=
   CT_VOICE_ACCESS_KEY_SECRET=
   CT_VOICE_SECURITY_TOKEN=
   ```

   前两项为 RAM 凭据；第三项仅 STS 临时凭据需要。最小权限为 `dyvms:SingleCallByTts`，资源 `*`。凭据仅在进程启动时加载，更新后重启 Server。Compose 已透传这三个变量。不要把密钥填到页面或提交到仓库。

4. 系统设置 → 客户 TPM 电话预警：填写 TTS 模板、客户站点 ID、用户 ID 和 NewAPI 用户名；再添加一个或多个内部值班号码。每个号码默认接收全部客户，也可选择只接收指定客户。电话预警有独立保存按钮；总系统设置保存不会覆盖电话配置。
5. 等待数据预热后，查看检测状态及最近 100 条电话记录。先用自己的值班号码完成一次真实联调，再配置正式对象。

`GET/PUT /api/dashboard/voice-alerts` 仅具有 `settings.manage` 权限的管理员 Session（包括全权限管理员）或已有管理 Bearer Token 可用，沿用 CSRF 校验。密钥不会返回到页面。电话记录中的手机号脱敏；配置修改写入操作审计，审计不保留明文手机号。

上报总量超过 10000 行时先舍弃可选用户×渠道拆分；仍超限则舍弃本批客户秒桶，并发送用户 0、request_count=1 的无效覆盖标记。判断窗口包含该标记时停止电话判断，避免缺失数据被当成流量下降；原有指标保留。

## 验证

- 单测覆盖 RPC 固定签名向量、公共/专属参数、STS、业务失败、超时无重试、缺 CallId、严格阈值、零流量、采集缺口、滚动边界、重启后的持久化冷却协议。
- MySQL 集成测试 `TestUserVoiceIntegration` 覆盖迁移、秒桶重复上报、普通指标隔离、8 并发单次抢占、客户与号码组合冷却到期、共享号码分钟/小时/24 小时额度及缺口拒绝。
- 使用独立本机 MySQL 测试实例验证，不访问生产数据库；实际服务和浏览器验证设置页加载及保存。
- 未提供真实凭据，因此未向阿里云发起真实付费电话；正式接听效果和模板审核结果仍需开通后联调。
