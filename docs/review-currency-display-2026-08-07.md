# 验收记录：账单页货币显示跟随站点 newapi（2026-08-07）

范围：d00d232（codex 自主交付）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 绿；真库冒烟栈复验响应契约。

## 结论：通过，验收修正一处 P1（汇率只换符号不换数字）

## 交付核验

- 快照采集扩三键（general_setting/USDExchangeRate/DisplayInCurrencyEnabled），解析 quota_display_type 四态（USD/CNY/CUSTOM/TOKENS，CNY 取 USDExchangeRate、CUSTOM 取自定义符号与汇率、旧版兼容键 false→TOKENS），最新日快照优先——解析与取新逻辑有测试；
- summary/channels 响应带 currency；两账单页优先用站点货币、无货币信息回退 CT 偏好——渐进兼容正确（旧快照未含新键 → 默认 USD，真库实证响应为 `{"type":"USD","symbol":"$","exchange_rate":"1"}`）。

## 验收修正（P1，我直接修）

**exchange_rate 带到前端但无人使用**：`money()/unitPrice()` 只替换符号、数字仍是 USD 口径——CNY 站点（汇率 7.2）会显示"¥0.0420"而正确值是"¥0.3024"，**符号对数字错 7.2 倍**；CUSTOM 同理。USD 站点（汇率 1）无感，但功能本为非 USD 站点而做。修复：两视图（BillingView/ChannelBillingV4View）金额与单价显示统一乘站点汇率（无效/缺失汇率回退 1），渠道页 PageData 增带 currencyRate。CSV/工作簿导出仍为 USD 原值不受影响（服务端无符号纯数字，口径一致）。

- 记档：余额列（balanceMoney/formatQuota）仍走 CT 偏好换算，与站点货币可能不一致——前置已如此，涉及 prefs 体系重构不搭车，P3；
- 记档：TOKENS 显示类型当前呈现为无符号 USD 数值，与 newapi 的"显示原始 quota"语义不完全一致——边缘配置，P3。

## 部署

无迁移。**货币信息来自生成时的每日快照**：升级后需重新生成账单，新快照才含货币键（未重生成前回退默认 USD 显示，无害）。rc35 不含本笔与修复。
