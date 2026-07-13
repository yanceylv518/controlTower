# M1 阶段点重新验证报告

## 验证信息

- 日期：2026-07-13
- 结论：**PASS**
- 代码提交：`f8bf4cada5ecfa5726112f506c4d8621e949605d`（验收时的 `origin/main`）
- 操作系统：Windows（amd64）
- Go：`go1.26.4 windows/amd64`
- MySQL：`9.7.1 Community Server`，本机 Windows 服务 `MySQL97`
- Bash：Git for Windows Bash
- 数据库：重新创建的空库 `control_tower_test`
- 服务地址：`http://127.0.0.1:18082`（8080 已被本机 Java 进程占用，按验收文档同步调整相关地址）
- 敏感配置：数据库密码、Agent/Dashboard token 均未在本报告记录明文

## 执行结果

### 步骤 1：构建

**PASS**。执行：

```text
go build -o dist/control-tower-server.exe ./server/cmd/control-tower-server
```

产物：`dist/control-tower-server.exe`（11,812,352 bytes）。

### 判据 A：Server 启动与迁移

**PASS**。

测试库已先执行 DROP/CREATE。Server 随后完成全部迁移，并成功创建初始管理员：

```text
2026/07/13 18:40:06 initial admin created; change the password after first login
```

8080 已被本机 Java 进程占用，因此按验收文档改用 18082。首次启动时发现上轮验收遗留的本项目 Server 仍占用 18082；确认进程路径后停止该遗留进程，并重新启动本次构建。Server 随后在 18082 成功保持运行，无 `apply migration` 报错。因初始管理员已在首次迁移时创建，重新启动时未重复输出 `initial admin created`，符合文档说明。

### 判据 B：全链路 E2E

**PASS**。Git Bash 中执行 `deploy/e2e-server.sh`，原始输出如下：

```text
[e2e] health
[e2e] login
[e2e] me
[e2e] create-instance
[e2e] heartbeat
[e2e] command-confirm-required
[e2e] command-create
[e2e] command-deliver
[e2e] command-complete
[e2e] notification-channel
[e2e] error-report
[e2e] alert-timeline
[e2e] notification-resend
[e2e] notification delivery not ready; skip resend (runner interval/configuration)
[e2e] mismatch
[e2e] rotate
[e2e] rotation-grace
[e2e] disable
[e2e] list-no-token
[e2e] passed
E2E_EXIT_CODE=0
```

全部步骤通过，最终输出 `[e2e] passed`，脚本退出码 0。`notification-resend` 因 runner 时序/配置尚未形成可重发记录而按脚本设计跳过，不影响通过结论。

### 判据 C：数据库抽查

**PASS**。实际输出：

```text
user_count
1

event_type    actor
firing        system
acknowledged  admin

status     created_by
succeeded  admin

operation_type  target_id  actor_id
channel.update  77         admin
```

四项查询均与验收文档预期一致。

## 最终结论

**PASS**。

提交 `f8bf4ca` 可在全新 `control_tower_test` 上完成迁移并启动；完整 E2E 全部通过，四项数据库抽查结果与预期一致。M1 通过阶段点验收。本次仅更新验证报告，未修改任何业务代码。
