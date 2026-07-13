# Codex 任务：M2-B4——设置页 + 横切打磨 + 新前端转正（M2 收官）

M2 最后一批：补齐设置页与横切细节，然后执行**切换**——新前端从 `/next/` 转正到 `/`，旧静态页（`web/index.html`、`web/assets/**`）正式删除。本批是唯一允许动 `server/internal/httpapi` 与删除旧静态页的批次；`agent/**` 与其余 `server/**` 依旧零改动。

**文末自查清单粘贴进 commit message。**

## 工作项

### 任务 1：设置页 `/settings`

- 修改密码表单：旧密码/新密码/确认新密码；前端校验（新密码 ≥8、两次一致），提交 `POST /api/auth/password`；成功后提示"密码已修改，请重新登录"并登出跳转 `/login`（后端已使旧 session 失效，前端同步清 store）；401（旧密码错）显示可读文案。
- 账户信息区：当前用户名与角色（来自 auth store）。

### 任务 2：横切打磨

1. **404 路由**：catch-all → NotFound 视图（返回总览按钮）。
2. **document.title**：每路由设置 `页面名 · Control Tower`（router afterEach）。
3. **favicon**：webapp 内置一枚简单 SVG favicon（塔形或 CT 字母，自绘 data 内联，禁外链）。
4. **空环境引导**：总览页当 instances 列表为空时，KPI 区上方显示引导条"尚未创建实例——前往实例管理创建并部署 Agent"（链接 /instances）——全新部署的第一分钟体验。
5. 登录页支持回车提交与 loading 态（若 B1 已有则复核）。

### 任务 3：切换转正（本批核心）

1. **Vite**：`base` 从 `'/next/'` 改为 `'/'`；Router `createWebHistory('/')`。
2. **Go `httpapi/mux.go`**：
   - 删除旧静态页托管（原 `web/` 根托管与 `/assets/`）；
   - 根路径 `/` 改为服务 SPA（沿用现 handleNextWeb 逻辑改名 `handleWebApp`，服务目录默认 `web/dist/desktop`，Options 注入名同步改造但保持向后兼容字段名或注明破坏性变更）；SPA fallback：非 `/api/**`、非 `/healthz` 的未命中路径回 index.html；未构建时 503 `{"error":"webapp_not_built"}`（错误信息中提示 `cd webapp && pnpm install && pnpm build`）；
   - **`/next/` 与 `/next/*` 301 重定向到对应根路径**（书签兼容，保留一个版本周期）；
   - `/api/**`、`/healthz`、`/api/agent/**` 优先级与行为不变（测试保证）。
3. **删除** `web/index.html`、`web/assets/`（整目录）；`web/dist/` 保持 gitignore。
4. mux 测试更新：`/` 服务 SPA 与深链 fallback、未构建 503、`/next/foo` 301 → `/foo`、`/api` 与 `/healthz` 不受影响、旧 `/assets/` 现在走 SPA fallback（断言不再 200 旧文件）。

### 任务 4：文档

- `docs/development-progress.md`：P6/M2 行更新——旧静态页退役、新前端转正。
- `webapp/README.md`：部署说明（Server 服务 `web/dist/desktop`，未构建时 503 提示）。
- 根 README：访问方式更新为 `/`。

## 验证要求

1. `pnpm typecheck`、`pnpm build`、`go vet`、`go test ./...`、CI 双 job 全绿。
2. **手工验证记录**：构建后起 Server——`/` 直接进登录页；登录后左侧全部 12+ 页面路由可达；任一深链（如 `/alerts`）刷新不 404；`/next/alerts` 301 到 `/alerts`；改密流程走通并被登出；404 路由与标题/favicon 生效；停 Server 前端错误态正常。
3. 删除确认：`git ls-files web/assets | wc -l` 为 0；`web/index.html` 不存在。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 任务 1~4 逐节核对
- [ ] 切换语义完整：/ 转正、/next 301、旧页删除、SPA 深链、503 提示、/api 与 /healthz 不受影响（对照 mux 测试逐条）
- [ ] 零新依赖；产物与 node_modules 未提交；agent 与其余 server 包零改动
- [ ] 手工验证结果逐项记录
- [ ] 一个 commit：`feat(web): settings page, polish, and cutover to root (M2-B4)`

## 明确不做

暗色主题、移动端（M3）、用户管理（多用户，后续版本）、vitest。
