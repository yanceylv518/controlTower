# Codex 任务：M1 阶段点验证（执行 + 出报告，不改代码）

这是**验证任务，不是开发任务**。目标：在本机真实 MySQL 上把 M1 全链路（认证→实例→Agent 模拟上报→告警→通知→命令→审计）跑通一遍，并出验证报告。

## 红线

1. **禁止修改任何 `server/**`、`agent/**`、`web/**`、`deploy/e2e-server.sh` 代码来"让验证通过"**——任何一步失败都是有价值的发现，如实记录并停止，交回 review 处理。
2. 允许创建的文件只有验证报告 `docs/m1-stage-verification.md`；允许的环境操作只有：建/清测试库、设环境变量、启停本次拉起的 Server 进程。
3. 报告中不得出现真实密码/token 明文（用占位符）。

## 前置条件

- 本机 MySQL 8 运行中，存在测试库（已有 `control_tower_test` 可直接复用——五个迁移全部幂等，旧表不影响；若想干净验证可先 `DROP DATABASE control_tower_test; CREATE DATABASE control_tower_test CHARACTER SET utf8mb4;`）。
- Go 1.24 可用；**bash 环境**：优先 Git Bash（随 Git for Windows 安装，含 curl/gzip/sed），WSL 可用亦可。e2e 脚本必须在 bash 中执行。
- 端口 8080 空闲。

## 步骤

### 1. 构建（仓库根目录）

```bash
go build -o dist/control-tower-server ./server/cmd/control-tower-server
```

（Windows 下产出 `dist/control-tower-server.exe`，后续命令相应替换。）

### 2. 启动 Server（终端 A，保持前台观察日志）

bash（Git Bash）：

```bash
export CT_PUBLIC_BASE_URL=http://127.0.0.1:8080
export CT_DATABASE_DSN='<mysql用户>:<mysql密码>@tcp(127.0.0.1:3306)/control_tower_test?parseTime=true'
export CT_AGENT_TOKEN=legacy-agent-token-test
export CT_DASHBOARD_TOKEN=dash-token-test
export CT_AGENT_TOKEN_PEPPER=verify-pepper-2026
export CT_ADMIN_USERNAME=admin
export CT_ADMIN_INITIAL_PASSWORD='Admin12345'
./dist/control-tower-server
```

**判据 A**（记入报告）：日志出现 `initial admin created`（首次；复用旧库且 users 已有数据时无此行，属正常，注明即可）与 `listening on :8080`；无 `apply migration` 报错。

### 3. 跑全链路脚本（终端 B，bash）

```bash
export CT_ADMIN_USER=admin
export CT_ADMIN_PASS='Admin12345'
bash deploy/e2e-server.sh
```

**判据 B**：依次输出各 `[e2e] 步骤名` 且最终打印 **`[e2e] passed`**，退出码 0（`echo $?` 确认）。

### 4. 数据库抽查（终端 B，任选 mysql 客户端）

```sql
USE control_tower_test;
SELECT COUNT(*) FROM users;                                   -- >=1
SELECT event_type, actor FROM alert_events ORDER BY id;       -- 应含 firing(system) 与 acknowledged(admin)
SELECT status, created_by FROM channel_commands;              -- succeeded / admin
SELECT operation_type, target_id, actor_id FROM operation_audits; -- channel.update / 77 / admin
```

**判据 C**：四条查询结果与注释预期一致（记录实际输出）。

### 5. 出报告并提交

新建 `docs/m1-stage-verification.md`，内容：

- 验证日期、环境（OS、MySQL 版本、bash 来源、代码 commit）；
- 判据 A/B/C 的实际结果（e2e 完整步骤输出原样粘贴；敏感值脱敏）；
- 结论：`PASS` 或 `FAIL`；
- 若 FAIL：失败步骤名、终端 B 从失败处开始的输出、终端 A（Server）对应时间的日志片段——**然后停止，不要尝试修复**。

一个 commit：`docs: M1 stage verification report (PASS|FAIL)`，推送。

### 6. 清理（可选）

停止 Server 进程；测试库可保留（后续 M2 联调还会用）或 DROP。

## 常见问题排查（仅限环境问题，可自行处理）

- `dial tcp ... connection refused` 起服务失败 → MySQL 未启动或 DSN 主机/端口错。
- `Access denied` → DSN 账号密码错，或账号无该库权限。
- e2e 第一步 `health` 失败 → Server 没起来或端口不对（`CT_BASE` 默认 8080，可 `export CT_BASE=` 调整）。
- `gzip: command not found` → 未在 Git Bash/WSL 中运行。
- 端口占用 → 换 `CT_PUBLIC_BASE_URL`/监听端口需查 `CT_LISTEN_ADDR`（见 server.env.example），并同步 `CT_BASE`。
