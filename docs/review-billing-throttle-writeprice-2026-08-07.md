# 验收记录：缓存写价快照对齐 + 账单扫描限速（2026-08-07）

范围：895feaa + 6916e18（codex 自主交付，server-only）。验收环境：Linux，Go 全量 vet+test 绿；无前端改动。

## 结论：通过，零缺陷

## 895feaa 未配置模型的缓存写价全线归零

把 48185bd 的显式计价政策（用户 2026-08-06 拍板）延伸到 CT 已配价路径：当天 ratio 快照中该模型无 CreateCacheRatio → **连 CT 计价表存的 cache_write_price 也压 0**（summary/details/invoice 三处，双向测试钉住）。动机：1.25 默认价时代导入的 CT 价格行携带隐式写价，需按新政策中和。快照缺失/解析失败时保守不动原价。

- **政策后果（明示记档）**：CT 从此**不能独立对缓存写计费**——newapi 未配 CreateCacheRatio 的模型，即使管理员在 CT 计价页显式填了写价也按 0 计。要收缓存写费的唯一路径 = 在 newapi 配 CreateCacheRatio。与用户"不发明价格"的拍板方向一致，如需 CT 独立写价能力需明示推翻本条。
- P3：计价页展示的写价可能非零而账单实际按 0 计（明细行如实显示 0，但计价页与账单存在观感差）——下次动计价页时加"未随 newapi 配置生效"标注。

## 6916e18 账单扫描限速 + other 字段投影

- **页间歇**：生成任务逐 5000 行页扫 newapi logs，页间 sleep `CT_BILLING_PAGE_PAUSE_MILLISECONDS`（默认 500ms，ctx 感知可中断；env 样例/compose 已带）。生成时长换算：百万行 ≈ 200 页 ≈ +100s，换生产库负载平滑，划算且可调；
- **other 投影**：分页查询不再整块拖 other JSON，SQL 端 JSON_OBJECT 只投影计费字段。**与 parseBillingCacheUsage 的 13 个键名逐一核对无遗漏**（读 4 别名/写 3 别名/5m·1h 各 2/usage_semantic+claude），JSON_VALID 兜底非法值，缺键 null 被解析器正确忽略；投影契约测试在（含反向断言排除 request_path/stream_status/admin_info 大字段）；
- 配置项进 Keys() 注册表 + 默认值测试 + 契约测试齐全——本笔交付自带测试，好转记档。

## 部署

- 新 env 可选项 `CT_BILLING_PAGE_PAUSE_MILLISECONDS`（不配默认 500）；
- **rc32 不含 2026-08-06 晚间以来三笔（80c22b7/895feaa/6916e18 及我的预检测试）**——下次 rc33 收齐；账单 request_key 已在 v6，本两笔不再升版（写价归零属报表层现算，重生成即生效）。
