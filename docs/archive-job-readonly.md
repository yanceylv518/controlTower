# 新版归档库只读查询

本功能提供归档用量统计、历史价格展示和异常分析所需读取接口及页面，不包含正式归档账单导出。Server 只读归档库，完全不访问 new-api 源库。Agent 离线时，只要归档库可达仍可查询。

## 配置

### 页面配置（优先方式）

升级Server/Web并完成CT库097迁移后，在「日志归档 → 设置 → 归档库连接」填写地址、端口、数据库名、专用只读账号和密码。TLS默认启用，校验证书与主机名（信任Server系统证书链，不提供跳过验证模式）。Server须已设置稳定的CT_SECRET_KEY；更换密钥会使原密码无法解密。

先测试连接，查看返回的source_hash并核对数据库属于当前站点，勾选确认后保存。保存时再次连接校验，之后读取接口立即优先使用页面配置，不依赖Agent在线。修改连接参数使本次测试失效；密码留空沿用已保存密码；接口不回传密码或密文。已保存站点身份不允许通过修改表单自动改绑另一个库。配置使用版本号防止并发覆盖。

CT库新增archive_read_connections保存公开配置、加密密码、配置版本和更新人；归档库无新增迁移。测试不写归档库，也不保存草稿到CT，页面显示的最近测试是最近成功测试时间，不代表实时健康状态。

接口（均要求archive.manage）：GET/PUT `/api/dashboard/log-archive-connection?site_id=...`，POST `/api/dashboard/log-archive-connection/test?site_id=...`。密码请求字段password；CSRF、操作审计沿用系统机制，审计屏蔽密码。

未保存页面连接时回退以下文件配置。已保存配置损坏或解密失败时直接报错，不静默回退另一份连接。CT表未迁移时也会报错，需要先完成Server迁移。

### 文件配置（兼容方式）

Server 现有环境变量 `CT_ARCHIVE_READONLY_CONNECTIONS_FILE` 指向部署方维护的 JSON 文件。新增 `job:<site_id>` 条目，与旧数据集的 storage_ref 条目可以共存；`site_id` 必须是 CT 实际站点 ID，不是随意填写的显示名称。

```json
{
  "job:actual-site-id": {
    "dsn_env": "CT_ARCHIVE_SITE_A_DSN",
    "source_hash": "从对应归档库读取的64位小写十六进制值"
  }
}
```

从已确认对应站点的归档库查询身份：

```sql
SELECT schema_version, source_hash
FROM log_archive_meta WHERE singleton_id = 1;
```

`schema_version` 必须为 1。部署者确认站点和库的对应关系后，将 source_hash 固定到配置；Server 每次读取再次校验，不自动信任目标库返回的新身份。

DSN 放在 Server 进程的 `CT_ARCHIVE_SITE_A_DSN` 环境变量中；使用专用 MySQL 只读账号。配置文件只包含环境变量名和身份 hash，不写密码。Compose 部署需要实际传入这些环境变量，并将 JSON 文件只读挂载到容器；仅修改宿主机文件不会自动进入容器。网络、TLS 和账号来源地址按实际部署设置，不开放公网数据库端口。

账号可授予目标归档数据库的 SELECT（兼容 RDS 库级只读），或仅授予以下表的 SELECT：

- log_archive_meta
- log_archive_days
- log_archive_day_versions
- log_archive_daily_stats
- 需要查询的 logs_YYYYMM 月表

权限按能力分类：允许 SELECT、SHOW VIEW、LOCK TABLES、PROCESS、REPLICATION SLAVE/CLIENT、XA_RECOVER_ADMIN、USAGE，不按数据库/表名限制这些只读或观察类授权。拒绝 INSERT/UPDATE/DELETE、DDL、EXECUTE、FILE、SUPER、授权/角色及未识别的管理权限。使用表级授权时，新月份月表需补 SELECT；使用目标库级 SELECT 时无需逐月授权。Agent 写入账号不要复用。旧读取器和新读取器账号应独立，避免各自表白名单校验冲突。只读查询不修改归档库结构；页面配置需要CT库097迁移。

## API

需要登录且拥有 `archive.manage` 权限；viewer 不可访问，接口不接受 DSN、数据库名或表名。

```text
GET /api/dashboard/log-archive-read/days?site_id=actual-site-id&date=2026-07
GET /api/dashboard/log-archive-read/stats?site_id=actual-site-id&date=2026-07-04&limit=100
GET /api/dashboard/log-archive-read/logs?site_id=actual-site-id&date=2026-07-04&category=empty_output&limit=100
GET /api/dashboard/log-archive-read/anomalies?site_id=actual-site-id&date=2026-07-04
```

