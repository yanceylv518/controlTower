# 账单生成与源库压力复查（2026-09-30）

## 范围与结论

沿用户账单、上游账单、补齐调度、日报生成、币种快照、分页及错误计数读取链路复查。保留先前月账单复用日快照、同站点分页串行/间隔、5 分钟索引检查缓存、优先发布完整日的改动；本轮补充遗漏项，没有改源库表结构/索引、费用计算或账单分组。

## 修复内容

1. 分页原 SQL 在同一查询层取日志宽字段、执行 JSON 价格投影、连接渠道并排序限页。现在通过含 LIMIT 的派生表先选 id/created_at，再按主键读取本页完整记录及渠道名，外层保持 created_at/id 顺序。保留用户/渠道集合/令牌筛选、时间半开区间、NULL token=0 语义和游标。此优化限制宽字段处理范围，不声称消除了索引扫描或 filesort。
2. 报表末尾 type=5 失败请求 COUNT 原先绕过共享读取槽，且没有自己的 SQL 超时；现在纳入同站点账单/报表日志读取预算，SQL 使用 readonlyQueryTimeout，取消等待时不执行查询。失败仍显示未知，不能冒充 0。
3. 报表源库生成加入既有索引前置检查；没有 created_at 前导索引时停止，而非直接扫描。复用已有 5 分钟检查缓存，不给客户源库自动添加索引。
4. 报表源库短页（小于 BillingPageSize）处理后立即结束，省掉一次尾部空页查询及等待；archive 不使用此规则，避免分片短页丢数据。
5. captureBillingMoney 优先调用只读货币配置的 MoneyOptionsSnapshot，排除 ModelRatio/CompletionRatio/CacheRatio/CreateCacheRatio/GroupRatio 配置；兼容旧测试/实现的 RatioSnapshot 回退。
6. 同一次 Fill 内多日作业复用同一份实际观察到的币种快照，保持历史失败作业原快照优先；没有跨批次长期缓存，下一次 Fill 仍可观察新配置。
7. 入队前增加 CT 数据库容量预检查（最多探测 5 条 pending），满队列不再先读取源库 options；入队事务继续在锁内做权威容量检查，预检查不替代并发保护。
8. 调度先查 CT 的缺失日/月，有待办才查自动用户角色；完成或排队目标的空唤醒不再查询源库元数据。需要创建任务时仍检查管理员角色。

## 验证

- Go dashboard/mysqlstore/billing/httpapi/server main 全包回归通过；最后调度调整后相关专项和既有自动任务测试再次通过。
- 新测试：满队列零次 options 读取；一次补齐三天仅观察一次币种；空目标零次角色/货币读取；货币 SQL 不带模型倍率配置；失败计数遵守忙槽/取消/查询超时；缺索引不读订单；报表短页一次读取完成。
- 真 MySQL：在本地 CT 测试库建立唯一命名的 logs/channels 测试表并清理，覆盖同秒多记录、多页、用户/单渠道/渠道集合、NULL 令牌 0、日期边界及历史计费证据保留；没有写真实源 logs/channels。最初临时表方案因 MySQL 禁止同一查询二次引用临时表而不能测试该 JOIN，随后改为唯一命名的隔离普通测试表，测试通过。
- 对 inst-demo-a 执行 EXPLAIN FORMAT=JSON（未用 ANALYZE），并做修改前后各 17 条的受限样本读取，规范化结果完全一致。旧计划 logs range 使用 idx_logs_user_type_time_quota，估计 355 行；新计划内部范围估计仍 355，派生页估计 17，随后 logs/channels 均 eq_ref，证明宽记录读取移至限页之后。仍有排序，不能据此推断生产提速比例。
- 本地新后端 local/runtime/control-tower-server-source-pressure.exe，PID 28996，原 control_tower_test/18081/API-only 配置保持。healthz 正常，原六月账单明细 355 条、首屏价格齐全，月令牌页金额 93.504544、价格集合及隐藏令牌 ID 正常。日志 local/runtime/server-source-pressure.out.log 和 server-source-pressure.err.log。

## 尚存边界

- 用户账单、上游账单及报表仍分别生成；同一批源请求可能被不同任务重复读。要大幅进一步减少读取量，需要持久化一次原始读取供各业务独立计算，或让这些任务消费已经核验的归档数据；本轮没有改变数据来源或共用结算结果。
- 共享槽按单个 Server 进程内的逻辑站点生效；多 Server 副本，或不同站点配置指向同一物理数据库，不共享限流预算。
- 缺少匹配筛选/排序的复合索引时，仍可能在日期范围内扫描、排序较多索引记录；没有强制站点特定索引或改源表。
- 没有执行生产大账单压测，未比较 RDS CPU/IO/扫描行数峰值，不报告百分比收益。本地行为/执行计划验证不能替代生产负载验收。
- 主工作区包含此前多个任务改动，本轮未提交/推送/发布；交付时需精确选择修改。

