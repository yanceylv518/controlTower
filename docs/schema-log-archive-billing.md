# 归档日志与用户账单数据库结构

> 2026-09-22：后续结构规划见[两任务归档与多维日统计方案](archive-two-task-daily-rollup-design.md)，采用多维日汇总而非全量新增逐条事实作为必经流程。本文既有结构仍用于理解现行实现，不授权直接删除生产表。

**2026-09-21 追加式校验最新调整**：继续只读源表；默认流程改为源读取时捕获摘要、目标事务内逐批回读比较、归档库日终核对后封存，不再默认四轮核验或周期复扫最近7天。已结束历史通常读源一次；曾实时采集的日期结束后额外收尾补齐一次以覆盖迟到插入。新增归档库022 `archive_scan_evidence`，源逐行hash复用既有核验明细表；依赖仅追加、迟到有界和未归档不清理的声明，不声称源快照一致性。覆盖下文旧实现说明，详见[最新操作说明](archive-workflow-operations.md)。

**2026-09-21 最新补充**：归档库新增 Agent 020 `archive_workflow`（singleton_id、state_json、updated_at）和 021 `archive_workflow_days`（log_date、state、revision、config_version、error_code、updated_at）。前者保存统一任务和恢复游标，后者保存每日结果和重试依据。CT 复用既有站点配置/状态 JSON，无新增迁移，最新仍为 088。旧原始月表复用，派生台账统计可由统一接入重建，详细约束见[统一任务说明](archive-workflow-operations.md)。以下早期阶段描述按对应阶段阅读。

- 日期：2026-09-21，Asia/Shanghai；状态：P1–P4 及简化后的 P5 金额快照已有实现；其余账单相关结构仍为后续设计，未对生产执行 DDL。
- 依据：[整体设计](design-log-archive-billing.md)、[实施计划](implementation-log-archive-billing.md)。
- 核对现行基线：main / `9de6735f` 的迁移、归档写入和账单任务实现；实际生产数据库结构未连接核验。
- 本文给出表边界、字段、键、关系和主要建表草案；示例 SQL 用于评审，不能直接当生产升级脚本。

实际迁移：P1 使用 CT `085_archive_foundation.sql` 与 Agent 独立 `migrations/001–005`；P2 增加 CT `086_archive_writer.sql` 持久 writer epoch，以及 Agent `006–008` 贡献台账、raw hash 和 last batch ID。001–005 校验和保持不变。P2 已使用 checkpoint/回执和可变日目录完成原子增量写入；预建日版本/任务表仍不代表封存、补齐或账单执行器开放。详情见[基础操作说明](archive-foundation-operations.md)与 [P2 操作说明](archive-p2-operations.md)。

P3 增加 CT `087_archive_backfill.sql`，在现有数据集表保存覆盖声明/策略/确认人及 revision，在现有任务表保存日期、调度退避、租约会话和观察进度。目标 `009_scan_tasks.sql` 增加 `(task_id,attempt)` 扫描状态，`010_ingest_issues.sql` 增加 `(stream_key,source_id)` 原始行问题记录；既有 checkpoint/receipt 承担独立时间游标和完成回执，不改 001–008 校验和。补齐扫描计数不写入正式日版本计数。详见 [P3 操作说明](archive-p3-operations.md)。

P4 新增 Agent `011–019`，CT 复用现有任务表，无 088 迁移。正式 DDL 以 `agent/internal/logarchive/migrations/` 为准，下面的早期草案由下列实现细化：

- 核验为 `archive_reconcile_runs` + `archive_reconcile_issues`，固定 assurance/coverage、四阶段扫描状态、精确摘要和逐 ID 比对；equal 摘要也保留用于分页恢复，仅差异计为异常。
- 原 `archive_issues` 草案按用途拆成既有 `archive_ingest_issues`、核验差异、`archive_fact_issues` 与 `archive_raw_repairs`，分别约束恢复、固定事实和修复审计生命周期，不创建另一张含原文的大型通用异常表。
- `archive_seal_builds` 保存完整日期集合及分批构建/复核进度。月表仍是 `archive_billing_evidence_YYYYMM` 和 `billing_facts_YYYYMM`，由两个必须保持空的模板生成。事实另保留规范化 fact_json 供审计，与全部结构化列一同核验。
- `archive_subject_index` 调整为按版本的可重建索引：PK(subject_type,subject_id,parent_user_id,day_version_id)，其余列为 name_snapshot、first_log_date、last_log_date；读取必须关联 published 日版本。这样在构建页完成索引，最终发布不扫描整日事实，也不暴露 building 元数据。
- 018 为 receipt 增加 nullable cohort_dates_json。新回执写实际同 ID 跨日修正集合或 `[]`；旧 NULL 保守使用 affected_dates_json。最终发布整个集合使用同一个 catalog revision。
- manifest 使用确定性 JSON 对象键序与大整数十进制字符串形成 SHA-256，MySQL JSON 空白/键序变化不影响复验。quota 保持有符号原始整数；NULL 和解析异常在事实与摘要中保留，不代表可换算金额。

迁移表用途、读写权限和 API 见 [P4 操作说明](archive-p4-operations.md)。封存后端已接入，但尚无 P5/P6 正式归档账单执行器，前端新操作留待 P7。

## P5 实际结构更新（2026-09-21）

按用户确认，原第 5 节的三张历史配置时间线表暂不实施，改用 CT 迁移 `088_billing_money_snapshots.sql` 的两张表：

| 表（均在 CT 库） | 实际用途 |
| --- | --- |
| `billing_money_snapshots` | `id CHAR(64)` 内容校验和主键、`instance_id`、UTC 微秒 `observed_at`、`snapshot_json LONGTEXT`；保存 quota 单位的精确字符串、基础 USD 币种、显示币种/汇率、来源证据及口径版本，按站点/观察时间索引。无更新金额内容的业务入口。 |
| `billing_job_money_snapshots` | `job_id VARCHAR(40)` 主键，`snapshot_id CHAR(64)` 外键；每任务固定一份。删除失败任务可级联删绑定，但快照记录保留；重建失败任务绑定原快照。 |

