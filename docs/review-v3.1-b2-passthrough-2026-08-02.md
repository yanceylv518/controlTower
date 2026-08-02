# 验收记录：v3.1-B2 只读直连（2026-08-02）

范围：c8ec057（B2 主体）+ cfb3bac/d5deb47/002feae（跟进修补）。验收环境：Linux，Go 1.24.5 + Node 22。

## 结论：通过，1 个 B1 遗留硬项与 1 个 P2 回归已由验收方修复（c897a83）

### 通过项（B2 主体）

- 023 纯增量（instances.logs_readonly_dsn）；中央白名单闸正确扩入两个 passthrough 路径（GET-only，viewer scope 在 handler 内强制：scope 未配置显式报错、user 集合注入）；时间区间必填且 ≤31 天、分页上限 100、内容摘要脱敏、直连查询审计——批次护栏全数落地且有测试（passthrough_scope_test / passthrough_handler_test / secret_test）；
- **超规格加分项：DSN 落库加密**（AES-GCM，密钥 `CT_SECRET_KEY` 环境配置）——凭证存放面的担忧被主动收敛；DSN 按站点解析（ReadonlyDSNForSite）；未配置站点优雅降级；
- 跟进修补：viewer 创建改为站点/客户下拉选取（好于手工录 ID）、实例表布局调整；自查清单已贴；全量测试 + typecheck 绿。

### 验收修复（c897a83）

1. **B1 遗留登录审计仍未做**（B2 批次明文硬项）：已补——viewer 登录成功写 `auth.viewer_login`（含 scope 站点与客户端 IP），带回归测试；admin 登录不记（按批次口径，降噪）。
2. **P2 回归（002feae 引入）**：实例管理表格改为仅显示启用实例——停用一个实例后其行连同"启用"开关一起消失，界面上再无法重新启用。已恢复全量显示（enabled 过滤仅保留给站点首行 DSN 逻辑）。

### 记档

023 无独立反向断言测试（两行纯 ALTER，风险低，惯例项记录）；**部署新增必做：配置 `CT_SECRET_KEY` 环境变量**（未配则 DSN 无法保存/读取，界面会显式报错）——写入部署清单。

### 验收补充（2026-08-02 用户反馈"没有日志列表页面"，5cd1654）

**P1 白屏**：两个直连视图在 `<script setup>` 顶层 `await`（异步组件），而应用的 router-view 无 `<Suspense>` 边界——路由切入静默渲染空白，页面"存在但不可见"。typecheck 无法发现此类运行时问题（验收盲区记档：涉及新页面的批次，验收应跑一次 `pnpm build` + 实际渲染确认）。修复：await 移入 onMounted、管理员也自动加载、站点切换联动刷新；build 验证通过。

## 后续

用户侧：站点配置只读 DSN（复用 agent 整库只读账号）→ 建首个 viewer 试用两页 → B3 账单开工。手工验收段（EXPLAIN 走 newapi 用户索引）随用户配置 DSN 后在生产完成。
