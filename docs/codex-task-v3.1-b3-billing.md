# Codex 任务：v3.1-B3——用户消费账单

背景：docs/design-v3.1-scoped-admin.md §4。金额口径（用户定稿）：`金额 = (非缓存输入(prompt−cache)/1M×输入价 + 缓存 cache/1M×缓存价 + 输出 completion/1M×输出价) × 分组倍率`，示例：(298/1M×$2.10 + 8507/1M×$0.42 + 194/1M×$8.40)×1 = $0.005828。查询时计算；quota 换算（÷QuotaPerUnit）保留为对照列。**依赖：v3.1-B1/B2 已合入（scope 中间件、只读直连）。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### 023 迁移：billing_daily + 两张计价配置表

- `billing_daily(instance_id VARCHAR(64), user_id BIGINT, username VARCHAR(128), model_name VARCHAR(255), group_name VARCHAR(64), tier_from BIGINT NOT NULL DEFAULT 0, day DATE, request_count BIGINT, prompt_tokens BIGINT, completion_tokens BIGINT, cache_tokens BIGINT, quota BIGINT, updated_at DATETIME(6), PRIMARY KEY(instance_id, user_id, model_name, group_name, tier_from, day), KEY idx_billing_daily_day(instance_id, day))`（分组维度=logs.group，空归 ''；**tier_from=档位下限**，日切聚合时按单请求 prompt_tokens 与该日生效档位边界分类，无阶梯配置的模型恒 0——分档必须在聚合前完成，聚合后无法再分）；
- `billing_prices(model_name VARCHAR(255), effective_from DATE, tier_from BIGINT NOT NULL DEFAULT 0, input_price DECIMAL(12,6), output_price DECIMAL(12,6), cache_price DECIMAL(12,6), updated_at, updated_by, PRIMARY KEY(model_name, effective_from, tier_from))`——**阶梯计价**：单档模型一行 tier_from=0，阶梯模型每档一行（如 0/128000），请求按 prompt_tokens ∈ [tier_from, 下一档) 落档整条计价；单价/1M tokens，货币无关（显示用系统货币符号）；
- `billing_group_ratios(group_name VARCHAR(64) PRIMARY KEY, ratio DECIMAL(8,4) NOT NULL DEFAULT 1, updated_at, updated_by)`。
全部纯增量+反向断言。

### 金额计算（server 端统一函数，查询时算）

对每条日切行：取该 day 生效、且 tier_from 匹配的价格行（effective_from ≤ day 的最新一套档位表中对应档）与分组倍率（缺省 1）——`amount = (max(prompt−cache,0)/1M×input + cache/1M×cache_price + completion/1M×output) × ratio`；模型无有效价格 → 行标 `unpriced:true`，金额区显示"未定价"并汇总提示（**不得静默按 0**）；quota 对照列 = quota÷QuotaPerUnit。计算用 decimal 或先乘后除避免浮点误差累积，展示保留 4 位小数。

### 计价配置界面与接口

`GET/PUT /api/dashboard/billing/prices`（列表含生效历史与档位；调价/改档=新增一套生效日期行，不改旧行；档位编辑校验 tier_from 严格递增且首档为 0）与 `GET/PUT /api/dashboard/billing/group-ratios`；仅 admin；修改写 operation_audits；账单页顶部提示存在未定价模型时列出清单。

### 日切任务（server runner）

- 每日 02:00（服务器本地时区，交付说明注明口径）对每个配置了只读 DSN 的站点：直连聚合**昨日** `SELECT user_id, username, model_name, group, DATE(created_at), COUNT/SUM(tokens/cache/quota) FROM logs WHERE type=消费 GROUP BY 用户,模型,分组,日` upsert 入 billing_daily（幂等，重跑覆盖）；**分档聚合**：有阶梯配置的模型按该日生效边界用 CASE 分档单独聚合，其余模型一把聚合 tier_from=0；单站点失败不影响其他站点，失败日志+下轮重试；
- **回填**：admin 接口 `POST /api/dashboard/billing/backfill {instance_id, from, to}`——按天分段串行执行（每天一条聚合查询，段间 sleep 500ms 限速），进度写日志，operation_audits 记录触发；
- 当日数据：账单接口对"今天"这一段实时直连聚合（单日轻查询，同规则分档），与 billing_daily 拼接返回。