快照与新 statement 在同一 CT 事务提交。金额记录属于站点观察，本批不伪造 dataset/generation 绑定或历史生效区间；P6 创建归档输入时另行固定真实数据集和日版本。归档库与源 new-api 库本批无 DDL。下面未落地的表草案继续只代表后续设计，以本段和实际迁移为准。

## 1. 物理布局：两类数据库，三个逻辑用途

```text
CT 数据库（现有）
├─ 归档注册、调度与状态投影
├─ 历史金额配置
└─ 账单任务、输入版本引用、计算结果、文件目录

归档数据库（每个 dataset / source generation 独立 schema）
├─ 原始日志月表：可通过核验修正的镜像
├─ 采集游标、核验、日期版本与异常
└─ 不可变计费原文及账单事实月表

NewAPI 源数据库
└─ 继续只读，不增加表或索引，不修改原日志
```

账单结果不另建第三个数据库。归档 schema 可以位于同一归档 MySQL 实例，但不同源 ID 空间不能共用同一套 `logs_YYYYMM(id)`。

CT 写自己的库；Agent 写对应归档库；Server 的归档连接仅 SELECT 目录、已发布事实及历史身份索引，不获得原始证据的通用读取或写权限。

## 2. 类型、身份和版本约定

| 概念 | 建议类型/规则 |
| --- | --- |
| 新 dataset、generation、day_version、task ID | `BINARY(16)`，由 Go 生成；API 输出 UUID/十六进制文本，不依赖数据库 UUID 函数 |
| 已有账单 job ID | 继续 `VARCHAR(40)`，不改既有主键类型 |
| 源日志、用户、令牌 ID | `BIGINT`，保持源有符号范围；未知可 NULL，不把 NULL 静默变为 0 |
| 哈希 | `BINARY(32)` 保存 SHA-256；对外使用 64 位十六进制 |
| 原始事件时间 | `BIGINT` Unix 秒，与源字段语义一致 |
| 账务日期 | `DATE`，按 Asia/Shanghai 从事件时间计算 |
| 系统操作时间 | `DATETIME(6)` 存 UTC，连接时区固定为 UTC |
| 批量计数、quota/Token 合计 | `DECIMAL(38,0)`；API 用十进制字符串避免精度丢失 |
| 金额 | 计算用有理数/十进制，保存精确中间值；显示金额固定 scale，不以 float 累加 |
| 状态 | `VARCHAR` 的明确枚举，由代码校验；不依赖 ENUM 演进或旧版 MySQL CHECK 执行 |

全局日志身份为 `(dataset_id, source_generation_id, source_log_id)`。归档库内 dataset/generation 由唯一元数据行固定，因此大月表不用每行重复存两个 UUID；跨库引用仍必须携带它们。dataset_id 在 CT 全局唯一，绑定的 generation 不允许原地更换。

三个版本不能混用：`mutation_revision` 是原始日数据变化计数；`day_version_id` 是已冻结内容的身份；`catalog_revision` 是当前可用目录/阻断状态整体版本。Agent 重启会改变执行 epoch，不改变这三个数据版本的身份。

## 3. CT 库：表清单与关系

| 表 | 处理方式 | 键与主要内容 |
| --- | --- | --- |
| `site_log_archive_control` | 复用扩展 | 原 PK(site_id)；增加 active_dataset_id、协议要求，保留配置 revision/租约 |
| `log_archive_targets` | 复用扩展 | 原 PK(instance_id,agent_id)；增加能力/格式版本、观测到的数据集身份 |
| `archive_datasets` | 新增 | PK(dataset_id)，UNIQUE(site_id,source_generation_id)；注册、存储引用、生命周期与清理互斥 |
| `archive_day_catalog` | 新增 | PK(dataset_id,log_date)；归档权威目录的可重建投影 |
| `archive_tasks` | 新增 | PK(task_id)，UNIQUE(dataset_id,request_key)；补齐/核验/封存任务与恢复参数 |
| `billing_config_observations` | 新增 | PK(observation_id)，INDEX(dataset_id,observed_at)；当时读到的配置和出处 |
| `billing_config_versions` | 新增 | PK(config_version_id)，UNIQUE(dataset_id,version_no)；不可变配置时间线版本 |
| `billing_config_segments` | 新增 | PK(config_version_id,segment_no)；该版内的金额配置有效区间 |
| `billing_jobs` / `billing_job_steps` | 复用扩展 | 保留原主键；增加来源、输入版本、修订及兼容信息 |
| `billing_statement_series` | 新增 | PK(series_id)，UNIQUE(business_key)；同业务账单修订的唯一入口/并发锁 |
| `billing_job_inputs` | 新增 | PK(job_id)；创建占位、固定输入、历史显示信息、版本和快照哈希 |
| `billing_job_archive_days` | 新增 | PK(job_id,log_date)；该账单具体引用的每日版本 |
| `billing_job_daily_totals` | 新增，仅 archive 模式 | PK(job_id,bill_day,dimension_hash)；保留分组/规则/单位维度的结果 |
| `billing_job_artifacts` | 新增 | PK(job_id,artifact_key)；主表/日文件/ZIP 的固定清单与 hash |
| `billing_statement_jobs` / `billing_statements` | 复用 | 原任务主体、已生成账单身份和列表结果 |

原 `site_log_archive_days` 只表示旧版累计状态，键没有 dataset/generation，保留为 legacy 兼容数据；不直接把它变成新版就绪目录。旧 `billing_compact_daily_totals` 和旧文件表保持兼容，原因见第 7 节。

### 3.1 `archive_datasets`：持久归档身份

