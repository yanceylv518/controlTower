# Control Tower 从数据库初始化到 Web 完整呈现

本文用于第一次生产部署，也适用于重建空库后的恢复。目标是让下面这条链路完整跑通：

```text
new-api MySQL（只读）
  → Control Tower Agent
  → Control Tower Server API
  → Control Tower MySQL（持久化、聚合）
  → Control Tower Web
```

推荐拓扑：

- Control Tower 服务器：Ubuntu 22.04/24.04，2 核 4G、系统盘不小于 40G；运行 Docker Compose、Server、Web、MySQL 8。
- 每台 new-api 服务器：运行一个 Agent；Agent 只读本机 new-api MySQL，并向 Control Tower Server 上报。
- 企业微信告警：由 Agent 直接发送，是独立于 Server/Web 的冗余链路。

文中的占位符必须替换：

| 占位符 | 含义 |
| --- | --- |
| `CT_IP` | Control Tower 服务器公网或内网 IP |
| `NEW_API_IP` | new-api 服务器出口 IP |
| `INSTANCE_ID` | Web 创建的实例 ID，例如 `inst-prod-01` |
| `INSTANCE_TOKEN` | 创建实例时只显示一次的 Agent Token |
| `RELEASE_VERSION` | 要部署的版本，例如 `v2.0.0-rc5` |

> 发布版本必须真实存在。不要移动或覆盖旧 Tag。若最新修复晚于 rc4，应先发布新版本，不要继续部署旧 rc4 Server 包。

---

## 1. 上线前确认

### 1.1 安全组

Control Tower 服务器入站只开放：

| 端口 | 来源 | 用途 |
| --- | --- | --- |
| TCP 22 | 管理员公网 IP/32 | SSH |
| TCP 8080 | 管理员公网 IP/32 | Web |
| TCP 8080 | 每台 new-api 出口 IP/32 | Agent 上报 |

MySQL 3306 不对公网开放。Control Tower MySQL 只在 Compose 内部网络使用。

### 1.2 获取代码并固定版本

```bash
sudo mkdir -p /opt/controlTower
sudo chown "$USER":"$USER" /opt/controlTower
git clone https://github.com/yanceylv518/controlTower.git /opt/controlTower
cd /opt/controlTower
git fetch --tags
git checkout RELEASE_VERSION
git status --short
```

预期：最后一条无输出。将 `RELEASE_VERSION` 替换为真实 Tag。

---

## 2. 安装 Docker

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER"
exit
```

重新 SSH 登录后：

```bash
docker version
docker compose version
```

预期：Client、Server 和 Compose 版本均正常显示。

---

## 3. 初始化 Control Tower MySQL

数据库不需要单独购买。项目 Compose 会创建 MySQL 8、数据库、应用账号和持久化数据卷。

### 3.1 创建配置

```bash
cd /opt/controlTower/deploy/compose
cp .env.example .env
chmod 600 .env
```

生成凭证，每条结果分别保存到密码管理器：

```bash
openssl rand -hex 24   # MYSQL_PASSWORD
openssl rand -hex 24   # MYSQL_ROOT_PASSWORD
openssl rand -hex 32   # CT_AGENT_TOKEN，全局兼容值
openssl rand -hex 32   # CT_DASHBOARD_TOKEN
openssl rand -hex 32   # CT_AGENT_TOKEN_PEPPER
openssl rand -base64 24 # 管理员初始密码
```

用 nano 编辑：

```bash
nano .env
```

至少确认这些值：

```ini
MYSQL_DATABASE=control_tower
MYSQL_USER=controltower
MYSQL_PASSWORD=<应用数据库强密码>
MYSQL_ROOT_PASSWORD=<root 强密码>

CT_SERVER_LISTEN_ADDR=0.0.0.0:8080
CT_PUBLIC_BASE_URL=http://CT_IP:8080
CT_DATABASE_DRIVER=mysql
CT_DATABASE_DSN=controltower:<与 MYSQL_PASSWORD 完全相同>@tcp(mysql:3306)/control_tower?parseTime=true&loc=UTC
CT_MIGRATION_PATH=server/migrations/001_init.sql

CT_AGENT_TOKEN=<强随机值>
CT_DASHBOARD_TOKEN=<强随机值>
CT_AGENT_TOKEN_PEPPER=<强随机值，部署后不可随意更换>

