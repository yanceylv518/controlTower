# Codex 任务：用户账单按令牌分组（令牌月账单/日账单/明细）

背景：用户账单现聚合到 用户×模型×分组×档位×天,无令牌维度;newapi logs 有 token_id/token_name（源码实证）。需求=每用户下按令牌再分组：令牌月账单（区间合计+金额）、日账单、请求明细。**设计定稿：新增独立令牌日表,不改 billing_daily_versions 主键**（035 主键重建教训,禁止 DROP/ADD PRIMARY KEY）。

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量 `go test ./...` + `pnpm -r typecheck` + `pnpm -r build`。迁移注释禁分号。**

## 1. 迁移 046（单条增量 CREATE TABLE IF NOT EXISTS）

`billing_token_daily_versions`：job_id VARCHAR(40), instance_id VARCHAR(64), user_id BIGINT, token_id BIGINT NOT NULL DEFAULT 0, token_name VARCHAR(128) NOT NULL DEFAULT '', username VARCHAR(128) DEFAULT '', model_name VARCHAR(255), group_name VARCHAR(64) DEFAULT '', tier_from BIGINT DEFAULT 0, day DATE, request_count/prompt_tokens/completion_tokens/cache_tokens/cache_write_tokens/cache_write_5m_tokens/cache_write_1h_tokens/quota BIGINT DEFAULT 0, updated_at DATETIME(6), PRIMARY KEY(job_id,user_id,token_id,model_name,group_name,tier_from,day), KEY idx_token_versions_report(instance_id,day,user_id,token_id)。

## 2. 生成路径（最小侵入）

- 账单扫描投影（billingLogsPageQuery/PagedLogRecord）补 `COALESCE(l.token_id,0)`、`COALESCE(l.token_name,'')`——扫描行集不变,**request_key 不升版**;
- 日切聚合同一趟额外按 token 维度累计,ReplaceBillingDay 事务内同步整日替换令牌表（与用户表同 job 同事务,原子一致）;
- 旧 job 无令牌行属预期：令牌接口对无行任务返回明确标记（见 §3）,**部署后重生成账单一次令牌数据才齐**。

## 3. 读接口（复用用户账单读闸 billingJobForRead("generate") 语义,409 一致;scope 校验与 /billing/detail 相同:非 admin 站点+用户双闸）

- `GET /api/dashboard/billing/tokens?instance_id&user_id&from&to[&job_id]`：该用户每令牌一行（token_id/token_name/区间合计 request/prompt/completion/cache/cache_write tokens/quota + **ct_amount:计价引擎在令牌分片行上跑现行回退链**）;job 有效但令牌表零行且用户表有行→返回 `token_data_missing:true`（前端提示"该账单生成于令牌功能上线前,重新生成后可见"）;
- `GET /api/dashboard/billing/tokens/daily?instance_id&user_id&token_id&from&to[&job_id]`：该令牌 日×模型 行（含金额）;
- **请求明细只做下载,不做在线翻页**（用户拍板:实时逐页查 newapi 太慢）——令牌明细 CSV 走既有异步导出任务机制（BillingExportJobHandler 模式:建任务→后台流式写文件→轮询状态→下载）,内部用 DetailedLogsPage 窗转 id 手法追加 `AND l.token_id=?` 分页扫全区间（页大小沿用 billingWorkbookPageSize=500）;列=时间(业务时区)/request_id/模型/普通输入/缓存读/缓存写/输出/quota;
- 汇总 CSV：令牌汇总+该令牌日账单两段（BOM,billingDownloadName 惯例）——同步接口即可（读 CT 表,快）。

## 4. 前端（用户账单抽屉内加"按令牌"分区,布局已定:上下结构）

- 抽屉顶部 Tab："按模型（现状,默认,零改动） | 按令牌"；
- **令牌汇总表=月账单,常驻 Tab 顶部**：每令牌一行（令牌名,空名显示"(未命名) #id"/请求数/输入/缓存读/输出 tokens/ct 金额/quota 参考）,按金额降序,行可点选;
- **点选令牌 → 下方详情区 = 日账单表**（日期/模型/请求数/输入/输出/金额）,不做在线请求明细;
- **token_data_missing**：汇总区与日账单显示引导条"该账单生成于令牌功能上线前,重新生成账单后可见";明细下载不依赖重生成照常可用（实时扫 logs）;
- 选中令牌上两个下载按钮：[导出 CSV]（汇总+日账单两段,同步）与 [下载明细]（异步任务,按钮态=生成中→下载,失败 toast 透出原因）。

## 5. 测试要求

1. 046 单语句契约;投影契约（token 两列在,billingOtherProjection 不动）;
2. 聚合：多令牌同用户同模型正确拆行、令牌行与用户行同 job 合计相等（守恒断言）;ReplaceBillingDay 同事务替换两表;
3. 金额：令牌分片计价与用户级同价源（同模型同档同分组单价一致）;
4. 接口：scope 矩阵（非 admin 越权 403）、job 409、token_data_missing 路径、明细导出任务状态流转（running→completed/failed）;
5. CSV 两段结构。

## 6. 明确不做（记档）

- 异常单不拆令牌;不做全站点令牌横向汇总;不做令牌级工作簿 xlsx（CSV 先行）;不改用户账单现有任何口径;不动 request_key。

## 自查清单（粘贴进 commit message）

- [ ] go test ./... 全绿（Linux）
- [ ] pnpm -r typecheck && pnpm -r build 全绿
- [ ] 046 单语句;未触碰 billing_daily_versions 结构与 request_key
- [ ] 守恒断言测试在（令牌行合计=用户行合计,同 job）
- [ ] token_data_missing 路径+scope 矩阵+409 测试在
- [ ] 部署说明含"需重生成账单"