字段：

```text
dataset_id                 BINARY(16) PK
site_id                    VARCHAR(64) NOT NULL
source_generation_id       BINARY(16) NOT NULL
storage_ref                VARCHAR(128) NOT NULL    -- 部署配置引用，不是 DSN/密码
source_identity_json       JSON NOT NULL            -- 脱敏物理指纹及连续性确认
archive_format_version     INT NOT NULL
lifecycle_state            VARCHAR(24) NOT NULL     -- registering/active/paused/retired/deleting
coverage_from              DATE NULL                -- 经确认的起点，不从 MIN(log_date)猜测
coverage_evidence_json     JSON NULL
config_revision            BIGINT UNSIGNED NOT NULL
observed_catalog_revision  BIGINT UNSIGNED NOT NULL
current_config_version_id  BINARY(16) NULL
created_at, updated_at      DATETIME(6) NOT NULL
UNIQUE(site_id, source_generation_id)
INDEX(site_id, lifecycle_state)
```

同一站点当前采集哪个数据集由 `site_log_archive_control.active_dataset_id` 指向；历史数据集仍可读取。锁此注册行作为账单保留占位、目录维护和清理的 CT 侧互斥入口。

### 3.2 `archive_day_catalog`：页面状态投影

主键 `(dataset_id,log_date)`；`source_generation_id` 随注册关系核验，不允许相同 dataset 指向别代。字段为 `state VARCHAR(24)`、`current_version_id BINARY(16) NULL`、`manifest_hash BINARY(32) NULL`、`catalog_revision BIGINT UNSIGNED`、`mutation_revision BIGINT UNSIGNED`、`all_rows/consume_rows DECIMAL(38,0) NULL`、`consume_quota DECIMAL(38,0) NULL`、`verified_at DATETIME(6) NULL`、`block_reason VARCHAR(64) NULL`、`synced_at DATETIME(6)`。

未知计数为 NULL，核验确认为空才为 0。INDEX(dataset_id,state,log_date)。CT 通过归档只读连接读取同一 catalog_revision 下的完整目录快照后，单事务更新投影和 observed_catalog_revision；不能逐条覆盖后提前宣布整批已同步。正式出账还需读取归档权威 manifest，不能只信这张缓存表。

### 3.3 `archive_tasks`：补齐/核验/封存调度

字段：`task_id BINARY(16)`、`dataset_id/source_generation_id BINARY(16)`、`task_type VARCHAR(24)`、`request_key BINARY(32)`、`request_hash BINARY(32)`、`from_unix/to_unix BIGINT`、`status VARCHAR(24)`、`parameters_json JSON`、`attempt_no INT`、`lease_epoch BIGINT UNSIGNED`、`result_run_id BINARY(16) NULL`、`error_code VARCHAR(64) NULL`、`requested_by VARCHAR(128)`、创建/更新/完成时间。

PRIMARY(task_id)，UNIQUE(dataset_id,request_key)，INDEX(dataset_id,status,created_at)。重复 request_key 必须比较 request_hash；参数不同返回冲突。同一逻辑任务重试增加 attempt_no，目标库的核验运行记录保留每次尝试。

CT 保存调度意图；目标归档库保存权威扫描游标及核验结果，避免 CT 状态上报丢失导致从错误位置继续。

## 4. 归档库：复用原表并增加版本层

| 表 | 处理方式 | 主键/用途 |
| --- | --- | --- |
| `logs` | 复用 | 源同结构模板，不作为账单读取表 |
| `logs_YYYYMM` / `logs_undated` | 复用 | PK(id)，原始字段保持源结构 |
| `archive_log_state` | 复用扩展 | PK(id)，跨月唯一定位/旧贡献；增加 raw_row_hash、last_batch_id |
| `log_daily_stats` / `log_monthly_stats` | 复用 | 观测统计，不是账单金额结果 |
| `archive_dataset_meta` | 新增 | 单行 PK(singleton_id=1)，绑定身份及 writer/catalog 状态 |
| `archive_checkpoints` | 新增 | PK(stream_key)，每条增量/补扫扫描流的权威游标 |
| `archive_batch_receipts` | 新增 | PK(batch_id)，与本批写入一起提交的幂等凭据 |
| `archive_days` | 新增 | PK(log_date)，可变日期状态、revision、当前版本、冻结标记 |
| `archive_reconcile_runs` | 新增 | PK(run_id)，每次核验过程、证据、摘要和结果 |
| `archive_issues` | 新增 | PK(issue_id)，缺行/字段差异/无效日期等隔离证据 |
| `archive_day_versions` | 新增 | PK(day_version_id)，UNIQUE(log_date,version_no)，不可变封存清单 |
| `archive_billing_evidence_YYYYMM` | 新增月表 | PK(evidence_hash)，不可变计费原文字节 |
| `billing_facts_YYYYMM` | 新增月表 | PK(day_version_id,source_log_id)，账单实际读取的事实 |
| `archive_subject_index` | 新增可重建索引 | PK(subject_type,subject_id,parent_user_id)，历史用户/令牌检索 |

月表是按月份路由的物理表，不是第一版 MySQL PARTITION 设计。`YYYYMM` 只能由可信日期生成并检查白名单，不接收用户直接传入表名。

### 4.1 元数据、游标与批次

`archive_dataset_meta` 字段：`singleton_id TINYINT UNSIGNED`、`dataset_id/source_generation_id BINARY(16)`、`site_id VARCHAR(64)`、`format_version INT`、`schema_fingerprint BINARY(32)`、`writer_epoch BIGINT UNSIGNED`、`writer_session BINARY(16) NULL`、`writer_lease_until DATETIME(6) NULL`、`catalog_revision BIGINT UNSIGNED`、`unscoped_blocking_issues BIGINT UNSIGNED`、`updated_at DATETIME(6)`。PRIMARY(singleton_id)，UNIQUE(dataset_id)。业务仅允许 ID=1，启动检查行数和身份；不能仅靠 CHECK 约束这个前提。

