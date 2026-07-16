# Control Tower 迭代记录

本文档是 Control Tower 的**版本迭代总账**：每发布一个版本，在这里记录它的迭代思路、功能范围、开发过程中的问题、部署过程中的问题、已知限制和下一步方向。目标是让任何人读完某个版本的章节，就能完整了解"当时为什么这么做、做了什么、踩了什么坑、还欠什么账"。

## 记录规则

- 每个版本一个二级章节，按时间倒序排列（最新版本在最上面，v1.0 除外作为起点）。
- 每个版本章节固定包含：版本定位与迭代思路 / 功能范围 / 开发过程与关键决策 / 开发问题 / 部署实录 / 部署问题 / 已知限制 / 遗留问题 / 下一版本方向。没有内容的小节写"无"，不删小节。
- 问题记录格式：现象 → 原因 → 解决办法 → 防复发措施（如有）。
- 涉及密钥、Webhook、密码的内容一律脱敏；真实配置永不入库。
- 长期开发计划见 `development-plan.md`（M0–M5），阶段任务看板见 `development-progress.md`；本文档记录的是"每个版本实际发生了什么"，三者互补。
- 文档末尾附新版本章节模板，起新版本时复制使用。

## 版本总览

| 版本 | 日期 | 内容概要 | 关键 commit |
| --- | --- | --- | --- |
| v1.0 错误预警版 | 2026-07-11 | Agent 独立运行，按渠道/客户监控最近 10 条请求错误 ≥3 发钉钉告警；一键安装部署 | `63b31fc`（功能）、`155126e`（部署文档） |

---

## v2.7-B1 Web 体验修正（完成，2026-07-16）

用户走查反馈的名称、指标顺序、分页、延时默认态和维度页滚动问题已集中修正。API 只新增 `display_name` 与慢样本 `offset`，不修改既有字段；七处列表统一默认每页 20 条；维度页左列独立滚动并默认渲染前 50 项。实现详情、兼容性与验证命令见 `docs/v2.7-b1-delivery.md`。

---

## v2.4-B1 Request ID 关联延时诊断（完成，2026-07-16）

Agent、Server、Dashboard API 与延时分诊页面的精确关联链路已完成。Nginx 慢样本携带 `$upstream_http_x_oneapi_request_id`，Server 只按 `instance_id + request_id` 批量关联 Control Tower 自有 `log_samples`/`log_events`，并明确区分 `matched`、`unmatched`、`multiple`。旧样本与采样截断安全降级为未关联，不影响错误告警和企业微信提醒。完整实现、限制及验证见 `docs/v2.4-b1-delivery.md`。

---

## v2.4-B1 Request ID 关联延时分诊（规划，2026-07-16）

### 1. 版本定位与迭代思路

现有 Nginx timing 能判断慢在首响应段还是传输段，但慢样本无法直接回答对应的用户、渠道和模型。生产验证确认 `X-Oneapi-Request-Id` 与 new-api 使用日志的 `Request ID` 完全一致，因此下一批使用 `instance_id + request_id` 做精确关联，不采用时间近邻猜测。

### 2. 功能范围

- Agent 从 timed access log 解析并上报慢样本 request_id；
- Server 在 Control Tower 自有数据内关联使用日志维度；
- 延时分诊展示并筛选用户、渠道、模型、令牌及关联状态；
- 缺失、多匹配和旧日志均显式降级，不影响采集、上报或企业微信提醒。

### 3. 开发过程与关键决策

本轮先完成方案与生产前提验证，功能代码尚未开始。确认通用 `X-Request-Id` 与 new-api 业务日志 ID 不同，最终应采集 Nginx `$upstream_http_x_oneapi_request_id`。完整执行计划见 `docs/codex-task-v2.4-b1-request-linked-latency.md`。

### 4. 开发问题

无。当前仅形成计划。

### 5. 部署实录

生产 Nginx 已人工验证目标日志字段；Control Tower 代码和 Agent 尚未升级。

### 6. 部署问题

无。

### 7. 已知限制

当前 Agent 尚未解析 request_id；`uht` 是上游首响应时间，不等同于精确模型 TTFT。

### 8. 遗留问题

按任务文档完成 Agent、Server、Web、迁移与端到端验收。

### 9. 下一版本方向

完成 v2.4-B1 后再评估从 new-api 日志提取精确首字时间，以及是否需要全量而非仅慢样本的维度级延时统计。

---

## v2.0.0-rc5 Control Tower Web 收尾发布（2026-07-15）

### 1. 版本定位与迭代思路

在 rc4 已完成 Agent 企业微信告警与双模式上报的基础上，重新打包最新 Control Tower Server/Web 和 Agent，集中交付延时诊断、维度性能与 Web 可用性收尾。

### 2. 功能范围

- Agent amd64/arm64 与 Server amd64 三份发布包；
- Nginx timing 采集和延时分诊页面；
- 维度 latest 查询索引与批量名称解析性能优化；
- 告警可读名称、维度现场跳转、P95 量程提示；
- 渠道搜索、状态分组、折叠组与健康墙；
- 1h/6h/24h 区间汇总指标和静默自动刷新。

