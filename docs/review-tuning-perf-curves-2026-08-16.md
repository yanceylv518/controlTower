# 验收记录：调权性能治理+因子曲线可配+缓存口径统一（2026-08-16）

范围：41cbe63..2f4ce7a 共 10 笔（codex 交付,rc47-55 期间用户自行打 tag 部署迭代,本记录为补验收）。验收环境：Linux,vet+全量三树测试、直连真库集成、vue-tsc、desktop build 全绿。

## 结论：通过,零缺陷;三条 P3 记档

## 性能治理(实地部署暴露的问题)

- **DB 驱动级超时（41cbe63）**：mysqlstore.Open 统一注入 connect 5s/read 30s/write 30s（显式 DSN 值优先）——治"单条坏连接卡死唯一调权循环";
- **桶查询站点级批量化（5f790d4）**：旧 foldErrorDecay 每渠道一条查询,且谓词按 `SUBSTRING_INDEX(dimension_key)` 计算渠道号无法走索引定位——每渠道重复整段范围扫描,N 渠道 N 倍;新批量 SQL 单次扫全窗口按渠道分组,**FORCE INDEX(idx_metric_1m_bucket_dimension) 已核实 001 迁移即存在**;批量失败回落逐渠道路径（接口断言+契约测试在）;每渠道排序保持 newest-first 契约,fold 的游标跳过逻辑保证批量超集不重复折叠;
- **off 模型整体跳过（a74c5f7）**：全 off 站点不再拉指标/状态;分阶段耗时日志（653d4f1/8c15f1c）覆盖 policy/base/metrics/states/桶批量/渠道循环/状态落库(≥1s 才记)/直连写。

## 因子曲线可配（0baae23+2f4ce7a）

18 个新策略参数：三性能因子各自的指数/上下限、错误分段四率三系数、综合倍率上下限——**默认值全部等于原硬编码常量,默认行为严格等价**;校验强制节点递增/系数递减/区间含 1;旧策略 JSON 兼容读回默认;v1 遗留 KError 逆映射改用策略曲线（升级首轮时存量策略必为默认曲线,正确性成立）。设置页新"评估系数曲线设置"折叠区带公式对照(αs/Ls/Us 记号),悬浮解释与计算权重说明全部参数化;评估停滞(≥3min)红字与刷新失败透出。

## 缓存口径统一（b4311fe+0dd8353）

缓存 Token 比率三处统一为"**仅输入 >512 Token 的 consume 请求**:缓存读取 Token÷提示 Token"——agent 聚合器（**需升 agent 生效**）、server 端回落聚合、监控页 CacheHitRate 改用同一比率;调权缓存因子与监控页数字从此同源,解释文案注明口径。agent 测试钉比率边界。

## 记档（P3）

1. **otps_cap 加入死参数行列**（引擎改用 otps_max_factor,设置页输入已删,模型/校验/defaults 仍在）——与 min_write_interval/write_deadband 同批清理;
2. **server 端回落聚合器硬编码 512**,agent 侧用可配阈值（cacheHitMinPromptTokens）——若设置页改了缓存边界,两路径口径分叉;回落路径仅服务不预聚合的旧 agent,影响面小;
3. **30s 读超时封顶所有 CT 库查询**：长查询从"挂着"变"报错"（多数场景是改善);留意保留清理/大表迁移类操作,显式 DSN 参数可放宽。行为记档:off 模型的状态行冻结(含残留暂停标签),切回 observe/auto 首轮自愈,显示层"模型已关闭"优先无误导。

## 部署

无迁移。缓存新口径需 agent 升级;生产已在 rc47-55 线上迭代,当前生产版本以用户告知为准。

## 跟进（同日,用户发令三 P3 一并修,验收方实施）

1. **三死参数退役**：otps_cap/write_deadband_percent/min_write_interval_minutes 从策略结构/默认值/校验/前端类型与 defaults 全部移除;observe 事件锚点粒度改固定常量 5%（注释记载);已存策略里的遗留键由 JSON 解码天然忽略（新增容忍测试:带遗留键的旧策略解码后校验零错误）。
2. **缓存阈值单一事实源**：新建共享包 `internal/cachemetrics.MinPromptTokens=512`，agent 聚合与 server 回落聚合均直接使用该常量；退役 agent 独有的 `CT_CACHE_HIT_MIN_PROMPT_TOKENS` 覆盖，彻底消除两条路径的部署配置分叉（无缓存告警阈值仍独立可配）。
3. **30s 超时暴露面收口**：迁移改走 OpenForMigrations 专用连接（仅连接超时,无读写死线——长 ALTER 不再会被 30s 掐断）,用完即关;**保留清理 12 表 DELETE 全部改 LIMIT 20000 分批循环**——原无界 DELETE 在大积压下会 30s 超时→回滚→下小时更大积压,永久卡死;分批每条必然有界；行为测试覆盖多批累计、整批边界、执行失败保留进度及 RowsAffected 失败。

全量三树+真库集成+typecheck+build 绿。
