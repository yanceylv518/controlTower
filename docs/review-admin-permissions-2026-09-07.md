# 验收记录：账号管理与管理员菜单权限 + Agent 客户错误告警口径（2026-09-07）

- 范围：`8944aec feat(auth): add menu permissions and revoke sessions on password changes`、
  `578aeda fix(agent): align customer error alerts and remove cache-miss alerts`
- 结论：**两笔均通过，零缺陷**。设计说明 `docs/admin-permissions.md` 与实现一致。

## 8944aec 账号管理与菜单权限

### 变更
- "访问账号"更名"账号管理"，管理员/查看账号分页签；管理员可设姓名、初始密码、
  按二级菜单勾选的功能权限，支持改权、启停、重置密码。权限目录由后端返回（23 项），
  旧的 `monitor.read`/`data.read`/`billing.manage` 在服务端兼容展开。
- 存储：066 迁移为 users 加 `display_name`、`permissions`（JSON 数组）。NULL=原有
  全权限管理员，`["*"]`=全部权限含后续新功能，`[]`=什么都不能访问；新建管理员
  永远写显式数组，请求缺 `permissions` 也不会退化成全权限。
- 边界：每次请求读最新权限（撤权即时生效）；管理接口要求 `accounts.manage`，只能
  授予自身已有权限，不能动权限超出自身范围的管理员；禁止改自己；事务锁定全部
  管理员行防并发停用/降权最后一个全权限管理员；停用、改本人密码、重置密码都
  清除目标账号全部会话；系统 Bearer Token 保留原集成权限；实例列表对受限账号
  隐藏直连配置与 Agent 运行信息；未映射的 API 对受限管理员默认拒绝。
- 审计：`auth.account_create/account_update/password_reset`，不记密码或哈希。

### 实证
- `go vet`、`go test ./...` 全绿（auth 包 16 条含权限矩阵/防提权/撤权/会话失效/最后
  全权限管理员保护/审计入库）；`pnpm typecheck`/`build` 通过。
- **路由覆盖核对**：把 mux 全部 dashboard 路由用"勾满 23 项目录权限的受限管理员"跑
  一遍 `allowAdminRequest`，真实路由全部可达，只有本就不存在的 POST 变体被拒
  （默认拒绝生效）。
- **066 乱序套用**：烟测库台账按文件名判断，067 已在的情况下 066 正常套用，
  `users.permissions` 列到位。
- **接口实测**（烟测栈）：建只有"运行总览"的管理员 → overview 200；tuning/policy、
  operation-audits、按渠道维度的 metrics、billing/summary 均 403；instances 200 但
  `control_api_url`/`control_admin_user_id`/`control_configured` 置空、token 不返回
  （全权限管理员同一接口可见配置）；受限账号创建 `["*"]` 管理员 403，列账号 403；
  管理员改自己 400；重置密码后旧会话 401、新密码可登录；本人改密后自己与另一个
  并行会话均 401；停用后登录 401；审计三类操作均入库。
- **页面**（1366）：账号管理页两页签、权限标签、状态开关、"配置权限/重置密码"
  正常；受限账号登录后访问 `/tuning` 被导回总览，左侧菜单只剩"总览"。

### 记档（P3）
- 受限管理员的会话撤销依赖数据库 sessions 表；系统 Bearer Token 不受权限体系约束，
  仍是全权限，需妥善保管（既有行为，本批未改）。
- `metrics`/`metric-history` 按 `dimension_type` 鉴权，前端必须带该参数；新增维度
  或页面时要同步维护服务端映射与前端页面映射，否则受限账号默认被拒（设计如此，
  文档已写明）。
- 没有删除账号的接口，只能停用；停用账号仍占用用户名。

## 578aeda Agent 客户错误告警口径

- 移除缓存失效告警（v1.1.1 引入，`CT_ALERT_NOCACHE_*` 三项配置删除；旧 env 文件
  残留这些键不会导致启动失败，加载器只读已知键）。客户自身错误码现在同时不进
  渠道与客户告警窗口（此前仍进客户窗口）。README 与示例配置同步。
- agent 全部包测试通过。**本笔动了 Agent**，要拿到新口径需升级 Agent；不升级只是
  沿用旧告警口径，server 不受影响。

## 部署要点
- 8944aec：server + 前端，066 迁移首启自动套用（编号在 067 之前但台账按文件名，
  乱序无影响）；建议部署后立即为运维人员建受限管理员，主管理员改密。
- 578aeda：只动 Agent。
- rc96 打在 967c6f6 不含两笔，上线需重打 rc97（server + 前端 + agent）。