### 3. 开发过程与关键决策

Dashboard API 保持向后兼容，仅新增告警维度字段和 metric-history 的可选聚合参数。Agent 上报与企业微信直发仍相互隔离，Control Tower 上报失败不影响 new-api 请求链路。

### 4. 开发问题

发布前发现远程新增了静默刷新验收要求，已先快进同步并实现，再执行全量测试和打包，避免从过期提交制作发布包。

### 5. 部署实录

发布 Tag：`v2.0.0-rc5`。由 tag 流水线生成三份 tar.gz、`SHA256SUMS` 与 GHCR Server 镜像；生产部署完成后补充实际运行结果。

### 6. 部署问题

暂无。部署时必须保留现有 Agent 配置和 Compose `.env`，仅替换二进制或镜像。

### 7. 已知限制

- 无域名时 Web 仍通过公网 IP 与 8080 端口访问；
- Nginx 延时分诊需要在 Agent 配置中设置真实的 `CT_NGINX_ACCESS_LOG`，且日志格式必须包含 timing 字段；
- 百万行级 latest 查询仍需在真实部署数据库完成最终耗时核验。

### 8. 遗留问题

发布后核对 GitHub Actions、Release 附件、GHCR 镜像和两台 Agent 的持续上报。

### 9. 下一版本方向

依据 rc5 生产观察结果处理真实数据和部署反馈，不在本次发布中扩大功能范围。

---

## v1.1.2 Agent 告警切换企业微信机器人（2026-07-14）

### 1. 版本定位与迭代思路

按运维沟通渠道调整，将 Agent 直发告警从钉钉机器人整体切换为企业微信群机器人；告警规则、滑动窗口、episode、提醒和缓存失效判断均不改变。

### 2. 功能范围

- 新配置为 `CT_WECOM_WEBHOOK_URL`，支持独立告警和双模式；
- 使用企业微信文本机器人协议并校验响应 `errcode`；
- 旧 `CT_DINGTALK_WEBHOOK_URL` 不再读取，避免迁移期间重复向两个群发送；
- 安装脚本、配置示例、部署手册同步更新。

### 3. 开发过程与关键决策

企业微信与原钉钉文本载荷结构一致，继续使用标准库 HTTP/JSON 实现，零新增依赖。Server 通知中心不在本次范围内。

### 4. 开发问题

无。

### 5. 部署实录

待部署：先创建并直测企业微信机器人，再将生产配置中的旧钉钉变量替换为 `CT_WECOM_WEBHOOK_URL`，最后替换 Agent 二进制并重启。

### 6. 部署问题

无。

### 7. 已知限制

企业微信群机器人 Webhook 泄漏后必须删除并重新创建机器人；真实地址不得进入截图、聊天记录或代码仓库。

### 8. 遗留问题

Server 通知中心仍保留通用 Webhook/钉钉渠道，后续如需统一切换另开批次。

### 9. 下一版本方向

观察企业微信投递稳定性和告警噪音，按需增加渠道级缓存告警豁免。

---

## v1.0 错误预警版（2026-07-11）

### 1. 版本定位与迭代思路

**定位**：最小可用的告警闭环，第一个真正部署到生产的版本。

**迭代思路**：Control Tower 完整规划（Server + Web + App，见 `development-plan.md` M0–M5）体量较大，先切出用户最迫切的一个需求做成独立可交付的产品——"渠道或客户持续出错时，钉钉群里立刻知道"。选择 Agent 端独立实现而不是走 Server 告警链路，基于三个判断：

1. **部署成本**：只需要在 new-api 服务器上放一个二进制 + 一个只读数据库账号 + 一个钉钉 webhook，不需要部署 Server、MySQL、Web 中的任何一个。
2. **数据完整性**：Agent 直接读源库 `logs` 表，天然能看到全部事件；Server 端评估同样的规则需要 Agent 以 `full_debug` 模式全量上报，数据量大且多一跳延迟。
3. **可靠性**：告警不依赖 Server 存活；规则是无状态滑动窗口，对进程重启、事件重复都不敏感。

Server 端的同规则链路（`recent_errors` 告警规则 + 钉钉通知渠道类型）也同步实现了，作为后续正式版（Web 告警中心）的基础，但 v1.0 的生产路径是纯 Agent 端。

### 2. 功能范围

- **规则**：按（实例, 渠道）和（实例, 客户）两个维度独立维护滑动窗口，各自统计最近 `CT_ALERT_ERROR_WINDOW`（默认 10）条请求中的错误数，达到 `CT_ALERT_ERROR_THRESHOLD`（默认 3）条即触发。
- **通知**：钉钉群机器人 webhook，`msgtype=text`，消息含实例、维度标签、失败比例、最新错误摘要（已脱敏）、时间；校验钉钉响应 `errcode`（钉钉被拒时 HTTP 仍是 200）。
- **防刷屏**：同一维度一个故障周期（episode）只发一次；窗口内错误降回阈值以下后重新武装，再次恶化才发下一条；发送失败下个采集周期自动重试。
- **独立模式**：配置 `CT_DINGTALK_WEBHOOK_URL` 后 `CT_SERVER_URL` 可选；不配 Server 即"只采集 + 只告警"模式，不心跳不上报。
- **首启不回放历史**：全新安装自动把游标定位到 `logs` 表当前末尾，历史错误不触发告警。
- **部署**：`install-agent.sh` 一键安装（交互 / `--dsn`+`--webhook` / `--config` 三种方式），自动建用户、写配置（0600）、跑 preflight、装 systemd（非 root、文件系统保护）、启动。