- days：整月（最多31天）状态、封存版本、revision、raw_rows、步骤和错误。无记录日期不补0。
- stats：单日当前已封存版本的原始多维汇总，包含所有日志类型。消费统计需要调用方明确筛选 dimensions.type=2。dimensions/amounts 为 JSON 字符串，计数和quota不经过浮点转换。仅版本、日期、revision匹配已发布记录时读取。
- logs：北京时间单日原始日志固定字段（ID、时间、类型、用户、渠道、模型、输入/输出Token、quota、content摘要）。不读取 arbitrary columns、完整other、API Key等。content本身可能包含源系统记录的敏感错误信息，仅归档管理员可见，不写入服务端错误日志。最多4096字符，content_truncated 明确标记截断，不将摘要称作完整原文。
- stats/logs支持 user_id、model（精确匹配）、channel_id；不支持的日期状态筛选会返回400。
- 日志 category：empty_output = type2且输出0；missing_output = type2且输出NULL；error = type5。空输出不等同业务失败。不传category返回当日所有日志类型。
- anomalies：同一只读事务内按北京时间自然日汇总当前归档月表，返回最多24个小时桶（hour为0–23）。字段 log_rows、consumption、empty_output、missing_output、error、charged_empty_output 均为十进制字符串；最后一项为type2输出0且quota>0的记录数。支持user_id/model/channel_id，不接受category、版本或日志游标；与明细分页独立，不受limit截断，has_more始终false。空桶可视为本次归档快照中零条，但不能推断源库该时段没有请求。时间索引、查询超时、只读授权和站点身份校验与logs相同。

返回 `items`、`has_more`；stats额外返回 `version`。items数值字段保留十进制字符串，SQL NULL保留null，避免JavaScript整数精度损失。

分页：stats下一页传回 version 和本页最后 group_hash 作为 after_hash；logs下一页传最后 created_at/id 作为 after_time/after_id。每页默认100，最大200。只有 has_more=true 才继续；筛选或日期变化须清空游标。统计版本变化返回409，需从首页重新读取，不能拼接不同版本。

原始月表读的是当前数据，不承诺跨页快照；采集/修复期间可能变化，正式账单不能直接以该日志分页结果认定已封存证据。

## 查询保护和故障

全部使用只读事务、参数绑定及固定白名单。请求最多10秒，数据查询 MAX_EXECUTION_TIME 为3秒。日志月表必须存在以created_at开头的索引，否则拒绝扫描；不替用户建索引。返回数据总字段字节约2MiB封页、单行1MiB上限（JSON编码开销另计），不跳过超大行。统计阶段没有自动缓存和全量多日聚合，后续统计接口应复用该基础并增加版本缓存。

- 400 archive_invalid_query：日期、游标、筛选或分页不合法。
- 409 archive_identity_or_schema_mismatch：站点绑定hash或schema不匹配。
- 409 archive_sealed_version_unavailable：未封存、发布记录不一致或分页版本变化。
- 503 archive_readonly_permissions_required：账号含非只读/未支持管理能力，或权限核验失败。
- 503 archive_read_time_index_required：月表缺少时间索引（也应先确认目标月表存在）。
- 503 archive_read_row_too_large：单行超过读取预算。
- 503 archive_readonly_unavailable：配置、连接或查询失败；不回退源库，不将异常伪装为空结果。

本轮未配置生产连接或部署。真实上线时需验证上述身份、授权和代表性日期查询。

## 统计和异常页面

用量统计按月枚举已封存日期并携带版本读取日汇总；消费口径type=2，BigInt累计，不完整读取不展示合计。金额通过当前站点只读 options 获取币种、QuotaPerUnit 与汇率：quota / QuotaPerUnit × 汇率；TOKENS 保留原始额度。配置失败不回退美元。CSV包含筛选、版本清单及币种/单位/汇率/读取时间；当前配置不等于历史汇率或正式账单。

模型历史价格按模型及完整pricing证据分组，保留不同分组倍率/折扣、表达式和观测首末日期。参考输入价格为model_ratio×1000000/站点QuotaPerUnit再乘当前汇率，输出和缓存读取再乘对应倍率；model_price按当前汇率显示每次参考价。TOKENS显示原始额度参考价。参考价不再乘分组倍率或用户折扣，不能用于重算已汇总quota。零价保留，缺失显示未知，表达式不执行也不推导Token单价；没有查询当前源库价格配置。

异常分析保留按日分页明细，新增独立当日汇总与24小时趋势，分别显示空输出、输出NULL和type5错误。空输出占比的分母仅为type2消费日志数，分母为零显示未知；不提供混合三类记录的“失败率”。更换筛选、站点或重试时丢弃旧响应；明细翻页不重复汇总。查询失败明确提示，不能把失败当零数据。两个页面均要求Server新版只读接口与归档连接就绪；本次无需新增归档库迁移，仍需CT097。

连接测试区分 archive_tls_failed、archive_auth_failed、archive_network_failed、archive_database_missing；仅返回固定分类，不回传驱动错误、密码或DSN。未分类失败仍显示通用提示。

RDS只读模板兼容：截图中的全局观察权限、目标库SELECT/LOCK TABLES/SHOW VIEW及mysql系统表SELECT均纳入回归。额外只读授权不扩大应用查询范围；归档SQL仍为固定表和绑定站点身份，旧版账单reader继续使用原表级限制。CURRENT_ROLE需为NONE，尚不解析角色有效权限。

月表渠道字段按实际结构探测，优先channel_id、其次channel；两列同时存在时用COALESCE，与统计维度筛选一致，响应统一为channel。无渠道字段时显示NULL；带渠道筛选则返回archive_channel_column_missing，不静默忽略筛选。

Agent 按源 logs 的 SHOW CREATE TABLE 克隆月表，并原样保留列和索引；Server 不假设月表采用固定渠道列名。可选 content 缺失时明细预览为NULL。必需列不匹配、缺表、查询权限与超时分别返回 archive_read_schema_mismatch、archive_read_table_missing、archive_read_access_denied、archive_read_timeout；不将全部查询错误归为连接失败。