### API（scope 中间件覆盖，viewer 自动限定绑定用户）

- `GET /api/dashboard/billing/summary?instance_id=&month=&page=&page_size=&search=&sort=`：每用户一行（消费额=计价表金额、quota 对照额、请求数、prompt/completion/cache tokens、当前余额（直连 users，可缺省）、未定价标注）；**服务端分页（默认 50/页）、用户名搜索、默认按消费额降序**；响应含全站合计（SQL 聚合，与分页无关）；admin 全站，viewer 仅 scope；
- `GET /api/dashboard/billing/detail?instance_id=&user_id=&month=`：按模型分组合计 + 按日序列；viewer 越权 user_id → 403/空；
- 导出三档（均 `format=csv`，Content-Disposition，UTF-8 BOM 防 Excel 乱码，支持 `from/to` 任意日期区间、缺省整月）：
  - 汇总导出（summary，每用户一行，千级）；
  - 单用户明细导出（detail，日×模型×档位）；
  - **全站明细导出**（summary 页可选）：**流式写出**（边查边写不驻内存，按天分段查询），行数可达数十万也不得 OOM；
- api-contracts.md 更新。

### Web（账单页，admin 与 viewer 共用）

- 列表：月份选择（默认当月）+ 用户表格（分页/搜索/按消费额降序）+ 全站合计行 + 导出按钮（汇总/全站明细）；金额用设置中心货币符号；
- 详情：模型×档位分组表（单档模型不显示档位列）+ 按日消费曲线（复用图表组件与 chartRenderQueue）+ 导出；"查看日志"链到 B2 使用日志页（带用户与月份参数）；
- 站点未配置 DSN 的降级提示与 B2 一致（billing_daily 有历史数据时仍可展示，仅当日段缺失标注）。

## 验证要求

1. 全量测试 + typecheck；新增测试：日切聚合 SQL 契约（含分组维度）、upsert 幂等（同日重跑数值不翻倍）、回填分段与限速、当日拼接、scope 越权、CSV 格式（BOM/表头）、**金额公式（对用户示例 298/8507/194×$2.10/$0.42/$8.40=$0.005828 逐位断言）**、生效日期取价（调价前后两天各取各价）、**阶梯分档（边界两侧请求各落各档、首档 0 校验、改档新生效日期后历史分类不变）**、未定价标注、分组倍率缺省 1、**分页与合计行独立（翻页合计不变）、流式导出内存曲线（大数据量造数测不 OOM）**。
2. 手工：配好价格表后回填近 3 个月 → 抽 2 个用户核对金额与 quota 对照列偏差在舍入误差内（偏差大=价格配置与 newapi 倍率不一致，正是对照列的用途）；viewer 登录仅见绑定用户账单；调价新增生效行 → 历史金额不变、新日期用新价。
3. 交付说明记录：日切时区口径、回填耗时实测。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 日切幂等有测试；单站点失败不阻塞其他站点
- [ ] viewer 越权（summary/detail/csv 三处）均有测试
- [ ] 当日段实时拼接正确；未配 DSN 降级不报错
- [ ] 023 三表纯增量+反向断言；回填与价格/倍率修改均有审计
- [ ] 金额公式对示例逐位断言通过；未定价不静默按 0
- [ ] 一个 commit：`feat(server,web): per-user consumption billing with daily rollup (v3.1-B3)`

## 明确不做

按月累计用量阶梯（本批阶梯=逐请求按上下文长度判档；累计阶梯是另一种计费模型，需求出现再议）；从 newapi 自动同步价格（倍率体系与直读单价不同构，手工维护+对照列校验）；分用户定价（分组倍率已覆盖分层，更多档需求出现再议）；充值与余额对账；账单 PDF/邮件发送；按渠道拆分账单（按模型够用，记档）；billing_daily 归档/保留策略（数据量到千万级或用户提出保留年限要求再议，记档）；按日批量打包多文件导出（区间导出已覆盖）。