CT_ADMIN_USERNAME=admin
CT_ADMIN_INITIAL_PASSWORD=<管理员初始强密码>
```

nano 保存退出：按 `Ctrl+O`，回车确认文件名，再按 `Ctrl+X`。

注意：

- DSN 密码必须与 `MYSQL_PASSWORD` 完全一致。
- 密码若包含 `@`、`:`、`/` 等 DSN 特殊字符，需要 URL 编码；使用十六进制随机值可避免该问题。
- `CT_AGENT_TOKEN_PEPPER` 一旦更换，已签发的实例 Token 会全部失效。
- `.env` 是生产配置，升级时不得用 `.env.example` 覆盖。

### 3.2 启动 MySQL、Server 和 Web

```bash
docker compose up -d --build
docker compose ps
```

预期：

- `mysql` 为 `healthy`；
- `server` 为 `running`；
- 8080 已映射到宿主机。

查看日志：

```bash
docker compose logs --tail=100 mysql
docker compose logs --tail=100 server
```

首次启动时 Server 自动执行 `001` 到 `007` 迁移，并创建初始管理员。不要手工逐个导入 SQL。

### 3.3 验证数据库初始化

```bash
docker compose exec mysql sh -c \
  'mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE" -e "SHOW TABLES;"'
```

预期：显示用户、实例、Agent、指标、告警、通知、命令、调权、Nginx timing 等相关表，不出现 `Access denied`。

确认数据卷：

```bash
docker volume ls | grep ct-mysql-data
docker compose down
docker compose up -d
docker compose ps
```

预期：重启后 MySQL 仍为 healthy，管理员与数据不丢失。不要执行 `docker compose down -v`，它会删除数据库卷。

### 3.4 健康检查与首登

```bash
curl -i http://127.0.0.1:8080/healthz
```

预期：HTTP 200。

浏览器打开：

```text
http://CT_IP:8080
```

使用 `.env` 中的管理员账号和初始密码登录，然后进入“设置”立即修改密码。空库首次登录显示“尚未创建实例”属于正常状态。

管理员已创建后，建议从 `.env` 删除 `CT_ADMIN_INITIAL_PASSWORD` 的明文值，再执行：

```bash
docker compose up -d
```

已有管理员不会被删除。

---

## 4. 在 Web 创建实例和 Token

每台 new-api 对应一个实例：

1. 打开“实例管理”。
2. 点击“创建实例”。
3. 填写稳定且唯一的实例 ID，例如 `inst-prod-01`。
4. 填写可读名称，例如“生产 new-api 上海”。
5. 创建成功后立即复制 Token。
6. 勾选“我已保存”后关闭窗口。

Token 只显示一次。丢失时不要查数据库明文；在实例管理中轮换 Token，并更新 Agent 配置。

此时实例列表应显示：实例已创建，但 Agent 尚未在线。

---

## 5. 准备 new-api 只读数据库账号

在每台 new-api 数据库创建独立只读账号。下面以 new-api MySQL 在本机 127.0.0.1:3306、数据库名 `newapi` 为例：

```sql
CREATE USER 'ct_reader'@'127.0.0.1' IDENTIFIED BY '<只读账号强密码>';
GRANT SELECT ON newapi.logs TO 'ct_reader'@'127.0.0.1';
GRANT SELECT ON newapi.channels TO 'ct_reader'@'127.0.0.1';
FLUSH PRIVILEGES;
```

只授予 `SELECT`，不授予 INSERT、UPDATE、DELETE、DDL 或全库管理权限。

验证：

```bash
mysql -h 127.0.0.1 -u ct_reader -p newapi -e \
  'SELECT MAX(id) AS latest_log_id FROM logs; SELECT COUNT(*) AS channels FROM channels;'
```

若 new-api MySQL 在 Docker/RDS 中，按实际来源地址创建账号并限制白名单，但权限仍保持只读。

---

## 6. 安装或升级 Agent 为双模式

双模式含义：

- `CT_WECOM_WEBHOOK_URL`：Agent 独立发送企业微信告警；
- `CT_SERVER_URL` + 实例 Token：Agent 向 Control Tower 上报数据；
- 两条链路互不替代，Server 暂时不可用时企业微信告警仍可工作。

### 6.1 下载与校验

在 new-api 服务器执行：

```bash
cd /tmp
uname -m
```

`x86_64` 选择 amd64，`aarch64` 选择 arm64。以下以 amd64 为例：

```bash
export RELEASE_VERSION=RELEASE_VERSION
wget "https://github.com/yanceylv518/controlTower/releases/download/${RELEASE_VERSION}/control-tower-agent-${RELEASE_VERSION}-linux-amd64.tar.gz"
tar xzf "control-tower-agent-${RELEASE_VERSION}-linux-amd64.tar.gz"
cd "control-tower-agent-${RELEASE_VERSION}-linux-amd64"
sha256sum -c SHA256SUMS
```

若校验清单位于 Release 外层，先单独下载 Release 的 `SHA256SUMS`，再校验对应文件。

### 6.2 备份现有部署

已有 Agent 必须先备份：

```bash
sudo systemctl stop control-tower-agent
sudo cp -a /usr/local/bin/control-tower-agent \
  /usr/local/bin/control-tower-agent.bak.$(date +%Y%m%d%H%M%S)
