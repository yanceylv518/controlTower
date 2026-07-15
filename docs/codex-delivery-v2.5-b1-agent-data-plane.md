# v2.5-B1 Agent 数据面交付说明

日期：2026-07-16

## 交付结果

- Agent 从 `logs.other.frt` 独立解析流式首响应毫秒值，非法、非正数和超过 1 小时的值按缺失处理。
- 1m 聚合新增精确 P50/P95/P99、大输入缓存命中分子/分母、流式 TTFT count/sum/P95；原始分位数数组每桶最多保留 10000 条。
- 新增 `CT_CACHE_HIT_MIN_PROMPT_TOKENS`，默认 512，只有 `consume` 且 `prompt_tokens > 512` 的请求进入缓存命中率统计。
- 010 迁移为 1m/5m 指标增加可空数据面列，为渠道快照增加 `group_name`、`priority`；历史数据不回填。
- 5m 只累加可加和字段，精确分位数保持 NULL；读侧为空时使用直方图桶内插值，并在页面注明 5m 为近似值。
- 维度页新增缓存命中率与 TTFT 指标卡、趋势图；渠道详情展示分组和优先级。
- API 与上报契约均为 additive；旧 Agent 缺失字段时写入 NULL，新 Agent 向旧 Server 上报时未知 JSON 字段可被忽略。

## 部署顺序

必须先升级 Control Tower Server（执行 010 迁移），确认服务健康后，再升级各 new-api 主机上的 Agent。该顺序保证新 Agent 上报的新字段已有存储位置。

## 验证记录

- `go test ./...`：通过。
- `pnpm typecheck`：通过。
- `pnpm build`：通过（仅有既存的大 chunk 提示）。
- `pnpm test`：通过（当前脚本执行 Vue 类型检查）。
- 自动化覆盖：512/513 边界、精确分位数、TTFT 仅流式、frt 防御、10000 条原始值上限、旧 payload NULL 兼容、010 迁移字段与数据库属性。

本批未修改 new-api，也未读取请求体或密钥。渠道 `group`/`priority` 已解锁后续供应商分组和 severe 调权规则，但本批不实现对应规则。

## 自查清单

- [x] 部署顺序声明：先 Server 后 Agent；契约只增不改。
- [x] 512 阈值边界、frt 防御、仅流式聚合有单测。
- [x] 5m 精确分位数列 NULL，读侧回退桶内插值，页面注明近似。
- [x] 快照 `group` 使用反引号并与 `priority` 一起 COALESCE，渠道页已展示。
- [x] 010 钉 ENGINE/CHARSET/COLLATE。
