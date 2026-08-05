# 验收记录：账单缓存语义对齐 new-api（2026-08-05）

范围：c998218（codex 自主交付，无批次文件）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 全绿。

## 结论：通过，验收修正一处 P1 风险（形态守卫恢复）

## 交付内容核验

金额口径重构：Usage 各泳道归一化——PromptTokens 恒为非缓存输入，缓存读/写在源头（logs.other 解析）按语义分流，Amount 不再做减法。新增缓存写计价：`cache_write_price`（5 分钟档），1 小时档 = 5m 价 × 1.6（8/5 硬编码，与 Anthropic 定价 1.25×/2× 输入价之比一致）。034 迁移给六张账单表加缓存写三列 + prices/anomaly 加写价列（纯增量，靠 1060 容错重放，与仓库迁移模式一致）。

- **request_key 升 v4-cache-semantics**：口径切换强制旧账单失效重生成——旧存量行（prompt 含缓存的老语义）不会被新 Amount 误算，正确的失效方式。**升级部署后已生成账单需再各重生成一次（继 v3 之后第二次）**；
- 逐位钉死的 $0.005828 断言测试原样保留且通过；新增缓存写计价与 1h 倍率测试、别名字段不双计测试；
- 语义检测：`other.usage_semantic=="anthropic"` 或 `other.claude==true` → anthropic（prompt 已不含缓存，不减）；否则 openai（prompt 含缓存读写，减去归入缓存泳道）。判档 ContextTokens 同步分流（anthropic=prompt+读+写，openai=原始 prompt）——与"判档含缓存"决策一致；
- **搭车改动（与账单无关，记档）**：agent 上报合约新增 `channel_snapshot_complete`（omitempty 加法字段），server 收到 true 时走新 `SyncChannelSnapshots` 全量同步 channel_current（清掉已删渠道的幽灵行）；**旧 agent（rc22）不发该字段 → false → 原逐行插入路径不变，向后兼容核验过**。要吃到幽灵行清理需升级 agent，不升级无损。agent 侧渠道采集加 limit+1 超限报错守卫。
- **搭车改动之二（初次验收漏记，2026-08-05 用户问询后补录）**：调权中心（ContinuousTuningView）把渠道×模型基础值列表挂进既有 30s 运行时轮询——渠道列表/权重变化免手动刷新即可跟上；带脏数据保护（正在编辑未保存的行不被轮询覆盖），模型下拉与 dispatch_modes 随列表补齐。与上一条合成"调权中心渠道列表定时检测"完整功能：**新增/变更检测旧 agent 即可用（channel_current 逐行上插本就在跑）；删除检测需 agent 升 rc29（仅新 agent 声明全量，server 才敢删行）**。

## 验收修正（P1 风险，我直接修）

**无标记 anthropic 形态会被清零输入泳道**。新代码只认显式标记；但用户钉死的生产实例（prompt=298 非缓存、cache=8507）正是"prompt 已不含缓存"的形态——若生产 other JSON 无 usage_semantic/claude 标记（旧版 new-api 完全可能），按 openai 减法会把 298 减成 0，输入项系统性消失。旧代码的形态守卫（cache>prompt 则不减）这次被删。而 **OpenAI 语义下缓存必为 prompt 子集、不可能超过 prompt**，故守卫是零代价纯保护：恢复为 `resolveBillingCacheSemantic`（读+写 > prompt 且未标记 → 按 anthropic 处理），应用于三处解析点（聚合/明细/分页含 ContextTokens），回归测试用 298/8507 形态钉死。

**守卫覆盖不到的残余**：anthropic 语义 + 缓存 ≤ prompt + 无标记的行（小缓存读的 claude 请求）仍会被误减。~~部署后校准必做~~ **→ 同日已用生产样本闭环**：用户提供真实 other JSON——`usage_semantic:"anthropic"` 与 `claude:true` 双标记确实存在，语义检测主路径成立；样本内 `cache_creation_ratio:1.25` / `cache_creation_ratio_1h:2` 之比 = 1.6，硬编码倍率与 newapi 实际定价严格一致；解析器逐字段核对正确（read=43268、write=2382 取 max 不双计、全量落 1h 档、负数哨兵忽略），样本已钉成回归测试。残余缩小为：**当前 new-api 版本升级之前产生的历史日志**若无标记且缓存 ≤ prompt 仍会误减——回填生成历史月份账单时留意，形态守卫兜住其中缓存 > prompt 的部分。另一稳健性质经样本推演确认：未识别键名的 5m 写入落 remainingWrite 按 5m 价（即默认价）计，不会错价——只有 1h 检测依赖键名，且 1h 键名已被样本证实。

## 记档

- `cache_write_price` 默认 0：anthropic 缓存写在配价前计 0 元——与旧行为一致（写从未计过价），无追溯惊吓；要对 claude 模型计缓存写费需在计价页配置（导入建议是否覆盖该列未核，配价时留意）；
- 1h=1.6×5m 为硬编码定值（与 λ/0.8/1.08 同类不开放）；
- 流程：无批次文件自主交付、账单与渠道快照两件事混在一个 commit，自查清单继续缺席；
- 前端：账单/渠道账单/计价/调权页加缓存写列与写价编辑，typecheck+build 绿（渲染未逐页点验，账单重生成后应抽验一页）。

## 部署

- 034 纯增量迁移；server 镜像替换即可；**agent 可选升级**（仅为渠道幽灵行清理，账单功能不依赖）；
- **rc28 不含本批**：账单缓存语义要上线需重打 rc29；
- 部署后顺序：重生成账单 → 抽两用户对账（重点：claude 渠道行的输入/缓存泳道拆分与 quota 对照差额）→ other JSON 标记抽样校准（上述残余风险闭环）。