sudo cp -a /etc/control-tower/agent.config \
  /etc/control-tower/agent.config.bak.$(date +%Y%m%d%H%M%S)
```

不要把示例配置直接覆盖生产配置。

### 6.3 编辑 Agent 配置

全新安装时先创建运行用户和目录；已有 Agent 可重复执行：

```bash
id -u ct-agent >/dev/null 2>&1 || sudo useradd -r -s /usr/sbin/nologin ct-agent
sudo install -d -m 0755 /etc/control-tower
sudo install -d -o ct-agent -g ct-agent -m 0750 /var/lib/control-tower-agent
sudo touch /etc/control-tower/agent.config
sudo chown ct-agent:ct-agent /etc/control-tower/agent.config
sudo chmod 600 /etc/control-tower/agent.config
```

```bash
sudo nano /etc/control-tower/agent.config
```

核心配置示例：

```ini
CT_AGENT_ID=agent-prod-01
CT_INSTANCE_ID=INSTANCE_ID
CT_SERVER_URL=http://CT_IP:8080
CT_AGENT_TOKEN=INSTANCE_TOKEN

CT_LOG_DSN=ct_reader:<只读密码>@tcp(127.0.0.1:3306)/newapi?parseTime=true&loc=UTC
CT_DATA_DIR=/var/lib/control-tower-agent
CT_LOG_POLL_INTERVAL_SECONDS=30
CT_LOG_BATCH_SIZE=1000
CT_LOG_QUERY_TIMEOUT_SECONDS=2
CT_REPORT_TIMEOUT_SECONDS=3
CT_MAX_LOCAL_BUFFER_EVENTS=5000
CT_LOG_EVENT_MODE=aggregate_with_samples
CT_LOG_SAMPLE_LIMIT=50
CT_SLOW_LOG_THRESHOLD_SECONDS=10

CT_NEW_API_STATUS_URL=http://127.0.0.1:3000/api/status
CT_CHANNEL_SNAPSHOT_ENABLED=true
CT_CHANNEL_SNAPSHOT_LIMIT=1000
CT_CHANNEL_SNAPSHOT_INTERVAL_SECONDS=600

CT_WECOM_WEBHOOK_URL=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=<机器人 key>
CT_ALERT_ERROR_WINDOW=10
CT_ALERT_ERROR_THRESHOLD=3
CT_ALERT_WINDOW_MAX_AGE_MINUTES=60
CT_ALERT_REMIND_MINUTES=60
CT_ALERT_NOCACHE_ENABLED=true
CT_ALERT_NOCACHE_MIN_PROMPT_TOKENS=512
CT_ALERT_NOCACHE_WINDOW=10
```

必须保证：

- `CT_INSTANCE_ID` 与 Token 所属实例完全一致，否则返回 403。
- 不再配置旧的 `CT_DINGTALK_WEBHOOK_URL`。
- 配置权限保持 600，且归 `ct-agent` 可读。
- 渠道命令默认关闭；只有确实需要 Web 下发渠道变更时，才配置 `CT_NEW_API_CONTROL_ENABLED=true` 及 new-api 管理凭证。

可选 Nginx timing：

```ini
CT_NGINX_ACCESS_LOG=/var/log/nginx/newapi-timing.log
CT_NGINX_SLOW_RT_SECONDS=10
```

启用前确认 Nginx 日志格式符合项目文档，并赋予 `ct-agent` 只读权限。该功能只在 Server 上报模式生效。

### 6.4 首次游标选择

游标行为分两种：

- 已运行过 standalone Agent：继续使用原 `CT_DATA_DIR/state.json`，切换双模式后从原游标继续，不回放更早历史。
- 全新 Agent 直接以双模式启动：本地与 Server 游标都为 0，会从 logs 表头开始分批回填历史；若同时启用企业微信，历史错误也可能进入告警判断。

生产环境推荐“从当前最新日志开始”，避免历史回放和历史告警。全新 Agent 在正式双模式启动前执行一次 standalone 游标初始化：

1. 在配置中临时注释 `CT_SERVER_URL` 和 `CT_AGENT_TOKEN`；
2. 临时设置 `CT_AGENT_RUN_ONCE=true`，保留企业微信 Webhook；
3. 使用相同的 `CT_DATA_DIR` 运行一次：

```bash
sudo -u ct-agent ./control-tower-agent \
  -config /etc/control-tower/agent.config
