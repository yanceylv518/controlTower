# 归档计费数据契约与合成样本（P0）

- 日期：2026-09-21，Asia/Shanghai。
- 适用：新版归档的原始证据、事实构建和未来账单 reader；配套[实施计划](implementation-log-archive-billing.md)及[表结构](schema-log-archive-billing.md)。
- 状态：本文件固定字段和兼容边界。P4 已实现 `internal/archivefacts` 的 codec/parser、分页事实构建和日版本封存；30 条合成样本经过真实证据/事实提取，并与旧生产解析器作兼容回归。P5/P6 金额配置与归档账单仍未实现，事实或封存成功不等于可出账。
- 代码依据：[现有源读取与提取](../server/internal/dashboard/passthrough_handler.go)、[PagedLogRecord / Job / 过滤](../server/internal/billing/jobs.go)、[扣费模式](../server/internal/billing/statement_usage.go)、[历史价格展示](../server/internal/billing/historical_prices.go)、[按日原文摘要](../agent/internal/logarchive/reconcile.go)。

## 1. 数据经过的四层

1. **原始镜像**：`logs_YYYYMM` / `logs_undated` 保留源列类型、NULL 和字节。源记录修正后可产生新的镜像；不能用镜像行充当不可变账单输入。
2. **不可变计费原文**：从镜像固定计费必要字段和完整 `other` 原文，保存 codec、源 schema 指纹、字段类型、NULL 标志和长度。hash 覆盖 codec、schema 和内容，不对 MySQL 重新序列化后的 JSON 直接求摘要。不新增请求/响应正文采集。
3. **标准化事实**：已归日的每条 type 2 原始记录都要对应事实或可追踪的转换异常，包括零价、零输出和解析失败。事实绑定 `day_version_id + source_log_id` 和证据 hash；保留缺失标志，不能把失败解析为正常零值。重解析产生新版本。
4. **PagedLogRecord 适配**：这是当前账单引擎的输入结构，不是证据存储格式。只有已确认能够表达的字段才进入此结构；不能表达的 NULL、未知类型或溢出须在适配前分类/阻断。未来 reader 不查询线上用户、渠道或 options 来补齐事实。

原始行 hash、计费 evidence hash、事实 hash、manifest hash 分别验证不同集合；不能互换。原始摘要包括全部源列；计费 evidence 只保存契约列，仍保留 `other` 原文。任何白名单增加都须明确 codec / schema / parser 版本。

## 2. 逐字段映射

事实列已有 P4 构建实现；当前尚未接入 P6 账单 reader。所有整数均保持源有符号 `BIGINT` 范围，传输使用十进制字符串，不能经过 JavaScript Number 或 `float64`。标识文本保持字节和 NULL；事实层长度超限报转换错误，禁止截断。

