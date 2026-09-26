# 模型 TPM 图内渠道维度

- 标识：2026-09-26-model-tpm-channel
- 更新时间：2026-09-27 06:55，Asia/Shanghai（UTC+08:00）
- 状态：功能提交 1fb92634 已推送 origin/main，远程 SHA 已核对；未发布或部署。
- 目标与验收条件：在模型监控的每张 TPM 图内部提供按客户、按渠道两个拆分维度，交互与客户监控一致；逐模型独立切换，渠道拆分总量准确、站点隔离、手机可用。
- 负责会话、分支与范围：Codex 本任务；`D:/CodexProjects/codex/control-tower` 的 `main`。前端 DimensionView、ModelChannelTraffic、复用的 CustomerTrafficPanel/Chart 与 customerTraffic 计算；Server 指标权限/渠道名称及对应测试。原有账单、电话预警、其他任务文档、缓存等改动保留。

## 实际结果与当前交互

## 最新主线同步与合并（2026-09-26 06:37）

- 根因：本地 main 的 d252a36 落后远程 36 个提交。执行 fetch 和 fast-forward 后，HEAD 与 origin/main 均为 d610eded554443e44edd456e3734e8c22cb27432。
- 同步前备份仍保留：stash@{1} `before-main-sync-20260926-model-tpm` 保存已跟踪改动；stash@{0} `local-agents-before-main-sync-20260926` 保存原未跟踪 AGENTS.md。其他未跟踪文件保留；两份协作规则均保留在 AGENTS.md，用户明确指令优先。
- 恢复改动时解决六处冲突，保留远程渠道按客户面积分层、全模型展示、最新分钟指标、复制名称及放大弹窗；本地模型逐卡按客户/按渠道、异步隔离及两维缓存继续生效，弹窗与原图维度联动。没有遗留未解决冲突；未提交或推送工作区改动。
- 本轮验证：根目录 `go vet ./...` 和 `go test ./...` 通过；webapp 中 `node --test packages/desktop/tests/*.test.mjs` 455 项通过；`pnpm typecheck`、`pnpm build` 通过（既有大 chunk 提示）；`git diff --check` 通过。合并初次类型检查发现可选 expanded 产生 boolean/undefined，已显式布尔化并重新通过类型及构建。未运行真实 MySQL 集成。
- Chrome 对本地 5192、既有远程 API 的只读实测：渠道 TPM 展示多个客户的面积分层；模型列表展示 13 个模型，首图切渠道后其他图仍按客户，放大图显示渠道 252/228 的面积曲线。控制台本次读取 error 为空；未做本次手机回归。远程尚未应用本地渠道名称补齐，因此模型渠道图仍使用 ID 回退名称。
- 后续：用户可继续本地验收；提交/发布及远程 Server 升级按后续安排。本次没有部署或生产写入，Workspace 总结仍待明确同意。

## 模型双维实现细节

- 进入模型监控 → TPM，每张模型图内部独立提供“按客户 / 按渠道”，默认按客户；两个维度均使用与客户监控相同的分层面积图、稳定颜色、图例及“更多”完整名单平均 TPM/占比，切换一张不影响其他模型。用户明确纠正了此前误实现的“总量 / 按渠道”，此前交互已被替换。
- 复用已有 `instance_model_user`、`instance_model_channel` 与完整模型前缀查询，不新增采集维度、源库扫描或迁移；无 Agent 变更。`monitor.models` 放行此维度的只读指标接口，viewer 限制不变。Server 按实例查询渠道名称，兼容模型名含冒号及内嵌 `:channel:`。
- 1/6 小时使用 1m，24 小时使用 5m 并除以 5 换算 TPM。只绘制已经结束且拆分 Token 和模型总量一致的桶；未知、缺失、无渠道归属造成的差额留空并提示，不归一化或补造“其他”。全部渠道保留，渠道 ID 必须是完整规范正整数。
- 图表靠近视口且位于当前图表模式时只加载当前选中的客户或渠道维度；缓存按完整模型/实例/时段/维度隔离。同轮成功数据复用，慢请求单飞后追赶新刷新轮次，失败保留同范围旧数据并提供重试。
- 父页面在站点、模型/渠道页面、时间范围改变后清空旧数据，并在异步阶段检查请求是否失效，阻止旧总量与新拆分混合。

## 初版验证（06:09，交互已被后续修正）

