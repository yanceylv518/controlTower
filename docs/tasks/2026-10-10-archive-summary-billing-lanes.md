# 归档日统计补齐计费用量（2026-10-10）

## 问题和范围

用户9九月日统计实扣 2105840.515722 元已由用户确认与账单一致；纯分项重算少 16878.129870 元。此前日统计只累计原始 prompt/completion/cache，未保存 CT 逐请求归一化后的普通输入及图像、音频等用量。旧 SQL 不能从这些总数准确恢复逐请求截零、输入语义和独立图像用量。

本次修复 Agent 的 log_archive_daily_stats 生成与升级，保留已扣 quota，不修改账单定价、补录折扣、已有 Excel、生产数据库或部署。不会通过摊差额伪造分项费用，也不修复另一个任务调查的 kimi-k3 历史写入价问题。

## 实现

- summary parser 从 2 升为 3，dimensions.summary_version=3。复用 internal/archivefacts 的逐请求用量解析；先按 CT 现有输入/缓存/多模态语义拆分、截零，再累计，禁止在日总数上做减法代替。
- 原 prompt_tokens/completion_tokens/quota 及旧缓存字段不变；新增 normalized 字段，全部用整数 Token 字符串存储，不除以百万。
- dimensions.usage 保存 semantic、semantic_basis、billing_path、cache_policy、version 和 issues；不把逐请求 Token 数放进分组键。
- dimensions.pricing 增加 image_ratio、request_rules、tool_surcharges，保留原有表达式和 matched_tier。新增历史价格/规则中的有效数值保留精确十进制字符串，避免 MySQL JSON 浮点化。
- 普通页和大记录流式投影均补充同一组字段。

### amounts 新字段

| 字段 | 含义 |
| --- | --- |
| input_tokens / output_tokens | 普通文本输入/输出，已按逐请求语义扣除应分列的多模态用量 |
| pricing_input_tokens | CT 历史计价输入口径；音频输入的独立展示口径可能与之不同 |
| context_tokens | 归一化上下文用量 |
| cache_read_tokens | 归一化缓存读取 |
| cache_write_tokens | 缓存写入总数，包含分档，不可再与分档相加 |
| cache_write_5m_tokens / cache_write_1h_tokens | 缓存写入分档 |
| cache_write_unclassified_tokens | 总写入扣除上述分档后的剩余写入 |
| image_input_tokens / image_output_tokens | 图像输入/输出 |
| audio_input_tokens / audio_output_tokens | 音频输入/输出 |
| tool_calls | 有效 tool_surcharges 数组的调用次数合计 |
| usage_issue_rows | 解析出现问题的日志条数 |

新增用量缺失时写对应 `<field>_missing` 条数，真实零保留为 0，不能把缺失自动解释为零。tool_calls_missing/tool_calls_invalid 区分工具信息缺失与非法。缺少原始证据的分项仍不能凭空恢复。

工具调用种类、单价、数量的结构化快照保留在 pricing.tool_surcharges；同组快照中的单次工具 count 需要按该组日志条数累计，不能把它当成整组次数。tool_calls 已累计，但不是工具金额。未知工具价格单位、表达式、任务折扣不得臆测。音频等没有历史独立价格时也不能仅有用量就生成金额。

## 升级与历史重建

Agent 写锁下首次升级状态 SummaryVersion：仅起始日及之后的 live 统计换新版本并重置扫描游标，避免旧组增量减到新组；范围内中途 summarize/seal 的旧任务换新版本并从头统计。旧版本数据保留用于审计。

历史任务完成范围内正常待处理日期后，逐日选择起始日及之后旧 parser 的 sealed 日期，从归档原始表重新校验条数及内容哈希，再生成 v3 并通过原 seal 事务切换指针。原始数据不一致时标记失败，不发布新统计。沿用原有不可变历史确认条件、截止日期/Frontier、调度和批大小；不是部署后立即完成整个九月。需要历史任务开启，原始归档仍完整可用。

Server stats 查询透传 dimensions/amounts，无需新增表列或 Server 数据库迁移。现有业务总额仍以 SUM(quota) 为准；分项导出须改用新增字段并检查缺失计数/summary_version，不能继续把旧 prompt_tokens 当普通输入。旧/新版本只选每日期当前发布版本，禁止一起累计。