| 源 logs / 身份 | 不可变证据与事实 | PagedLogRecord / 当前行为 |
| --- | --- | --- |
| 注册的 site / dataset / source generation | 证据上下文与日 manifest 固定三者；一个归档 schema 一个数据集/代际 | 当前结构无 dataset/generation；未来 reader 在外层验证，不能只靠 `InstanceID` |
| `id` | `source_log_id`；数据集/代际内唯一，原文保留精确整数 | `ID int64`；不可用 request_id 去重 |
| `created_at`（Unix 秒） | `created_unix` 和北京时间 `log_date`；不能归日进入隔离，不造假日期 | `CreatedUnix int64`，游标 `(CreatedUnix, ID)` |
| `type` | `log_type` 原值；type 2 是消费事实候选；其他类型保留原始镜像/类型统计 | 现有 SQL `WHERE l.type=2`，结构不含 type；未知类型的非零 quota 不能在新版静默过滤 |
| `user_id` | `user_id NULL`；身份是 generation + user_id | `UserID int64`；现有 SQL 直接扫描，NULL 会失败；新版不得用 0 代替未知用户后正式出账 |
| `token_id` / `channel_id` | 对应 nullable ID；NULL 和真实 0 分开 | `TokenID` / `ChannelID int64`；当前 SQL `COALESCE(...,0)`，是 legacy 兼容行为 |
| `username` / `token_name` | `username_snapshot` / `token_name_snapshot` 可 NULL；无名称可按已知稳定 ID 展示 | `Username` / `TokenName`；当前 NULL→空串；不能改成读取当前 users/tokens |
| `model_name` / `group` | `model_name` / `group_name` 可 NULL；保存当时名称/分组 | `ModelName` / `GroupName`；当前 NULL→空串；新版缺失影响计价/维度时显式处理 |
| `request_id` / `upstream_request_id` | 对应 nullable 文本；重复或空值合法，不是唯一键 | `RequestID` / `UpstreamRequestID`；当前 NULL→空串 |
| `quota` | nullable 原始整数扣费量；不保存按当前价格重算的值代替它 | `Quota int64`；现有 SQL NULL 会扫描失败；正式金额需要确定的 quota |
| `prompt_tokens` | `source_prompt_tokens` 保存原值；规范化输入另存，不覆盖原始用量 | `SourcePromptTokens sql.NullInt64`，`PromptTokens sql.NullInt64` 存计价输入通道 |
| `completion_tokens` | `source_completion_tokens` 保存原值；展示输出另存 | `CompletionTokens sql.NullInt64`；NULL 不等于输出 0 |
| `other` | 原文字节及 SQL NULL 原样进入 evidence；白名单投影存 pricing_snapshot / usage_context / issue_codes | 当前读取先经 `billingOtherProjection`，坏 JSON→`{}`；新证据禁止复用这一有损投影 |
| 源 `channels.name`（不属于 logs） | 不可追溯为请求时名称；若保留，只能作为明确带采集时点的辅助快照 | `ChannelName` 目前 LEFT JOIN 当前 channels；归档账单不得为此依赖线上库 |

`logs` 其他列仍属于完整原始镜像/核验摘要。它们不是当前账单算法输入；将来使用新列需升级契约，不从某个旧版本不存在的字段推断事实。

## 3. other 白名单与现有提取语义

以下是已核对的 legacy 基线，供共享解析器接入时回归，不声称 P0 已抽出或替换现有函数。新版必须同时保存 key 缺失、JSON null、合法 0、非法数值这四类状态；旧 `PagedLogRecord` 的若干 int64 字段没有能力表达这种区别。

