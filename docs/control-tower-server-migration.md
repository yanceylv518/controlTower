# Control Tower 服务器迁移操作手册

本文用于把正在运行的 Control Tower（Server、Web、Compose MySQL）迁移到一台新服务器，并保留：

- 管理员账号和已经修改过的登录密码；
- 实例、实例 Token、Agent 在线关系；
- 监控指标、告警、样本、审计和渠道快照；
- 现有 `.env` 中的密钥、保留策略和默认设置。

迁移时只更换服务器，不同时升级 Control Tower 版本。确认新服务器稳定后，再按常规升级流程升级版本。

## 1. 迁移原则和推荐顺序

推荐顺序：

1. 确认旧服务器版本、容量和健康状态；
2. 准备新服务器、Docker 和安全组；
3. 在旧服务器做一次预备备份并验证备份可读；
4. 在新服务器部署同一版本代码和镜像，只启动 MySQL；
5. 进入短暂停写窗口：停止各 Agent，再停止旧 Server；
6. 生成最终数据库备份并传到新服务器；
7. 在新 MySQL 恢复数据库，启动新 Server；
8. 验证 Web 和数据，再切换公网 IP 或逐台修改 Agent 地址；
9. 观察稳定后保留旧服务器至少 7 天，再下线。

> 禁止让新旧 Server 同时接收同一批 Agent 上报。否则会形成双写和两套不同的游标状态。

## 2. 变量约定

执行前先记录以下实际值。命令中的占位符必须替换，不要原样复制：

```text
OLD_CT_IP     旧 Control Tower 公网 IP
NEW_CT_IP     新 Control Tower 公网 IP
RELEASE       当前生产版本，例如 v2.0.0-rc13
ADMIN_IP      管理员当前公网 IP
AGENT_1_IP    第一台 new-api 服务器出口公网 IP
AGENT_2_IP    第二台 new-api 服务器出口公网 IP
```

如果云平台支持把旧公网 IP/EIP 解绑后绑定到新服务器，优先复用原 IP。这样 Agent 的 `CT_SERVER_URL`、浏览器地址和安全组来源规则通常不需要修改。

如果不能复用旧 IP，必须把所有 Agent 的 `CT_SERVER_URL` 改成新 IP，并把 `.env` 中的 `CT_PUBLIC_BASE_URL` 改成新地址。

## 3. 旧服务器迁移前检查

登录旧服务器：

```bash
cd /opt/controlTower/deploy/compose

docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
docker compose logs --since 10m server | grep -Ei 'error|failed|panic' | head -50
docker inspect -f '{{.Config.Image}}' "$(docker compose ps -q server)"
cd /opt/controlTower && git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD
df -h
docker system df
```

检查结果应满足：

- MySQL 为 `healthy`，Server 为 `running`；
- `/healthz` 返回成功；
- 日志没有持续报错；
- 记下正在运行的镜像标签和 Git Tag，迁移目标必须使用同一版本；
- 新服务器磁盘可用空间应大于旧服务器数据库体积的 2 倍，并额外预留镜像空间。

查看数据库卷和大致体积：

```bash
cd /opt/controlTower/deploy/compose
docker volume ls | grep ct-mysql-data
docker compose exec -T mysql sh -c \
  'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N -e "SELECT table_schema, ROUND(SUM(data_length+index_length)/1024/1024,1) AS mb FROM information_schema.tables WHERE table_schema=DATABASE() GROUP BY table_schema" "$MYSQL_DATABASE"'
```

## 4. 准备新服务器

### 4.1 安全组

新服务器入站规则：

| 端口 | 来源 | 用途 |
| --- | --- | --- |
| TCP 22 | `ADMIN_IP/32` | SSH 管理 |
| TCP 8080 | 管理员或允许访问 Web 的公网来源 | Web/API |
| TCP 8080 | 每台 new-api 服务器的出口公网 IP `/32` | Agent 上报 |

