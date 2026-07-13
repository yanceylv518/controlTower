# Codex 返工任务：M1-B2 验收未通过项

对 `24ada7a` 的 review 结论：迁移/存储双实现/e2e 脚本/token 只显示一次且列表无泄漏/instance_mismatch 403 —— 这些**已通过**。以下 5 项返工，完成后仍是一个 commit（`fix(server): M1-B2 rework per review`）。原任务文件 `codex-task-m1-b2-instances.md` 的纪律全部继续适用。

## R1（缺失的整个任务 4）：Dashboard 多实例过滤

按原任务 4 原文实现：`/api/dashboard/metrics`（含 latest 路径）、`/usage`、`/server-metrics`、`/health-checks`、`/docker-statuses`、`/agents`、`/overview` 支持 `?instance_id=` 过滤（缺的补上，已支持的仅补测试）。过滤下沉到 store 查询参数（runtime 类 query struct 多数已有 `InstanceID` 字段，只差 handler 解析）。**测试**：metrics、agents、server-metrics 各一个"两实例数据互不串"用例。

## R2（缺失测试）：Agent 网关五场景

`agentgateway` 新增测试：① 按实例 token 通过；② token 与请求体 instance_id 不匹配 → 403 `instance_mismatch`；③ 旧全局 token 回退通过；④ 轮换后旧 token 宽限期内通过、`DeleteExpiredInstanceTokens` 清理后（或模拟过期时间后）拒绝；⑤ 实例停用后 token 拒绝。用 MemoryStore 作为 TokenLookup。现有测试不动。

## R3（规格缺口）：实例列表响应

1. `GET /api/dashboard/instances` 每项附带 agents 概要（复用 `RuntimeStore.QueryAgents` 按 instance_id 过滤：agent id/version/last_seen_at/backlog_estimate/online）——原任务明确要求，Runtime 字段已注入但未使用。
2. **禁止裸序列化 storage 结构体**：当前响应是 PascalCase（`ID`/`Name`...），与全部现有 API 的 snake_case 相悖，M2 前端将直接消费。定义 DTO（`instance_id`/`name`/`enabled`/`created_at`/`updated_at`/`agents:[...]`），Update 响应同样改 DTO。测试断言字段名。

## R4（安全回归）：鉴权先于请求体解析

现状：handler 先 `hasBearerToken`（只看头存在）→ 解析 gzip 请求体 → `authorize`。任何带任意 Bearer 头的请求都能让服务器先做最多 8MiB 的解压——旧代码是先验完 token 再解析，这是回归。重构为两段：

1. **解析前**完成 token 认证：查表命中（得到 tokenInstanceID）或 legacy 全局 token 匹配，双双失败直接 401，不碰请求体；
2. 解析请求体后，仅对查表命中的通道校验 `req.InstanceID == tokenInstanceID`，不匹配 403。

补测试：无效 token 的请求在体解析前被拒（可用一个"永不合法的 gzip 体 + 错误 token"断言返回 401 而非 400/500）。

## R5（健壮性）：错误处理

`Update`/`Rotate` 中 `_ = i.Store.XXX(...)` 吞错——store 出错必须返回 500 `query_failed`（Create 已是正确示范）；对已停用实例的 rotate-token 返回 409 `instance_disabled`（给停用实例发新 token 没有意义且易误导）。补对应测试与 mux 路由存在性断言（原任务测试第 5 条）。

## 完成标准

`make test` 与 CI 全绿；`git grep` 确认 token 明文/哈希无日志泄漏；一个 commit。
