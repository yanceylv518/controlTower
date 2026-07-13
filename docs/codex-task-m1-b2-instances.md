# Codex 任务：M1-B2——实例管理 + 按实例 Agent Token + 多实例过滤

主线第二批（背景：`codex-batches-plan.md`、`development-plan.md` M1.2）。目标：实例的增删改查与按实例签发/轮换 Agent Token，Agent 网关鉴权从"全局静态 token"升级为"按 token 定位实例"，Dashboard 查询补齐多实例过滤。**全局 `CT_AGENT_TOKEN` 保留为兼容回退**（已部署的生产 Agent 在本批次后必须原样可用）。

## 背景速读

- Agent 网关鉴权现状：`server/internal/agentgateway/auth.go` 的 `validBearerToken`（与全局 `CT_AGENT_TOKEN` 常量时间比较）；handler 在同包，路由挂 `/api/agent/*`。
- `instances`、`agents` 表已在 001 迁移中存在（instances：id/name/env/region/base_url/enabled/时间戳）；**尚无 token 概念**。
- `CT_AGENT_TOKEN_PEPPER` 配置已存在且必填但**未被任何代码使用**——本批次启用：token 哈希 = `hex(sha256(pepper + token明文))`。
- Dashboard 鉴权用 M1-B1 的 `RequireSessionOrToken`；路由注册在 `server/internal/httpapi/mux.go`（Go 1.24 的 `http.ServeMux`，**可用 `"PUT /api/dashboard/instances/{id}"` 方法+通配模式与 `r.PathValue("id")`**）。
- 迁移：`server/migrations/` 目录按字典序全量应用（M1-B1 已改造），新文件编号 003。
- 内存测试存储：`server/internal/ingest/memory_store.go`；MySQL 按领域分文件。

## 硬性纪律

1. 零新依赖；`agent/**`、`web/**` 不改（Agent 侧配置按实例 token 属于 v2.0 接入时的事）。
2. **兼容回退**：旧全局 Agent token 继续可用（不绑定实例，行为与现状一致）；现有 agentgateway 测试不删改。
3. 安全红线：token 明文只在创建/轮换的响应里出现一次，任何列表/日志/错误信息不得含明文或哈希；token 生成用 `crypto/rand`（32 字节 → hex）；哈希比较走库查询（哈希本身即凭证摘要，无需常量时间比较，但**禁止**把明文写库。
4. UTF-8 无 BOM、LF；每个行为配套测试；`make test` 与 CI 必须绿。

## 工作项

### 任务 1：迁移 003 与存储层

`server/migrations/003_instance_tokens.sql`：

```sql
CREATE TABLE IF NOT EXISTS instance_tokens (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  token_hash CHAR(64) NOT NULL UNIQUE,
  created_at DATETIME(3) NOT NULL,
  expires_at DATETIME(3) NULL,
  INDEX idx_instance_tokens_instance (instance_id)
);
```

`expires_at` 为 NULL 表示现役；轮换时旧 token 行写入 `now+24h`（宽限期）。

`storage` 新增模型：`Instance{ID, Name, Env, Region, BaseURL, Enabled, CreatedAt, UpdatedAt}`、`InstanceToken{ID, InstanceID, TokenHash, CreatedAt, ExpiresAt *time.Time}`。

存储接口（mysqlstore + MemoryStore 双实现，SQL 全参数化）：

```go
ListInstances() ([]storage.Instance, error)                  // 按 id 升序
InstanceByID(id string) (storage.Instance, bool, error)
CreateInstance(storage.Instance) error                        // id 重复返回明确错误
UpdateInstance(id string, name string, enabled bool, now time.Time) error
CreateInstanceToken(storage.InstanceToken) error
// 鉴权热路径：命中要求 (expires_at IS NULL OR expires_at > now) 且所属实例 enabled=1
InstanceIDByTokenHash(hash string, now time.Time) (string, bool, error)
ExpireInstanceTokens(instanceID string, graceUntil time.Time, now time.Time) error // 该实例所有现役 token 的 expires_at 置为 graceUntil
DeleteExpiredInstanceTokens(now time.Time) (int, error)
```

### 任务 2：实例管理 API（新文件 `server/internal/dashboard/instance_handler.go`）

全部挂在 Dashboard 鉴权中间件后：

| 方法+路径 | 行为 |
| --- | --- |
| `GET /api/dashboard/instances` | 实例列表；每项附带其 agents 概要（复用现有 `QueryAgents` 按 instance_id 过滤：id/version/last_seen_at/backlog_estimate/online）；**响应不含任何 token 字段** |
| `POST /api/dashboard/instances` | body `{instance_id, name}`；instance_id 校验 `^[a-z0-9-]{1,64}$`、唯一；创建实例（env/region/base_url 存空串，enabled=true）并**同时生成首个 token**；响应 `{instance_id, name, token}` —— token 明文仅此一次 |
| `PUT /api/dashboard/instances/{id}` | body `{name?, enabled?}`（指针语义：缺省不改）；停用实例即其全部 token 立即失效（靠 `InstanceIDByTokenHash` 的 enabled 条件，无需改 token 行） |
| `POST /api/dashboard/instances/{id}/rotate-token` | 旧现役 token 全部置 24h 宽限；生成并返回新 token（仅此一次）；响应含 `grace_until` |

