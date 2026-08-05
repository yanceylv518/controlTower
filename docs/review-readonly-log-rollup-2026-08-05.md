# 验收记录：使用日志查询优化——小时聚合缓存（2026-08-05）

范围：4eb6efd（codex 自主交付，无批次文件）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 全绿。

## 结论：通过，验收修正三处（两处缺陷 + 一处测试缺口）

## 交付内容核验

架构：新增 `readonly_log_stats_hourly` 小时聚合表 + `readonly_log_rollup_cursors` 游标表（033 迁移，纯增量、幂等），后台 runner 每 30s 按站点从 newapi logs 表以 id 游标增量拉取（每轮 ≤10 批 × 5000 行），按 (站点,小时,类型,用户,渠道,模型,令牌,分组) 聚合累加进 CT 自库。查询侧：

- **列表去掉 COUNT(\*)**：/passthrough/logs 不再每页对生产库做全量计数（本批最大的减负点），改多取一行判 `has_more`；
- **精确总数拆到新端点 /logs/count**：完整小时段读 CT 聚合表，头尾零头小时回落生产库小范围原始查询；聚合未就绪（覆盖不足/滞后>2min/带 request_id 筛选)时整段回落原始 COUNT；前端异步加载，显示"总数统计中…/暂不可用"，不阻塞列表；
- **/logs/stat 的 quota 汇总同样走聚合+零头拼接**；RPM/TPM 仍查生产库最近 60s（廉价）；
- 就绪判据正确：`coverage_from` 之前的区间不用聚合；`caught_up_at` 超过 2 分钟视为滞后回落原始查询——不会拿半新不旧的数据充当全量。

逐项核验过的正确性要点：

- **ApplyReadonlyLogRollups 事务性幂等**：游标行 FOR UPDATE + `current >= lastLogID` 短路，聚合累加与游标推进同事务提交——重试/并发不会双计；
- 维度哈希 = SHA-256(站点+小时+全维度 \x00 拼接)，主键唯一，聚合正确性有单测（跨小时/跨类型分桶）；
- 小时桶按 UTC 截断，上海时区为整小时偏移，与账单/日志页的半开区间语义兼容；`completeHourWindow` 有单测；
- 聚合表筛选与列表的精确匹配语义一致（username/model/token/group/channel_id/type 均等值）；
- /logs/count 已进 viewer 白名单，viewer scope（user_ids IN）在聚合路径与原始路径都强制注入，无越权面；
- mux 经类型断言接线 Rollups，mysqlstore 实现齐全；runner 随 server 启动，复用站点已配置的 logs_readonly_dsn，无新配置项。

## 验收修正（我直接修）

- **P1 分页 limit+1 张冠李戴**：`limit+1` 误加在 Users 处理器（且未截断——用户管理页每页多显一行、跨页首尾重复），而依赖它的 Logs 处理器却没加，导致 `has_more` 恒为 false——一旦 /logs/count 失败，前端兜底 total=当前页行数，翻页按钮失效。已对调：Users 恢复 `limit`（其仍有 COUNT total），Logs 改 `limit+1`。
- **P2 游标追表头会永久丢行**：MySQL 自增 id 的可见顺序≠提交顺序；runner 追到活表头时，若低 id 行尚未提交而高 id 行已读走，游标一旦越过，该行永久漏计（统计永久少计且无法自愈）。修复：每轮先取"created_at 早于 now-10s 的最大 id"作安全水位，批量查询加 `id<=水位` 上界，年轻行留给下一轮。补契约测试（fake source/store：首轮止步水位、晚到行次轮补齐）。残余假设：单条日志插入事务耗时不超过 10s——newapi 自动提交场景成立。
- **测试缺口**：/logs/count 进白名单但越权矩阵没跟（/logs/stat 当时的同款遗漏），补 GET 200 / POST 403 两例。

### 跟进（同日，回应 codex 对修正的评审）

