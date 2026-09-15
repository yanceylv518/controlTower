# 账单明细和每日统计展示历史单价与规则

- 标识：2026-09-16-billing-historical-prices
- 更新时间：2026-09-16，Asia/Shanghai。
- 状态：实现及自动化验证完成，待人工验收；未提交、未发布、未部署。
- 目标与验收条件：明细展示历史单价，日订单统计同步显示；同日不统一价格时逐套展示规则，原始扣费金额不变。
- 负责会话、分支和范围：当前 Codex 会话；codex/billing-historical-price-display，基线 a58f278f / rc117；隔离目录 D:/CodexProjects/codex/control-tower/.tmp/latest-billing-review。主目录电话预警分支及原有业务修改保留。
- 实际结果与关键决策：普通计费独立从日志历史倍率/价格快照还原单价，不改变金额和核对结果；缺失显示未记录，免费价格为零。表达式不假定可拆为固定单价，保留 expr_b64 解码全文、matched_tier、group_ratio、request_rules、工具附加费用；表达式缺失/无效明确显示，原始模式不执行表达式。
- 交付界面：逐请求 XLSX、渠道 CSV、新版汇总 XLSX 每日账单页、Web 日订单统计。单日多套价格保持分项价格和规则对应关系。XLSX 规则自动换行；Web 可展开完整规则。
- 性能与数据边界：生成明细同时写去重规则工作表，后续读取只解析规则表，保留延迟加载/签名缓存；用户汇总跨渠道合并，上游规则按渠道隔离。原始日志仍按既有任务站点/对象/日期/过滤范围读取，无额外源库扫描。规则随 gob 暂存、JSON 分流和文件发布保存。
- 兼容：新任务 usage_version=2，版本进入查重键，允许相同旧账期新建完整价格格式任务。旧任务/文件不回填，不修改旧计费方式。无新增迁移，依赖已有 080；无需升级 Agent/NewAPI。
- 验证：go vet ./...、go test ./... 全量通过；pnpm typecheck、pnpm build 通过（仅既有大包提示）；node --test packages/desktop/tests/*.test.mjs，87 项通过。新增端到端覆盖原金额不变、20/100/2 历史价格、零价/缺价、未知表达式不执行、gob→JSON→XLSX、日规则合并、渠道隔离、CSV 列对齐及零元按次价格。故意写坏逐请求 XML 后仍可加载规则及下载每日汇总，证明新格式不扫描明细。
- 未验证与限制：CT_MYSQL_TEST_DSN 未配置，实库集成未执行；没有浏览器/Excel 人工验收或生产账单验收。音频分项单价暂不独立还原，规则明确说明；表达式不能保证拆分，展示历史原文供核对。此前精度/旧音频任务审查事项未在本轮修复。
- 下一步：人工验收后按用户安排提交、打包和部署；旧账单需新建任务生成历史价格信息。
- 自动归档：本机 auto_capture=true，但查询及归档均无法连接 AI Workspace；保留本地记录，尚未上传。
- 相关资料：docs/design-billing-source-mode.md；server/internal/billing/historical_prices.go；server/internal/billing/historical_price_sheet.go；server/internal/dashboard/billing_historical_rules.go；对应 *_test.go。
