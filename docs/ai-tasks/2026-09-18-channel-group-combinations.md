# 调权渠道分组组合

- 标识：2026-09-18-channel-group-combinations
- 更新时间：2026-09-18，Asia/Shanghai。
- 状态：实现和自动检查完成，待环境验收。
- 目标与验收条件：落实已确认交互稿，主题与主界面一致；多选组合及分组、自由增删、命名组合持久化和管理；保持站点及渠道边界。
- 负责会话、分支和修改范围：当前会话，main；前端组合编辑组件/API、服务端模板接口/存储/082 迁移、渠道分组写入校验、对应测试与文档。其他未跟踪内容保留。
- 实际结果与关键决策：组合按站点共享，独立入口可提前建立；多个模板合并去重，应用复制成员而非持续绑定。模板保存有 revision 并发校验。采用 Element Plus/主界面主题变量。用户单独明确允许新分组名称后移除历史名单限制，格式/长度/渠道归属/确认校验保留。操作人和更新时间持久化；线上渠道沿用现有执行与审计链路。
- 验证：go vet ./...、go test ./... 通过；153 项前端 Node 测试全部通过；pnpm typecheck、pnpm build、git diff --check 通过。新增测试覆盖模板增删改、跨站点隔离、并发编辑、去重、非法名称/长度、权限、新名称下发、前端草稿与站点切换；构建仅有包大小提示。默认 Go 依赖源网络失败，进程内改用 goproxy.cn 下载锁定依赖后通过；未修改 go.mod/go.sum。
- 未验证与阻塞：未配置 CT_MYSQL_TEST_DSN，真实 MySQL（含新增持久化测试）跳过；浏览器工具返回 nodeRepl.fetch request failed，未验收真实视觉和线上请求。未部署、未执行迁移；远程旧 Server 暂不能提供组合库。自动审批曾拒绝移除旧白名单，用户明确回复“允许”后继续，当前无审批阻塞。
- 下一步：按用户安排升级 Server/Web、应用 082 迁移，在测试环境核对真实页面及模板跨会话保存；用户此前要求先不打包，本次不打包。
- 相关设计、代码或证据：docs/channel-group-combinations.md；ChannelGroupEditor.vue；channel_group_presets_handler_test.go；channelGroupEditor.test.mjs；channel_group_presets_test.go。
