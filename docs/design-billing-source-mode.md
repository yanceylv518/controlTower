# 账单计费来源与多媒体用量（2026-09-15）

## 计费来源

`POST /api/dashboard/billing/statements` 新增可选布尔字段 `recalculate`。

- 缺省或 `false`：任务 `pricing_source=newapi`，每条计费订单金额直接取 NewAPI 日志 `quota / QuotaPerUnit`。不调用倍率、表达式、Token 单价重算，不因这些参数缺失产生核对差异。金额使用有理数计算，按 12 位小数写入既有汇总列；币种单位换算沿用站点 QuotaPerUnit，未配置时沿用 NewAPI 默认值。
- `true`：任务 `pricing_source=recalculate`，沿用已有按日志倍率/表达式重算、核对差异、缺价按日志扣费兜底的规则。
- `exclude_zero_output` 与结构异常过滤继续生效。上游渠道折扣仍作用于账单基础金额，不属于 Token 重算；用户账单不应用渠道折扣。
- 任务返回 `pricing_source`、`usage_version`；新任务 `usage_version=1`。查重键同时包含原始参数、计费来源、用量版本，支持同账期不同计费方式。旧任务保持原查重键。
- `GET /api/dashboard/billing/jobs` 列表新增 `pricing_source_selection=true`。前端仅在明确支持时允许提交，防止旧 Server 忽略新增字段后执行重算。

## 用量展示

新任务在区间、每日、令牌汇总和每日 XLSX/渠道 CSV 中保留图像输入、图像输出、音频输入、音频输出四个独立 Token 数值；请求明细经压缩暂存和数据库 compact 日汇总继续保留这些字段。

- `prompt_tokens`：显示普通输入；源读取器原已扣除图像及按来源语义处理缓存，新展示再扣除音频输入，最小为零。
- `completion_tokens`：显示普通输出；从日志输出量扣除图像、音频输出，最小为零。
- 四个多媒体字段分别为 `image_input_tokens`、`image_output_tokens`、`audio_input_tokens`、`audio_output_tokens`。
- 缓存读取/写入继续单列。不同上游的缓存与多媒体用量可能重叠，不能保证把所有拆分列相加等于原始日志总数；拆分只影响展示，不反向改变计费。
- 图像字段沿用现有显式键和 `usage_billing_path` 版本兼容规则。音频兼容 `audio_input_token_count`；本地 NewAPI 源码 `service/text_quota.go` 确認该键用于单独定价的音频输入日志。无可信字段不估算，按次图片/视频没有 Token 数时仍保留其日志扣费。
- 原始计费模式的分项单价标记“未拆分”，不显示零价、不反推单价。总额与计费来源明确显示；仅重算模式加载原有单价快照。

## 升级与边界

080 迁移给任务表增加来源/版本、compact 表增加四类多媒体用量。旧任务默认 `recalculate` / 版本 0，旧明细和未完成任务保持旧展示语义，不回填历史 Token、不自动重生成。新建任务可生成新口径账单。

只需升级 Server/前端及执行迁移，Agent/NewAPI 不需要修改。数据库迁移和任务续跑已在独立本地 MySQL 8.0 实测；尚未部署或验收真实生产账单、浏览器及 Excel 应用。
