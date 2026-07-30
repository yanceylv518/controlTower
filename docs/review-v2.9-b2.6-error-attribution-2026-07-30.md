# 验收记录：v2.9-B2.6 错误归因（2026-07-30）

范围：b9ae408。验收环境：Linux，Go 1.24.5 + Node 22。

## 结论：通过，零缺陷

- `go vet` + 全量测试 + `pnpm typecheck` 全绿；自查清单已贴入 commit。
- **分类器**（agent/internal/errorclass）：四种提码正则、100~599 范围校验、提不出默认渠道侧——与批次一致，注释明确写了"未知错误保持对调度与告警可见"的原则。
- **全链路透传逐环核验**：agent 配置（CT_USER_ERROR_CODES 默认 400,413,422，非法 fail-fast）→ 聚合器分类计数（main.go:446 实配接入）→ reporter/gateway 契约字段 → ingest → server 聚合 MergeMetric 加法 → 1m/5m rollup → mysqlstore 两种 upsert（覆盖/累加）与**全部 7 处显式列清单 SELECT** 同步 → 017 迁移（两条独立 ALTER，重放各自 1060 容忍，反向断言测试在）。
- **引擎归因口径**：ChannelMetric.ErrorRate() 改为 (error-user)/requests 并钳负，判定/熔断/回填三处同口径；TotalErrorRate 独立保留；evidence 带 error_rate_total 与 user_error_count 明细，冤案可核查。
- **企微告警**：渠道错误窗口过滤用户侧错误（main.go:101 实配接入），客户维度维持原口径——语义与批次一致。
- 展示口径零变化（error_count 含义不动，页面无改动）。

## 记档

- 默认码集 400,413,422 **待生产错误样本校准**（已知：用户余额不足若报 403 会归渠道；客户端断开类无码错误归渠道）——部署后在样本页抽错误内容核一遍，码集改 agent 配置即可。
- 引擎测试中 ChannelMetric 定位构造随结构加字段同步更新，判定断言未动。

## 部署提示

先 Server（017 自动迁移）后 Agent（新配置键有默认值，零配置升级）；存量指标行 user_error_count=0，调度错误率自然退化为现口径，随新数据生效。
