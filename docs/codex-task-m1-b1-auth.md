# Codex 任务：M1-B1——Server 用户认证体系

监控系统产品主线第一批（背景见 `codex-batches-plan.md`、`development-plan.md` M1.1）。目标：用"用户名密码登录 + Session Cookie"替换 Dashboard 的单一静态 token，同时**保留静态 token 作为兼容回退**——现有静态 Web 和任何脚本在本批次后必须原样可用。

## 背景速读

- Dashboard 鉴权现状：`server/internal/dashboard/auth.go` 的 `RequireBearerToken`（常量时间比较单一 token），在 `server/internal/httpapi/mux.go` 逐路由包裹，token 来自 `CT_DASHBOARD_TOKEN`。
- 迁移机制：`server/cmd/control-tower-server/main.go` 启动时读 `cfg.MigrationPath`（默认指向 `server/migrations/001_init.sql`）一个文件，`mysqlstore.ApplySQL` 按分号拆句执行，容忍重复建表/索引错误（幂等）。
- 测试用内存存储：`server/internal/ingest/memory_store.go`（一个结构体实现全部 store 接口）；MySQL 实现按领域分文件在 `server/internal/mysqlstore/`。
- Go 1.24：**标准库已有 `crypto/pbkdf2`**（本批次密码哈希用它，零新依赖；若发现该包不可用，改为用 `crypto/hmac`+`crypto/sha256` 手写 RFC 8018 PBKDF2，约 30 行，并附 RFC 6070/8018 测试向量验证）。

## 硬性纪律

1. 零新依赖（含 `golang.org/x/*`）；密码哈希用标准库 PBKDF2。
2. **向后兼容**：静态 Bearer token 继续有效（session 与 token 任一通过即放行）；Agent 网关鉴权（`/api/agent/**`）本批次完全不动；`agent/**`、`web/**` 不改。
3. 安全红线：密码与哈希绝不写日志；session id 用 `crypto/rand`；哈希比较用 `crypto/subtle`；登录接口的失败响应不区分"用户不存在/密码错误"。
4. 所有新文件 UTF-8 无 BOM、LF；每个行为配套测试；CI（`make test`）必须绿。

## 工作项

### 任务 1：迁移加载器升级 + 002 迁移

1. `main.go`：迁移加载改为"取 `cfg.MigrationPath` 所在目录，按文件名字典序应用目录内全部 `*.sql`"。抽出可测函数（如 `mysqlstore.ApplyDir(ctx, db, dir)`，内部逐文件 `ApplySQL`）；`MigrationPath` 配置语义不变（仍可指向 001 文件，向后兼容）。
2. 新增 `server/migrations/002_users.sql`：

```sql
CREATE TABLE IF NOT EXISTS users (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  username VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  role VARCHAR(16) NOT NULL DEFAULT 'admin',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  id VARCHAR(64) PRIMARY KEY,
  user_id BIGINT NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  created_at DATETIME(3) NOT NULL,
  INDEX idx_sessions_expires (expires_at)
);
```

3. `server/internal/storage/schema_test.go` 风格如有对应契约测试则同步补 002。

### 任务 2：`server/internal/auth` 包

**密码哈希**：
- `HashPassword(password string) (string, error)`：PBKDF2-HMAC-SHA256、600000 次迭代、16 字节随机盐、32 字节密钥；存储格式 `pbkdf2$sha256$600000$<salt_base64>$<key_base64>`（迭代数写入格式，便于将来升级）。
- `VerifyPassword(stored, password string) bool`：解析格式（迭代数从字段读），`subtle.ConstantTimeCompare`。

**存储接口**（auth 包定义，mysqlstore 与 ingest.MemoryStore 各自实现）：

```go
type UserStore interface {
    UserByUsername(username string) (storage.User, bool, error)
    CreateUser(user storage.User) error
    UpdateUserPassword(id int64, passwordHash string, now time.Time) error
    CountUsers() (int, error)
}
type SessionStore interface {
    CreateSession(session storage.Session) error
    SessionByID(id string) (storage.Session, bool, error)
    DeleteSession(id string) error
    DeleteExpiredSessions(now time.Time) (int, error)
}
```

`storage` 包新增 `User{ID, Username, PasswordHash, Role, CreatedAt, UpdatedAt}`、`Session{ID, UserID, ExpiresAt, CreatedAt}`。

**Manager**：
- `Login(username, password, now)`：查用户 → 校验密码 → 生成 session（32 字节 `crypto/rand` 转 hex，TTL 来自配置）→ 落库。
- 登录限速：内存 map 按 username 记连续失败数与锁定截止；5 次失败锁 10 分钟（锁定期内直接拒绝、不校验密码）；成功后清零。加锁保护并发。
- `Validate(sessionID, now)`、`Logout(sessionID)`、`ChangePassword(userID, old, new)`（校验旧密码；新密码长度 ≥ 8）。
- `CleanupLoop(ctx)`：每小时 `DeleteExpiredSessions`；由 main 启动 goroutine。

### 任务 3：HTTP 端点（新文件 `server/internal/auth/handlers.go`，mux 挂载）

