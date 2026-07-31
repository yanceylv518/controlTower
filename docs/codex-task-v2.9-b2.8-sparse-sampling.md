# Codex 任务：v2.9-B2.8——低流量渠道稀疏采样规则

背景：用户补充规则（2026-07-31）——窗口内请求过少的渠道不应因样本不足被跳过，改用"最近 10 条请求"统计。引擎数据源为 metric_1m 分钟桶聚合（无逐条请求），实现为**计数式回看窗口**：评估窗口样本不足时向前逐桶累计至 ≥ sparse_min_samples 条请求，按这些桶的合计算错误率。此规则同时修复两个已记档盲区：低流量渠道永不被评估、低流量渠道试岗留任判定永不达成。**依赖：1b0ec0a（B2.7）已合入。**

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck`。**

## 设计

### 参数（scheduling 段新增，页面同步）

- `sparse_min_samples`：默认 10（1~min_samples 间校验，必须 ≤ min_samples）；
- `sparse_lookback_minutes`：默认 360（≥ window_minutes，≤2880）。

### 引擎规则

评估在岗渠道（及试岗中渠道）时：

1. 窗口内 `request_count >= min_samples` → 现行路径不变；
2. 不足时启用稀疏统计：新 store 方法 `QueryRecentChannelBuckets(instance, channel, lookback, limit)` 按 bucket_time 倒序取该渠道近 lookback 分钟的桶（request_count>0），从最新往回累计到 ≥ sparse_min_samples 条即止；对选中桶合计 request/error/user_error 得出归因错误率；
3. **新鲜度守卫**：选中桶中最新一桶必须落在本评估窗口内（即窗口内有新请求），否则跳过——同一批旧请求不得在无新信息时反复驱动判定；
4. 凑不齐 sparse_min_samples → 跳过（与现行为一致）；
5. **适用范围**：错误率判定（degrade/severe，evidence 标注 `sparse:true` 与实际样本数、跨越时段）与**试岗留任/再降判定**；**延迟判定与动态配权不适用**（P95 跨桶合并是噪声、配权需组内可比新鲜指标），稀疏路径下 latencyWindows 一律清零；
6. 防抖语义不变（sustained_windows 照常，稀疏窗口逐次都需过新鲜度守卫）。

### 页面

- 调度参数区加两个字段（带说明："低流量渠道回退用最近 N 条请求统计，仅作用于错误率与恢复验证"）；
- 降级标准说明卡补一条稀疏规则说明；建议流水 evidence 展示带"稀疏统计"标注。

## 接线点（逐个核对，不得遗漏）

两参数 schema+校验+兼容（旧 JSON 无字段取默认）、QueryRecentChannelBuckets（mysqlstore+memory/fake store）、evaluateActive 稀疏分支（含试岗留任路径）、新鲜度守卫、evidence 标注、页面两字段+说明卡、api-contracts 若有字段示例则更新、测试同步。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck`；新增测试：窗口足量走原路径、不足回看累计触发 degrade、新鲜度守卫拦截无新请求、凑不齐跳过、试岗中渠道靠稀疏统计留任/再降、延迟判定在稀疏路径不触发、参数校验（sparse_min_samples ≤ min_samples）。
2. 手工：造低流量渠道（窗口 <20 条但近几小时累计 ≥10 条且含坏请求）→ 建议流水出现带 sparse 标注的降级建议；停流量 → 下窗口不再重复触发。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 足量渠道行为与 B2.7 版本逐项一致（回归）
- [ ] 新鲜度守卫有测试；无新请求不重复触发
- [ ] 稀疏路径不影响延迟判定与动态配权
- [ ] 试岗留任在低流量渠道可达成（盲区闭合测试）
- [ ] 一个 commit：`feat(server,web): sparse count-based sampling for low-traffic channels (v2.9-B2.8)`

## 明确不做

逐条请求级统计（数据源为分钟桶聚合）；稀疏路径用于延迟判定/动态配权；log_samples 兜底读取（桶数据够用）。