## 继续优化：保持筛选与结算等价（同日追加）

用户强调优化必须保证正确性。检查跨用户账单、上游账单、报表共用原始日志方案后，本轮没有引入缓存：源数据修正、强制重新生成、配置变化和不完整读取的失效机制需要独立设计；不能为了减少查询而改变数据来源或沿用过期源日志。

本轮落地两个限定范围的改动：

1. 上游 `processStep` 仍从 CT 的 `billing_statement_channels` 读取作业冻结绑定。新增可选 `UpstreamModelPageSource`，只读源实现把渠道对应的模型筛选放入有 LIMIT 的派生页。渠道白名单始终保留；NULL 规范化为空字符串，以 UTF-8 字节比较精确保持 Go `==` 的大小写/空格语义，所有值均参数化。未限制模型的渠道仍读取所有模型，未绑定渠道不能由模型 map 扩大范围。模型和渠道分支规模超过 1,000 时回退原渠道查询，Go 端原筛选始终保留。归档来源没有实现这个接口，继续原分片读取和本地筛选。
2. 用户/令牌/单渠道/多渠道/全站的源库分页统一把显式时间下界设为 `max(start, cursor.CreatedUnix)`；原时间上界与 `(created_at > cursor.time OR created_at = cursor.time AND id > cursor.id)` 保持。不能把下界改成下一秒，否则同秒请求会漏账。此改动为优化器给出更窄的显式范围，当前本地 MySQL 已能从原 OR 条件推导相同范围，因此该样本没有新增扫描行数收益。

### 对照验证

- Runner 用 4,105 条多页记录分别执行原渠道读取、新筛选读取和回退模式，包含多个历史单价、渠道折扣生效边界、明确零原价、空输出异常，以及从同秒 ID 2,000 继续读取。新旧逐条 RequestDetail（包括单价、结算与金额）完全一致，计费及异常计数一致。完整读取：原 3 页/4,105 条，新 1 页/1,290 条，计费明细 968 条、异常 322 条；续读：原 2 页/2,105 条，新 1 页/662 条，计费 497 条、异常 165 条。这是构造场景中返回明细行数的减少，不是生产源库扫描行数或 CPU 的测量。
- `TestBillingModelFilterMySQLEquivalence` 在 CT 本地测试库创建唯一命名隔离表，使用不区分大小写的 utf8mb4_general_ci，187 条夹具覆盖大小写、尾空格、组合 Unicode、中文、NULL/空字符串、SQL 特殊字符、重复模型、未绑定渠道、渠道名称缺失、坏 JSON、NULL 用量/令牌、超过 2^53 的 ID、乱序 ID/同秒时间以及半开时间边界。旧查询读后 Go 筛选与新查询分页结果完整字段 DeepEqual。混合绑定范围原读取 101 条，新 49 条；严格绑定新 21 条；无模型限制和大绑定回退结果保持。建表/插入/删除仅发生在隔离表，未写源库 logs/channels。
- 原 `TestBillingNarrowPageMySQL` 改用新范围参数并通过，全站、用户、单/多渠道、NULL 令牌分页继续验证。
- 本地 inst-demo-a 仅读 EXPLAIN FORMAT=JSON，加首批/续读各新旧 17 条抽样，共四次有上限查询，标准化字段完全一致。初始范围估计 355、续读 339，新旧相同；限页宽记录读取仍为主键 eq_ref，filesort 仍存在。
- `go test ./server/internal/dashboard ./server/internal/billing -count=1` 通过；mysqlstore/httpapi/server main 三包回归和 server build 通过。新增 MySQL 用例必须显式设置 CT_MYSQL_TEST_DSN，默认测试会跳过；此次已设置并实际通过。
- 本地 18081 已更新为 `local/runtime/control-tower-server-source-filters.exe`，PID 28660，沿用 control_tower_test 和 API-only。启动前示例站点无活动账单，启动后 healthz 正常。重启前后全部 355 条日明细和月统计/每日模型/每日令牌三个视图的规范化 SHA256 相同：`5d699d89c7f852138373d9ac09b65dd1f6587b650538f8021c96a96c710ea2a3`。金额 93.504544、单价完整、令牌 ID 隐藏均保持。没有重生成、改写旧账单。日志 `local/runtime/server-source-filters.out.log` / `server-source-filters.err.log`。

继续保留前述限制：源库实际压力收益需生产观察；绑定全部模型的渠道不会减少返回行数；不同业务仍独立读取源日志，单进程/逻辑站点限流边界未改变。本轮未提交、推送或发布。
