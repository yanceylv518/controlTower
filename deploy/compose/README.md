# Control Tower Docker Compose 部署

## 从零部署

1. 克隆仓库并进入部署目录：

   ```bash
   git clone https://github.com/yanceylv518/controlTower.git
   cd controlTower/deploy/compose
   ```

2. 创建配置并替换所有 `REPLACE_WITH_*` 值。`CT_DATABASE_DSN` 中的账号和密码必须与 MySQL 配置一致：

   ```bash
   cp .env.example .env
   vi .env
   ```

3. 构建并启动 MySQL 与 Server：

   ```bash
   docker compose up -d --build
   ```

4. 浏览器打开 `http://服务器地址:8080`，使用 `.env` 中的初始管理员登录，并立即修改初始密码。

检查状态与日志：

```bash
docker compose ps
curl -f http://127.0.0.1:8080/healthz
docker compose logs --tail=100 server
```

## 升级

`.env` 与 MySQL 数据卷不会被下列操作覆盖：

```bash
git pull --ff-only
docker compose up -d --build
```

若改用 GHCR 镜像，按 `docker-compose.yml` 注释切换 `build`/`image`，然后执行 `docker compose pull && docker compose up -d`。

## 数据库可全量重建

Control Tower 数据库只保存可由 Agent 重新采集或重新建立的监控数据，不承载业务主数据。本库可全量重建，不提供 HA、主从或异地备份。

数据卷丢失后的重建步骤：

1. 修复或重新创建 MySQL 数据卷，执行 `docker compose up -d`；Server 会自动执行全部迁移。
2. 使用 `.env` 中的管理员引导配置登录；若此前已移除引导值，临时填入新的 `CT_ADMIN_USERNAME` 与 `CT_ADMIN_INITIAL_PASSWORD` 后重启。
3. 在 Web 中重建实例，为每个实例换发 Agent Token。
4. 将新 Token 写入对应 Agent 配置并重启 Agent，确认心跳和数据恢复。

正常情况下全流程不超过 10 分钟。重建完成后应移除 `.env` 中的初始管理员密码并再次启动容器。

## 可选的轻量备份

如需保留最近 7 天压缩 dump，可在宿主机 cron 中加入一行（路径按实际调整）：

```cron
0 3 * * * cd /opt/controlTower/deploy/compose && docker compose exec -T mysql sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" control_tower' | gzip > /var/backups/control-tower-$(date +\%F).sql.gz && find /var/backups -name 'control-tower-*.sql.gz' -mtime +7 -delete
```

## 故障排查

- 首页返回 503：镜像中的前端未构建。确认使用仓库根 `Dockerfile` 完整重建，并检查 `/app/web/dist/desktop`。
- Server 因迁移失败退出：执行 `docker compose logs server` 查看具体 SQL；确认 MySQL 使用 `utf8mb4_unicode_ci`，且应用账号拥有目标库 DDL 权限。
- MySQL 长时间 unhealthy：执行 `docker compose logs mysql`，重点检查磁盘空间、密码初始化和数据卷权限；修改密码时必须同步更新 `CT_DATABASE_DSN`。
