# Codex 任务：v3.1-B1——受限管理员账号模型与权限中间件

背景：docs/design-v3.1-scoped-admin.md §1/§2。本批只建账号与权限骨架，直连与账单在 B2/B3。**依赖：rc20 基线。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

- **022 迁移**：users 表 `ADD COLUMN role VARCHAR(16) NOT NULL DEFAULT 'admin', ADD COLUMN scope_site VARCHAR(64) NOT NULL DEFAULT '', ADD COLUMN scope_user_ids TEXT NULL`（纯增量 1060 容忍+反向断言；存量账号自动 admin 零感知）。
- **权限中间件**（server）：session 载 role/scope；`viewer` 仅放行白名单接口（客户监控相关 metrics（instance_user 维度）、B2 起的日志明细/用户只读、B3 起的账单），其余一律 403；放行接口强制注入 `站点=scope_site AND user ∈ scope_user_ids` 过滤（**过滤在 server 端 SQL/内存层执行，非前端**）；viewer 登录与关键查询写 operation_audits。
- **超管维护界面**：CT 用户管理页支持创建 viewer（选站点+录入 newapi 用户 ID 集合，校验数字列表）、改 scope、停用；viewer 仅可自助改密。
- **Web**：viewer 登录后菜单仅显客户监控（B2/B3 页面上线后自动出现）；auth store 带 role 供路由守卫。

## 验证要求

1. 全量测试 + typecheck；新增测试：中间件白名单矩阵（viewer 访问管理接口 403、放行接口 scope 注入、拼 URL 越权返回空/403）、021 反向断言、scope 校验。
2. 手工：建 viewer → 登录只见客户监控且数据只含绑定用户；admin 行为与升级前零变化。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 存量 admin 行为零变化（回归）
- [ ] viewer 越权（直接拼接口）有测试且为 403/空
- [ ] scope 过滤在 server 端强制，非仅菜单隐藏
- [ ] 022 纯增量+反向断言；审计落库
- [ ] 一个 commit：`feat(server,web): scoped viewer accounts and enforcement middleware (v3.1-B1)`

## 明确不做

只读直连（B2）；账单（B3）；viewer 操作能力；按分组自动圈定；跨站点 viewer。