| 方法+路径 | 行为 |
| --- | --- |
| `POST /api/auth/login` | body `{username, password}`；成功：Set-Cookie `ct_session=<id>; Path=/; HttpOnly; SameSite=Strict; Max-Age=<ttl 秒>`（不设 Secure，注释注明 TLS 由反代承担），返回 `{username, role}`；失败统一 401 `{"error":"invalid_credentials"}`；锁定返回 429 `{"error":"locked"}` |
| `POST /api/auth/logout` | 删除 session 并清 Cookie（Max-Age=0）；未登录也返回 200 |
| `GET /api/auth/me` | 需有效 session；返回 `{username, role}`；否则 401 |
| `POST /api/auth/password` | 需有效 session + body `{old_password, new_password}`；成功后**使当前用户全部 session 失效**（简化：删当前 session 并要求重新登录即可，注明取舍） |

错误响应格式沿用 dashboard 现有 `{"error":"code"}` 风格。

### 任务 4：Dashboard 中间件替换

- `auth` 包新增 `RequireSessionOrToken(manager *Manager, legacyToken string, next http.Handler) http.Handler`：
  1. 有效 `ct_session` Cookie → 放行；若请求方法非 GET，**要求请求头 `X-Requested-With: XMLHttpRequest`**（CSRF 缓解），缺失返回 403 `{"error":"csrf"}`。
  2. 否则按现有逻辑校验 Bearer token（复用常量时间比较），通过则放行（token 通道不做 CSRF 检查——非浏览器凭证）。
  3. 双双失败 → 401。
- `mux.go`：所有 `/api/dashboard/**` 路由从 `dashboard.RequireBearerToken(...)` 换成新中间件；`Options` 增加 auth manager 注入；`/api/auth/*` 路由挂载（login/logout 无需鉴权，me/password 内部自校验）。
- `/healthz`、静态资源、`/api/agent/**` 一律不变。

### 任务 5：启动引导与配置

- 配置新增（`server/internal/config`，含 env 注册与校验）：`CT_ADMIN_USERNAME`（默认空）、`CT_ADMIN_INITIAL_PASSWORD`（默认空）、`CT_SESSION_TTL_HOURS`（默认 720，1~8760）。二者非必填——都为空时跳过引导。
- main 启动（迁移之后）：`CountUsers()==0` 且两个 env 均非空 → 创建管理员并打日志提示"首次登录后请修改密码"；users 为空且未配 env → 打一条 info（仅 token 鉴权可用）。**任何情况下不打印密码**。
- `deploy/server.env.example` 补三个键与注释。

### 任务 6：文档

- `docs/api-contracts.md` 末尾新增「Dashboard Auth API」节：四个端点的请求/响应示例、Cookie 与 CSRF 规则、兼容性说明（Bearer token 仍有效）。
- `docs/development-progress.md`：P5 表格对应行标记进展（认证中间件升级为 session+token 双通道）。

## 测试要求

1. auth 单元：哈希往返 + 篡改哈希/错密码拒绝 + 格式解析容错（字段数错、base64 坏）；限速 5 次锁定/锁定期拒绝/到期解锁/成功清零；session 生命周期（创建/校验/过期/删除/清理计数）。
2. handler（memory store）：登录错误密码 401 且响应不区分原因；正确登录拿到 Cookie；带 Cookie 访问 me 成功；logout 后 me 401；改密旧密码错 401、成功后旧 session 失效；锁定 429。
3. 中间件：session Cookie 放行 GET；session + POST 无 X-Requested-With → 403、带头 → 放行；纯 Bearer token 放行（含 POST 不查 CSRF）；无凭证 401。
4. 迁移：`ApplyDir` 对临时目录两个 sql 文件按序执行、重复执行幂等（用注释语句或 memory 方式验证拆分顺序即可，无真实 MySQL 时跳过集成）。
5. mux 路由测试补 `/api/auth/login` 存在性断言。

## 完成标准

1. `make test`（vet + 23+ 包）本地与 CI 全绿。
2. 兼容性自查：`mux_test` 中"旧 Bearer token 访问 dashboard 接口"用例保持通过。
3. 提交信息 `feat(server): session auth with legacy token fallback (M1-B1)`，一个 commit。

## 人工验证参考（review 通过后执行，~20 分钟，本地或测试机）

```bash
# 起本地 Server（配 CT_ADMIN_USERNAME=admin CT_ADMIN_INITIAL_PASSWORD=xxx）
curl -i -X POST localhost:8080/api/auth/login -d '{"username":"admin","password":"xxx"}'   # 拿 Cookie
curl -i localhost:8080/api/auth/me -H "Cookie: ct_session=..."                              # 200
curl -i localhost:8080/api/dashboard/overview -H "Cookie: ct_session=..."                   # 200
curl -i -X POST localhost:8080/api/dashboard/alerts/action -H "Cookie: ct_session=..."      # 403 (无 CSRF 头)
curl -i localhost:8080/api/dashboard/overview -H "Authorization: Bearer <旧token>"          # 200 (兼容)
# 连错 5 次密码 → 第 6 次 429；logout 后 me → 401；改密后旧 Cookie 失效
```

## 明确不做

- Web 登录页面（M2 前端工程做）；静态页继续用 token。
- Agent 网关鉴权改造、按实例 token（M1-B2）。
- 角色权限（role 字段只存不用）；session 的"记住我/滑动续期"。