### 3. 开发过程与关键决策

- **规则实现为无状态窗口，不做增量计数器**：每个采集 pass 把新事件按序喂入内存窗口，判断整个窗口。逻辑简单、可测，对重启/乱序/重复天然免疫；代价是重启后窗口清空，需要新事件重新累积（可接受）。
- **episode 去重语义**：触发后置 `alerted=true`，只有窗口错误数先降到阈值以下才重置——保证"持续故障不刷屏、恢复后复发再报"。发送失败立即重置 `alerted`，靠下个 pass 的全量评估自动重试（无需给该维度来新事件）。
- **首启游标自动定位**：初版部署文档要求手动 `SELECT MAX(id)` 写 state.json，容易漏做且做错会导致全量历史回放刷屏。改为代码内检测"无状态文件"时自动 `Backlog(0)` 取当前最大 ID 落盘，该 pass 只定位不采集。
- **顺手修复 Server 侧一个既有缺陷**：通知投递去重原本是"同一告警发送成功后永不再发"，告警恢复后再次触发会永远静默。修复为告警 resolved 时把已发送投递置为过期，下一个 episode 可重新通知。该缺陷影响所有告警规则，不只这条。
- **preflight 适配独立模式**：原本必查 Server `/healthz`，独立模式下会误报失败；改为 `CT_SERVER_URL` 为空时跳过该检查项。

### 4. 开发问题记录

| 问题 | 现象/风险 | 解决 |
| --- | --- | --- |
| 通知永久去重 | 告警恢复后再次触发不再通知（投递记录 status=sent 后 `NotificationDeliveryDue` 永远 false） | resolved 告警的 sent 投递置 expired 并重置 next_attempt；带 E2E 测试（触发→通知→恢复→再触发→再通知） |
| 首启历史回放 | 游标从 0 开始会回放全部历史日志，旧错误批量触发钉钉告警 | 独立模式首启自动定位游标到 `logs` 当前最大 ID；单测覆盖"首个 pass 只定位不采集" |
| preflight 独立模式误报 | 不配 Server 时 preflight 因 healthz 不可达报 FAIL，安装脚本因此中止 | ServerURL 为空时该项输出 `skipped: standalone alert-only mode` |
| 钉钉假成功 | 钉钉机器人拒绝消息（如关键词不匹配）时 HTTP 仍返回 200 | 解析响应体 `errcode`，非 0 记为发送失败并重试 |

### 5. 部署实录（Ubuntu + new-api 生产服务器）

> 以下为 v1.0 在生产环境的完整部署流程与验证方法（原 `deployment-error-alert.md` 内容并入于此）。

#### 5.1 部署范围

本次部署使用 standalone 模式：

- Agent 直接读取 new-api MySQL 的 `logs` 表，每 30 秒增量查询一次。
- Agent 按渠道和客户分别维护最近 10 条请求，错误数达到 3 条时发送钉钉告警。
- 不需要部署 Control Tower Server；不修改 new-api 代码、路由或请求链路。
- 不读取请求体、响应体、API Key 或 Cookie。

#### 5.2 确认服务器架构

```bash
uname -m
```

| `uname -m` | 部署包 |
|---|---|
| `x86_64` | `linux-amd64` |
| `aarch64` | `linux-arm64` |

本次服务器返回 `x86_64`，选择 `error-alert-agent-63b31fc-linux-amd64.zip`。

#### 5.3 创建 MySQL 只读账号

在 new-api MySQL 中执行：

```sql
CREATE USER 'ct_readonly'@'%' IDENTIFIED BY '设置强密码';
GRANT SELECT ON newapi.logs TO 'ct_readonly'@'%';
FLUSH PRIVILEGES;
```

检查权限与索引：

```sql
SHOW GRANTS FOR 'ct_readonly'@'%';
SHOW INDEX FROM newapi.logs;
```

不应授予 `INSERT`、`UPDATE`、`DELETE`、`ALTER` 或 `DROP` 权限。重点确认 `logs.id` 存在索引——Agent 按日志 ID 增量读取，不会每 30 秒扫全表。

#### 5.4 创建钉钉机器人

在钉钉目标群中：群设置 → 机器人管理 → 添加自定义机器人 → 安全设置选择"自定义关键词"，关键词设置为 `告警` → 复制 Webhook 地址。

当前 Agent 使用 Webhook 方式发送文本告警，暂不支持钉钉加签模式。

#### 5.5 上传和解压部署包

```powershell
scp "error-alert-agent-63b31fc-linux-amd64.zip" root@SERVER_IP:/tmp/
```