`archive_checkpoints` 字段：`stream_key VARCHAR(96) ASCII`、`stream_type VARCHAR(24)`、`task_id BINARY(16) NULL`、`from_unix/to_unix BIGINT NULL`、`after_created_unix BIGINT NULL`、`after_id BIGINT`、`cursor_version INT`、`last_batch_id BINARY(16) NULL`、`updated_at DATETIME(6)`。增量用 after_id，补扫用 `(after_created_unix,after_id)`，两种扫描流分开。

`archive_batch_receipts` 字段：`batch_id BINARY(16)`、`stream_key VARCHAR(96) ASCII`、`payload_hash BINARY(32)`、`writer_epoch BIGINT UNSIGNED`、`cursor_before_json/cursor_after_json JSON`、`row_count/byte_count BIGINT UNSIGNED`、`affected_dates_json JSON`、`committed_at DATETIME(6)`；INDEX(stream_key,committed_at)。相同 batch_id 不同 payload_hash 必须失败，不能 INSERT IGNORE。

一次目标事务提交：月表原始行 + archive_log_state + 日/月统计差额 + 日期 revision/dirty 状态 + checkpoint + receipt。事务失败全部不前进。

### 4.2 `archive_days`：每日期的当前状态

```sql
CREATE TABLE archive_days (
  log_date DATE NOT NULL,
  mutation_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  state VARCHAR(24) CHARACTER SET ascii NOT NULL,
  current_version_id BINARY(16) NULL,
  latest_version_no INT UNSIGNED NOT NULL DEFAULT 0,
  last_reconcile_run_id BINARY(16) NULL,
  freeze_task_id BINARY(16) NULL,
  freeze_epoch BIGINT UNSIGNED NULL,
  freeze_revision BIGINT UNSIGNED NULL,
  freeze_until DATETIME(6) NULL,
  blocking_issue_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  catalog_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (log_date),
  KEY idx_archive_days_state (state, log_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
```

state：`collecting / needs_fill / verifying / dirty / sealed / unknown`；运行错误保存在任务中，不混作“无数据”。current_version_id 可在 dirty 时保留旧值用于追溯，但新正式任务不得选它。

冻结不是只有时间戳：必须同时检查 task、epoch、mutation_revision；过期后的构建必须重新校验才可发布。`logs_undated` 不造一个假 DATE，其未归期收费异常计入 meta.unscoped_blocking_issues。

### 4.3 `archive_reconcile_runs` 与 `archive_issues`

核验表字段：`run_id BINARY(16)`、`task_id BINARY(16)`、`attempt_no INT`、`log_date DATE`、`state VARCHAR(24)`、`method VARCHAR(32)`、`source_schema_hash BINARY(32)`、`source_schema_json JSON`、`codec_version INT`、`target_revision BIGINT UNSIGNED`、`source_cursor_json/target_cursor_json JSON NULL`、`source_rows/target_rows/consume_rows DECIMAL(38,0) NULL`、`source_hash/target_hash BINARY(32) NULL`、`source_summary_json/target_summary_json JSON NULL`、`coverage_evidence_json JSON`、开始/完成时间、error_code。

PRIMARY(run_id)，UNIQUE(task_id,attempt_no,log_date)，INDEX(log_date,finished_at)。方法如稳定期分页或有界源快照要明确；分页中断若摘要状态不可可靠恢复则重新开始同一日期，不能拼接两个快照的摘要。

异常表字段：`issue_id BINARY(16)`、`issue_key BINARY(32)`、`run_id BINARY(16) NULL`、`source_log_id BIGINT NULL`、`affected_day DATE NULL`、`reason_code VARCHAR(64)`、`impact VARCHAR(24)`、`source_hash/target_hash BINARY(32) NULL`、`evidence_payload LONGBLOB NULL`、`evidence_codec INT`、`resolution_state VARCHAR(24)`、`resolution_json JSON NULL`、创建/解决时间。UNIQUE(issue_key)，INDEX(affected_day,resolution_state)，INDEX(source_log_id)。

issue_key 包含 run/日志身份、原因及证据 hash，重复上报幂等；新内容不覆盖旧证据。可变的是处理状态，不是证据。不能归日的消费日志和未知类型的非零 quota 必须有全局阻断影响；纯缺展示单价不自动阻断原始 quota 金额。

### 4.4 `archive_day_versions`：可追溯的日版本

```sql
CREATE TABLE archive_day_versions (
  day_version_id BINARY(16) NOT NULL,
  log_date DATE NOT NULL,
  version_no INT UNSIGNED NOT NULL,
  previous_version_id BINARY(16) NULL,
  build_task_id BINARY(16) NOT NULL,
  state VARCHAR(16) CHARACTER SET ascii NOT NULL,
  verified_mutation_revision BIGINT UNSIGNED NOT NULL,
  reconcile_run_id BINARY(16) NOT NULL,
  parser_version INT UNSIGNED NOT NULL,
  fact_schema_version INT UNSIGNED NOT NULL,
  evidence_codec_version INT UNSIGNED NOT NULL,
  storage_month CHAR(6) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  all_rows DECIMAL(38,0) NULL,
  consume_rows DECIMAL(38,0) NULL,
  consume_quota DECIMAL(38,0) NULL,
  raw_digest BINARY(32) NULL,
  facts_digest BINARY(32) NULL,
  manifest_json JSON NULL,
  manifest_hash BINARY(32) NULL,
  publish_revision BIGINT UNSIGNED NULL,
  created_at DATETIME(6) NOT NULL,
  published_at DATETIME(6) NULL,
  PRIMARY KEY (day_version_id),
  UNIQUE KEY uq_archive_day_version (log_date, version_no),
  UNIQUE KEY uq_archive_day_id (log_date, day_version_id),
  KEY idx_archive_version_build (state, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
```

