# Codex 任务：v2.0-B1——部署编排 + 发布流水线

v2.0 发布准备第一批：Server 容器化、Compose 一键部署（自建 MySQL）、CI 打 tag 出全套发布产物。完成后 v2.0-B2 只剩"上服务器执行"。

**数据库决策（用户已定）**：Control Tower 库为可再生监控数据——Compose 内置 MySQL + 数据卷持久化即可；备份仅提供可选的一行 cron dump；部署文档必须写明"本库可全量重建"及重建步骤。不做 HA/异地备份。

**文末自查清单粘贴进 commit message。**

## 背景速读

- Server 运行时按相对路径读两类资产：迁移 `CT_MIGRATION_PATH`（默认 `server/migrations/001_init.sql`，启动时应用其所在目录全部 `*.sql`）与前端 `web/dist/desktop`（未构建返回 503）。镜像内 WORKDIR 布局必须满足这两个默认值。
- Agent 版本号：`agent/cmd/control-tower-agent/main.go` 的 `const agentVersion = "0.1.0"`。
- 安装脚本 `deploy/install-agent.sh` 按 `control-tower-agent-linux-<arch>` 或 `control-tower-agent` 文件名自动定位二进制。
- CI 现有 `.github/workflows/ci.yml`（push/PR 质量门，双 job）。

## 硬性纪律

业务代码零改动（唯一例外：`agentVersion` 从 const 改 var 以支持 `-ldflags` 注入，行为不变）；零新 Go/前端依赖；CI 只用官方或 docker 官方 action；所有脚本 LF + `bash -n` 自检；secrets 不入库（GHCR 推送用内置 `GITHUB_TOKEN`）。

## 工作项

### 任务 1：Server 镜像（仓库根 `Dockerfile` + `.dockerignore`）

多阶段：

1. `node:20-alpine`：`corepack enable` → `pnpm install --frozen-lockfile` → `pnpm build`（产出 web/dist/desktop）；
2. `golang:1.24-alpine`：`CGO_ENABLED=0 go build ./server/cmd/control-tower-server`（`-trimpath -ldflags "-s -w"`）；
3. 运行层 `alpine:3.20`：非 root 用户（uid 1001），`WORKDIR /app`，布局 `/app/control-tower-server`、`/app/server/migrations/*.sql`、`/app/web/dist/desktop/**`（满足默认相对路径，不需要任何 env 覆盖）；`EXPOSE 8080`；`ENTRYPOINT ["/app/control-tower-server"]`。

`.dockerignore`：`.git`、`webapp/**/node_modules`、`web/dist`、`dist`、`docs`。

### 任务 2：Compose（`deploy/compose/`）

- `docker-compose.yml`：
  - `mysql`：`mysql:8`，`MYSQL_DATABASE=control_tower`，密码走 env，数据卷 `ct-mysql-data`，healthcheck（`mysqladmin ping`），`command` 显式 `--character-set-server=utf8mb4 --collation-server=utf8mb4_unicode_ci`（与迁移钉扎一致，双保险）；
  - `server`：`build: ../..`（也支持 `image:` 注释切换到 GHCR 镜像），`depends_on: mysql: condition: service_healthy`，env 全套 CT_*（引用 `.env`），`ports: 8080:8080`，`restart: unless-stopped`；
  - 两服务 logging `max-size: 50m, max-file: 3`。
- `.env.example`：全部变量带中文注释（数据库密码、CT_DATABASE_DSN 模板、AGENT/DASHBOARD token、PEPPER、管理员引导、可选调优项）。
- `README.md`（部署手册）：从零部署四步（clone → 填 .env → `docker compose up -d --build` → 浏览器开 8080 登录）；升级步骤（pull + up --build）；**"本库可全量重建"声明与重建步骤**（丢库后：compose 重启自动跑迁移 → env 引导管理员 → Web 重建实例并给 Agent 换发 token，全程 ≤10 分钟）；可选备份一行 cron（`mysqldump ... | gzip > ...`，保留 7 天）；故障排查（503 未构建、迁移失败、healthcheck）。

### 任务 3：发布流水线（`.github/workflows/release.yml`，tag `v*` 触发）

共享打包脚本 `deploy/package.sh`（本地与 CI 同一份，Makefile 加 `package` 目标调用）：

1. Agent：双架构构建（`-X main.agentVersion=$VERSION` 注入）→ 每架构 tar.gz：`control-tower-agent`（统一文件名，安装脚本兼容）、`install-agent.sh`、`control-tower-agent.service`、两个 config example、`agent/README.md`；打包前对脚本强制 `LF` 归一。
2. Server：linux/amd64 二进制 + `server/migrations` + 构建好的 `web/dist/desktop` 打成 `control-tower-server-<ver>-linux-amd64.tar.gz`（裸机部署备选）。
3. 全部产物生成 `SHA256SUMS`。

workflow 步骤：checkout → setup go/node/pnpm → `deploy/package.sh $TAG` → 构建并推送镜像 `ghcr.io/yanceylv518/controltower-server:<tag>` 与 `:latest`（`permissions: packages: write`，`GITHUB_TOKEN` 登录 GHCR）→ `gh release create $TAG dist/release/*`（`permissions: contents: write`）附全部 tar.gz 与 SHA256SUMS。

`agentVersion` const → var；进程启动日志打印版本（main 里加一行 `log.Printf("control tower agent %s", agentVersion)`——允许的最小改动，server 侧同理可选）。

### 任务 4：文档

`docs/development-progress.md` 更新（M4 最小部署完成项）；根 README 增加 Deployment 一节指向 `deploy/compose/README.md`；`docs/codex-batches-plan.md` 不用改（我维护）。

## 验证要求

1. `make test`、CI push 质量门绿；`bash -n` 全部新脚本。
2. 本机有 Docker 则：`docker compose -f deploy/compose/docker-compose.yml up -d --build` 起来后 `/healthz` 200、`/` 登录页可达，记录结果；无 Docker 如实注明留待 rc 验证。
3. **发布演练**：推测试 tag `v2.0.0-rc1` → Actions release workflow 绿 → GitHub Release 出现 3 个 tar.gz + SHA256SUMS，GHCR 出现镜像。把 run 链接记入 commit message（tag 触发在 push 之后，可在交付说明中补记）。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 任务 1~4 逐节核对；镜像布局满足两个默认相对路径（migrations 与 web/dist/desktop）
- [ ] 业务代码除 agentVersion const→var 与版本日志行外零改动
- [ ] package.sh 本地与 CI 共用；脚本 LF；产物含 SHA256SUMS
- [ ] compose 的 MySQL 显式 utf8mb4_unicode_ci；README 含"可全量重建"声明与重建步骤
- [ ] 一个 commit：`feat(deploy): docker compose stack and release pipeline (v2.0-B1)`（rc tag 另行推送）

## 明确不做

Caddy/TLS（公网需要时按既定反代方案另加）；HA/主从/异地备份；K8s；Agent 自动升级。
