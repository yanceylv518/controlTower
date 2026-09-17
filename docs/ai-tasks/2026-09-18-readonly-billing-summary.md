# 只读日志动态计费摘要

- 标识：2026-09-18-readonly-billing-summary
- 更新时间：2026-09-18，Asia/Shanghai。
- 状态：完成本地实现与验证。
- 目标与验收条件：动态表达式日志不再显示普通倍率的零占位单价；其他计费摘要保持原行为。
- 负责会话、分支与范围：当前会话，main；ReadonlyLogsView.vue、摘要回归测试及项目记录。
- 实际结果与关键决策：复用详情已有 isTieredBilling 判断，在普通倍率或按次判断之前展示“动态计费 · 查看计费详情”。不从扣费反推单价，不修改结算。用户要求提交与推送作为同一次交付执行。
- 验证：Node 相关回归 29/29 通过；pnpm typecheck、pnpm build、git diff --check 通过。构建有包大小提示。首轮测试受沙箱子进程 EPERM 限制，获准执行后通过。
- 未验证与阻塞：未读取截图订单原始响应，不能确认该订单 billing_mode；未做登录后浏览器验收、未部署；无实现阻塞。无 Go 改动，未运行 Go 测试。
- 下一步：用户刷新本地前端核对该订单，远端上线按后续发布安排。
- 相关证据：webapp/packages/desktop/tests/readonlyLogsBillingSummary.test.mjs；本次测试覆盖动态零倍率、动态按次占位、缺失倍率、普通单价、真实零价、按次和错误日志。
