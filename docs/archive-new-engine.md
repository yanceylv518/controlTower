# 全新两任务归档执行器

本实现与旧四任务状态、检查点、统计台账无关。旧辅助表保留，新执行路径不调用旧迁移、旧台账清理或旧封存执行器。共用的是原始 `logs_YYYYMM`；现有月表不执行 DROP、TRUNCATE、ALTER、RENAME，尚不存在的月份从源日志结构创建。

## 新入口

- Agent：`startArchiveJobs` → `agent/internal/archivejob`，仅 `collection`、`history` 两个开关。
- 控制接口：`/api/agent/log-archive-jobs/poll`。
- 页面接口：`/api/dashboard/log-archive-jobs`。
- 旧 Agent 归档轮询和旧页面的写配置入口返回 HTTP 410；独立旧采集启动方式明确拒绝启动。不存在失败后回退旧执行器的逻辑。
- 控制配置不从旧站点配置复制，新配置默认暂停。新 Server/Agent 必须配套部署；共享月表要求旧执行进程及在途批次先退出，再启动新任务。

## 新归档库表

| 表 | 当前字段与作用 |
| --- | --- |
| log_archive_meta | singleton_id、schema_version、source_hash、state_json、updated_at；state_json 原子持久化采集检查点、历史日期/步骤/扫描检查点、日期边界和调度次数。两个任务的进度不再拆成四个可调度任务 |
| log_archive_days | log_date、revision、state、version_id、raw_rows、step、error_code、updated_at；raw_rows 为 NULL 表示尚未统计，不冒充零 |
| log_archive_batches | batch_id、task、before_id、after_id、read_rows、inserted_rows、changed_rows、unchanged_rows、committed_at；与数据和游标在同一事务提交 |
| log_archive_daily_stats | version_id、group_hash、log_date、dimensions、amounts；构建版本隔离，多维分组与大整数金额合计 |
| log_archive_day_versions | version_id、log_date、revision、row_count、content_hash、parser_version、sealed_at；校验通过后发布版本 |
| log_archive_issues | issue_id、log_date、step、error_code、created_at；失败原因保留 |

Server 通过迁移 `091_log_archive_jobs.sql` 建立 `log_archive_job_control`、`log_archive_executors`、`log_archive_day_reports`，只保存控制与上报，不直接扫描源库。归档库的新表由新执行器初始化。

## 推进与恢复

1. 新元数据不存在时，取全部已有月表最大 ID 初始化；已存在时只能恢复已提交检查点。最早日志日期优先取归档，全部为空才取源库。
2. 采集按 ID 读取。遇到尚在延迟窗口的日志即停止该页，不跨过该 ID 推进；月表写入与游标同事务。
3. 历史处理仅选昨天及以前、已有后续日期原始日志证据的日期。步骤为扫描源日志并补齐、扫描归档核对内容哈希及数量、构建多维日统计、原子发布封存版本。
4. 新增或内容实际改变才增加日期版本；相同日志重放不使结果失效。历史修补不推进采集游标。整理、封存时版本改变则重新处理日期。
5. 数据或计费解析问题记录当天失败，继续后续日期；数据库连接等基础设施错误保留检查点重试。失败日期由显式重试令牌重新排队。
6. 调度按批次交接，默认四次采集机会后一次历史机会；只启用一项时独立执行。当前实现为了事务边界清晰，整个批次串行，不运行旧四任务并发逻辑。
7. 连接拥有的数据库锁与有时限的 Server 授权防止两个新执行器同时提交。单批次受行数、8 MiB 读取量和查询时间限制。

## 日统计口径

保留用户、令牌、模型、渠道、分组和日志类型维度，以及历史价格/表达式/用户折扣快照。不同用户模型折扣不合并。计费整数用任意精度求和；源 quota、折前、减免、折后分别累计，不二次打折，也不拿用户折后扣费替代上游成本。缺失字段按 missing 计数保留，缓存总量与细分分别保存，不能相加当作总量。

当前版本保存源记录的请求级扣费结果和计价证据，不执行新的上游定价表达式，也没有将既有正式账单接口切到新日统计。单条归档明细查询、日统计查询和正式出账属于后续接口接入，不能把新表已生成等同于账单功能已交付。

## 验证与上线边界

### 2026-09-25：按字节预算分页修复

单批读取保留8 MiB字段字节预算，与配置行数共同限制。采集、源校验补齐、目标核验和日统计均使用字节分页：下一完整记录放不下时，返回当前完整记录前缀，不推进越界记录游标；历史各阶段明确区分字节截断与读取结束，不能仅凭短批次进入下一阶段。单条超过8 MiB时保留前缀推进能力，随后停在该行，错误仅报告日志ID、字节数及上限，不包含内容。

无需表迁移或重置任务，更新Agent后接续既有检查点。本次go vet/test全量通过，归档新引擎隔离MySQL全包8.456s通过，新增超过8 MiB分批/重启/统计失败回滚/单条超限专项通过；未生产部署。下方为最初交付时的验证边界，不代表本次实库检查未执行。

已增加协议、二进制哈希、计费分组/精度和前端请求隔离测试。数据库集成用例覆盖最大 ID 初始化、历史补漏、两折扣分组、封存、重启续跑及旧表不变，需要显式配置 `CT_ARCHIVE_TEST_DSN` 的隔离测试数据库。本机 Docker daemon 未启动，未执行真实 MySQL 验收，未修改远程数据库或部署远程服务。

仍需真实 MySQL 验收的关键场景包括事务断连、暂停恢复、多执行器互斥、持续写入时的日期失效、现有月表结构差异及数据库权限。源库必须具有可用 created_at 索引，缺失时明确报错，不自动修改源库结构。源历史保留声明是封存前提；手动绕过执行器修改月表不会自动产生新数据版本。


## 可配置交替批次（2026-09-25）

设置页支持采集连续批次和历史连续批次，各1–100，默认4:1。tasks.collection_batches/history_batches存于既有配置JSON；缺失或0按默认解析以兼容旧配置，负数或超过100由Server和Agent拒绝。两个任务都启用时按轮次交替；只启用一个则连续推进。轮次持久化到既有状态JSON，比例修改从下一批起开启新周期，日志游标和封存版本不重置。

此计数为调度批次机会（空批也占一次），不是行数，也不等待一天完成才切换。每批条数和执行间隔另行配置。需要配套更新Server、Web和Agent；旧Agent忽略新字段并保持4:1，不能仅凭配置版本确认就认为旧Agent支持新比例。没有新增数据库迁移。