不要把 MySQL 3306 开放到公网。MySQL 只在 Compose 内部网络使用。

### 4.2 安装 Docker

```bash
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-v2
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"
exit
```

重新 SSH 登录后验证：

```bash
docker version
docker compose version
```

### 4.3 获取与旧服务器相同的版本

```bash
sudo mkdir -p /opt/controlTower
sudo chown "$USER":"$USER" /opt/controlTower
git clone https://github.com/yanceylv518/controlTower.git /opt/controlTower
cd /opt/controlTower
git fetch --tags
git checkout RELEASE
git describe --tags --exact-match
```

把 `RELEASE` 替换成第 3 节记录的生产 Tag。不要在迁移过程中直接使用 `main`。

## 5. 备份并复制生产配置

`.env` 包含数据库密码、Token Pepper 和兼容 Token。实例 Token 能否继续使用，不只取决于数据库，还取决于 `CT_AGENT_TOKEN_PEPPER`，因此必须原样迁移旧 `.env`。

在旧服务器执行：

```bash
cd /opt/controlTower/deploy/compose
sudo install -d -m 700 /var/backups/control-tower-migration
sudo cp -a .env /var/backups/control-tower-migration/control-tower.env
sudo chmod 600 /var/backups/control-tower-migration/control-tower.env
sha256sum .env | sudo tee /var/backups/control-tower-migration/control-tower.env.sha256
```

从管理员电脑安全下载，再上传到新服务器；不要把 `.env` 发到聊天、工单或公开网盘：

```bash
# 在管理员电脑执行
scp OLD_USER@OLD_CT_IP:/var/backups/control-tower-migration/control-tower.env ./control-tower.env
scp ./control-tower.env NEW_USER@NEW_CT_IP:/tmp/control-tower.env
```

在新服务器安装配置：

```bash
sudo install -m 600 -o "$USER" -g "$USER" /tmp/control-tower.env /opt/controlTower/deploy/compose/.env
rm -f /tmp/control-tower.env
cd /opt/controlTower/deploy/compose
docker compose config --quiet
grep '^CT_SERVER_IMAGE=' .env
```

确认 `CT_SERVER_IMAGE` 与旧服务器实际镜像版本相同。如果新服务器使用新公网 IP，修改：

```ini
CT_PUBLIC_BASE_URL=http://NEW_CT_IP:8080
```

其他密码、`CT_AGENT_TOKEN_PEPPER`、`CT_AGENT_TOKEN`、保留策略不要重新生成。

## 6. 在新服务器预启动 MySQL

只启动 MySQL，不启动 Server：

```bash
cd /opt/controlTower/deploy/compose
docker compose pull mysql server
docker compose up -d mysql
docker compose ps
docker compose logs --tail=100 mysql
```

等待 MySQL 显示 `healthy`。此时新库是空库，不要登录 Web 创建管理员或实例。

## 7. 预备备份验证

先在旧服务器做一次预备备份，确认导出命令可用。该备份只用于验证，不作为最终切换备份：

```bash
cd /opt/controlTower/deploy/compose
BACKUP="/var/backups/control-tower-migration/control-tower-precheck-$(date +%Y%m%d-%H%M%S).sql.gz"
docker compose exec -T mysql sh -c \
  'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction --quick --routines --triggers --events --hex-blob --no-tablespaces "$MYSQL_DATABASE"' \
  | gzip | sudo tee "$BACKUP" >/dev/null
sudo gzip -t "$BACKUP"
sudo ls -lh "$BACKUP"
```

`gzip -t` 无输出且退出码为 0 才算通过。不要使用宿主机 shell 中手工输入的 root 密码；命令会读取 MySQL 容器创建时实际使用的 `MYSQL_ROOT_PASSWORD`。

## 8. 正式切换窗口

建议选择低流量时段，预留 15～30 分钟。通知相关人员开始维护，并暂停管理后台的渠道操作。

### 8.1 停止所有 Agent

在每台 new-api 服务器执行：