```

预期日志出现：

```text
control tower standalone mode: starting from current log id ...
```

4. 重新编辑配置，恢复 `CT_SERVER_URL`、`CT_AGENT_TOKEN`，并改回 `CT_AGENT_RUN_ONCE=false`。

若明确需要把历史监控数据回填到 Web，可以跳过初始化，但建议回填期间暂时留空 `CT_WECOM_WEBHOOK_URL`，追平后再启用，避免历史告警打扰生产群。

### 6.5 预检、替换二进制并启动

先保持旧二进制不动，用解压目录的新二进制预检：

```bash
chmod +x ./control-tower-agent
sudo -u ct-agent ./control-tower-agent \
  -config /etc/control-tower/agent.config \
  -preflight
```

预期全部为 pass：配置已加载、数据目录可写、Control Tower Server 可达、MySQL 可连接、logs/channels 可查询、logs.id 索引存在。

然后安装：

```bash
sudo install -m 0755 ./control-tower-agent /usr/local/bin/control-tower-agent
sudo systemctl daemon-reload
sudo systemctl start control-tower-agent
sudo systemctl enable control-tower-agent
sudo systemctl status control-tower-agent --no-pager
```

查看日志：

```bash
sudo journalctl -u control-tower-agent -n 100 --no-pager
```

预期：

- 版本正确；
- 无 401、403、connection refused；
- 周期出现 `alert pass`；
- 上报没有连续失败；
- 企业微信失败计数为 0。

采用推荐的游标初始化后，Web 数据从 Agent 正式接入后的新流量开始出现：

1. 接入后主动产生几条正常 new-api 请求；
2. 等待 Agent 下一次 30 秒轮询；
3. 再等待 Server 聚合任务，通常总计 1～2 分钟；
4. 刷新 Web 并确认选中了正确实例。

不要为了“立刻看到历史数据”手工删除或篡改游标文件。

---

## 7. 从 Agent 上报到 Web 完整呈现

按以下顺序验收，前一步失败时不要继续猜测页面问题。

### 7.1 实例与心跳

Web → “实例管理”：

- Agent 显示在线；
- 版本正确；
- 最后心跳时间持续更新；
- 积压值没有持续扩大。

失败时检查：

```bash
sudo journalctl -u control-tower-agent --since -10m --no-pager
curl -i http://CT_IP:8080/healthz
```

### 7.2 总览

产生新流量后等待 1～2 分钟。Web 顶部选择正确实例，然后检查：

- 请求数、成功率、错误率、Token 指标出现；
- 近一小时趋势出现新的分钟桶；
- 当前告警与实际状态一致。

### 7.3 客户、渠道和模型

- “客户监控”：按 user 维度出现请求、错误率和延迟。
- “渠道监控”：出现渠道名称、状态、权重、模型，以及指标趋势。
- “模型监控”：按模型显示请求、成功率、错误率和 Token。

渠道名称和状态来自 `channels` 表。若只授予 logs 权限，核心指标仍可上报，但渠道元数据不完整。

### 7.4 样本与用量

- “样本分析”：错误或慢请求发生后显示样本；不会保存完整请求体、响应体、API Key 或 Cookie。
- “用量统计”：出现客户、渠道和模型维度的 Token/Quota 汇总。

### 7.5 系统状态

“系统状态”应显示：

- Agent 在线状态、游标、源库最新 ID 和积压；
- new-api 服务器 CPU、内存、磁盘、网络；
- `CT_NEW_API_STATUS_URL` 健康检查；
- 若 `CT_DOCKER_ENABLED=true` 且 Agent 有权限，则显示容器状态。

### 7.6 告警中心与企业微信

企业微信先做独立链路测试：

```bash
curl -sS -X POST '<企业微信 Webhook>' \
  -H 'Content-Type: application/json' \
  -d '{"msgtype":"text","text":{"content":"[告警] Control Tower 企业微信链路测试"}}'