```bash
ssh root@SERVER_IP
apt-get update && apt-get install -y unzip   # Ubuntu 可能没有 unzip
rm -rf /tmp/control-tower-agent
mkdir -p /tmp/control-tower-agent
unzip /tmp/error-alert-agent-63b31fc-linux-amd64.zip -d /tmp/control-tower-agent
cd /tmp/control-tower-agent
ls -lh
```

正常应包含：`control-tower-agent`、`install-agent.sh`、`control-tower-agent.service`、`agent.standalone.config.example`、`README.md`、`SHA256SUMS`。

#### 5.6 配置 Agent

```bash
cp agent.standalone.config.example agent.config
nano agent.config
```

```ini
CT_AGENT_ID=agent-prod-01
CT_INSTANCE_ID=inst-prod-01
CT_LOG_DSN=ct_readonly:数据库密码@tcp(127.0.0.1:3306)/newapi?parseTime=false&timeout=2s
CT_DATA_DIR=/var/lib/control-tower-agent
CT_DINGTALK_WEBHOOK_URL=https://oapi.dingtalk.com/robot/send?access_token=钉钉Token
CT_ALERT_ERROR_WINDOW=10
CT_ALERT_ERROR_THRESHOLD=3
CT_LOG_POLL_INTERVAL_SECONDS=30
```

说明：`CT_LOG_DSN` 同时包含数据库用户名和密码；standalone 模式不需要配置 `CT_SERVER_URL`；不要把真实配置提交到 Git。

#### 5.7 修复 Linux 换行并安装

部署包如在 Windows 环境生成，Shell 文件可能带有 CRLF 换行，先执行：

```bash
sed -i 's/\r$//' install-agent.sh
sed -i 's/\r$//' control-tower-agent.service
chmod +x control-tower-agent install-agent.sh
```

然后安装：

```bash
sudo ./install-agent.sh --binary ./control-tower-agent --config ./agent.config
```

安装脚本会创建 `ct-agent` 用户、安装二进制、创建数据目录、执行 preflight、安装 systemd 服务并启动 Agent。

#### 5.8 验证安装结果

```bash
sudo systemctl status control-tower-agent
sudo systemctl is-active control-tower-agent
sudo systemctl is-enabled control-tower-agent
sudo journalctl -u control-tower-agent -f
```

正常状态为 `Active: active (running)`；preflight 日志应包含：

```text
preflight pass mysql_ping: connected
preflight pass logs_table: queryable
preflight pass logs_id_index: logs.id index found
```

首次启动时 Agent 从当前最新日志 ID 开始，不扫描历史日志，不会因历史错误立即发送告警。

#### 5.9 停止和卸载

```bash
sudo systemctl disable --now control-tower-agent
sudo rm -f /etc/systemd/system/control-tower-agent.service
sudo rm -f /usr/local/bin/control-tower-agent
sudo rm -rf /etc/control-tower
sudo rm -rf /var/lib/control-tower-agent
sudo systemctl daemon-reload
```

### 6. 部署问题记录

| # | 问题 | 现象 | 原因 | 解决 | 防复发 |
| --- | --- | --- | --- | --- | --- |
| 1 | Ubuntu 没有 unzip | `Command 'unzip' not found` | 最小化系统未预装 | `apt-get install -y unzip` | 后续部署包考虑改用 tar.gz |
| 2 | CRLF 换行导致脚本无法执行 | `/usr/bin/env: 'bash\r': No such file or directory` | 部署包经 Windows 生成/中转，Shell 脚本被转成 CRLF | `sed -i 's/\r$//'` 后再执行 | 仓库已加 `.gitattributes` 强制 `*.sh`/`*.service`/`*.example`/`*.sql` 始终 LF 检出 |
| 3 | 配置模板文件名带 example 被误用 | 直接拿模板当生产配置 | 命名歧义 | `cp` 改名为 `agent.config` 后编辑，用 `--config` 传入 | 部署文档固化正确流程 |
| 4 | Webhook Token 泄露 | Webhook 出现在截图/聊天中 | 操作过程未脱敏 | 钉钉机器人设置中重新生成 Webhook，更新 `/etc/control-tower/agent.config` 后 `systemctl restart` | 真实 Webhook、数据库密码、生产配置永不入库、不入截图 |

### 7. 已知限制

**告警延迟受写日志时机制约**。规则依赖 new-api 将请求结果写入 `logs` 表：一个等待 600 秒才最终写入 504 的请求，告警时间约为 `600 秒 + 0~30 秒`。当前 Agent 无法发现仍在执行中的请求，也无法在请求完成前判断它最终是否失败。本方案适合"已完成请求"的客户/渠道维度告警，不适合请求尚未结束时的实时告警。

**600 秒超时盲区的推荐解法：两层告警**。

