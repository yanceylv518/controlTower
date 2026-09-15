# 账单明细和每日统计展示历史单价与规则

- 标识：2026-09-16-billing-historical-prices
- 更新时间：2026-09-16，Asia/Shanghai。
- 状态：已提交推送 main，v2.0.0-rc118 已发布；未部署，待人工验收。
- 目标与验收条件：明细展示历史单价，日订单统计同步显示；同日不统一价格时逐套展示规则，原始扣费金额不变。
- 负责会话、分支和范围：当前 Codex 会话；codex/billing-historical-price-display，基线 a58f278f / rc117；隔离目录 D:/CodexProjects/codex/control-tower/.tmp/latest-billing-review。主目录电话预警分支及原有业务修改保留。
- 实际结果与关键决策：普通计费独立从日志历史倍率/价格快照还原单价，不改变金额和核对结果；缺失显示未记录，免费价格为零。表达式不假定可拆为固定单价，保留 expr_b64 解码全文、matched_tier、group_ratio、request_rules、工具附加费用；表达式缺失/无效明确显示，原始模式不执行表达式。
- 交付界面：逐请求 XLSX、渠道 CSV、新版汇总 XLSX 每日账单页、Web 日订单统计。单日多套价格保持分项价格和规则对应关系。XLSX 规则自动换行；Web 可展开完整规则。
- 性能与数据边界：生成明细同时写去重规则工作表，后续读取只解析规则表，保留延迟加载/签名缓存；用户汇总跨渠道合并，上游规则按渠道隔离。原始日志仍按既有任务站点/对象/日期/过滤范围读取，无额外源库扫描。规则随 gob 暂存、JSON 分流和文件发布保存。
- 兼容：新任务 usage_version=2，版本进入查重键，允许相同旧账期新建完整价格格式任务。旧任务/文件不回填，不修改旧计费方式。无新增迁移，依赖已有 080；无需升级 Agent/NewAPI。
- 验证：go vet ./...、go test ./... 全量通过；pnpm typecheck、pnpm build 通过（仅既有大包提示）；node --test packages/desktop/tests/*.test.mjs，87 项通过。新增端到端覆盖原金额不变、20/100/2 历史价格、零价/缺价、未知表达式不执行、gob→JSON→XLSX、日规则合并、渠道隔离、CSV 列对齐及零元按次价格。故意写坏逐请求 XML 后仍可加载规则及下载每日汇总，证明新格式不扫描明细。
- 未验证与限制：CT_MYSQL_TEST_DSN 未配置，实库集成未执行；没有浏览器/Excel 人工验收或生产账单验收。音频分项单价暂不独立还原，规则明确说明；表达式不能保证拆分，展示历史原文供核对。此前精度/旧音频任务审查事项未在本轮修复。
- 下一步：按用户安排部署并人工验收；旧账单需新建任务生成历史价格信息。
- 自动归档：本机 auto_capture=true，但查询及归档均无法连接 AI Workspace；保留本地记录，尚未上传。
- 相关资料：docs/design-billing-source-mode.md；server/internal/billing/historical_prices.go；server/internal/billing/historical_price_sheet.go；server/internal/dashboard/billing_historical_rules.go；对应 *_test.go。

## 提交与 rc118 发布

- 用户明确授权提交推送并重新打包；fetch 后 origin/main 仍为 a58f278f，无需合并。21 个本任务文件提交为 ba9a2f5e2822e2208d2c0eae9949064be73318dd，推送 main 成功；标签 v2.0.0-rc118 指向该提交。本轮无业务代码修改。
- 远端 CI 35031183849、release 35031229720 均 completed/success，Go 和 Web 质量门、安装包构建、镜像构建推送、GitHub Release 创建全部成功。
- 发布页：https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc118；镜像 ghcr.io/yanceylv518/controltower-server:v2.0.0-rc118，同时更新 latest。
- Server Linux amd64、Agent Linux amd64/arm64 三份安装包和 SHA256SUMS 已上传并下载到主目录 release/v2.0.0-rc118，三份 SHA-256 全部一致；Server 包内二进制、前端入口和 080 迁移已核对。
- 从 rc117 升级无新迁移，升级 Server/前端即可；未部署生产、未生成真实账单。此前精度和旧音频任务问题未修复。

## 缺失信息误报多套价格修复（2026-09-16，待交付）

- 用户截图两条 kimi-k3 记录都是输入20、输出100、缓存2、分组倍率1；仅图像价格20与未记录不同。根因为直接按整段规则文本去重，把信息完整度差异当成价格变化。
- 在 codex/billing-order-verification（基线42edaa0b）修复规则展示：仅识别本系统普通倍率规则格式，且条件/档位/分组等上下文完全一致时合并相容的已记录价格；缺失字段单独说明，不推断该订单实际价格，不改变缓存原始证据或金额。
- 同字段已知价格冲突时保留各记录，缺失记录不能桥接不同价格；冲突与缺失并存时显示记录清单、不宣称记录条数等于不同价格套数。零价是已知价格，表达式与不同条件不会合并。
- 本轮 go vet ./...、go test ./... 通过，新增截图复现、真正变价、零价、缺失桥接和表达式/条件隔离回归。只改 Server 展示层，已有 rc118 文件无需重生成，主账单重新下载即可；没有新增前端改动或迁移。未提交、未发布部署；未做浏览器人工验收或实库测试。
- 前面逐订单独立核对需求仍待实现：保留原始扣费，复用历史公式做核对，固定表达式编译复用、仅算总额，不逐条反复拆解分项单价；缺信息必须标为无法核对。本轮截图修复没有声称该功能已完成。
- AI Workspace 查询失败，本地文档保留，尚未归档本轮修复。