| 白名单来源 | 事实 / PagedLogRecord | 当前兼容规则 |
| --- | --- | --- |
| `cache_tokens`, `cached_tokens`, `cache_read_input_tokens`, `prompt_cache_hit_tokens` | `cache_read_tokens` / `CacheTokens` | 按此顺序取第一个可解析的正整数，不叠加别名 |
| `cache_creation_tokens`, `cache_write_tokens`, `cached_creation_tokens` | `cache_write_tokens` / `CacheWriteTokens` | 取别名最大值，再与 5m+1h 之和取大，不重复相加 |
| `cache_creation_tokens_5m`, `claude_cache_creation_5_m_tokens` | `cache_write_5m_tokens` / `CacheWrite5mTokens` | 首个正值；5m 比率缺失时旧计价回退通用 creation ratio |
| `cache_creation_tokens_1h`, `claude_cache_creation_1_h_tokens` | `cache_write_1h_tokens` / `CacheWrite1hTokens` | 首个正值；使用独立 1h 比率，不凭 5m 推造历史 1h 单价 |
| `usage_semantic`, `claude` | `usage_context.usage_semantic` / `UsageSemantic` | anthropic 标记优先；仅没有任何现代计价快照/多媒体用量且缓存超过 prompt 时，旧逻辑推断 anthropic |
| `image_input`, `image_tokens` | `image_input_tokens` / `ImageInputTokens` | 首个正值 |
| `image_output_tokens` | `image_output_tokens` / `ImageOutputTokens` | 明确图像输出；不与下行旧别名混淆 |
| `image_output` + `admin_info.usage_billing_path` | 兼容图像输入 | 只有明确 image_input 和 image_output_tokens 都没有正值、且 billing path 非空时，`image_output` 才作为图像输入；无 path 的旧诊断字段不拆出计价通道 |
| `audio_input`, `audio_input_token_count`, `audio_output` | audio 输入/输出 / `AudioInputTokens`, `AudioOutputTokens` | 输入按别名顺序取正值；历史音频分项单价目前未独立还原 |
| `model_price`, `model_ratio`, `completion_ratio`, `cache_ratio`, `cache_creation_ratio`, `cache_creation_ratio_5m`, `cache_creation_ratio_1h`, `group_ratio`, `image_ratio` | 精确十进制字符串 / 同名驼峰字段 | `model_price>=0` 可构成按次模式，0 是有效免费价；空串意味着未记录；`model_price=-1` 是常见非按次标记 |
| `billing_mode`, `expr_b64`, `matched_tier` | 原字符串 / `BillingMode`, `ExprBase64`, `MatchedTier` | 表达式重算使用该请求时间和规则；坏表达式不能偷偷回退倍率算法 |
| `request_rules`, `tool_surcharges` | 保留原始子值并提供版本化投影 / `RequestRules`, `ToolSurcharges` | 旧投影重新编码 JSON；不可变原文仍必须保存原字节；工具价现行口径是金额/千次，按组倍率处理 |

特殊现行规则：令牌名 TrimSpace 后等于“模型测试”时，缓存诊断不作为计费通道，四个缓存数清零；原始 evidence 仍保留诊断数据。OpenAI 语义的计价 prompt 为 `max(0, source_prompt - cache_read - cache_write - image_input)`；Anthropic 为 `max(0, source_prompt - image_input)`，上下文另加 cache_read/write。两者都不能把 NULL prompt 改成 0。

展示用量与计价输入独立：当前 usage version 1+ 的普通输入继续扣 audio_input；普通输出从原 completion 扣 image_output 和 audio_output，逐步夹到非负。原始 completion 不因展示拆分而被覆盖。事实必须在 usage_context 保存足以重建 `ContextTokens`、语义和缺失状态的信息，不能只保留展示值。

当前 `parseBillingCacheUsage` 使用普通 JSON 解码，JSON 数字经过 float64，超 2^53 的整数和高精度小数可能损失精度；字符串数值保留的范围更好，但不是新版保证。新证据只拷贝字节，未来精确提取使用 `json.Number` / 有理数和范围校验，遇到不支持值报 issue。修复提取语义要生成新 parser 版本，不能回写已发布事实，也不能未经回归改变 legacy 账单。

## 4. NULL、异常、过滤与金额

- **NULL / 缺失 / 0 / 空串**：分别保留；统计须同时记录已知量和缺失量。SQL SUM 忽略 NULL 不意味着完整；核验确认为空的日期才记 0，未检查的日期计数为 NULL。
- **坏 other**：先保留证据。若原始 quota、单位、身份、筛选条件均确定，`pricing_source=newapi` 可以显示原始扣费并提示缺少规则/用量；依赖缺失内容的重算或筛选必须阻断或隔离，不能伪造成功解析。
- **零输出**：`completion_tokens=0` 是正常已知值；只有任务固定 `exclude_zero_output=true` 才按现有策略排除。NULL 输出是 `output_token_missing`，不能按正常零输出处理。
- **零价**：显式 `model_price=0` / 合法零倍率与缺少价格不同，不应报“缺历史价”。非零 quota 与零价重算结果不符时按固定校验规则报告差异。
- **范围**：消费事实不得因当前过滤条件被删除。纳入、排除、隔离三类保留可追踪行数与整数 quota；同一行不能重复归类，未知 quota 单独计数。
- **无法归期**：无法解析时间、未知类型且有收费等进入 issues。未明确影响范围的收费异常增加全局阻断；不能归到发现当天，也不能因为某一天摘要匹配就忽略。
- **金额**：固定历史 QuotaPerUnit/单位/换算因子后，按配置片段精确累加 quota / QuotaPerUnit；最终按固定版本舍入。分子分母或十进制都不能经过 float64。数据库范围/scale 超限明确失败，不自动四舍五入入库。

