# Codex 任务：v2.8-B2——站点数据层与分组展示

背景：站点显式化第一期（见 docs/design-v2.8-multi-site.md）。本批只做数据层+机器面分组+文案，**不动业务指标查询与总览**（那是 B3）。**依赖：无；与 agent 侧 v2.8-B1 并行。可能与在途 v2.3-B2 web 批次撞文件，冲突 rebase 手工合并。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试。**

## 设计

- **迁移 `014_instance_site.sql`**：`ALTER TABLE instances ADD COLUMN site_id VARCHAR(64) NOT NULL DEFAULT ''`。纯增量 ADD COLUMN，靠 1060 容忍幂等（013 模式）；**禁止任何表重建型 ALTER**（010 教训，加反向断言测试）。不建索引（instances 表量级个位数）。
- **读时回退语义（全系统唯一一处实现，严禁散落）**：`siteOf(instance) = site_id != "" ? site_id : instance_id`。server 侧提供统一 helper，web 侧统一 util——存量数据零迁移，单实例站点是退化情形。
- **实例管理**（`server/internal/dashboard/instance_handler.go` + web 实例管理页）：
  - Create/Update 接受可选 `site_id`；校验 `^[a-z0-9_-]{0,64}$`，非法 400 带字段错误；
  - List 响应**增量**加 `site_id` 字段（Dashboard API v1 只增不改）；
  - web 表单加站点输入框（带命名约定提示 `<站点>_<序号>`），实例列表加站点列。
- **系统状态页分组**（web）：按 `siteOf` 分组，站点做分组标题、实例做行（资源/容器/健康/在线），沿用现有图表组件；单实例站点组头照常显示（即当前生产视觉只是多了组头，无信息损失）。
- **通知文案带站点**：instance_offline 规则生成的告警标题/摘要加站点名（如"pinducloud_cn 节点 pinducloud_cn_2 离线"）；实例有 display_name 用 display_name。只改文案拼装处，不动规则逻辑。

## 接线点（逐个核对，不得遗漏）

014 迁移 + 反向断言测试、instance_handler Create/Update/List、web 实例管理表单与列表、系统状态页分组、offline 告警文案、siteOf helper（server/web 各一处）单测。

## 验证要求

1. 全量测试绿；014 sanity + 无重建语句断言；siteOf 回退语义单测（空→instance_id）。
2. 手工：升级后不填 site_id → 系统状态页与现在等价（每实例自成一组）；给两个实例填同一 site_id → 归入同组；离线告警标题带站点名。
3. List 接口响应新增字段不破坏旧前端（增量字段，B3 之前 web 其余页面不消费 site_id）。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 014 纯增量、幂等、无重建语句（反向断言）
- [ ] siteOf 回退语义 server/web 各只有一处实现且有测试
- [ ] site_id 校验拒绝表齐全；List 增量字段有测试
- [ ] 存量数据（site_id 空）页面表现与升级前一致
- [ ] 一个 commit：`feat(server,web): instance site model and grouped system status (v2.8-B2)`

## 明确不做

总览/维度页站点筛选（B3）；独立 sites 表、站点 display_name；多站点告警路由；指标表 schema 改动（站点视图靠实例集合过滤，B3 实现）。
