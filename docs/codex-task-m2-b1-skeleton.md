# Codex 任务：M2-B1——Web 行走骨架（脚手架 + API client + 登录 + 总览 + Go 托管）

M2 第一批。目标是**打穿全部技术风险**：pnpm 工作区、typed API client（session 认证 + CSRF）、ECharts 图表、Vite 构建产物由 Go Server 托管——骨架通了，后续页面就是批量复制。开发期间新前端挂 `/next/`，与旧静态页共存。

**文末自查清单逐项填好粘贴进 commit message。**

## 已批准的依赖（仅限这些，锁定版本写入 lockfile）

Node ≥ 20 + pnpm；Vue 3、Vite、TypeScript、Vue Router、Pinia、Element Plus、ECharts（按需引入）。**vitest 等测试框架未批准，本批不引入**——质量门为 `vue-tsc` 类型检查 + 构建成功 + Go 侧路由测试。

## 背景速读

- API 契约已冻结：`docs/api-contracts.md`（认证四端点、overview/metrics/metric-history/usage、alerts+events+action、runtime 四查询、notification 渠道/投递、instances、channel-commands、operation-audits）。错误格式统一 `{"error":"code"}`。
- 认证：登录 `POST /api/auth/login` 设 `ct_session` HttpOnly Cookie；**Cookie 通道的非 GET 请求必须带 `X-Requested-With: XMLHttpRequest`**（否则 403 csrf）；`GET /api/auth/me` 探测登录态。
- 静态托管现状：`server/internal/httpapi/mux.go` 托管 `web/`（旧静态页在 `/`，`/assets/`）。
- CI：`.github/workflows/ci.yml` 目前只有 Go 质量门。

## 硬性纪律

1. 依赖仅限已批准清单；`pnpm-lock.yaml` 必须提交；不使用 CDN 外链（一切经 Vite 打包）。
2. 不改 `agent/**`；`server/**` 只允许本任务规定的静态托管改动；**不动旧静态页 `web/index.html`、`web/assets/**`**（共存期照常服务）。
3. 所有文件 UTF-8 无 BOM；`.gitattributes` 增加 `*.ts`、`*.vue`、`*.json` 的 `text eol=lf`。
4. 构建产物 `web/dist/` 加入 `.gitignore`，不提交。
5. API 基址不写死：开发走 Vite 代理，生产同源相对路径。

## 工作项

### 任务 1：pnpm 工作区脚手架

```
webapp/
├── package.json            # pnpm workspace 根（scripts: dev / build / typecheck）
├── pnpm-workspace.yaml
├── packages/
│   ├── shared/             # @ct/shared：API client + 类型 + 工具
│   └── desktop/            # @ct/desktop：Vite + Vue3 + TS 应用
```

- desktop 用 Vite 官方 vue-ts 模板为底，接入 Vue Router + Pinia + Element Plus（组件按需自动导入可用 element-plus 官方推荐方式，但**不得引入未批准的辅助插件**——用手动全量引入亦可，注明选择）。
- `vite.config.ts`：`base: '/next/'`；build outDir `../../..//web/dist/desktop`（相对 workspace 正确指向仓库 `web/dist/desktop`）；dev server `proxy: {'/api': 'http://127.0.0.1:8080'}`。
- 根 `webapp/README.md`：pnpm install / dev / build 三步说明。

### 任务 2：shared 包——typed API client

- `client.ts`：fetch 封装——同源相对路径；**非 GET 自动附加 `X-Requested-With: XMLHttpRequest`**；`credentials: 'same-origin'`；响应非 2xx 解析 `{"error":code}` 抛出 `ApiError{status, code}`；**401 时触发注入的 onUnauthorized 回调**（由应用层跳登录）。
- `api/auth.ts`：login/logout/me/changePassword；`api/dashboard.ts`：overview、metrics(latest)、metricHistory、alerts、alertEvents、alertAction；类型手写自冻结契约（本批只需以上端点的类型，其余批次再补）。
- 类型示例须与契约字段一致（snake_case 保持原样，不做驼峰转换——注明理由：与契约文档一一对应便于核对）。

