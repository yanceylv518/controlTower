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