## 验证与状态

新增测试覆盖普通/图像重叠截零、音频、Anthropic 语义、缓存别名与分档、现代/旧版图像字段、零与缺失、非法用量、工具次数，以及逐字节流式投影等价。验证逐请求截零先于汇总，原 quota 不变。

本地 MySQL 隔离库升级测试覆盖 live 换版本及幂等、源日志已不存在时从归档校验重建、完成前旧版本指针不变、完成后 parser=3、新字段可用以及旧审计统计保留。

验证结果：

- `go test ./internal/archivefacts ./server/internal/archivereader -count=1` 在本地 MySQL 测试环境通过（与 Agent 全套一起执行）。
- Agent 全套首次仅 `TestLargeRecord181MiBEndToEndMySQL` 失败：本地 max_allowed_packet=64MiB，REPEAT 构造 181MiB 测试数据被 MySQL 拒绝，尚未进入功能执行；未修改全局配置。
- `go test ./agent/internal/archivejob -skip '^TestLargeRecord181MiBEndToEndMySQL$' -count=1 -timeout=180s`，本地 MySQL 隔离库回归通过（27.425s）。
- dashboard 现有输入/缓存/图像语义专项回归通过。
- `go vet ./internal/archivefacts ./agent/internal/archivejob` 和 `git diff --check` 通过。

代码已提交并推送为 62fc9b36，已随 v2.0.0-rc163 发布；未部署生产，生产九月数据尚未重建和验收。181MiB 大记录压力用例需要满足其环境条件后补验。


## 起始日期设置（同日追加修复）

用户要求此前数据不重建，只统计规定日期以后。日志归档 → 设置 → 编辑设置新增「新统计起始日期」，配置字段 `tasks.summary_from_date`，北京时间、含当天。设置显示已配置日期；配置尚未指定时显示 Agent 上报的实际日期。

- 未配置时，首次新版执行批次取当日北京时间，并保存到 Agent 状态 `SummaryFromDate`；后续重启/跨日沿用，不每天滚动，也不默认回溯全部历史。
- 日期严格校验 `YYYY-MM-DD`（1000 年及之后有效公历日期），Server 与 Agent 共用 Settings 校验；既有 JSON 配置读写及下发链路保留新字段，无需数据库迁移。
- 历史任务选择（待处理、失败重试、旧 sealed 重建）和实时任务选择均限制 `log_date >= 起始日`。此前旧统计不重置、不增量追加、不重新统计；缺少统计的旧日期也不自动补算。历史处理的原日志补齐也从该日开始；连续原始日志采集继续运行，不改采集游标。
- 调整范围时取消范围外正在执行的历史/大记录统计任务，保留已生成的数据；此前已经完成的重算不能通过推后日期撤销。
- 起点提前，只重置新纳入范围的 live 版本；范围重叠部分不重复重置。起点推后，已有 live 版本不重置。
- 原始日志真实发生变化时仍遵守原有 revision/封存失效规则；不把已变化原日志的旧封存结果宣称为仍经校验的一致快照。
- 生效需要配套新版前端、Server 和 Agent；代码已随 rc163 发布，未部署或替用户设置生产日期。

追加验证：MySQL 起始日边界、旧 live 版本/金额保留、范围外大记录任务取消、实时/历史选择、范围提前/推后及北京时间默认值跨重启固定测试通过；归档相关 MySQL 回归（排除上述环境受限的181MiB用例）通过，前端 archiveJobs 10 项行为测试与 vue-tsc 类型检查通过。修复了该前端测试夹具对已有权限模块 @ct/shared 导入的加载支持，未修改权限业务代码。


## 提交前独立快照验证

2026-10-10 按用户「提交并推进」要求，仅将本任务20个文件/对应进度条目纳入索引；其他账单、菜单等未提交工作保持原样。从索引导出 local/archive-summary-submit-20261010 独立验证：go vet ./...、go test ./...、前端10项行为测试、离线依赖安装及生产构建通过；MySQL归档/读取回归通过（排除上述181MiB环境受限用例）。git merge-base 已确认包含 v2.0.0-rc162，不依赖版本号推断修复覆盖。生产部署和日期选择仍未执行。


发布与产物核验见 [rc163 归档修复交付记录](../ai-tasks/2026-10-10-archive-summary-rc163.md)。