### 任务 3：desktop 应用——登录 + 总览

- **路由**：`/login` 与 `/`（总览）；全局前置守卫——非 login 路由先 `me()`，401 重定向 `/login?redirect=`；登录成功回跳。Pinia store 存当前用户。
- **登录页**：Element Plus 表单（用户名/密码/提交），错误显示"用户名或密码错误"（401）与"已锁定，请稍后再试"（429）；回车提交。
- **总览页**：
  1. KPI 卡片行（成功率/请求数/TPM/错误率/平均与 P95 耗时/健康检查/容器）——数据 `GET /api/dashboard/overview`；
  2. 趋势图（ECharts 折线）：请求量 + 错误数双序列，数据 `metric-history?dimension_type=instance&window=1m&hours=1`（instance 维度键从 metrics latest 取，无数据显示空态）；
  3. 当前告警列表（前 5 条，级别标色）——`alerts?active_only=true`；
  4. 30 秒自动刷新，`document.visibilityState` 不可见时暂停；顶部显示最后刷新时间与登录用户名 + 退出按钮。
- 布局：左侧导航（本批只有"总览"一项 + 占位分组）、顶栏、内容区——为后续批次页面预留插槽结构。

### 任务 4：Go 托管 `/next/`

- `mux.go`：`/next/` 前缀服务 `web/dist/desktop` 目录，**SPA fallback**（该前缀下未命中的非 `/api` 路径返回其 index.html）；目录不存在时返回 503 `{"error":"webapp_not_built"}`（而非 panic/404，便于未构建时诊断）。`/api/**` 与旧静态页路由优先级不受影响。
- `mux_test.go`：三个断言——`/next/` 在目录缺失时 503；临时目录放置假 index.html 时 `/next/` 与 `/next/any/spa/route` 均返回其内容；`/api/**` 不被 `/next` 抢占。（服务目录做成 Options 可注入路径，测试用 t.TempDir。）

### 任务 5：CI 前端质量门

`.github/workflows/ci.yml` 增加 job（与 Go job 并行）：checkout → pnpm/action-setup + actions/setup-node@v4（node 20，pnpm 缓存）→ `pnpm install --frozen-lockfile` → `pnpm typecheck` → `pnpm build`。产物不上传。

### 任务 6：文档

- `docs/development-progress.md`：M2/P6 对应行更新（M2-B1 完成，标注 /next 共存策略）。
- 根 README Development 节补 webapp 三行说明。

## 验证要求（无 vitest，以下必须全绿）

1. `pnpm typecheck`（vue-tsc --noEmit）零错误；`pnpm build` 成功产出 `web/dist/desktop/`。
2. `go vet ./...`、`go test ./...` 全绿（含新增 mux 托管测试）。
3. **本地手工验证并在 commit message 记录结果**：起 Server（M1 验证同款环境）→ `pnpm build` → 浏览器开 `http://127.0.0.1:<port>/next/` → 登录 → 总览 KPI/趋势图/告警渲染 → 退出回登录页。无法起环境时如实注明未验证项。
4. push 后 CI 双 job 绿。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 任务 1~6 逐节核对
- [ ] 依赖未越界（对照已批准清单检查 package.json 全部 dependencies/devDependencies）
- [ ] pnpm-lock.yaml 已提交；web/dist 未提交；.gitattributes 已更新
- [ ] 旧静态页文件零改动（git diff 确认）
- [ ] 手工验证结果已记录（或如实注明未验证）
- [ ] 一个 commit：`feat(web): vue3 walking skeleton with login and overview (M2-B1)`

## 明确不做

- 其余页面（B2/B3）；`/next` → `/` 切换与旧页删除（B4）；暗色主题；移动端；vitest。
