# Codex 任务：v2.9-B2.6——错误归因（用户侧错误不计入调度错误率）

背景：用户指出（2026-07-30）降级标准的错误率太笼统——上下文超长、参数错误等用户侧错误会冤枉健康渠道。拍板方案：**按错误码分类**。日志事件无结构化错误码，从 ErrorSummary 文本提取 HTTP 状态码归类；用户侧码集合可配，无法提码默认算渠道（对未知上游故障保持敏感）。**依赖：v2.9-B2.5 已合入（引擎判定阈值在 DegradeCriteria 内）。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### 分类器（agent 侧新包 agent/internal/errorclass，唯一实现）

- `ExtractStatusCode(summary string) (int, bool)`：正则提取常见模式——`status code (\d{3})`、`statusCode[=: ]+(\d{3})`、`"code":\s*(\d{3})`、`HTTP (\d{3})`（大小写不敏感；命中多个取第一个；提不出返回 false）；
- `IsUserError(summary string, userCodes map[int]bool) bool`：提出码且 ∈ userCodes → 用户侧；否则渠道侧；
- agent 配置键 `CT_USER_ERROR_CODES`（逗号分隔，默认 `400,413,422`；校验 100~599）。**页面可配不做**——agent 无配置下发机制（既有记档），本批用 agent 本地配置，config example/README 同步。

### 指标管道（user_error_count 全链路）

- **017 迁移**：metric_1m 与 metric_5m 各 `ADD COLUMN user_error_count BIGINT NOT NULL DEFAULT 0`（纯增量，1060 容忍，反向断言测试照 014 模式）；
- agent metricaggregator：`LogType=="error"` 时按分类器判定，用户侧单独累加 user_error_count（error_count 口径**不变**，仍是全部错误——展示层语义不动）；
- report payload、server 聚合、MergeMetric、1m→5m rollup 全部加法透传该字段（对照 cache_tokens_total 等既有累加字段的接线模式，逐处核对不得遗漏）；
- 存量数据无该列=0：调度错误率退化为现口径，部署后随新数据自然生效，无需回填。

### 引擎（调度错误率换分子）

- ChannelMetric 增加 UserErrorCount；判定用**归因错误率** `(error_count - user_error_count) / request_count`（防负值钳到 0）对比 DegradeCriteria 阈值；熔断同口径；
- evidence 增加 `error_rate_total` 与 `user_error_count` 字段——建议流水里能看到"总错误率 20%，用户侧 15 条，判定用 5%"，冤案可核查；
- 回填（fillOutcomes）demote 命中判定同步换归因口径。

### 企微错误告警（共享同一分类器）

- erroralert 的错误窗口只计**渠道侧**错误（用户侧错误不入窗、不触发告警）；客户维度规则维持现口径（客户自己的错误对客户维度是有效信号）。行为变化写入 agent/README 告警章节。

## 接线点（逐个核对，不得遗漏）

errorclass 提码正则与码集校验、agent 配置键+example+README、聚合器分类计数、payload/服务端聚合/Merge/rollup 四处透传、017+反向断言、引擎归因口径（判定+熔断+回填+evidence）、erroralert 渠道维度过滤、既有测试同步。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck`；新增测试：提码四种模式+提不出、默认码集分类、聚合分双计数、Merge/rollup 透传、引擎归因错误率（用户错误占比高不触发降级/纯渠道错误照常触发/负值钳位）、回填新口径、erroralert 用户侧不入窗。
2. 手工：构造含 "status code 400" 与 "status code 502" 的错误日志各若干 → metric 表两列计数正确 → 前者不推动降级、后者推动。
3. 校准提醒写入交付说明：默认码集 400,413,422 待生产错误样本核准（403 用户余额场景已知会归渠道，记档）。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] error_count 展示口径零变化；user_error_count 全链路（聚合/payload/Merge/rollup）透传有测试
- [ ] 017 纯增量+反向断言；存量无列按 0 退化为现口径
- [ ] 引擎判定/熔断/回填三处全部换归因口径且 evidence 带明细
- [ ] erroralert 仅渠道维度过滤用户侧错误，客户维度不变
- [ ] 一个 commit：`feat(agent,server): attribute user-side errors out of dispatch error rate (v2.9-B2.6)`

## 明确不做

关键词白名单分类（码优先，不够用再议）；错误码集页面可配（agent 配置下发机制另议）；页面全面双错误率展示（evidence 可见即可，记档）；历史数据回填。
