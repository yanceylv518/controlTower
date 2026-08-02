# Codex 任务：v3.1-B3——用户消费账单

背景：docs/design-v3.1-scoped-admin.md §4。金额口径已定稿：quota 即 newapi 计价实扣，账单金额 = `SUM(quota) ÷ QuotaPerUnit`（settings 现成键+货币符号），不建计价表。**依赖：v3.1-B1/B2 已合入（scope 中间件、只读直连）。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### 023 迁移：billing_daily

`billing_daily(instance_id VARCHAR(64), user_id BIGINT, username VARCHAR(128), model_name VARCHAR(255), day DATE, request_count BIGINT, prompt_tokens BIGINT, completion_tokens BIGINT, cache_tokens BIGINT, quota BIGINT, updated_at DATETIME(6), PRIMARY KEY(instance_id, user_id, model_name, day), KEY idx_billing_daily_day(instance_id, day))`——纯增量+反向断言。

### 日切任务（server runner）

- 每日 02:00（服务器本地时区，交付说明注明口径）对每个配置了只读 DSN 的站点：直连聚合**昨日** `SELECT user_id, username, model_name, DATE(created_at), COUNT/SUM(...) FROM logs WHERE type=消费 GROUP BY ...` upsert 入 billing_daily（幂等，重跑覆盖）；单站点失败不影响其他站点，失败日志+下轮重试；
- **回填**：admin 接口 `POST /api/dashboard/billing/backfill {instance_id, from, to}`——按天分段串行执行（每天一条聚合查询，段间 sleep 500ms 限速），进度写日志，operation_audits 记录触发；
- 当日数据：账单接口对"今天"这一段实时直连聚合（单日轻查询），与 billing_daily 拼接返回。

### API（scope 中间件覆盖，viewer 自动限定绑定用户）

- `GET /api/dashboard/billing/summary?instance_id=&month=`：每用户一行（消费额=quota 换算、请求数、prompt/completion/cache tokens、当前余额（直连 users，可缺省））；admin 全站，viewer 仅 scope；
- `GET /api/dashboard/billing/detail?instance_id=&user_id=&month=`：按模型分组合计 + 按日序列；viewer 越权 user_id → 403/空；
- 两接口带 `format=csv` 导出（Content-Disposition，UTF-8 BOM 防 Excel 乱码）；
- api-contracts.md 更新。

### Web（账单页，admin 与 viewer 共用）

- 列表：月份选择（默认当月）+ 用户表格 + 合计行 + 导出按钮；金额用设置中心货币符号；
- 详情：模型分组表 + 按日消费曲线（复用图表组件与 chartRenderQueue）+ 导出；"查看日志"链到 B2 使用日志页（带用户与月份参数）；
- 站点未配置 DSN 的降级提示与 B2 一致（billing_daily 有历史数据时仍可展示，仅当日段缺失标注）。

## 验证要求

1. 全量测试 + typecheck；新增测试：日切聚合 SQL 契约、upsert 幂等（同日重跑数值不翻倍）、回填分段与限速、当日拼接、scope 越权、CSV 格式（BOM/表头）、金额换算用 settings 值。
2. 手工：回填近 3 个月 → 列表合计与 newapi 控制台该用户月消费一致（抽 2 个用户核对）；viewer 登录仅见绑定用户账单；改 QuotaPerUnit → 金额随之变化。
3. 交付说明记录：日切时区口径、回填耗时实测。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 日切幂等有测试；单站点失败不阻塞其他站点
- [ ] viewer 越权（summary/detail/csv 三处）均有测试
- [ ] 当日段实时拼接正确；未配 DSN 降级不报错
- [ ] 023 纯增量+反向断言；回填有审计
- [ ] 一个 commit：`feat(server,web): per-user consumption billing with daily rollup (v3.1-B3)`

## 明确不做

计价表/自定义价格（金额=quota 实扣换算，设计定稿）；充值与余额对账；账单 PDF/邮件发送；按渠道拆分账单（按模型够用，记档）。
