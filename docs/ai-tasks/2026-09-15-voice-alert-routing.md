# 电话预警方向与值班号码订阅

- 标识：2026-09-15-voice-alert-routing
- 更新时间：2026-09-15，Asia/Shanghai
- 状态：待真实环境验收
- 2026-09-15 主线整合接续：用户授权纳入版本发布，当前工作目录 .tmp/runtime-log-delivery。迁移编号已调整为 077；接口沿用 settings.manage，电话独立开关；修正合并后的上报超限覆盖保护。全量 Go/vet、69 项前端回归、构建及本地 MySQL TestUserVoiceIntegration 通过，真实拨号/浏览器未验收。最新交付状态见 docs/tasks/2026-09-15-runtime-log-delivery.md；下方 066/未合入描述为此前分支交付时状态。
- 目标与验收条件：阿里云 TTS 同时传入 NewAPI 用户名和实际波动方向；可配置多个内部值班号码，每个号码默认订阅全部客户，也可限制为指定客户。
- 负责会话或人员、分支和修改范围：当前 Codex 会话；`codex/customer-tpm-voice-alerts`；电话预警相关代码、迁移、Web 和文档。
- 提交推送范围：2026-09-15 用户要求提交并推送。远端 `main` 为 `8d7e6506`，与本地基线存在 1/61 个独有提交，采用独立功能分支交付。尚未合入远端 main；整合时需处理远端已有 `066_admin_permissions.sql` 的迁移序号冲突，并复核新权限和客户数据接口。
- 实际结果与关键决策：TTS 参数改为 `customer` + `direction`；方向按 11 点窗口中最近最高、最低点的时间顺序确定。客户配置中的播报字段明确为 NewAPI 用户名。新增值班号码订阅列表，空范围表示全部客户，非空范围按 `站点/用户ID` 精确匹配。同一客户触发时呼叫所有匹配号码；冷却改为客户与号码组合维度，号码分钟/小时/24 小时频控保持不变。电话记录新增方向。
- 验证：`go vet ./...` 和 `go test ./...` 通过；`pnpm build`（包含 `vue-tsc --noEmit`）通过。Go 输出存在无关的本机 telemetry token 权限提示，但命令退出码为 0。
- 未验证与阻塞：未配置 `CT_MYSQL_TEST_DSN`，真 MySQL 集成测试按测试保护条件跳过；未执行数据库迁移、浏览器人工操作、阿里云模板审核或真实付费拨号。
- 下一步：在测试环境执行 066 迁移，配置含 `customer`、`direction` 两变量的已审核模板，分别验证“全部客户”和“指定客户”订阅号码的实际拨号。
- 相关设计、代码或证据：`docs/customer-tpm-voice-alerts.md`、`server/internal/voicealert/`、`server/migrations/066_user_tpm_voice.sql`、`webapp/packages/desktop/src/components/VoiceAlertsSettings.vue`。