building 可以分批写入；published 前必须补齐所有计数/摘要/manifest。published 后此行、对应事实和证据禁止原地修改；旧版是否被替代看 archive_days 当前指针，不把旧版内容改成“最新”。building 失败可转 abandoned，再重新构建新版本。

在同库用 `(archive_days.log_date,current_version_id)` → `(archive_day_versions.log_date,day_version_id)` 的复合 RESTRICT 外键，或等价的强制事务校验，防止引用其他日期。本文草案先创建两表，再由正式迁移添加外键，避免循环建表依赖；同库小表最终优先使用外键，不做级联删除。

manifest_json 保存时区、时间范围、核验方法/证据、全部类型分布、原始用量合计、NULL/隔离计数、schema 指纹和解析版本。其 hash 来自固定编码的规范序列，不直接对 MySQL 返回 JSON 的空白/键顺序做 hash。

跨日修正：同时锁旧日和新日（按日期排序），两侧标 dirty；复验构建完成后，在同一目标事务切换两个 current_version_id，赋相同 publish_revision 并推进 meta.catalog_revision。无需另建一个发布批次表。

### 4.5 不可变计费原文

```sql
CREATE TABLE archive_billing_evidence_202609 (
  evidence_hash BINARY(32) NOT NULL,
  codec_version INT UNSIGNED NOT NULL,
  source_schema_hash BINARY(32) NOT NULL,
  payload LONGBLOB NOT NULL,
  payload_bytes BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (evidence_hash)
) ENGINE=InnoDB;
```

payload 是带字段类型和 NULL 标志、长度前缀的计费原文字节结构，包含必要源字段及 other 原文；不采用 JSON 数字浮点解码后再写回。hash 覆盖 codec、schema 及 payload，同 hash 插入时复核内容一致。跨日修正后各月可各存一份，优先保持月度恢复/清理边界简单。

这里不保存请求/响应正文，也不把整个 content 字段追加为账单输入。原始月表继续依现有归档保留规则管理；不可变计费证据保证的是旧版计费重解析，不等于永久保留整条日志全部内部字段。

### 4.6 `billing_facts_YYYYMM`：账单读取的核心明细

```sql
CREATE TABLE billing_facts_202609 (
  day_version_id BINARY(16) NOT NULL,
  source_log_id BIGINT NOT NULL,
  log_date DATE NOT NULL,
  created_unix BIGINT NOT NULL,
  log_type SMALLINT NOT NULL,
  user_id BIGINT NULL,
  token_id BIGINT NULL,
  channel_id BIGINT NULL,
  username_snapshot VARCHAR(255) NULL,
  token_name_snapshot VARCHAR(255) NULL,
  model_name VARCHAR(255) NULL,
  group_name VARCHAR(255) NULL,
  request_id VARCHAR(255) NULL,
  upstream_request_id VARCHAR(255) NULL,
  quota BIGINT NULL,
  source_prompt_tokens BIGINT NULL,
  source_completion_tokens BIGINT NULL,
  input_tokens BIGINT NULL,
  output_tokens BIGINT NULL,
  cache_read_tokens BIGINT NULL,
  cache_write_tokens BIGINT NULL,
  cache_write_5m_tokens BIGINT NULL,
  cache_write_1h_tokens BIGINT NULL,
  image_input_tokens BIGINT NULL,
  image_output_tokens BIGINT NULL,
  audio_input_tokens BIGINT NULL,
  audio_output_tokens BIGINT NULL,
  pricing_snapshot_json JSON NULL,
  usage_context_json JSON NOT NULL,
  evidence_hash BINARY(32) NOT NULL,
  raw_row_hash BINARY(32) NOT NULL,
  fact_hash BINARY(32) NOT NULL,
  parse_state VARCHAR(24) CHARACTER SET ascii NOT NULL,
  issue_codes_json JSON NOT NULL,
  PRIMARY KEY (day_version_id, source_log_id),
  KEY idx_fact_user_cursor (day_version_id, user_id, created_unix, source_log_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
```

首版覆盖全部能归日的 type 2，包括解析失败/零价/零输出，尚不能归日的收费记录进入 issues。NULL 保留“不知道”，不能把缺失输出量变成正常零输出。超过规范字符串长度时进入明确转换错误并保留原文，不静默截断 ID/模型名。

pricing_snapshot_json：model_price、各类 ratio、group_ratio、billing_mode、expr_b64、matched_tier、request_rules、tool_surcharges 等白名单；比率采用十进制字符串保精度。usage_context_json：usage_semantic、usage_billing_path、字段存在性及版本化提取依据。parser_version 随 day_version 固定，不允许同一版本混用解析器。

不在事实表存“按当前单价算好的金额”。它存当时实际 quota 和历史计费证据，金额由任务绑定的配置/算法计算。fact_hash 覆盖规范化字段及 evidence_hash；raw_row_hash 表示采集时完整原始行的摘要，与证据摘要是不同概念。

同一月的 facts.evidence_hash 引用该月 evidence 表；day_version 及日期/月对应关系在发布时全量检查。月表不设置跨 schema 外键；是否添加同库月表外键由恢复/清理演练确定，一期至少强制发布校验及引用保留。

### 4.7 历史身份检索

早期草案为按用户/令牌汇总一行；P4 正式实现改为 `archive_subject_index(subject_type VARCHAR(16), subject_id BIGINT, parent_user_id BIGINT, name_snapshot VARCHAR(255) NULL, first_log_date DATE, last_log_date DATE, day_version_id BINARY(16))`，主键增加 day_version_id，详见本文顶部实现说明。后续检索按 published 版本归并显示，不能直接将 staging 索引作为客户目录。

