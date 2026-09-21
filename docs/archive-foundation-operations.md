# 归档基础准备与验收（P0/P1）

本地实现日期：2026-09-20。本文描述 P0/P1 准备流程，注册后默认暂停。后续 P2 已实现目标事务游标与单写者，见 [P2 操作说明](archive-p2-operations.md)；P3 增加日期补齐与覆盖观察接口，见 [P3 操作说明](archive-p3-operations.md)。出账和日版本能力仍为 false，日期补齐/覆盖页面操作待 P7 联调。

## 两库实际落地范围

- CT：`server/migrations/085_archive_foundation.sql` 新建 `archive_datasets`、`archive_day_catalog`、`archive_tasks`，扩展站点控制及 Agent 能力字段。任务表此阶段没有执行器。
- 归档：Agent 内嵌 001–005 独立迁移，新建 `archive_dataset_meta`、`archive_checkpoints`、`archive_batch_receipts`、`archive_day_versions`、`archive_days`，以及迁移校验账本 `archive_schema_migrations`。
- 证据/月度事实、核验运行、异常明细、历史身份索引、历史金额配置及账单引用仍待后续批次。预建日版本表不代表具备封存能力。
- CT Server 启动只迁移 CT；不会替远程归档库执行 DDL。旧累计记录继续保留在旧表，绑定新版数据集后不会显示为新版日期覆盖。

## 1. 准备目标身份

每个归档 schema 只对应一个站点和源代际。`dataset_id`、`source_generation_id` 采用非零、32 位小写十六进制 ID；由部署人员生成并保存，不从 URL、库名或本地 checkpoint 推导。源身份指纹为源 MySQL server UUID 与数据库名的规范编码 SHA-256；另有源列结构 SHA-256。

地址变化且源身份不变可保持绑定。实际物理身份变化会阻断，必须核验连续性；本批没有自动换绑或强行接受新指纹的入口。禁止通过重新生成 ID 来掩盖不明确的来源。

数据库恢复、重建或日志 ID 空间重置必须由运维确认代际；物理指纹相同不能单独证明业务连续性。此阶段不实现 CDC 或自动识别所有历史重写。

准备前暂停站点归档，等当前批次/租约结束，停止所有旧 Agent 和本地归档进程。旧二进制不认识新版元数据防护，因此切换必须撤销旧账号写权限或使用隔离的新写账号。该操作需要在实际部署时执行，本次开发没有操作生产账号。

Agent 沿用受保护配置中的源只读连接和归档写连接，增加：

```text
CT_LOG_ARCHIVE_ENABLED=true
CT_LOG_ARCHIVE_MANAGED=true
CT_LOG_ARCHIVE_SITE_ID=<实际站点ID>
CT_LOG_ARCHIVE_DATASET_ID=<32位ID>
CT_LOG_ARCHIVE_GENERATION_ID=<32位ID>
```

保持现有 `CT_SERVER_URL`、实例/Agent 身份及 token 配置；不在命令行粘贴 DSN 或 token。显式执行 `control-tower-agent -config <受保护配置路径> -archive-prepare`，仅准备后退出，正常启动不会自动迁移。

真正空的目标库可由显式准备命令创建经过校验的 `logs` 模板，不复制记录；该操作核验源/目标实际数据库不同，并在独占准备锁下再次检查空库。非空目标必须已具有匹配源的 InnoDB `logs` 模板；额外唯一键、生成列、外键、分区或外部表选项等不支持的模板拒绝自动复制，不能把其他业务库当归档目标。

准备结果输出身份、格式版本和指纹，不输出连接信息。checkpoint、目录及 writer epoch 初始为零，不继承旧日累计作为完整性证据。迁移按独立 DDL 步骤记录 checksum，重复执行/DDL 成功但回执未写入可以恢复；未知迁移、checksum/结构冲突拒绝继续。

## 2. 配置 Server 专用只读连接

创建独立 MySQL 账号，直接授予归档 schema 内以下三张表的 SELECT：`archive_dataset_meta`、`archive_days`、`archive_day_versions`。本批目录读取不需要原始 `logs*`、证据表或写权限。

不接受全局/整库 SELECT、DML、DDL、角色授权或 GRANT OPTION。还检查 `CURRENT_ROLE()` 为 NONE，避免有效角色绕过检查。后续 reader 扩展允许的表仅限迁移账本、历史身份索引和规范命名的事实月表。

部署文件只保存引用与环境变量名，例如：

```json
{"archive-site-a":{"dsn_env":"CT_ARCHIVE_SITE_A_READONLY_DSN"}}
```

用 `CT_ARCHIVE_READONLY_CONNECTIONS_FILE` 指向此文件，DSN 通过对应受保护环境变量注入。API 只接收 `storage_ref`，不接收 DSN；连接失败返回固定错误码。每次目录读取使用一个只读、可重复读事务，核验唯一身份行、两项指纹、格式、目录 revision 和封存版本 revision，再提交完整快照。

当前实库测试为本机 MySQL 9.7。未验证生产版本、5.7/8.x/MariaDB；不应把本次通过视为跨版本兼容承诺。无法确认角色/权限的服务器拒绝连接。

## 3. 注册与同步接口

需已有登录会话及 `archive.manage`，写请求仍需现有 CSRF 头。接口目前供部署/联调调用，页面尚无注册向导。

`POST /api/dashboard/archive-datasets` 的 JSON：

```json
{
  "site_id":"<实际站点ID>",
  "dataset_id":"<准备结果中的dataset_id>",
  "source_generation_id":"<准备结果中的source_generation_id>",
  "storage_ref":"archive-site-a",
  "archive_format_version":2,
  "schema_fingerprint":"<准备结果中的64位指纹>",
  "source_fingerprint":"<准备结果中的64位指纹>"
}
```

注册前由 Server 从配置的只读连接实查目标；只有站点已暂停且旧租约到期才绑定并审计。相同注册可重试，身份、存储引用或指纹变化返回冲突。注册成功仍为 paused；旧 Agent 不获得新版租约。P2 Agent 上报基础与原子 writer 能力、预检状态，在管理员明确启用且配置版本确认后才获得写入授权。

- `GET /api/dashboard/archive-datasets/{dataset}?site_id=...`：读取注册与目录同步状态。
- `POST /api/dashboard/archive-datasets/{dataset}/sync?site_id=...`：Server 从登记目标读取整份目录，并在 CT 单事务替换投影；忽略旧 revision，重复 revision 必须 hash 相同。不使用请求体或旧 Agent 上报的日期数据。

日计数 NULL 与确认零条不同。目录投影不是正式出账授权；即使未来出现 sealed，仍需 P4/P5/P6 验证原始证据、固定日版本和金额配置。

## 验证入口与下一步

`go vet ./...`、`go test ./...` 为代码质量门。`CT_MYSQL_TEST_DSN` 驱动 CT 集成测试，只能指向专用测试库；`CT_ARCHIVE_TEST_DSN` 驱动目标迁移/只读 reader 集成测试，需本机测试管理员权限，测试自行生成随机 schema/账号并清理。不得把生产管理员连接用于这些测试。

测试覆盖身份错误、租约互斥、迁移重跑/中断、未来版本/篡改 checksum、NULL/大整数、整批目录原子回滚、旧 Agent 隔离、预检错误可见、权限及凭据不出现在错误响应。

P2 已接入原子写入和 writer epoch；本文中的“基础能力”和默认暂停不表示已启动写入，具体运行前提与旧数据接入限制见 [P2 操作说明](archive-p2-operations.md)。真实源规模/索引、日志清理与迟到窗口、备份恢复、网络和历史金额单位证据仍需环境验收。