- **第一层（网关快速告警，Control Tower 边界之外）**：在 Nginx/SLB/反向代理层采集 HTTP 状态码、504/5xx 数量、请求耗时、upstream 耗时、连接失败数。建议阈值：耗时 >60s 慢请求告警、>300s 严重慢请求告警、出现 504 立即告警、连续 3 个 504 升级告警。这层不等 `logs` 写入，能快速发现异常，但通常只能定位到实例/接口，识别不了客户和渠道。
- **第二层（Agent 业务维度告警，即本版本）**：读 `logs` → 按客户和渠道维护最近 10 条 → 3 条错误触发。延迟较大，但能准确回答"哪个客户、哪个渠道持续失败"。
- **后续如需请求开始即识别客户/渠道**：需要在请求开始时产生轻量事件（request_id、user_id、channel_id、model、started_at），可通过 new-api middleware、网关请求头或本地 Unix Socket/事件文件实现；事件中不包含请求体、响应体、API Key 或 Cookie。

**其他限制**：钉钉仅支持关键词模式（不支持加签）；滑动窗口在内存中，Agent 重启后需新事件重新累积；规则评估只看 `type IN (2,5)`（消费/错误）两类日志。

### 8. 遗留问题

- 钉钉加签（安全模式）支持——目前只能用自定义关键词模式。
- 部署包格式改 tar.gz（避免 unzip 依赖）；打包流程规范化（LF、SHA256SUMS 自动生成）。
- Agent 既有缺陷清单（见 `development-progress.md`「Agent 采集与上报修复计划」）：P0 = logs 采集 NULL 字段防护（可导致采集永久卡死）、渠道快照 collector 常驻化；P1 = 心跳解耦、缓冲毒条目防护、渠道命令去重。独立告警模式不受这些影响（不走上报链路，采集 SQL 的 NULL 风险仍在）。
- 告警消息模板不可配置；单钉钉群（多群需求未出现）。

### 9. 下一版本方向（候选，按价值排序）

1. **Agent P0 缺陷修复**（尤其 NULL 字段防护——它同样影响独立模式的采集稳定性）。
2. **告警能力增补**：慢请求维度（use_time 超阈值计入窗口或独立规则）、恢复通知（故障恢复后发一条"已恢复"）、静默时间段配置。
3. **钉钉加签模式**支持。
4. **打包与发布规范**：Makefile 出 tar.gz 部署包（LF 保证、含校验和），版本号 `-ldflags` 注入。
5. 回归主线：按 `development-plan.md` M0–M5 推进 Server/Web/App。

---

## 新版本章节模板

```markdown
## vX.Y 版本名（YYYY-MM-DD）

### 1. 版本定位与迭代思路
### 2. 功能范围
### 3. 开发过程与关键决策
### 4. 开发问题记录
| 问题 | 现象/风险 | 解决 |
### 5. 部署实录
### 6. 部署问题记录
| # | 问题 | 现象 | 原因 | 解决 | 防复发 |
### 7. 已知限制
### 8. 遗留问题
### 9. 下一版本方向
```


## v1.0.2 错误预警部署包与升级记录（2026-07-11）

### 1. 本次发布

基于提交 `1e5ecf7` 生成错误预警 Agent 部署包：

- `error-alert-agent-1e5ecf7-linux-amd64.zip`
- `error-alert-agent-1e5ecf7-linux-arm64.zip`

校验值：

```text
amd64  4190DC85FB3F67AE026FCF5AF26D4113C64820D2CFC7EE9A80A4B0124E9FE4F1
arm64  E29726651DE6DC984CED73E0A9E65EA0BC368D45782EE1FAF8765B3C9E316327
```

本次包内 Shell 脚本和 systemd 文件已统一为 Linux LF 换行，避免在 Ubuntu 上出现：

```text
/usr/bin/env: 'bash\r': No such file or directory
```

### 2. 更新部署步骤

在服务器上选择对应架构的包：

```bash
uname -m
# x86_64 选择 linux-amd64
# aarch64 选择 linux-arm64
```

上传并解压：

```bash
unzip error-alert-agent-1e5ecf7-linux-amd64.zip -d /tmp/control-tower-agent
cd /tmp/control-tower-agent
```

使用已有生产配置升级前，先备份：

```bash
sudo cp /etc/control-tower/agent.config /etc/control-tower/agent.config.bak
```

停止旧 Agent：

```bash
sudo systemctl stop control-tower-agent
```

执行安装升级：

```bash
sudo ./install-agent.sh \
  --binary ./control-tower-agent \
  --config /etc/control-tower/agent.config
```

安装脚本会覆盖二进制和 systemd 服务，并重新执行 preflight。数据库用户名从 DSN 中读取，安装脚本不假设用户名固定为 `ct_readonly`。

启动并验证：

```bash
sudo systemctl status control-tower-agent
sudo systemctl is-active control-tower-agent
sudo journalctl -u control-tower-agent -n 50 --no-pager
```

### 3. 配置和权限说明

- `CT_LOG_DSN` 中的用户名和密码必须替换为实际数据库账号。
- `logs` 表的 `SELECT` 权限是必需的。
- `channels` 表的 `SELECT` 权限是可选的，仅用于在钉钉消息中显示渠道名称。
- 没有 `channels` 权限时，Agent 继续运行，告警显示渠道 ID。
- 不要删除 `/var/lib/control-tower-agent/state.json`，否则 Agent 会从当前日志末尾重新建立游标。
- Webhook 或数据库密码出现在截图、日志或聊天中后，应立即轮换。

