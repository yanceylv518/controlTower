# 归档页面首屏请求优化

- 标识：2026-10-01-archive-first-load
- 更新时间：2026-10-04（Asia/Shanghai）
- 状态：本地实现、待生产性能验收
- 目标与验收条件：用户明确反馈刚进入页面慢；减少可核实的首屏冗余请求，保持统计/金额/权限口径。
- 负责会话、分支和范围：本会话，main；ArchiveDataView.vue、LogArchiveJobsView.vue及对应两份组件测试。未改Agent或Server。
- 实际结果：统计首次加载立即发出，后续筛选保留350ms防抖；相同在途查询共用Promise；使用AbortSignal取消被替代或卸载的请求；隐藏统计tab不继续查询。用户身份变化重新选择身份隔离的缓存。状态watch立即初始化，onMounted只加载实例列表并安装轮询，取消隐藏旧日历自动跳转最早月造成的额外请求。
- 并发处理：期间其他操作同步了main并产生ArchiveDataView冲突，后续观察到冲突已被合并；确认保留option_names、scope/loadedQuery、空筛选归一化等新逻辑，再对合并结果验证。本轮没有执行pull/stash，也没有覆盖其他业务修复。
- 验证：node --test webapp/packages/desktop/tests/archiveData.test.mjs webapp/packages/desktop/tests/archiveJobs.test.mjs，30项通过；webapp下pnpm typecheck、pnpm build通过，保留既有大chunk警告；git diff检查通过。测试props改用Vue reactive，以真实模拟查询key随站点变化，否则非响应式替身会掩盖竞态。
- 未验证与阻塞：生产浏览器已登录，只读确认统计页面可显示已有8月数据；工具无法获取Resource Timing，未获得请求时间瀑布图、DB慢查询或前后耗时对照。每次归档读请求重新连接及权限检查仍存在，不能未经测量就认为这是根因；本轮不放宽安全检查。当前截图10月0/0是数据状态，不能当加载持续中的证据。
- 下一步：按用户后续授权发布Server/Web，Agent保留原版本；生产首次进入测量overview、jobs、currency接口和JS加载耗时，必要时针对实际最慢段继续优化。
- 交付状态：2026-10-04修复提交cebc1338已推送main并回读确认；未发布、未部署。其余工作区改动保持原状。

## 提交验证（2026-10-04）

- 本次重新运行30项归档组件回归、`pnpm typecheck`、`pnpm build`，全部通过；构建仍有既有大chunk提示。未修改Go代码，未重跑Go检查或生产性能验收。
- 精确提交两个Vue页面、两份组件测试及本记录/当前总览；旧接口预览脚本logArchive-preview-server.mjs留在本地，其他任务文档和缓存/发布目录未纳入。
- 修复提交`cebc1338d80c7ba5fe7e1d4bce13d2112d60f9a9`已推送main，远端回读一致；本轮不发布或部署，仅Web需要后续更新。