- `go test ./server/internal/auth ./server/internal/dashboard`：通过；覆盖仅模型权限、其他权限拒绝、viewer 拒绝、跨实例名称、带冒号模型名、非法 ID 及最新/历史/汇总接口。
- 根目录 `go vet ./...`、`go test ./...`：通过（部分包命中本地测试缓存）。环境未配置 `CT_MYSQL_TEST_DSN`，不计作真实 MySQL 验证。
- `webapp` 中 `pnpm typecheck`、`pnpm build`：最终图内切换版本通过；构建仍有既有大 chunk 提示。
- `node --test packages/desktop/tests/customerTraffic.test.mjs packages/desktop/tests/customerTrafficRefresh.test.mjs packages/desktop/tests/modelChannelTraffic.test.mjs packages/desktop/tests/modelChannelTrafficRefresh.test.mjs packages/desktop/tests/customerTooltip.test.mjs packages/desktop/tests/modelCustomerNames.test.mjs`：46 项通过。
- Chrome + 实际 Vue 页面 + 本地合成 API：桌面验证渠道分层、全部渠道名称/占比、单模型总量切换且其他模型保留分层；24 小时 5m、缺口与无渠道历史空态、切换空站点清除旧图通过。390×844 手机截图及 DOM 核验：图内切换、分层图和图例正常，主区 x=0，页面宽度与滚动宽度均 390，无水平溢出；控制台 error 为空。
- 合成页面仅访问本地测试数据，不转发真实站点。复现辅助脚本保留于忽略目录 `local/model-channel-preview.mjs`。
- `git diff --check`：通过。

## 未验证与下一步

- 真实 NewAPI/Agent/CT 数据链路、真实 MySQL 和生产负载未验收；同桶对齐不代表迟到采集已经全部结束。
- 完整交付需同步升级 Server/Web，以使受限模型权限与渠道名称生效；无需新迁移或 Agent 升级。
- 下一步按用户安排提交/发布并验收真实模型渠道历史。Workspace 总结尚未上传，等待本次明确同意。

## 相关实现

- [模型监控页面](../../webapp/packages/desktop/src/views/DimensionView.vue)
- [模型图内切换与加载](../../webapp/packages/desktop/src/components/ModelChannelTraffic.vue)
- [分层计算](../../webapp/packages/desktop/src/utils/customerTraffic.ts)
- [指标名称](../../server/internal/dashboard/metric_handler.go)
- [权限映射](../../server/internal/auth/permissions.go)

## 本地远程联调启动（2026-09-26 06:11，UTC+08:00）

- 用户要求本地运行并连接远程地址。核实本地 5192 未监听，拟沿用此前确认的 `http://124.220.201.99:8080` 作为进程级 API 目标，启动当前前端于 `http://127.0.0.1:5192/models`。
- 隐藏后台 Vite 启动命令被自动审批以 `blocked by policy` 拒绝，未提供更具体原因；未执行启动，未改持久配置或远程环境，尚未进行 HTTP 验证。
- 用户随后明确回复“是的”，已确认本地 5192 与远程 API `http://124.220.201.99:8080`。按该授权重试启动仍被自动审批以 `blocked by policy` 拒绝，未提供具体原因；服务仍未启动，不能把阻塞归因于缺少用户授权。
- 下一步：执行策略阻塞解除后按已确认目标启动并验证页面/代理，无需重复询问授权；不可宣称服务已运行。

## 按客户 / 按渠道修正（2026-09-26 06:28，UTC+08:00）

- 当前交互为每张模型 TPM 图内“按客户 / 按渠道”，默认按客户。移除总量选项；客户使用 instance_model_user，渠道使用 instance_model_channel，都是真实拆分数据。图例、空态、错误提示随维度显示客户或渠道。
- 维度纳入请求作用域和缓存键，快速切换隔离迟到响应，分别复用同轮成功缓存；仅本次前端修正，无新增 Go 或 Agent 改动。
- 本轮 47 项相关前端回归、pnpm typecheck、pnpm build、git diff --check 通过，构建有既有大 chunk 提示。测试包含客户名称/ID、模型名含冒号、跨实例隔离、5m 换算、缺口、逐卡独立切换及两维缓存/竞态。
- 本地 5192 已监听（PID 31920）；Chrome 登录页面只读加载真实站点，模型 TPM 已显示客户名称与分层曲线，两维切换入口已确认。未再次执行 Go 检查、MySQL 集成或本次手机验收；前一版模拟验收不替代本次真实链路全量验收。未部署或写远程配置。
- 真实页面进一步验证：首张模型切为按渠道后显示渠道 252/228 的分层图，第二张仍保持客户名单及分层；两图均有实际 SVG 曲线且无错误提示。远程尚未应用本地渠道名称补齐，当前按渠道 ID 回退显示，需后续升级 Server 才能补齐名称。

## 提交与推送（2026-09-27）

- 用户授权提交推送，功能提交 `1fb926342bbfd7b9a59a2abb6f571f72e08e236f` 已推送 `origin/main`，`git ls-remote` 核对一致。10 个功能文件；其他归档页面、AGENTS.md、历史文档及缓存等工作保留原状。
- 在 `local/model-tpm-submit-20260927` 从暂存索引导出的独立源码快照上执行 Go 全量 vet/test、455 项前端测试、pnpm typecheck/build，全部通过；依赖复用本机已有 node_modules。生产构建仍有既有 chunk 大小提示。未重做 MySQL 集成或浏览器验收，前次真实页面证据见上文。
- 当前总览仅暂存本任务记录，其他会话总览改动保留在工作区。功能已提交推送不代表发布部署；渠道名称与受限权限仍需配套升级 Server。Workspace 总结尚未推送，等待本次明确同意。