### 4. 回滚步骤

如果升级后异常：

```bash
sudo systemctl stop control-tower-agent
sudo cp /etc/control-tower/agent.config.bak /etc/control-tower/agent.config
sudo cp /path/to/previous/control-tower-agent /usr/local/bin/control-tower-agent
sudo systemctl start control-tower-agent
sudo journalctl -u control-tower-agent -n 50 --no-pager
```

回滚只替换二进制和配置，不删除状态文件，避免重复读取历史 `logs`。

### 5. 验证结果

- Agent 单元测试：通过。
- amd64/arm64 构建：通过。
- 两个包均验证为 Linux ELF。
- Shell/systemd 文件：已验证无 CRLF。


## v1.0.3 安装升级重启修复与新部署包（2026-07-11）

### 1. 问题

使用旧安装脚本升级已运行的 Agent 时，脚本虽然复制了新二进制并执行了 `systemctl enable --now`，但如果服务已经处于运行状态，systemd 不会因为 `enable --now` 自动重启已有进程。结果是磁盘上的二进制已更新，但运行中的 Main PID 仍然是旧进程。

### 2. 修复

安装脚本现在使用：

```bash
systemctl enable control-tower-agent
systemctl restart control-tower-agent
```

因此首次安装和已有服务升级都会真正启动/重启 Agent。

### 3. 新部署包

基于提交 `f7d3df1` 生成：

- `error-alert-agent-f7d3df1-linux-amd64.zip`
- `error-alert-agent-f7d3df1-linux-arm64.zip`

校验值：

```text
amd64  ECCE9C4D5F8F3DEB8163D0F57BB63D6DB78A2F714F71F29FDAF8391389777880
arm64  ACA7D6F80383A45818FE2A000996B7625DE7300C577BBE2D1A5E744F0F065C72
```

包内 Shell 和 systemd 文件均已验证为 LF 换行。

### 4. 推荐升级步骤

升级前备份配置：

```bash
sudo cp /etc/control-tower/agent.config /etc/control-tower/agent.config.bak
```

进入新包目录并执行：

```bash
sed -i 's/\\r$//' install-agent.sh
sed -i 's/\\r$//' control-tower-agent.service
chmod +x control-tower-agent install-agent.sh

sudo cp /etc/control-tower/agent.config ./agent.config

sudo ./install-agent.sh \
  --binary ./control-tower-agent \
  --config ./agent.config
```

验证新进程已经加载：

```bash
sudo systemctl status control-tower-agent
sudo systemctl show control-tower-agent -p MainPID -p ActiveEnterTimestamp
sudo journalctl -u control-tower-agent -n 50 --no-pager
```

重点确认 `ActiveEnterTimestamp` 是本次升级时间，而不是旧启动时间。

### 5. 回滚

如果升级异常：

```bash
sudo systemctl stop control-tower-agent
sudo cp /etc/control-tower/agent.config.bak /etc/control-tower/agent.config
sudo cp /path/to/previous/control-tower-agent /usr/local/bin/control-tower-agent
sudo systemctl start control-tower-agent
```

不要删除 `/var/lib/control-tower-agent/state.json`，避免重新读取历史日志。


## v1.0.4 钉钉告警关键词编码修复与新部署包（2026-07-12）

### 1. 问题

Agent 服务正常运行，但发生多条错误后钉钉没有收到告警。检查发现告警消息模板中的关键词存在编码异常，而钉钉机器人安全设置使用的是关键词“告警”。钉钉机器人可能返回 HTTP 200，但通过 `errcode` 拒绝关键词不匹配的消息。

### 2. 修复

告警消息前缀改为 Go Unicode 转义，运行时明确输出真实关键词：

```text
[告警]
```

同时增加自动化测试，确保消息包含“告警”关键词。当前规则和采集逻辑不变：

- 每 30 秒增量查询 `logs`
- 按渠道和客户维护最近 10 条
- 错误达到 3 条触发
- 同一故障期间只发送一次
- 发送失败时下一轮重试

### 3. 新部署包

基于提交 `f77d495` 生成：

- `error-alert-agent-f77d495-linux-amd64.zip`
- `error-alert-agent-f77d495-linux-arm64.zip`

校验值：

```text
amd64  6BC11D2A18832CB8FC5678295E2D8BFCFF62961DCF6DC0DD7491DBA45A2073BB
arm64  8A097911A60B37F742AA6A23E6F2A7393849DA9377240E33C19501D4FF90A494
```

### 4. 服务器升级

上传对应架构的新包，解压后执行：

```bash
cd /tmp/error-alert-agent-f77d495-linux-amd64
sed -i 's/\\r$//' install-agent.sh
sed -i 's/\\r$//' control-tower-agent.service
chmod +x control-tower-agent install-agent.sh

sudo cp /etc/control-tower/agent.config /etc/control-tower/agent.config.bak
sudo cp /etc/control-tower/agent.config ./agent.config

sudo ./install-agent.sh \
  --binary ./control-tower-agent \
  --config ./agent.config
```

