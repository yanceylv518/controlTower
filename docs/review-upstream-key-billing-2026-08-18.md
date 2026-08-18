# 验收记录：上游渠道 key 账单（2026-08-18）

范围：7bba0cc（codex 按批次 codex-task-upstream-key-billing.md 交付,自查清单已贴）。验收环境：Linux,vet+全量三树测试、真库集成（045 经 ApplyDir 双重放建表核实）、vue-tsc、desktop build 全绿。

## 结论：通过,零缺陷;两条 P3 记档

## 交付面对批次逐项核（新流程首单）

六项全在规格内,无越权扩展：①045 单语句 CREATE+契约测试;②passthrough 库内 SHA2/RIGHT 指纹（较批次多 COALESCE 空值守卫,合理加固）;③列表/详情/CSV 三接口（同 handler 按路径分流）;④树形表格默认展开+三字段搜索+列序按对账主次+未归组桶折叠;⑤测试五类齐;⑥"明确不做"清单零僭越。

## 核验

- **key 红线成立**：SQL 常量中 key 仅现于 SHA2/RIGHT;契约测试手法=移除两个允许形态后断言零裸 key;CT 表/结构体/响应仅含 fp+尾4位;
- 合并正确性：channel 聚合扫描把 channel_id 落在 AggregateRow.UserID（既有约定）,BuildUpstreamGroups 同字段取值一致;组/成员双层累计同源行;明细 Merge 按 日×模型×分组×档位 键并保留 5m/1h 写分解;未归组（fp=''）恒排末位;
- 读闸：绑 channel_generate 最新 complete 任务,409 语义与渠道账单页一致（同 billingJobForRead）;admin 角色闸+viewer 由 auth 白名单天然隔离;
- 快照语义：整批 upsert 不删旧行——已删渠道留最后映射（专项测试在）;刷新失败降级用既有快照仅记日志;
- CSV 两段（日×模型+成员小计）、BOM、"普通输入Token"列名与 v6 泳道语义（prompt=非缓存）一致。

## 记档（P3）

1. 每次打开上游列表都全量刷新映射（channels 全表扫+逐行 upsert）——渠道量级下无碍,渠道数上千时可加节流;
2. 映射快照有 base_url 但 key 为空的边角显示名退化为"…"（空尾）;multi-key 渠道整包哈希不拆（批次记档项,注释在)。

## 部署

迁移 045 增量。**rc62 不含本批,含本批需 rc63**;部署后打开渠道账单页→"按上游 key"视角,首次加载即建快照;对账口径=请求数/tokens 为主、quota 参考。