### data_source 与 pricing_source 独立

| 字段 | 值 | 语义 |
| --- | --- | --- |
| `data_source` | `legacy_live` / `archive` | 日志与上下文从哪里读取；老任务缺失值只按 legacy_live 解释 |
| `pricing_source` | `newapi` / `recalculate` | 直接使用日志 quota 换算，或按该日志历史价格规则重算 |

`pricing_source=newapi` 不表示联网读取 new-api；归档事实同样可以保存其原始 quota。现有空 pricing_source 继续沿用旧重算分支；升级不能改旧任务金额含义。新增 data_source 列或类型不等于已经实现 ArchiveBillingSource、任务快照或 worker 隔离；这些在 P6 验证后才开放入队。

现有历史单价展示已使用 `logs.other` 快照，并有 `HistoricalPriceUsageVersion=2`。缺少的是可信的历史 QuotaPerUnit/单位有效期、固定任务上下文和归档读取闭环，不是从零开始写历史价格展示。

## 5. 身份、版本与不可替代关系

| 标识 / 版本 | 负责的边界 |
| --- | --- |
| `site_id` | CT 站点授权边界；不能拿另一站回执重置显示后继续落库 |
| `dataset_id` + `source_generation_id` | 一组归档数据与源 ID 空间；同 id 在不同代际可同时存在，不合并为同请求 |
| 源物理指纹 / 连续性证据 | 地址或主备变化触发复核；不能据地址自动切代或证明数据连续 |
| `archive_format_version` | 目标结构/协议能力；高于支持版本拒绝，旧 Agent 不获得新任务 |
| `writer_epoch` | 单写者授权隔离；进程重启并不自动使 epoch 增长，授权接管才分配新 epoch |
| `mutation_revision` | 原始日期集合每次变更的计数；核验/冻结绑定它 |
| `day_version_id` / `version_no` | 已固定日内容的不可变身份及展示序号；构建中版本不可读作已封存 |
| `catalog_revision` | 当前可用版本与阻断状态的一整批目录；跨日修正一次发布，CT 原子投影，旧回执不得回退 |
| `batch_id` / receipt ID | 幂等批次/回执身份；重复 ID 内容相同才可复用，不以 ID 相同忽略内容冲突 |
| `evidence_codec_version` / source schema hash | 原始证据编码与源字段解释；与 facts schema 分开 |
| `parser_version` / `fact_schema_version` | 原文字段提取语义 / 事实结构；新解析不得修改旧版本 |
| `usage_version` | 账单展示用量语义；当前 0、1、2 的已有产物保持原含义 |
| pricing / filter / model metadata version | 金额算法、异常/过滤规则和模型上下文，固定在任务 manifest |
| config / template / renderer version | 历史金额配置时间线与输出渲染，任务重试不能选择最新版替代 |

版本支持必须具体声明能力。P1 仅具有身份/迁移/目录基础时，不声明可封存、可补齐或可归档出账。旧累计 `site_log_archive_days` 不是新版 coverage 或封存证据。

## 6. 服务端正式出账判定

账期使用北京时间 **`[from,to)`**。`from < to`；相交自然日必须全部有已发布、当前未标脏且可读取的封存版本。`to` 恰在 00:00 时不包含该日；精确半日账期仍要求其相交整日已封存。今天/未来、unknown/缺日不能靠改前端参数放行。

判定需同时满足：

