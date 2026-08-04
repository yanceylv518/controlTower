# Codex 任务：v3.1-B4——渠道账单（上游用量对账）

背景：用户需求（2026-08-04）——按渠道查询消耗用量，与上游供应商控制台对账。**明确定调：只做用量查询，不做上游价格表/汇率/毛利**。金额列 = quota÷站点 QuotaPerUnit（newapi 口径，现成换算）。**依赖：当前 main（761df61 之后）。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck` + `pnpm --filter @ct/desktop build`。**

## 设计

### 026 迁移

`billing_channel_daily(instance_id VARCHAR(64), channel_id BIGINT, channel_name VARCHAR(255), model_name VARCHAR(255), day DATE, request_count BIGINT, prompt_tokens BIGINT, completion_tokens BIGINT, cache_tokens BIGINT, quota BIGINT, updated_at DATETIME(6), PRIMARY KEY(instance_id, channel_id, model_name, day), KEY idx_channel_daily_day(instance_id, day))`——纯增量+反向断言。无档位维度（无上游价格则档位无意义）。

### 日切 runner 扩展（零额外生产库查询）

- RollupDay 现有逐行扫描中**顺手**聚合渠道维度 map（channel_id 从日志行取；LogsForBilling 的 SELECT 需补 channel_id 列；channel_name 从 channel_current 一次性取映射，缺失回退 `渠道 <id>`）；
- 与用户日切同事务节奏写入：`ReplaceBillingChannelDay`（整日替换幂等，对齐 ReplaceBillingDay 模式）；
- 回填自动同时生成两套日切（同一次扫描），无需独立回填入口；**已回填过用户账单的历史需重跑回填才有渠道数据**——交付说明注明。

### API（admin-only：上游成本敏感，viewer 的中央闸本就不放行，勿加白名单）

- `GET /api/dashboard/billing/channel-summary?instance_id=&month=&format=`：每渠道一行（渠道名/请求/输入/输出/缓存 tokens/quota 金额换算）+ 合计行；`format=csv` 带 BOM；
- `GET /api/dashboard/billing/channel-detail?instance_id=&channel_id=&month=&format=`：模型×日明细 + CSV；
- 金额换算用该站点 ratio 快照的 QuotaPerUnit（缺省 500000），复用 AmountFromQuota；查询只读 CT 表，零 newapi 访问；
- api-contracts.md 增量更新。

### Web

- 用户账单页顶加页签或工具栏入口"渠道账单"（admin 可见）：列表（月份选择+合计行+CSV）→ 行点开详情抽屉（模型×日+CSV）；
- 样式与用户账单一致；无数据/未回填态给明确提示（"该月无渠道日切数据，如需历史请先回填"）。

## 验证要求

1. 全量测试 + typecheck + build；新增测试：渠道聚合与用户聚合同源一致（同一批日志两套合计的 tokens/quota 总量相等）、整日替换幂等、channel_name 回退、金额换算、viewer 访问 403（闸默认拒绝断言）、CSV BOM。
2. 手工：回填一个月 → 渠道账单合计与用户账单合计的 quota 一致；抽一个渠道与上游控制台用量对照。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 渠道聚合复用同一次日志扫描，生产库零新增查询
- [ ] 两套日切同源一致有测试；整日替换幂等有测试
- [ ] viewer 403；CSV 带 BOM；api-contracts 更新
- [ ] 026 纯增量+反向断言
- [ ] 一个 commit：`feat(server,web): per-channel usage billing for upstream reconciliation (v3.1-B4)`

## 明确不做

上游价格表/应付金额（用户定调）；汇率；毛利分析；档位维度；上游余额跟踪；viewer 可见性。
