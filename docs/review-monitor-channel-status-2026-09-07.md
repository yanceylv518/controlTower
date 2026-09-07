# 验收记录：渠道监控状态识别与维度切换重置（2026-09-07）

- 范围：`d7ea3c7 fix(web): recognize numeric enabled status in channel monitoring`、
  `c732363 fix(web): reset filters when switching channel and model monitoring`
- 结论：**通过，验收补根治一处**（状态编码不一致的写入侧）。

## d7ea3c7 数字状态识别

- **现象**：Agent 渠道快照把 new-api 状态归一成 `enabled/disabled/auto_disabled`，而
  09-05 批的 Server 直连刷新（`StoreInstanceChannels`）把数字状态原样存成 `"1"/"2"/"3"`。
  渠道监控页按 `!== "enabled"` 判断，直连刷新后所有启用渠道被标成"已禁用"。
- codex 的修法：行分类接受 `"1"`。但同一页第 380 行的状态标签仍按 `!== 'enabled'`
  判断，`"1"` 会在每个启用渠道旁多挂一个写着 "1" 的标签；`"2"/"3"` 也显示成数字而非
  "停用/自动停用"。调权中心的 SQL 早已兼容两种编码，其它读者不再逐个排查。
- **验收补根治**：写入侧统一——`StoreInstanceChannels` 与 `ApplyChannelWrite`/Agent
  回执回写把数字状态映射成 Agent 同款标签（1→enabled、2→disabled、3→auto_disabled），
  `channel_current.status` 只剩一种编码；前端标签判断同步加 `"1"` 容错（兼容 rc96
  之前若已产生的数字行；生产尚未部署 rc96，实际不会有）。回归
  `TestServerChannelWritesStoreNormalizedStatus`（真库：直连刷新与确认写入落库均为标签）。

## c732363 维度切换重置筛选

- 渠道监控与模型监控共用 DimensionView，切换时清空状态筛选、搜索词与选中项，避免沿用
  上一页的筛选导致空列表。逻辑直接，`watch` 新旧值解构正确。

## 实证

- `go vet`、`go test ./...` 全绿；`pnpm typecheck`/`build` 通过。
- 烟测栈：渠道 3 为数字状态时，修正后页面把它归入"无流量"而非"已禁用"（顶部计数
  无流量 3 / 已禁用 0）。

## 部署要点

- 只改 server（状态归一）与前端，无迁移，未动 agent。
- rc98 打在 8315005 不含本批，上线需重打 rc99。