```

预期返回 `errcode: 0` 且群内收到消息。

Agent 真实触发告警后：

- 企业微信群收到消息；
- Web “告警中心”出现对应生命周期记录；
- 禁用渠道不参与渠道告警，也不会发送企业微信错误提醒；
- 客户维度告警不因渠道禁用而被关闭。

### 7.7 延时分诊

只有配置了 `CT_NGINX_ACCESS_LOG`，且 timed access log 有新行时，“延时分诊”才会出现数据。至少等待一个分钟桶。

检查 Agent 日志中是否有文件不可读或格式解析警告，并确认：

```bash
sudo -u ct-agent test -r /var/log/nginx/newapi-timing.log && echo readable
```

### 7.8 渠道命令

只有启用了 new-api 控制配置时才验收：

1. Web 渠道详情点击“下发命令”；
2. 选择状态/权重/优先级之一；
3. 勾选明确确认框；
4. Agent 心跳领取命令；
5. 状态应从 pending → delivered → succeeded/failed；
6. “操作审计”出现对应管理员、目标渠道和结果。

首次验收建议选择测试渠道，禁止直接对核心生产渠道做破坏性验证。

---

## 8. 完整验收清单

- [ ] MySQL 容器 healthy，Server running。
- [ ] `/healthz` 返回 200，Web 登录成功。
- [ ] 管理员初始密码已修改，`.env` 权限为 600。
- [ ] 实例已创建，Token 已安全保存。
- [ ] new-api 数据库账号只有 logs/channels SELECT 权限。
- [ ] Agent preflight 全部通过，systemd 为 active (running)。
- [ ] Agent 日志无持续 401/403/网络错误。
- [ ] 实例管理显示 Agent 在线和正确版本。
- [ ] 新流量产生后 1～2 分钟，总览出现指标。
- [ ] 客户、渠道、模型、样本、用量、系统状态数据正确。
- [ ] 企业微信直测成功，Agent 告警循环正常。
- [ ] Nginx timing 已配置时，延时分诊出现分钟桶。
- [ ] 渠道命令启用时，命令和审计闭环通过。
- [ ] 切换多个实例时数据不串。
- [ ] `docker compose down && up -d` 后数据仍存在。

全部通过后进入 3～7 天观察期，再发布正式 `v2.0.0`。

---

## 9. 备份、升级与回滚

### 9.1 每日数据库备份

```bash
sudo mkdir -p /var/backups/control-tower
cd /opt/controlTower/deploy/compose
docker compose exec -T mysql sh -c \
  'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"' \
  | gzip | sudo tee "/var/backups/control-tower/control-tower-$(date +%F).sql.gz" >/dev/null
```

定期删除超过 7 天的备份：

```bash
sudo find /var/backups/control-tower -name 'control-tower-*.sql.gz' -mtime +7 -delete
```

### 9.2 Server 升级

```bash
cd /opt/controlTower
git fetch --tags
git checkout <新版本 Tag>
cd deploy/compose
docker compose up -d --build
docker compose ps
curl -f http://127.0.0.1:8080/healthz
```

该过程不会覆盖 `.env`，也不会删除 MySQL 数据卷。

### 9.3 Agent 回滚

```bash
sudo systemctl stop control-tower-agent
sudo cp /usr/local/bin/control-tower-agent.bak.<时间戳> /usr/local/bin/control-tower-agent
sudo cp /etc/control-tower/agent.config.bak.<时间戳> /etc/control-tower/agent.config
sudo systemctl start control-tower-agent
sudo systemctl status control-tower-agent --no-pager
```

---

## 10. 常见故障

| 现象 | 原因与处理 |
| --- | --- |
| MySQL unhealthy | 检查密码、磁盘、卷权限：`docker compose logs mysql` |
| Server migration failed | 查看 `docker compose logs server`；确认 DSN 与 MySQL 应用密码一致 |
| 首页 404/503 | Server 包未包含当前 Web 构建产物；使用完整 Release 或从仓库根目录 Compose 构建 |
| Agent 401 | Token 错误或已轮换；在 Web 重新轮换并更新配置 |
| Agent 403 instance_mismatch | `CT_INSTANCE_ID` 与 Token 所属实例不一致 |
| Agent timeout/refused | 安全组未放行 new-api 出口 IP，或 Server 未运行 |
| Agent 在线但总览为空 | 首次不回放历史；产生新流量、等待 1～2 分钟并确认实例筛选 |
| 渠道名/状态缺失 | 只读账号没有 `newapi.channels` SELECT 权限 |
| 延时分诊为空 | 未配置 timed log、文件不可读、没有新日志行或格式不匹配 |
| 企业微信失败 | 直测 Webhook，检查 `errcode`、机器人 key 和群机器人安全策略 |
| 数据重启后丢失 | 误删了 volume；不要使用 `docker compose down -v` |

诊断时禁止把 `.env`、数据库密码、实例 Token、企业微信完整 Webhook 贴到截图或公开日志中。
