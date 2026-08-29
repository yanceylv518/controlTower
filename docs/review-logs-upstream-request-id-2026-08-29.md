# 验收记录：使用日志接口返回上游 Request ID（2026-08-29）

- 范围：`4166ad2 feat(logs): return upstream request ID`
- 结论：**通过**。

## 变更内容

使用日志列表接口（`/api/dashboard/passthrough/logs`）响应增加
`upstream_request_id` 字段：只读 SQL 投影补 `COALESCE(upstream_request_id,'')`、
`PassthroughLog` 结构体与扫描列序对齐、共享 TS 类型同步。

## 交付面清点

- 仅 API 字段 + 类型定义,**未加 UI 列**——使用日志页面本笔无变化,属
  接口打底（与上游账单「上游 Request ID」追踪口径衔接）。无
  PROJECT_PROGRESS 条目;若后续要在日志页展示/复制该列属另一笔交付。

## 核对要点

- SQL 列序与 `rows.Scan` 目标逐一对齐（request_id → upstream_request_id →
  content → group → ip → is_stream → other）,错位即整行扫描失败,真库
  套件覆盖。
- 依赖前提:newapi `logs.upstream_request_id` 列实存——账单扫描路径
  （billingLogsPageQuery）自请求 ID 批次起已无条件引用该列,生产已实证;
  无该列的旧版 newapi 站点在账单路径本就不可用,本笔不扩大破坏面。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库）;
  webapp typecheck + build 通过;agent/internal 未触及。
