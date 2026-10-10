# 旧用户账单入口退役（2026-10-10）

## 范围与行为

用户要求去掉旧用户账单。旧BillingView.vue仅包装BillingRecordsView的user模式，筛选usage_version<3；所用任务、价格预览、结果、删除及下载API同时服务新版/上游，不能整体移除。此次退役旧用户账单页面与导航，不清理历史数据或移除共用后端计费、任务与导出能力；前轮发现的旧汇总计价问题不算已修复。

- 删除webapp/packages/desktop/src/views/BillingView.vue及路由导入。
- /billing、/billing/overview、/billing/generated重定向到/billing/new，保留query/hash；不读取旧账单页面。
- 侧栏（含移动菜单）与菜单设置只保留一个“用户账单”，使用现有/billing/new；工作区及页面标题去掉“新版”字样。新版权限保持billing.users，viewer依旧无法访问。
- 现有/billing/new菜单显示设置保留。后端菜单GET过滤旧/billing键，PUT兼容旧页面发送的布尔键并丢弃；不将旧键覆盖到新版键，不读取时写库。审计保留原设置及新设置。
- 上游账单、报表中心、新版用户日/月/临时账单和生成历史保持现有接口。无需数据库迁移或Agent升级。

## 验证

- 新增billingRetirement.test.mjs用真实Vue Router内存路由验证三个旧地址和新版地址、query/hash、权限拒绝以及上游/报表页面。
- 24项前端回归通过：退役路由、菜单配置、账单工作区、临时账单、账单目录、下载互斥。
- go test ./server/internal/dashboard -run TestMenuVisibility -count=1及go vet ./server/internal/dashboard通过；新增旧键true/false与新版键相反时的GET/PUT兼容验证。
- git diff --check通过。pnpm build（含vue-tsc类型检查）通过，Vite构建13.90s，仅既有大包提示；未浏览器点击/视觉验收。

## 交付边界

未提交、推送、发布、部署或重启服务。保留其他会话正在进行的归档日统计和既有实验/文档改动，不纳入本次实现。历史账单文件与数据未删除。后续若需彻底退役旧聚合API/算法，应另行核对上游、核对报表及历史下载的依赖。