```bash
sudo systemctl stop control-tower-agent
sudo systemctl is-active control-tower-agent
```

预期输出 `inactive`。Agent 停止不会影响 new-api 自身对外提供 API，也不会影响 new-api 数据库。

### 8.2 停止旧 Server，保留旧 MySQL

在旧 Control Tower 服务器执行：

```bash
cd /opt/controlTower/deploy/compose
docker compose stop server
docker compose ps
```

确认旧 Server 已停止、旧 MySQL 仍为 `healthy`。从现在开始不要再启动旧 Server，直到决定回滚。

### 8.3 生成最终数据库备份

```bash
cd /opt/controlTower/deploy/compose
FINAL_BACKUP="/var/backups/control-tower-migration/control-tower-final-$(date +%Y%m%d-%H%M%S).sql.gz"
docker compose exec -T mysql sh -c \
  'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction --quick --routines --triggers --events --hex-blob --no-tablespaces "$MYSQL_DATABASE"' \
  | gzip | sudo tee "$FINAL_BACKUP" >/dev/null
sudo gzip -t "$FINAL_BACKUP"
sudo sha256sum "$FINAL_BACKUP" | sudo tee "${FINAL_BACKUP}.sha256"
sudo ls -lh "$FINAL_BACKUP" "${FINAL_BACKUP}.sha256"
```

记下最终文件名和 SHA256。

### 8.4 传输最终备份

推荐通过管理员电脑中转：

```bash
# 管理员电脑
scp OLD_USER@OLD_CT_IP:/var/backups/control-tower-migration/control-tower-final-YYYYMMDD-HHMMSS.sql.gz ./
scp ./control-tower-final-YYYYMMDD-HHMMSS.sql.gz NEW_USER@NEW_CT_IP:/tmp/
```

在新服务器再次校验：

```bash
sha256sum /tmp/control-tower-final-YYYYMMDD-HHMMSS.sql.gz
gzip -t /tmp/control-tower-final-YYYYMMDD-HHMMSS.sql.gz
```

SHA256 必须与旧服务器一致。

## 9. 在新服务器恢复数据库

新服务器执行：

```bash
cd /opt/controlTower/deploy/compose
docker compose ps
gzip -dc /tmp/control-tower-final-YYYYMMDD-HHMMSS.sql.gz \
  | docker compose exec -T mysql sh -c \
    'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"'
```

恢复完成后检查核心数据：

```bash
docker compose exec -T mysql sh -c \
  'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE" -e "
SELECT COUNT(*) AS instances FROM instances;
SELECT COUNT(*) AS agents FROM agents;
SELECT COUNT(*) AS log_events FROM log_events;
SELECT COUNT(*) AS metric_1m FROM metric_1m;
SELECT COUNT(*) AS alerts FROM alerts;
SELECT MAX(bucket_time) AS latest_metric FROM metric_1m;
"'
```

在旧服务器运行同一组查询并比较。停写后，两边数量和最新时间应一致。

## 10. 启动并验证新 Server

```bash
cd /opt/controlTower/deploy/compose
docker compose up -d --no-build server
docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
docker compose logs --since 5m server | grep -Ei 'migrat|error|failed|panic' | head -100
docker inspect -f '{{.Config.Image}} {{.Image}}' "$(docker compose ps -q server)"
```

要求：

- MySQL 为 `healthy`，Server 为 `running`；
- `/healthz` 成功；
- 数据库迁移没有报错；
- 镜像版本与旧服务器一致。

浏览器访问 `http://NEW_CT_IP:8080`，使用原管理员账号和已经修改过的密码登录。以下内容应保留：

- 实例列表和默认实例；
- Agent Token 关系（Token 不会回显，但无需轮换）；
- 历史监控、告警、样本和操作审计；
- 系统设置。

此时 Agent 尚未启动，页面显示离线属于正常现象。

## 11. 切换网络并恢复 Agent