codex 指出契约测试的 fake 用"取最大 id"建模安全水位，与真实 SQL（`ORDER BY created_at DESC,id DESC LIMIT 1` = 取最新 created_at 那行的 id）语义不同，测不出 SQL 排序写错的回归。已补强：① fake 改为精确镜像真实 SQL 语义；② 新增 id/created_at 倒挂场景测试（倒挂行暂留水位之上，新 settled 行抬升水位后补齐——延迟可接受、丢失不可接受）；③ 按 mysqlstore/*_contract_test.go 既有模式加源码文本 SQL 契约测试，锁死两条查询的排序与边界写法。倒挂下真实行为核验：水位取"最新 created_at 行的 id"意味着更高 id 但更旧 created_at 的行会晚一轮同步，不会丢；极端情况（倒挂行之后站点长期无新日志）该行会一直悬着，属理论边角，记档不处理。

后续复核推翻了上述“理论边角可接受”的判断：quiet site 会持续刷新 `caught_up_at`，使查询端在倒挂行未入聚合时仍使用本地统计，形成静默少计。现已拆分首次回填游标与运行期水位：首次游标取覆盖窗口内最小 id 的前一位（空窗口取当前表头）；运行期先捕获最大可见 id，等待 10 秒沉淀后仅处理到该上界，使较低 id 的短事务有时间提交，同时不依赖 created_at 顺序。倒挂且无后续日志的测试要求同一轮追平。

**上述 codex 修复（1cd6574）已复验通过，零缺陷**：① 快照头+沉淀窗与 created_at 顺序彻底解耦——id≤快照头的行都在快照时刻前分配了 id，10s 内提交必可见，残余假设仍仅"单条插入事务≤10s"，且 quiet-site 悬置洞关闭（上界是 MAX(id)，倒挂行同轮即收）；② 初始游标 SQL 的 NULL 传染路径核验过（窗口空时 GREATEST(NULL,0)=NULL → COALESCE 落表头，不扫历史；窗口非空时窗口内任何行都 ≥ MIN(id) 不可能漏）；③ MAX(id) 走主键，比原 created_at 排序查询更便宜；④ fake/倒挂测试/SQL 契约同步更新，SettleDelay=-1 测试哨兵与默认 10s 接线正确；gofmt/vet/全量测试绿，webapp 未动。新记档 P3：沉淀 sleep 按站点串行，每个有新数据的站点每轮 +10s——两站点约 50s/轮仍远低于 2min 就绪阈值，但站点扩到 4-5 个都活跃时会逼近阈值使统计回落原始路径，届时改并发 syncSite 或持久化快照头免 sleep。窗口外 id 交错的老行会被聚合成 coverage_from 之前的死桶——ready 门挡住不会被读，无害记档。

## 行为变更与记档

- **日志页 username 筛选对 viewer 开放**（原 admin 专属，含账单深链的 username 参数）：服务端 scope 仍强制 IN(user_ids)，viewer 筛自己范围外的用户名只会得到空结果，无泄露；属 UX 放开，记档。
- **列表 total 语义变更**：/passthrough/logs 的 total 不再是精确总数（= offset+本页行数），精确值由 /logs/count 提供；已核对 api-contracts.md 未收录 passthrough 端点，无需更新。
- **超时 15s→120s**（列表/统计/count 共用一个常量）：与"少慢 SQL 打生产库"原则有张力，靠连接池上限兜底（HTTP 侧每站点 2 连接 + runner 独立 Handler 再 2 连接，合计 4）。可接受，但若生产观察到 count 回落路径拖 120s 满，建议把列表与 count 的超时拆开。
- **统计新鲜度**：聚合路径下 quota 汇总/总数最多滞后约 2.5 分钟（30s 同步周期 + 2min 就绪阈值 + 10s 安全水位）；列表始终实时，两者可能有秒级出入。
- **覆盖窗口只从部署时点回补 7 天**、向前只增：部署初期整月区间查询仍整段回落生产库原始 COUNT/SUM（约 23 天后整月查询才能全走聚合）。回补速度 ≤10 万行/分钟/站点，大表首次追平需数小时，期间 count 均走原始路径。
- P3：区间端点语义不一致——聚合路径头尾零头用半开 `[start,end)`，原始回落路径与列表仍是 BETWEEN 闭区间，end 整秒上的行两条路径相差 1 行。与 83ac4d3 的半开统一方向不符，建议下次顺手统一。
- P3：runner 在 main.go 另建了一个 PassthroughHandler（独立连接池），与 HTTP 侧不共享——多一份连接配额，行为正确，仅记档。
- 流程记档：无批次文件的自主交付，自查清单继续缺席。

## 部署

- 033 迁移纯增量（两张新表），无销毁性操作；无新配置项、agent 不动。
- **rc27（5ba84d1）不含本批**：需要上线时在验收修正后的 main 重打 rc28。
- 部署后观察：`readonly_log_rollup_cursors` 的 last_error/caught_up_at（追平前 count 走原始路径属预期）；EXPLAIN 确认 `logs` 上 `created_at` 索引支撑水位查询（与既有 stat 查询同型）。
