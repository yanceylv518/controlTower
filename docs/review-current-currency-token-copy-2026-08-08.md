# 验收记录：货币现读 newapi + Token 复制 http 兼容（2026-08-08）

范围：d71a1bb + c94080b。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 绿。另：rc36 tag 由用户侧打在 84afa1f（昨日验收收尾处，内容干净），本两笔不含其中。

## 结论：通过，验收修正一处 P2（可用性降级）

## d71a1bb 货币改为现读 newapi

账单/渠道账单页货币不再取生成时快照，改为每次现读 newapi options 解析 general_setting——**消除"重生成账单才显示新货币"限制**（rc35 部署注意事项相应作废），改币种立即生效；CNY 契约测试在。

**验收修正（P2）**：原实现货币读取失败直接 500——账单页查看历史数据本不依赖活的 newapi 连接，此改动让 newapi 库不可达时整页历史账单挂掉（可用性回归）。修复：货币是显示糖衣，读取失败降级为生成时快照货币、快照亦无则 USD 默认，页面照常返回；补降级测试（source 报错 → 200 + USD）。现读成功时行为与 codex 原意完全一致。

## c94080b Token 复制 http 兼容

navigator.clipboard 仅安全上下文可用；补 isSecureContext 判断 + execCommand 兜底 + 失败时诚实提示"请手动复制"。零问题。

## 部署

无迁移。rc36（84afa1f）不含本两笔与修正；下一 tag（rc37）收齐。当前生产 server rc26/agent rc22——rc36/37 部署要点沿用 rc35 部署单（迁移 033-039 增量、账单 v6 重生成一次、agent 可选、EXPLAIN+对账抽验）。

## 跟进（同日）：币种仍不生效——根因是没读 newapi 源码（用户点破）

用户反馈币种依旧不对并指出根本问题：无人核对过 new-api 源码中该设置的真实存储位置。拉源码（QuantumNous/new-api）核实：**GeneralSetting 经 config.SaveToDB 按字段持久化为点号独立键**（`general_setting.quota_display_type` / `.custom_currency_symbol` / `.custom_currency_exchange_rate`，裸值），options 表**不存在**整块 `general_setting` JSON 键——codex 实现查的恰是后者，生产永远查不到，币种永远回退 USD。字段名/枚举/USDExchangeRate 键（默认 7.3）均猜对，唯持久化形态错。

修复（验收方）：快照采集补三个点键；解析器以点键为权威（裸值），blob 分支降为兼容回退；CNY 汇率默认对齐源码 7.3；三态点键测试 + 冒烟以真实形态种数据实证返回 `¥/CNY/7.2`。

**教训入档：对接外部系统的数据结构，必须以其源码/实库为准实证，不得以"实现自洽+测试通过"替代**——本案测试全绿恰因测试数据与实现犯了同一个假设错误（fixture 是自己发明的形态）。此前 other JSON 五倍率能对是因为有用户提供的生产样本钉着；options 键名从未经过同等核对。newapi 源码已留在 scratchpad（newapi-src），后续对接一律先查源码。
