# Codex 任务：v3.1-B2——newapi 库只读直连（日志明细与用户只读）

背景：docs/design-v3.1-scoped-admin.md §2/§3。**依赖：v3.1-B1 已合入。**架构破例受控：直连仅服务本批两类查询接口。

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

- **023 迁移**：instances 表 `ADD COLUMN logs_readonly_dsn VARCHAR(512) NOT NULL DEFAULT ''`（站点级；实例管理页可填，仅 admin 可见可改，页面注明"只读账号，仅授 logs/users SELECT"）。未配置站点相关页面显示"未开通明细查询"。
- **直连管理**（server 新包）：按站点惰性建连接池（MaxOpenConns 2、查询超时 5s、只读会话）；DSN 变更热生效（每次取时比对）。
- **日志明细接口** `GET /api/dashboard/passthrough/logs`：参数 站点/用户（viewer 强制 scope 注入，admin 可查站点内任意用户）/**时间区间（必填，缺省最近 24 小时，单次区间上限 31 天——杜绝无区间全表扫）**/分页（上限 100/页）；SQL 强制 `user_id IN (...) AND created_at BETWEEN` 走 newapi 原生用户索引（部署时 EXPLAIN 验证记入交付说明）；返回字段对齐 newapi 日志页（时间/模型/渠道名/tokens/quota/耗时/内容摘要脱敏——复用 agent 侧 redact 正则的 server 版）。
- **用户只读接口** `GET /api/dashboard/passthrough/users`：scope 内用户的 id/用户名/显示名/余额/状态/已用额度；同过滤规则。
- **Web**：使用日志页（viewer 主页面之一，admin 也可用）+ 用户管理页（只读列表）；两页均带站点未开通降级态。
- 审计：viewer 的直连查询记 operation_audits（查询参数摘要）；**同批补上 B1 遗留的 viewer 登录审计**（B1 验收记档转入的硬项）。

## 验证要求

1. 全量测试 + typecheck；新增测试：DSN 未配置降级、分页上限钳制、scope 注入（viewer 传他人 user_id 返回空）、超时与连接池参数、脱敏。
2. 手工（需真实 newapi 库只读账号）：建 `GRANT SELECT ON logs,users` 账号 → 配 DSN → 明细分页/时间过滤与 newapi 日志页一致；EXPLAIN 走 user 索引。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 直连仅两接口可达，采集/指标链路零改动
- [ ] scope 注入有越权测试；分页/超时护栏有测试
- [ ] 未配置 DSN 优雅降级；DSN 仅 admin 可见
- [ ] 023 纯增量+反向断言；审计落库
- [ ] 一个 commit：`feat(server,web): read-only newapi passthrough for logs and users (v3.1-B2)`

## 明确不做

写路径；账单（B3）；直连用于任何指标/采集用途；DSN 加密存储（只读账号风险面已评估，记档）。
