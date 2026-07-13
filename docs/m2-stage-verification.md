# M2 阶段验证报告

## 验证信息

- 日期：2026-07-13
- 结论：**PASS**
- 首轮代码提交：`a9fb16fd906126e355d2309b4caa92b68bac7086`
- 第二轮代码提交：`b654f5a`（验收时的 `origin/main`，新增演示数据播种器与聚焦清单）
- 操作系统：Windows（amd64）
- 服务地址：`http://127.0.0.1:18083`
- 数据库：本地 MySQL 测试库（沿用 M1/M2 e2e 数据）
- 敏感配置：数据库密码、Dashboard/Agent token、实例 token 和通知 secret 均未记录明文

## 自动化质量门

**PASS**。

- `pnpm typecheck`：通过。
- `pnpm build`：通过；仅有 Vite 大于 500 kB 的非阻断提示。
- `go vet ./...`：通过。
- `go test ./...`：全部包通过。
- GitHub Actions：提交 `a9fb16f` 对应 CI Run #43 成功。
- 验证结束后 `git status --short` 为空，未修改业务代码。

## 首轮浏览器与数据流验证

以下内容保留首轮执行时的原始结论；其中的数据依赖项已在后文“第二轮聚焦补验”闭环。

### 入口、认证与切换

**PASS（锁定项除外）**。

- `/` 在未登录状态跳转 `/login?redirect=/`，错误密码显示“用户名或密码错误”。
- 正确登录进入总览，顶栏显示 `admin`；退出后回到登录页。
- `/alerts` 深链直接加载，无 404；`/next/alerts` 跳转到 `/alerts`。
- 未知路由显示 404 与“返回总览”；页面标题按路由更新。
- 未连续输入 5 次错误密码：该操作会锁定本地唯一管理员并阻塞后续走查；锁定行为由现有认证单测覆盖。

### 页面路由与只读内容

**PASS（数据依赖项除外）**。

- `/customers`、`/channels`、`/models`、`/alerts`、`/samples`、`/usage`、`/runtime`、`/notifications`、`/instances`、`/audits`、`/settings` 全部可达，无加载错误或误入 404。
- 渠道页存在详情、历史趋势与渠道操作区；系统状态页包含 Agent、网络 RX/TX 和健康检查；用量页包含客户/渠道/模型三类排行。
- 本地 `channel-snapshots` 返回空数组，因此无法验证渠道快照名称、权重、状态和模型 chips 的有数据展示。
- 本地缺少第二套完整 e2e 指标，未验证多实例切换后各页数据隔离。

### 错误态与恢复

**PASS**。

- 运行态页面加载后停止 Server，等待自动刷新进入“数据加载失败”状态并出现“重试”。
- 重启 Server 后页面恢复；浏览器控制台 error 日志为空。

### 告警

**PASS（静默时序项除外）**。

- 页面提交确认备注“M2 阶段验收确认”后，告警状态真实落库为 `acknowledged`。
- 时间线 API 出现 `acknowledged` 事件，actor 为 `admin`，note 正确。
- 未等待并验证静默 30 分钟后的完整状态变化。

### 通知

**PASS（重发项除外）**。

- 在本地测试库创建禁用的钉钉渠道 `m2-stage-dingtalk`，填写测试 secret。
- 列表/API 返回 `has_secret=true`，响应中不含 secret 明文。
- 当前没有适合安全重发的 failed/exhausted 投递记录，未验证重发后的状态变化。

### 实例与 Token

**PASS**。

- 创建测试实例 `m2-stage-verify-2`，创建响应返回一次性 Token。
- 轮换响应返回新 Token 与 `grace_until`；实例列表不含 token 字段或明文。
- 停用实例后，使用轮换 Token 调用 Agent 心跳返回 HTTP 401。
- 一次性 Token 弹窗的警告、复制、等宽展示和必须勾选“我已保存”后关闭，已在 M2-B3 交付验收中实测，本轮复核数据层约束。

### 渠道命令与审计

**PASS（状态流转项除外）**。

- 命令对话框显示“直接影响线上渠道”警告。
- 显示“我确认要对渠道 #77 执行此变更”复选框；未勾选时“确认下发”按钮禁用。
- 为避免对现有测试渠道产生实际变更，本轮未下发命令，未验证 `pending → delivered → succeeded` 与对应新增审计记录。

### 设置与杂项

**PASS（沿用本提交前一轮实测）**。

- 旧密码错误提示、成功改密后登出、新密码重新登录以及改回原密码已在提交 `6aa9a39` 的 M2-B4 手工验收中走通。
- favicon、动态标题、404 已验证；浏览器控制台无红色错误。

## 第二轮聚焦补验

依据提交 `b654f5a` 更新后的 `docs/m2-stage-checklist.md`，重建空的 `control_tower_test`，以 `CT_ADMIN_USERNAME=admin`、`CT_ADMIN_INITIAL_PASSWORD` 启动 Server，并执行 `deploy/seed-demo-data.sh`。播种器成功创建双实例、48 条维度指标、渠道快照、错误/慢样本、运行态、告警与必失败通知渠道。

### 空库与双实例

**PASS**。

- seed 前总览显示“尚未创建实例——前往实例管理创建并部署 Agent”。
- `inst-demo-a` 显示 `OpenAI-主力 (#77)`、权重 10、enabled、`gpt-4o` 模型；切换到 `inst-demo-b` 后显示 `Claude-备用 (#88)`、权重 10、enabled、`claude-sonnet`，两实例数据肉眼可辨且不串。

### 渠道快照与样本

**PASS**。

- 渠道详情正确显示快照名称、编号、状态、权重与模型 chips。
- 样本页同时显示 error/slow 数据及分页控件；选择“错误”并填写 `claude-sonnet` 后点击查询，只保留 `seed-err-inst-demo-b`，慢样本与 `gpt-4o` 均被过滤。
- 当前种子每类样本数量不足 50，上一页/下一页按预期禁用。

### 通知重发

**PASS**。

- 2 秒 notification runner 产生 4 条 failed 投递，页面均显示“重发”。
- 点击重发出现“已安排重发”，目标投递 attempts 从 3 重置为 0，next_attempt_at 随之更新。

### 命令与审计

**PASS**。

- `deploy/e2e-server.sh` 完整执行 `command-confirm-required`、`command-create`、`command-deliver`、`command-complete`。
- 命令最终为 `succeeded`，审计记录 actor 为 `admin`、target_id 为 `77`；脚本最终输出 `[e2e] passed`，退出码 0。

### 正式豁免项

以下三项按第二轮聚焦清单豁免，不再作为 M2 关闭阻塞项：

1. 连续错误登录锁定：认证单测已覆盖，浏览器实测会锁死唯一管理员。
2. 30 秒自动刷新与标签页恢复：B1 已手工验证同一 `useAutoRefresh`，后续页面均复用。
3. 告警静默 30 分钟后过期：等待成本高，状态与过期语义已有单测覆盖。

## 最终结论

**PASS，M2 阶段验证通过。**

首轮自动化质量门、远程 CI、入口认证、全路由、错误态恢复、告警、通知安全、实例 Token 与危险操作保护均通过；第二轮借助可重复种子数据补齐空库、双实例、渠道快照、样本筛选、通知重发、命令与审计全链路。三项高成本重复人工项已由 Claude 的聚焦清单正式豁免并有既有手工或单测覆盖。M2 可正式关闭，进入 v2.0 发布准备（Agent 双模式接入方案与部署）。本次仅更新验证报告和静态开发日志，不修改业务代码。