错误格式沿用 `{"error":"code"}`：404 `instance_not_found`、409 `instance_exists`、400 `invalid_instance_id`。

Token 生成与哈希放辅助函数（`tokenHash(pepper, token)`），main 注入 pepper。

### 任务 3：Agent 网关鉴权升级

- `agentgateway` 增加注入项：`TokenLookup interface{ InstanceIDByTokenHash(string, time.Time) (string, bool, error) }` + pepper + 旧全局 token。
- 鉴权顺序：① 计算所呈 token 的哈希查表——命中则得到 `tokenInstanceID`，**校验请求体的 `instance_id` 必须等于它**，不等返回 403 `{"error":"instance_mismatch"}`（防拿 A 实例 token 冒充 B 实例上报）；② 未命中则按现有逻辑与全局 token 常量时间比较（兼容通道，不绑实例）；③ 双失败 401。
- heartbeat 与 report 两个端点同一套逻辑（现有中间件/校验函数所在位置改造，保持 handler 结构）。
- `httpapi/mux.go` 与 server `main.go` 接线（Options 传 store 与 pepper；过期 token 清理并入 M1-B1 的每小时清理 goroutine 或新起一个，注明选择）。

### 任务 4：Dashboard 多实例过滤补齐

逐一核对以下端点对 `?instance_id=` 的支持，**缺的补上、已有的不动**（过滤逻辑压到 store 查询参数，已有 query struct 的用现成字段）：

- `/api/dashboard/metrics`（含 `latest=true` 路径）与 `/api/dashboard/usage`：按 `InstanceID` 过滤聚合行
- `/api/dashboard/server-metrics`、`/health-checks`、`/docker-statuses`、`/agents`
- `/api/dashboard/overview`：接受 `instance_id` 时各分块按实例过滤
- logs/log-samples、alerts：已支持则仅在测试中断言

### 任务 5：`deploy/e2e-server.sh`（本批起步，此后每批生长）

bash + curl + gzip，env 变量 `CT_BASE`（默认 `http://127.0.0.1:8080`）、`CT_ADMIN_USER/PASS`、`CT_LEGACY_AGENT_TOKEN` 可选。步骤（任一步失败即退出非零并打印步骤名）：

1. `GET /healthz` 200。
2. 登录拿 Cookie（`curl -c jar`），`GET /api/auth/me` 200。
3. `POST /api/dashboard/instances` 建 `inst-e2e-<时间戳>`，提取 token。
4. 用该 token 发 heartbeat（JSON 用 `gzip -c` 管道 + `Content-Encoding: gzip`，`instance_id` 匹配）→ 200；**故意用错 instance_id** → 403。
5. rotate-token：旧 token 仍可 heartbeat（宽限期）、新 token 可用。
6. `PUT` 停用实例 → 新旧 token 均 401/403。
7. `GET /api/dashboard/instances`（Cookie + `X-Requested-With` 规则注意 GET 免检）确认字段无 token 泄漏。

脚本可执行权限、LF、开头注释写明用途与前置条件（本地起好 Server + MySQL）。**CI 不跑此脚本**。

### 任务 6：文档

- `docs/api-contracts.md`：新增「Instance 管理 API」与「Agent 鉴权（按实例 token）」节：端点示例、token 只显示一次、24h 宽限、instance_mismatch 语义、全局 token 兼容说明。
- `docs/development-progress.md`：对应行更新。

## 测试要求

1. 存储双实现：token 命中/宽限期内命中/过期拒绝/实例停用拒绝/`DeleteExpiredInstanceTokens` 计数；实例 CRUD 与重复 id。
2. 网关：按实例 token 通过 + instance_id 不匹配 403 + 全局 token 兼容通过 + 过期 token 401 + 停用实例 401；现有 agentgateway 测试不动全过。
3. 实例 handler：创建（响应含 token、二次创建 409、非法 id 400）；列表不含 token/哈希字段（对响应 JSON 做否定断言）；rotate 后新旧并存语义；PUT 部分更新。
4. 多实例过滤：至少对 metrics、agents、server-metrics 各写一个"两实例数据互不串"的用例。
5. mux 路由断言补新端点。

## 完成标准

1. `make test` 本地与 CI 全绿。
2. `git grep` 自查：`token_hash`、token 明文变量不出现在任何日志语句中。
3. 提交信息 `feat(server): instance management and per-instance agent tokens (M1-B2)`，一个 commit。

## 明确不做

- Agent 侧改动（Agent 换用按实例 token 属 v2.0 接入批次）。
- 移除全局 token 回退（上线检查时做）。
- Web 界面（M2）。
