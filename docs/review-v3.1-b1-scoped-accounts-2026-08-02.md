# 验收记录：v3.1-B1 受限管理员账号（2026-08-02）

范围：4fead94。验收环境：Linux，Go 1.24.5 + Node 22。

## 结论：通过；批次要求的越权测试缺失已由验收方补齐（ab0bd9e）；审计缺失转 B2 必补

### 通过项

- **中央白名单闸（默认拒绝）**：RequireSessionOrToken 内对 viewer 仅放行三个 GET 路径（metrics/metric-history 限 instance_user 维度、instances），其余方法与路径一律 403；**强制注入 site=scope_site 并剥离调用方 instance_id/site**——伪造参数在闸内即被覆盖，设计正确；
- **用户集合层过滤**：filterScopedMetricItems 按维度键尾部用户 ID ∈ scope_user_ids 过滤，metrics/metric-history 全部四个返回路径均已挂载；
- **022 迁移**：只补 scope_site/scope_user_ids/enabled（role 列 002 原生已有，未重复建——正确）；存量账号 role 默认 admin 零感知；enabled 停用同时切断登录与既有会话（Validate 校验 Enabled）；
- 用户管理接口（/api/auth/users）admin 门禁在；CreateScopedUser/UpdateScopedUser 校验完备（viewer 必须有站点与非空用户集合、密码 ≥8）；UsersView 界面与 viewer 菜单裁剪就位；全量测试 + typecheck 绿。

### 验收补齐（批次硬要求缺失）

**越权矩阵测试整体缺失**——白名单闸是本批的安全底线，交付却无一条测试覆盖。已补（ab0bd9e）：8 例白名单矩阵（放行/403/方法拒绝）、scope 注入与 instance_id 剥离断言、停用用户登录与既有会话双切断回归。

### 转 B2 必补

**审计缺失**：批次要求 viewer 登录与关键查询写 operation_audits，交付未做（auth 包零审计调用）。B2 本就要接直连查询审计，登录审计一并接入——已列为 B2 验收硬项。

### 记档

无自查清单（连续惯例记录）；instances 接口对 viewer 返回站点内实例列表（闸内 GET 放行）——viewer 可见实例名与站点结构，属预期最小信息，记档确认。

## 部署

随下次 tag（rc23 或更早）走；先于 B2 部署亦可用（viewer 仅客户监控可看，符合分批上线设计）。
