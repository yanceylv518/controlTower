# Codex 任务：账单读路径打磨——渠道页任务绑定 + 生成中状态透出

背景：2026-08-07 两条验收记档 P3 清账（review-billing-job-binding-2026-08-07.md）。用户账单页已在 c2ff356 完成 job_id 绑定与错误透传，渠道账单页留有同类缺口；且重生成运行期间读路径报 `billing_not_generated`，语义误导（任务明明在跑）。

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量测试 + `pnpm typecheck` + `pnpm build`。**

## 改动一：渠道账单页同步 job_id 绑定

对齐用户页（c2ff356）的模式，渠道侧全部读口径绑定页面正在展示的任务版本：

- `/api/dashboard/billing/channels`（含 channel_id 日明细与 CSV 导出分支）走 `billingJobForRead`（jobType=channel_generate）——接受可选 job_id，校验实例/类型/精确区间，不匹配 409；
- 渠道 workbook（channel-workbook 与 channel-workbook-jobs）同样接受并透传 job_id；异常单导出（channel_id 分支）同理；
- ChannelBillingV4View：抽屉明细、导出日账单、导出账单 Excel、异常订单四个入口把 `state` 里的 generation_job.id 传下去；导出下载改 blob 方式（与用户页 exportUser 一致，失败可弹真实原因）。

## 改动二：生成中状态透出（替代误导性"未生成"）

- server：detail/workbook/channels 的"无可用完成任务"分支细分——`billingJobForRead` 若找到匹配任务但 status ∈ {pending, running}，409 响应体改为 `{"error":"billing_generating","progress":<completed_steps/total_steps 百分比>,"job_id":...}`；找不到任务才是 `billing_not_generated`。导出任务错误透传已存在（fileDownloadWriter.responseError），`billing_generating` 会自然带到导出任务 error；
- web：httpError 与账单两页对 `billing_generating` 显示"账单生成中（N%），完成后重试"；`billing_not_generated` 维持现文案。日账单抽屉在该错误时也显示同样提示而非报错块。

## 接线点（逐个核对，不得遗漏）

billingJobForRead 状态细分（用户页与渠道页共用）、channels handler 三分支（JSON/details/CSV）、channel workbook 两个 handler、异常导出 channel 分支、V4 页四个入口 + blob 下载、httpError 文案映射、api-contracts 若有示例则更新、既有 job_id 绑定测试扩展渠道侧。

## 验证要求

1. `go test ./...` 全绿 + `pnpm typecheck` + `pnpm build`；新增测试：渠道 handler 假 job_id 409、运行中任务返回 billing_generating 带 progress、渠道 CSV 绑定 job_id 只出该版本数据；
2. 手工（真库冒烟栈可复用：scratchpad MariaDB 33061 / ct-server 18090）：生成渠道账单 → 抽屉/导出与页面同版本；触发重生成期间点导出 → 提示"生成中 N%"而非失败。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 渠道页四个入口全部携带 job_id，无一遗漏
- [ ] 运行中/未生成两种 409 文案区分正确，导出任务 error 透传两种码
- [ ] 用户页行为回归（c2ff356 的绑定与透传不被破坏）
- [ ] 一个 commit：`fix(server,web): bind channel billing reads to job and surface generating state`

## 明确不做

预检缺失清单渲染（billing_models_missing 文案，单独记档）；计价页"写价未随 newapi 配置生效"标注（单独记档）；异常计数查询失败降级（单独记档）。
