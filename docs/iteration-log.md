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