确认新进程已加载：

```bash
sudo systemctl show control-tower-agent -p MainPID -p ActiveEnterTimestamp
sudo journalctl -u control-tower-agent -n 50 --no-pager
```

### 5. 钉钉验证

机器人安全设置中的自定义关键词必须是：

```text
告警
```

如果仍然没有收到消息，先检查 Agent 日志：

```bash
sudo journalctl -u control-tower-agent --since "10 minutes ago" | grep -i "dingtalk\|alert\|failed"
```

如果出现关键词不匹配、Webhook 失效或 `errcode`，重新生成钉钉机器人 Webhook 并更新 Agent 配置。


## v1.0.5 episode 去重导致预警丢失的修复（2026-07-12）

### 1. 问题

2026-07-12 17:56 渠道 26 连续 3 条错误未触发钉钉告警。调查记录见 `agent-alert-missed-alert-analysis.md`，定案根因：当天 03:02 渠道 26 已触发过告警（episode 开始），该渠道成功请求极少（几乎每次都靠 fallback 救回），窗口内错误数从未降到阈值以下，episode 永不重臂——按"同一故障期间只发送一次"的设计，后续错误被无限静默。设计缺陷，非代码 bug。

### 2. 修复（本版本）

| 修复 | 配置 | 默认 | 说明 |
| --- | --- | --- | --- |
| 窗口时间衰减 | `CT_ALERT_WINDOW_MAX_AGE_MINUTES` | 60（0 关闭） | 超龄事件滑出窗口；稀疏渠道错误清空后 episode 自然重臂，新故障=新告警 |
| 持续告警重提醒 | `CT_ALERT_REMIND_MINUTES` | 60（0 关闭；初版 240，按反馈调短） | episode 持续 firing 超过该时长再发提醒，附起始时间与累计错误数；因窗口衰减，提醒只发给仍在持续出错的维度 |
| 按维度审计日志 | 无 | 常开 | 触发/提醒时输出 `dimension=... kind=... window=... errors=...`，补齐调查文档第 9 节要求 |

提醒消息示例：

```text
[告警] 【Control Tower 告警】渠道错误持续
实例: inst-prod-01
渠道 26(xxx) 自 07-12 03:02 起持续异常，累计 47 条错误，最近 10 条请求中 3 条失败
```

提醒发送失败沿用既有重试语义（下一轮采集重试）；错误窗口清空的维度状态自动清理，防止内存无限增长。

### 3. 验证

- `go test ./agent/...` 通过；新增用例：时间衰减后稀疏渠道重臂再告警、提醒间隔与累计计数、提醒失败重试、间隔内不重复提醒。
- 按本次事故重放：03:02 首告警 → 每小时"持续异常"提醒；或渠道曾有 1 小时无错误 → 窗口清空 → 17:56 为全新告警。两条路径均不再丢失。

### 4. 升级说明

替换二进制重启即可，新配置项有默认值无需修改配置文件；如需关闭新行为，设 `CT_ALERT_WINDOW_MAX_AGE_MINUTES=0`、`CT_ALERT_REMIND_MINUTES=0`。


## v1.0.6 logs 采集 NULL 字段防护与告警时间兜底（2026-07-13）

### 1. 内容

- **NULL 字段防护（P0，`f6c81f0`）**：采集 SQL 对全部可空列统一 `COALESCE`（文本→空串、数值→0），消除"源表出现 NULL 行 → Scan 报错 → 游标不推进 → 采集永久停摆"的风险。`id`/`type` 保持原样（主键不可空；type 为 NULL 的行匹配不上 `WHERE type IN (2,5)`）。
- **加固（review 发现）**：`COALESCE(created_at, 0)` 会把 NULL 时间变成 Unix 0（1970 年），污染所有下游——告警窗口的时间衰减会立即清出该事件（错误静默漏计），指标聚合会生成 1970 年的桶，上报路径会入库错误时间戳。修复放在**采集边界**（`scanLogRow`：`createdAtUnix <= 0` 时用采集时间代替，一处修好全部下游），`observeLocked` 保留 Unix ≤0 兜底作纵深防御；两层均有回归测试。

### 2. 验证

`go vet ./...`、`go test ./...` 全部通过；SQL 契约测试覆盖可空列默认值；真实源库 NULL 行验证待部署环境执行（测试库插入除 id/type 外全 NULL 的行，确认采集不中断、游标推进、错误正常计入窗口）。

### 3. 升级说明

替换二进制重启即可，无配置变化。此修复只有部署新二进制后才在生产生效。


## v1.1.1 移除慢返回告警 + 新增缓存失效预警（2026-07-14）

### 1. 版本定位与迭代思路

生产反馈驱动的两项调整：①慢返回窗口告警（v1.1-B1/信号 F）在生产被判定为噪音大于价值，整条规则移除（配置项一并删除，历史设计保留在 design-v1.1-early-warning.md 信号 F 节）；②新增面向成本的告警——渠道 prompt 缓存疑似失效检测（缓存失效直接放大 token 成本,比延时问题更需要即时人工介入）。

