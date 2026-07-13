# M1 阶段点重新验证报告

## 验证信息

- 日期：2026-07-13
- 结论：**FAIL**
- 代码提交：`4f4b02b003cdb2195d4295eb24e081f38c8475df`（验收时的 `origin/main`）
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

产物：`dist/control-tower-server.exe`（11,811,328 bytes）。

### 判据 A：Server 启动与迁移

**PASS**。

测试库已先执行 DROP/CREATE。Server 随后完成全部迁移，并成功创建初始管理员：

```text
2026/07/13 18:30:33 initial admin created; change the password after first login
```

首次尝试监听 8080 时发现端口已被本机 Java 进程占用，因此按验收文档改用 18082。Server 在 18082 成功保持运行，无 `apply migration` 报错。因初始管理员已在首次迁移时创建，改用 18082 再启动时未重复输出 `initial admin created`，符合文档说明。

### 判据 B：全链路 E2E

**FAIL**。Git Bash 中执行 `deploy/e2e-server.sh`，原始输出如下：

```text
[e2e] health
[e2e] login
[e2e] me
[e2e] create-instance
[e2e] heartbeat
curl: (22) The requested URL returned error: 401
E2E_EXIT_CODE=22
```

失败步骤：`heartbeat`。健康检查、登录、当前用户查询及实例创建均已通过；Agent 心跳请求收到 HTTP 401，脚本退出码 22，未输出 `[e2e] passed`。

失败时 Server 进程仍在 18082 正常运行，终端 A 未产生对应错误日志；HTTP 401 由接口正常返回。按任务红线，在记录失败后立即停止 Server，未尝试修改代码或配置规避失败。

### 判据 C：数据库抽查

**未执行**。判据 B 失败后按验收任务红线立即停止，没有继续执行 users、alert_events、channel_commands、operation_audits 四项查询。

## 最终结论

**FAIL**。

提交 `4f4b02b` 已修复空库迁移问题，Server 可以在全新 `control_tower_test` 上完成迁移并启动；但全链路 E2E 在 Agent `heartbeat` 步骤收到 HTTP 401，M1 仍未通过阶段点验收。本次仅更新验证报告，未修改任何业务代码。