1. 站点、数据集、源代际、主体归属一致；格式、parser、usage 和账单算法受支持。
2. 同一归档一致性事务读取一个 catalog_revision 下的整套日版本/manifest；存在构建中、脏、缺失、hash 不符或未归期收费阻断时拒绝。CT 目录只是投影，不替代归档权威校验。
3. 金额配置版本对精确账期的有效区间连续覆盖、互不重叠；观察到当前值不能证明此前一直相同，不默认补 500000。跨配置片段逐段计算。
4. 选定的价格/过滤规则可判断全部记录，异常有明确处理和守恒记录；输入身份、精确区间、日版本、配置/算法/过滤/显示身份和模板全部固定到任务。
5. 数据引用受保留保护后才入队。重试与所有下载读取同一输入；源库离线不会导致自动回退 legacy。

“封存”只是第 2 项的一部分。P0/P1 的结构和协议测试通过，不构成以上门槛通过。

## 7. 可复用合成样本

文件：[log_cases.json](../internal/archivecontract/testdata/log_cases.json)。全部为人工构造，不包含生产用户、真实请求内容或凭据。

- `columns` 指定源列类型；`defaults.source_values` 加每条 `source_overrides` 得到完整源行。值只允许 UTF-8 字符串或 JSON null；字符串是模拟 `sql.RawBytes` 的精确内容，null 是 SQL NULL。未覆盖字段继承 defaults，而不是 SQL NULL。
- 文本 `"null"`、空串、`"{}"` 和 JSON null 互不等同。未来非 UTF-8 场景须增加明确 base64 字节编码，不能把无效字节强转 UTF-8；本批样本不假装覆盖任意二进制正文。
- 每条记录明确 case ID、标签、可选身份覆盖和预期；`legacy_projection` 只是已核对当前函数的兼容基线，未列字段没有断言。`required_behavior` 是未来归档实现的验收约束，不是本次已实现声明。
- 覆盖普通 token、表达式与工具费、5m/1h 缓存、多媒体新旧语义、模型测试、按次、零价/零输出、NULL/坏 JSON、同秒多 ID、重复 request_id、>2^53 ID/数值、高精度小额、跨北京时间月份和不同源代际复用 ID。
- 样本校验测试检查边界数据未损坏、时间/身份关系和精确金额预期；共享解析器接入时还需把相同输入交给线上/归档两条真实路径逐项断言，不能以样本自身校验替代 parser 回归或数据库验收。

## 8. 环境核验清单

本批没有远程探测或读取生产日志；以下未取得可引用的本轮实测证据，状态均为 **待核验**，不阻塞本地编码，不代表可生产启用。

| 条件 | 启用前需要的证据 |
| --- | --- |
| 源量级 | 日均/峰值行数、平均/P95/最大单行字节、到达率；原始样本无需带身份/正文 |
| 源索引与查询预算 | id 及 created_at-leading 索引、EXPLAIN、分页延迟/源 CPU IO；不自动修改源 DDL |
| 清理/迟到/修改约束 | 保留起点、实际清理任务、迟到窗口和历史修正行为；“从最小 ID 开始”不能证明此前无删除 |
| 归档数据库 | 实际 MySQL 版本/引擎/schema、容量/增长、账号权限、备份与恢复演练结果 |
| 目标连续性 | site/dataset/generation、源身份及迁移证据；旧写入进程停止、旧凭据隔离后再接管 |
| Server 只读连接 | Server 网络连通、独立只读账号与 secret ref、目标身份一致；不把 DSN 写入日志/回执/页面 |
| 历史金额 | QuotaPerUnit、单位/汇率变更边界的有效期证据；当前 options 快照不足以覆盖历史 |
| 负载与恢复 | 提交响应丢失、重启恢复、重复回执、接管及旧 epoch、源离线重放；逐阶段实际执行并记录 |

独立测试库迁移通过只证明对应测试环境；真实站点绑定、生产负载、历史数据覆盖和金额有效期仍需各自验收。
