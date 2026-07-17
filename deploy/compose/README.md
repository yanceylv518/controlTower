# Control Tower Docker Compose 部署

生产环境默认使用 GitHub Actions 已构建的 GHCR Server 镜像。服务器只负责拉取镜像和启动容器，不再现场执行 Go、Node、pnpm 或 Docker Build。

## 首次部署

1. 获取仓库并进入部署目录：

   ```bash
   git clone https://github.com/yanceylv518/controlTower.git /opt/controlTower
   cd /opt/controlTower/deploy/compose
   ```

2. 创建配置：

   ```bash
   cp .env.example .env
   nano .env
   ```

   必须替换全部 `REPLACE_WITH_*`。`CT_DATABASE_DSN` 中的账号和密码必须与 `MYSQL_USER`、`MYSQL_PASSWORD` 一致。生产环境应把 `CT_SERVER_IMAGE` 固定到实际发布标签，例如：

   ```dotenv
   CT_SERVER_IMAGE=ghcr.io/yanceylv518/controltower-server:v2.0.0-rc11
   ```

3. 校验、拉取并启动：

   ```bash
   docker compose config --quiet
   docker compose pull
   docker compose up -d --no-build
   ```

   如果 GHCR 返回 `denied` 或 `unauthorized`，使用具有 `read:packages` 权限的 GitHub Token 登录后重试：

   ```bash
   echo 'GITHUB_TOKEN' | docker login ghcr.io -u yanceylv518 --password-stdin
   ```

4. 验证：

   ```bash
   docker compose ps
   curl -fsS http://127.0.0.1:8080/healthz
   docker compose logs --since 5m server
   docker inspect -f '{{.Config.Image}} {{.Image}}' "$(docker compose ps -q server)"
   ```

浏览器打开 `http://服务器地址:8080`，使用 `.env` 中的初始管理员登录并立即修改密码。

## 更新部署

更新前备份 `.env` 和数据库。以下命令不会删除 MySQL 数据卷：

```bash
cd /opt/controlTower/deploy/compose
cp -a .env ".env.bak.$(date +%Y%m%d-%H%M%S)"
sudo mkdir -p /var/backups/control-tower
docker compose exec -T mysql sh -c \
  'exec mysqldump -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"' \
  | gzip | sudo tee "/var/backups/control-tower/control-tower-$(date +%Y%m%d-%H%M%S).sql.gz" >/dev/null
```

获取目标标签代码，但保留生产 `.env`：

```bash
cd /opt/controlTower
git fetch --tags
git checkout v2.0.0-rc11
cd deploy/compose
```

将 `.env` 中的镜像改为同一版本：

```dotenv
CT_SERVER_IMAGE=ghcr.io/yanceylv518/controltower-server:v2.0.0-rc11
```

拉取并快速替换 Server：

```bash
docker compose config --quiet
docker compose pull server
docker compose up -d --no-build server
docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
docker compose logs --since 5m server
```

不要在生产服务器执行 `docker compose up -d --build`。该命令会在服务器现场编译，并可能长时间占满 CPU。

## 回滚

把 `.env` 的 `CT_SERVER_IMAGE` 改回上一个可用标签，然后执行：

```bash
docker compose pull server
docker compose up -d --no-build server
curl -fsS http://127.0.0.1:8080/healthz
```

数据库迁移应保持向后兼容。若目标版本明确声明迁移不可回滚，应先恢复对应版本的数据库备份。

## 数据持久化与重建

MySQL 使用 `ct-mysql-data` 命名卷。普通的 `pull`、`up -d`、容器替换和 Server 回滚不会删除数据。不要执行 `docker compose down -v`，除非明确要销毁数据库。

数据卷丢失后，重新启动 Compose 会自动执行迁移；随后需要重新创建管理员、实例和 Agent Token。监控历史无法完全依靠 Agent 自动恢复，因此生产环境应定期备份。

## 故障排查

- GHCR 拉取失败：确认镜像标签存在；私有包需先执行 `docker login ghcr.io`。
- Server 启动失败：执行 `docker compose logs --tail=200 server`。
- MySQL 长时间 unhealthy：执行 `docker compose logs --tail=200 mysql`，检查磁盘、密码和数据卷权限。
- 迁移失败：检查应用账号是否对目标库具有 DDL 权限，并确认 MySQL 使用 `utf8mb4_unicode_ci`。
- 首页 503：确认拉取的是完整正式镜像，并检查容器内 `/app/web/dist/desktop`。
