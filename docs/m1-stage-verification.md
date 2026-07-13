# M1 阶段点验证报告

## 验证信息

- 日期：2026-07-13
- 结论：**FAIL**
- 代码提交：`3ac9e049a5b88a844a14b7ed0799e4f68201bdae`
- 操作系统：Windows（amd64）
- Go：`go1.26.4 windows/amd64`
- MySQL：`9.7.1 Community Server`，本机 Windows 服务 `MySQL97`
- Bash：Git for Windows Bash 已安装；因判据 A 失败，按红线未进入 E2E 步骤
- 数据库：重新创建的空库 `control_tower_test`
- 敏感配置：数据库密码、Agent/Dashboard token 均使用测试值或占位符，本文不记录明文

## 执行结果

### 步骤 1：构建

**PASS**。执行：

```text
go build -o dist/control-tower-server.exe ./server/cmd/control-tower-server
```

产物：`dist/control-tower-server.exe`（11,808,768 bytes）。

### 判据 A：Server 启动与迁移

**FAIL**。测试库清空并重新创建后，Server 在应用迁移阶段退出，未进入监听状态，也未创建初始管理员。

终端 A 完整失败输出：

```text
2026/07/13 18:21:56 control tower server failed: apply migration: Error 1146 (42S02): Table 'control_tower_test.metric_1m' doesn't exist
```

失败步骤：应用 `server/migrations` 迁移。

### 判据 B：全链路 E2E

**未执行**。判据 A 失败后按照任务红线立即停止，没有运行 `deploy/e2e-server.sh`，因此没有 E2E 步骤输出或退出码。

### 判据 C：数据库抽查

**未执行**。Server 未完成迁移和启动，未执行 users、alert_events、channel_commands、operation_audits 四项结果查询。

## 环境问题记录

首次加载本地测试配置时被 PowerShell 默认执行策略阻止；随后仅对当前 PowerShell 进程使用 `ExecutionPolicy Bypass`，成功读取配置并重建测试库。该环境问题处理后，真正阻塞验收的是上述空库迁移失败。

## 最终结论

**FAIL**。

M1 不能通过阶段点验收：当前代码在全新 `control_tower_test` 空库上无法完整应用迁移，Server 因 `metric_1m` 表不存在而启动失败。依据验证任务约束，本次仅记录发现，不修改代码、不调整迁移、不继续执行后续链路。