### 方案 A：复用原公网 IP/EIP（推荐）

1. 在云平台把旧公网 IP 从旧服务器解绑；
2. 绑定到新服务器；
3. 确认新服务器安全组已放行 22 和 8080；
4. 从每台 new-api 服务器执行：

```bash
curl -fsS http://OLD_CT_IP:8080/healthz
```

成功后直接启动 Agent，无需改配置：

```bash
sudo systemctl start control-tower-agent
sudo systemctl status control-tower-agent --no-pager
```

### 方案 B：使用新公网 IP

在每台 new-api 服务器备份并修改配置：

```bash
sudo cp -a /etc/control-tower/agent.config "/etc/control-tower/agent.config.bak.$(date +%Y%m%d-%H%M%S)"
sudo nano /etc/control-tower/agent.config
```

只修改：

```ini
CT_SERVER_URL=http://NEW_CT_IP:8080
```

不要修改 `CT_INSTANCE_ID` 和 `CT_AGENT_TOKEN`。然后逐台验证并启动：

```bash
curl -fsS http://NEW_CT_IP:8080/healthz
sudo -u ct-agent /usr/local/bin/control-tower-agent \
  -config /etc/control-tower/agent.config \
  -preflight
sudo systemctl start control-tower-agent
sudo journalctl -u control-tower-agent --since -5m --no-pager
```

每完成一台，先在 Web 的“实例管理”确认该 Agent 在线、版本和心跳正常，再处理下一台。

## 12. 迁移验收清单

- [ ] 新服务器 MySQL `healthy`、Server `running`；
- [ ] `/healthz` 返回成功；
- [ ] 使用原管理员新密码可以登录；
- [ ] 实例、设置、历史指标、告警和审计均存在；
- [ ] 新旧数据库核心表数量一致；
- [ ] 每台 Agent 恢复在线，且没有持续 401、403、timeout；
- [ ] 新流量产生后 1～2 分钟，客户、渠道、模型和总览出现新指标；
- [ ] 多实例切换后数据归属正确；
- [ ] 旧 Server 保持停止，没有形成双写；
- [ ] 最终备份、`.env` 和 SHA256 已保存到安全位置。

Agent 日志快速检查：

```bash
sudo journalctl -u control-tower-agent --since -10m --no-pager \
  | grep -Ei '401|403|timeout|refused|failed|error|report'
```

新 Server 日志快速检查：

```bash
cd /opt/controlTower/deploy/compose
docker compose logs --since 10m server \
  | grep -Ei '401|403|timeout|failed|error|panic'
```

## 13. 回滚流程

只要旧服务器、旧数据库卷和旧 `.env` 仍保留，就可以回滚。

1. 停止所有 Agent；
2. 停止新 Server，避免继续写入新数据库；
3. 把公网 IP/EIP 绑回旧服务器，或把 Agent 的 `CT_SERVER_URL` 改回旧 IP；
4. 在旧服务器启动 Server；
5. 验证旧 `/healthz` 后逐台启动 Agent。

新服务器：

```bash
cd /opt/controlTower/deploy/compose
docker compose stop server
```

旧服务器：

```bash
cd /opt/controlTower/deploy/compose
docker compose start server
curl -fsS http://127.0.0.1:8080/healthz
```

> 新 Server 启动后收到的数据不会自动回写旧数据库。若新服务器已经运行较长时间，回滚前必须先决定以哪套数据库为准，禁止再次形成双写。

## 14. 迁移后的收尾

迁移后至少观察 24 小时，旧服务器建议保留 7 天：

- 不删除旧 `/opt/controlTower`、`.env` 和 Docker 数据卷；
- 不执行 `docker compose down -v`；
- 每天检查 Agent 在线、Server 错误日志、磁盘和数据库备份；
- 确认稳定后再释放旧服务器；
- 最终备份和 `.env` 应进入加密备份或密码管理系统；
- 迁移完成后再单独安排版本升级，不把迁移和升级混成一次变更。