PRIMARY(subject_type,subject_id,parent_user_id)，INDEX(subject_type,parent_user_id,subject_id)。用户行 parent_user_id=0，令牌行记所属用户；允许同 ID 的历史归属分别保留。该表是可重建索引，名称变化可更新，正式账单名称使用事实/任务输入中的快照，不读取“最新名字”覆盖历史。

## 5. 历史金额配置：观察记录、时间线版本、有效区间

不复用目前按天覆盖写的 `billing_ratio_snapshot` 来证明任意历史生效期。

### 5.1 `billing_config_observations`

字段：`observation_id BINARY(16) PK`、`dataset_id BINARY(16)`、`observed_at DATETIME(6)`、`source_kind VARCHAR(24)`、`source_version VARCHAR(128) NULL`、`values_json JSON`、`values_hash BINARY(32)`、`evidence_ref VARCHAR(512) NULL`、`created_by VARCHAR(128)`。仅追加，INDEX(dataset_id,observed_at)。值包含明确来源的默认值/显式值标记，不存数据库连接凭据。

### 5.2 `billing_config_versions`

字段：`config_version_id BINARY(16) PK`、`dataset_id BINARY(16)`、`version_no INT`、`state VARCHAR(16)`、`previous_version_id BINARY(16) NULL`、`timeline_hash BINARY(32) NULL`、`created_by VARCHAR(128)`、创建/发布时间；UNIQUE(dataset_id,version_no)。

building 完成有效期校验后转 published，之后不改。后续修正产生新时间线版本，旧 job 继续引用旧版。当前指针放在 archive_datasets.current_config_version_id，不能靠更新旧区间结束时间改写已被账单引用的配置。

### 5.3 `billing_config_segments`

```text
config_version_id          BINARY(16) NOT NULL
segment_no                 INT UNSIGNED NOT NULL
effective_from_unix        BIGINT NOT NULL
effective_to_unix          BIGINT NOT NULL
quota_per_unit             DECIMAL(38,12) NOT NULL
unit_code                  VARCHAR(32) NOT NULL
display_symbol             VARCHAR(32) NULL
display_exchange_rate      DECIMAL(38,18) NULL
source_observation_id      BINARY(16) NULL
evidence_kind              VARCHAR(24) NOT NULL    -- observed / verified_history / manual_confirmation
evidence_json              JSON NOT NULL
PRIMARY(config_version_id, segment_no)
UNIQUE(config_version_id, effective_from_unix)
```

每个 published 时间线内区间为 `[from,to)`，不重叠；普通 UNIQUE 无法验证这一点，发布时锁配置版本/数据集目录并检查全部区间。不同时间线可以覆盖同一日期；账单必须选择一个完整版本，不能把两条时间线随意拼接。

