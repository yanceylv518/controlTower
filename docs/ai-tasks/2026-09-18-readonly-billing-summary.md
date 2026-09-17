# 只读日志动态计费摘要

- 标识：2026-09-18-readonly-billing-summary
- 更新时间：2026-09-18，Asia/Shanghai。
- 状态：实现与自动检查完成，待真实页面验收。
- 目标与验收条件：动态表达式日志显示该请求具体计费价格，与 NewAPI rc35 摘要口径一致；其他计费摘要保持原行为。
- 负责会话、分支与范围：当前会话，main；ReadonlyLogsView.vue、billingDetails.ts、摘要回归测试及项目记录。
- 实际结果与关键决策：按日志 expr_b64（兼容原始表达式）和 matched_tier 解析命中档位，展示输入/输出、缓存及多媒体单价；缓存价格仅在请求有缓存用量时展示。货币换算复用站点 formatter，标签兼容 Unicode 比较符。不回退猜测其他档位，不修改结算。首版仅替换标签，用户指出不完整；本版补齐价格。用户要求不再询问 Workspace 推送。
- 验证：Node 相关回归 33/33 通过；pnpm typecheck、pnpm build、git diff --check 通过；Vite 日志确认热更新。构建有包大小提示。测试覆盖多档命中、UTF-8 标签、比较符归一、缓存读/写、无缓存、缺失/未知档位、损坏表达式、旧字段、多媒体和币种换算，以及原有标准/按次/错误摘要。
- 未验证与阻塞：浏览器工具两次返回 nodeRepl.fetch request failed，无法读取真实请求或验收登录后的界面；未部署。无 Go 改动，未运行 Go 测试。
- 下一步：用户刷新本地前端核对该订单，远端上线按后续发布安排。
- 相关证据：webapp/packages/desktop/tests/readonlyLogsBillingSummary.test.mjs；只读对照本机 NewAPI rc35 的 features/usage-logs/lib/format.ts 中 getTieredBillingSummary、components/columns/common-logs-columns.tsx 中 buildTypeDetailSegments，以及 features/pricing/lib/billing-expr.ts 的标签归一逻辑。
