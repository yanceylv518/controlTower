# Codex 任务：v2.7-B4——稀疏曲线符号只标孤立点（修"曲线变珠串"）

用户部署 rc7 后反馈 TTFT/缓存命中率"曲线都变成点了"。诊断（已实证 ECharts 6.0.0 的 connectNulls 正常画线）：B3 的 sparse 模式给**所有**数据点开了 `showSymbol`,而繁忙维度这两条序列实际上每分钟都有值——密集符号连成珠串,视觉上盖住了线。设计初衷只是让"孤立点不隐形"。

**清单进 commit;禁止 force push;Linux 跑全量测试。**

## 工作项（单文件:`TrendChart.vue`）

sparse 模式改为**逐点决定符号**:

1. 数据映射为 ECharts 对象形式:`{ value: [time, v], symbol: isolated ? 'circle' : 'none', symbolSize: 5 }`——`isolated` = 该点非空且左右相邻点均为空(或不存在);
2. 序列级 `showSymbol: true`(让逐点 symbol 生效)、`connectNulls: true` 保持;连续段只显示线,孤立分钟显示圆点;
3. 悬停行为保留(emphasis 时任何点都应可见,确认默认行为即可);
4. 密集序列(非 sparse)零改动。

## 验证要求

1. `pnpm build`、`pnpm test` 绿;
2. 手工三种数据形态截图:全密集(纯线,无珠串)、全稀疏(点+跨隙连线)、混合(密段线+孤立点圆点);
3. 交付说明附截图。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 孤立点判定含首尾边界(第一个/最后一个点只看单侧邻居)
- [ ] 三种数据形态截图齐
- [ ] 非 sparse 序列渲染零变化
- [ ] 一个 commit:`fix(web): show symbols only on isolated sparse points (v2.7-B4)`