有效期只覆盖已有证据或明确确认的有限范围，不因一次读取当前配置自动扩展到全部历史/未来。缺口可以保留，但对应账期不能正式出货币金额。quota_per_unit 必须正值，超精度/范围拒绝而非隐式舍入；原始值另存在 observation，计算不用 FLOAT。DECIMAL 存精确值但仍受显式精度限制。[MySQL 官方说明](https://dev.mysql.com/doc/refman/8.0/en/fixed-point-types.html)

## 6. 账单任务：保留原体系，新增固定输入

### 6.1 现有 `billing_jobs` 增加字段

```text
data_source                VARCHAR(24) NOT NULL DEFAULT 'legacy_live'
source_contract_version    INT NOT NULL DEFAULT 0
input_hash                 BINARY(32) NULL
series_id                  BINARY(16) NULL
revision_no                INT UNSIGNED NULL
supersedes_job_id           VARCHAR(40) NULL
compute_attempt            INT UNSIGNED NOT NULL DEFAULT 0
export_state               VARCHAR(24) NULL
```

保留现有 pricing_source、usage_version、exclude_zero_output、request_key；新增 UNIQUE(series_id,revision_no)，旧记录两字段为 NULL，不强行回填新式版本。

首版建议使用 `archive_preparing / archive_pending / archive_running / archive_publishing` 作为新任务运行态，新 worker 显式领取；完成/失败可复用已有终态。旧领取器只识别旧 pending/running/publishing，新前缀提供额外隔离，但仍要求停旧 worker 后启用新队列，并同步列表/取消/超时/队列容量逻辑。不能仅新增 data_source 后就认为旧 SQL 已安全。

`billing_job_steps` 保留原主键和游标。增加 `day_version_id BINARY(16) NULL`，archive 每步限制在一个相交自然日，最终携带精确起止；确保续跑不跨版本。版本配置可在一天内分段，不以一个 step 只有一个 QuotaPerUnit 为前提。

### 6.2 `billing_statement_series`：普通创建和显式修订

字段：`series_id BINARY(16) PK`、`business_key BINARY(32) UNIQUE`、`canonical_request_json JSON`、`latest_revision INT`、`latest_job_id VARCHAR(40) NULL`、`created_at/updated_at DATETIME(6)`。

business_key 由站点/数据集代际、主体、精确账期及明确的业务计费/过滤语义生成，不含日版本、模板或重试次数。普通生成遇到同键返回已有任务；只有修订入口在锁定 series 行后分配新 revision。input_hash 描述本次实际数据/算法输入，不替代业务查重。相同业务键时还核对规范参数，避免摘要误用。

新 request_key 由 series/business_key 与 revision 派生，继续使用既有唯一索引。切源后普通创建还需兼容查询同站点/主体/账期/业务参数的 legacy 账单，不能因旧记录没有 series 或 dataset 就放行重复正式单据；已存在时返回旧单或经明确关联创建修订。修订原因及操作者持久化在任务审计/输入 manifest。

### 6.3 `billing_job_inputs`：创建保护及最终输入

字段：

```text
job_id                     VARCHAR(40) PK
dataset_id                 BINARY(16) NOT NULL
source_generation_id       BINARY(16) NOT NULL
input_state                VARCHAR(16) NOT NULL     -- reserving/frozen/cancelled
retention_state            VARCHAR(16) NOT NULL     -- reserved/pinned/released
catalog_revision           BIGINT UNSIGNED NULL
config_version_id          BINARY(16) NULL
parser_policy_json         JSON NULL
pricing_engine_version     VARCHAR(64) NULL
filter_version             VARCHAR(64) NULL
subject_snapshot_json      JSON NULL
model_metadata_snapshot    JSON NULL
template_snapshot_json     JSON NULL                -- 首版系统模板 ID/版本/渲染版本
input_manifest_json        JSON NULL
input_hash                 BINARY(32) NULL
created_at                 DATETIME(6) NOT NULL
frozen_at                  DATETIME(6) NULL
INDEX(dataset_id, retention_state, job_id)
```

先创建 reserving 行形成数据集级保留占位，再去归档库选版；frozen 后输入字段不可改，retention_state 单独控制保留状态。manifest 保存精确账期/过滤参数、日引用清单哈希、模型元数据及来源、配置与算法版本；既有 job 顶层重复字段必须与之匹配。

job_id 指向 billing_jobs，使用 RESTRICT，不能新加 ON DELETE CASCADE。现有删除失败任务/已完成账单的代码必须对 archive 改为明确的取消、作废或受控保留处理，不能无声删除输入证据/释放引用。

### 6.4 `billing_job_archive_days`：账单对应哪一版日志

```sql
CREATE TABLE billing_job_archive_days (
  job_id VARCHAR(40) NOT NULL,
  log_date DATE NOT NULL,
  dataset_id BINARY(16) NOT NULL,
  source_generation_id BINARY(16) NOT NULL,
  day_version_id BINARY(16) NOT NULL,
  storage_month CHAR(6) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  manifest_hash BINARY(32) NOT NULL,
  selected_from_unix BIGINT NOT NULL,
  selected_to_unix BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (job_id, log_date),
  KEY idx_job_archive_version (dataset_id, source_generation_id, day_version_id, job_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

同一账单只有一个数据集代际，冗余的 dataset/generation 必须与 inputs 一致；一个日期只能固定一个版本。跨库没有外键，服务端在冻结输入时验证身份、日期、storage_month、manifest hash 和版本可用状态；同库 job FK 在正式迁移按现有 job_id 的字符集/排序规则一致地添加 RESTRICT。

## 7. 账单结果和文件

### 7.1 新增 `billing_job_daily_totals`，旧 compact 表保留

现 `billing_compact_daily_totals` 的键为 `(job_id,bill_day,user_id,token_id,channel_id,model_name)`，没有分组、历史计价规则、单位配置片段；直接沿用可能把同模型不同规则压成一行。为避免修改旧主键及旧任务语义，新 archive 任务使用独立结果表，查询层适配。

主要字段：

```text
job_id VARCHAR(40), bill_day DATE, dimension_hash BINARY(32)
user_id BIGINT, token_id BIGINT NULL, channel_id BIGINT NULL
model_name VARCHAR(255), group_name VARCHAR(255) NULL
config_version_id BINARY(16), config_segment_no INT
pricing_rule_hash BINARY(32), disposition VARCHAR(24)
dimension_json JSON
request_count, logged_quota, source_prompt_tokens, source_completion_tokens DECIMAL(38,0)
input/output/cache_read/cache_write/cache_5m/cache_1h/image_input/image_output/audio_input/audio_output totals DECIMAL(38,0)
usage_missing_json JSON
amount_exact_json JSON      -- numerator/denominator 十进制字符串，算法版本定义
amount_display DECIMAL(38,12)
updated_at DATETIME(6)
PRIMARY(job_id, bill_day, dimension_hash)
INDEX(job_id, user_id, bill_day)
```

dimension_hash 由完整规范维度计算，NULL 有独立标记，复用哈希时核对 dimension_json；disposition 为纳入/排除/隔离类别。此表聚合的是同一 job 输入，不取代逐请求归档。缺失用量数量单独记录，不能仅 SUM 忽略 NULL 后声称用量完整。

精确金额存有理数分子分母，汇总先精确求和再统一输出；DECIMAL 显示列不作为后续累加的唯一来源。数值长度/精度超业务预算时明确失败，不默默截断。采用不同单位的金额不直接相加，必须按已固定换算规则处理或分开呈现。

逐页写入需要事务幂等：锁 job/step，核对 input_hash、compute_attempt 和 cursor_before，在同一 CT 事务内提交结果汇总增量、计数和 cursor_after；已提交游标不再累加。写 spool 的暂存路径/清单绑定 job、compute_attempt 与页范围，提交响应丢失按持久游标及页面摘要恢复，不能盲目追加两次。

正常失败续跑保持 attempt 和输入。确需从头重算但尚未生成 ready 文件时，在独占 job 锁内递增 compute_attempt，原子清理该任务未发布汇总并重置全部 step 游标；旧 worker 的 attempt 不匹配即拒写，旧 spool 分代隔离回收。已经产生 ready 文件或已发布账单后不进行同 job 全量重算，使用显式修订任务；单独的文件失败重试只读取既有计算结果。

既有 statement 表继续存已生成账单身份，legacy 继续旧 compact/旧文件接口。新结果的主表、日明细、令牌汇总统一从 archive 任务读取，不通过旧全站 active 指针替换历史单据。

### 7.2 `billing_job_artifacts`：完整文件清单

字段：`job_id VARCHAR(40)`、`artifact_key VARCHAR(96) ASCII`、`artifact_type VARCHAR(24)`、`audience VARCHAR(16)`、`bill_day DATE NULL`、`part_no INT`、`state VARCHAR(16)`、`storage_key VARCHAR(512) NULL`、`byte_size BIGINT UNSIGNED NULL`、`sha256 BINARY(32) NULL`、`input_hash BINARY(32)`、`render_version VARCHAR(64)`、`error_code VARCHAR(64) NULL`、创建/完成时间。

PRIMARY(job_id,artifact_key)，INDEX(job_id,state)。先登记预期成员，生成完填尺寸/hash；全部 ready 且校验成功才将 export_state 置 ready。文件缺失不能略过 ZIP 成员。客户包/internal 包分别记录 audience 并按白名单和权限过滤。

已经 ready 的文件和路径不覆盖；计算/模板输入相同的失败生成可重试。首版重下载读固定产物；以后允许同一计算换模板多次导出时，再引入独立 export_id，不通过重复创建业务账单实现。

## 8. 关键事务与关联示例

### 8.1 归档写入

目标事务按固定顺序锁 meta/epoch → 涉及的日期（排序）→ ID 状态；核验冻结/身份 → 原始行与贡献差额 → 日 revision/dirty → checkpoint/receipt → COMMIT。改变可选版本、日期可用状态或全局阻断时，在同事务递增 meta.catalog_revision，并更新相关日目录版本。遇到冻结不能推进游标越过该行。月表创建/结构迁移在数据事务外完成并验证，不能把 DDL 放进本批写事务。[MySQL DDL 隐式提交说明](https://dev.mysql.com/doc/refman/8.0/en/implicit-commit.html)

### 8.2 账单创建（无跨库事务假设）

1. CT 锁 archive_datasets/series，确认非 deleting，保存 archive_preparing job 与 inputs(reserving/reserved)。
2. 在归档库同一只读一致性事务读取 meta.catalog_revision、全局阻断数、日期目录和已发布版本，验证整个账期；这是选择版本的时间点。
3. CT 单事务保存 inputs、day refs、配置/模板快照与 input_hash，状态改为 frozen/pinned 和 archive_pending 后才允许领取。
4. 此后补日志不改变所选版本；新修订另建 job。失败恢复同一个 preparing/reserving 任务，显式取消才按保留规则释放，不因超时直接清理。

### 8.3 两天账期的实际关联

```text
账单 job_A / revision 1
  inputs: dataset D1, generation G1, catalog 18, config C3
  day refs:
    2026-09-01 → V101（第 1 版）→ billing_facts_202609
    2026-09-02 → V102（第 1 版）→ billing_facts_202609

发现一条日志实际应从 9 月 1 日移到 9 月 2 日
  同一发布事务：catalog 19
    2026-09-01 → V201（第 2 版）
    2026-09-02 → V202（第 2 版）

旧账单 job_A 继续引用 V101/V102，金额与文件不变
显式修订 job_B / revision 2 引用 V201/V202，并关联 job_A
```

## 9. 新旧表迁移与一期规模控制

- 新建控制/目录/版本表，原始月表复用；旧源 ID 空间不原地混合。旧归档经过身份、字段、覆盖证据核验后才能构建首个日版本。
- 旧 CT 日状态、旧账单、旧 compact 和旧下载继续兼容；不把旧累计行数自动转换成完整性证据，不重算已交付旧账单。
- 一期 facts 每日通常只有一版；只有修订才再存一版该日事实，未修订日期不复制整月。evidence 同月内容摘要去重，避免每份用户账单复制原始日志。
- 每份账单只新增小型 inputs/day refs、结果汇总和产物目录；逐请求事实在归档库由多个账单共享，临时 spool 不当永久证据。
- 归档库的用户/模型多维预聚合可后置；一期保留已有日/月观测统计，加用户游标索引即可开始流式出账，避免首版建通用数仓。
- 不能为减少表数把动态状态、不可变证据和任务结果混在一张大 JSON 表中。这里拆表对应的是不同生命周期与事务边界，不要求一次上线全部能力。

## 10. 实施时必须核验的约束

1. 同库外键按实际旧表字符集/类型添加 RESTRICT，跨库只有显式验证；新版引用不允许随旧 job 删除级联消失。
2. 不变性由专用权限、写路径和发布状态共同保证；表名/PRIMARY KEY 本身不能阻止 UPDATE。旧写账号必须隔离，管理员手工改表属于受控恢复操作。
3. 所有核心索引避免长文本组合索引；事实首选版本+用户+时间+ID。金额/多媒体规则不建 JSON 全字段索引，按真实查询计划再补令牌/渠道索引。
4. 核验 MySQL 实际版本、InnoDB、严格 SQL 模式、字符集、索引限制及最大包；本文不使用只适用于新版本的 JSON 默认表达式/函数索引。5.7 兼容不能靠 CHECK 声称保证业务约束。[MySQL 5.7 ALTER TABLE 说明](https://dev.mysql.com/doc/refman/5.7/en/alter-table.html?source=post_page---------------------------)
5. Schema 版本演进、building 恢复、checkpoint 原子性、冻结/跨日发布、配置区间、删单/清理并发必须实库验证。本轮仅设计，无执行 DDL、迁移、数据库性能或账单生成验证。

## 11. 本轮核对来源

- [旧归档站点控制](../server/migrations/072_log_archive_sites.sql)、[旧每日记录](../server/migrations/073_log_archive_days.sql)、[原始月表/贡献/统计](../agent/internal/logarchive/monthly.go)。
- [账单任务及步骤](../server/migrations/028_billing_jobs_and_anomalies.sql)、[任务幂等键](../server/migrations/032_billing_job_idempotency.sql)、[compact 汇总](../server/migrations/055_billing_compact_daily_totals.sql)、[statement 结果](../server/migrations/057_billing_statements.sql)、[计费来源与多媒体列](../server/migrations/080_billing_source_and_multimedia.sql)。
- [现有任务创建/失败重建](../server/internal/mysqlstore/billing_statements.go)、[任务领取](../server/internal/mysqlstore/billing_jobs.go)、[现有账单字段](../server/internal/billing/jobs.go)。