### 2. 功能范围

- **移除**：`CT_ALERT_SLOW_*` 全部配置、慢返回窗口/episode/消息、相关测试；错误窗口、衰减、提醒、禁用渠道抑制等其余行为不变；
- **新增缓存失效规则**（仅渠道维度）：`consume` 日志中 `prompt_tokens > CT_ALERT_NOCACHE_MIN_PROMPT_TOKENS`（默认 512）的请求进入独立窗口（`CT_ALERT_NOCACHE_WINDOW`，默认 10）；窗口攒满且全部无缓存命中（`other.cache_tokens` 缺失或为 0）→ 钉钉告警"渠道缓存疑似失效"；任一条命中缓存即重臂；episode 去重/时间衰减/60 分钟提醒复用既有骨架；`CT_ALERT_NOCACHE_ENABLED` 默认开。

### 3. 开发过程与关键决策

- "无缓存"判定含 cache_tokens 字段缺失：能捕捉"上游把缓存字段整个丢了"的故障形态,代价见已知限制；
- 触发条件是"满窗全未命中"（阈值=窗口大小）而非比例阈值——缓存命中率天然波动,只有连续 10 条大输入全不命中才是结构性失效的证据；
- 钉钉关键字前缀保持 unicode 转义写法（防 Windows 编码事故的既有纪律）。

### 5. 部署实录

待部署：重建二进制替换生产 Agent；旧配置中的 CT_ALERT_SLOW_* 行可留（未知键忽略）也可删。runbook 4.1 的钉钉链路验收已改为 webhook 直测（原步骤依赖慢返回规则）。

### 7. 已知限制

- 模型/上游本来就不支持缓存的渠道会告警一次并按提醒间隔重复——对策：调高 token 下限、或该部署关闭规则（后续如成噪音,可加渠道白名单）；
- 判定依赖 new-api 把缓存 token 写进 logs.other 的 cache_tokens 字段；上游不回报缓存用量时与"缓存失效"不可区分。

### 9. 下一版本方向

随 v2.0-B2 一起部署（需打新 tag 出产物）；观察缓存告警噪音水平,决定是否需要渠道级豁免配置。

## v1.1.0 慢返回窗口告警（v1.1-B1，合入 2026-07-13，生产部署确认 2026-07-14）

### 1. 版本定位与迭代思路

v1.1 预警体系的第一个落地信号（信号 F）：抓"返回了但很慢"的前奏。600 秒超时雪崩前通常先出现一批 120~300 秒才返回的慢请求，对它设窗口规则，比等超时写库早数分钟发现恶化。与错误窗口完全同构，复用 episode 去重/衰减/提醒/恢复重臂。

### 2. 功能范围

- 按渠道/客户维度：最近 `CT_ALERT_SLOW_WINDOW`(10) 条完成请求中 `use_time ≥ CT_ALERT_SLOW_SECONDS`(120s) 达 `CT_ALERT_SLOW_THRESHOLD`(3) 条 → 钉钉"慢返回"告警；
- 流式请求独立阈值 `CT_ALERT_SLOW_STREAM_SECONDS`(300s，设 0 排除流式)，避免误伤正常长回答；
- `CT_ALERT_SLOW_ENABLED` 默认开；慢规则与错误规则各自独立 episode；
- 同批附带：告警事件留痕 alert-events.jsonl（5MB 轮转、写失败不影响告警主链路）。

### 5. 部署实录

生产两台 new-api 机的独立模式 Agent 已重建二进制部署，慢返回告警在生产生效（用户 2026-07-14 确认；此前账本停在"已合入待部署"，本章补记）。

### 7. 已知限制

检测时点在请求完成时——"在途未返回"的挂起请求仍不可见（该盲区的推演见 latency-diagnosis.md 与 v1.1 设计信号 A/B 讨论：发现靠旁观真实流量的完成流,剪网关超时属独立的业务决策）。

### 9. 下一版本方向

v2.0-B2 双模式接入后,同一 Agent 升级为告警+上报并行;慢 499/静默信号等预警增强按 v1.1 设计批次推进。

## v1.0.7 禁用渠道不再告警（2026-07-13）

### 1. 需求

渠道因错误被人工/自动禁用后，其残留在窗口内的错误仍会触发提醒（最长一个衰减周期），造成"已处置还在报"的噪音。

### 2. 实现

- 渠道名字刷新（10 分钟周期）扩展为名字+状态一起取（`FetchStates`，status != 1 视为禁用）。
- 告警器新增禁用集合：禁用渠道的事件不入渠道窗口；进行中的 episode 静默关闭（事件日志记 kind=disposed，不发钉钉）；重新启用后从全新窗口开始。客户维度不受影响。
- 无 channels 表权限时优雅降级（照旧监控，仅日志提示一次）。

### 3. 边界

状态刷新周期 10 分钟，禁用动作到告警静默最长有 10 分钟滞后；`go test ./...` 24 包通过，新增两组回归（禁用不告警且客户维度不受影响、禁用关闭进行中 episode 且重启用为全新 episode）。替换二进制重启生效。
