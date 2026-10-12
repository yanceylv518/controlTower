# 上游账单生成进度名称修复

## 问题与原因

账单目录有上游名称，但刷新页面后恢复批量任务，进度弹窗仅显示“上游 #ID”。BillingWorkspaceView 默认目录模式不会加载选择器的 subjects，BillingGenerationBatch.restore 只能从这份空列表恢复名称；进度接口原来不返回名称。生成记录详情同样依赖这份列表。

## 修改

- GenerationProgress 增加可选 subject_name，上游进度从本站点 billing_upstreams 直接解析名称，不调用源站、不触发渠道同步。
- 排队等待检查、生成中及完成状态均可带名称，不依赖已生成账单；归档上游保留名称，站点和账单类型隔离。
- 旧冻结任务在响应时补齐缺失名称，不重写冻结进度；已有名称快照继续保留。独立任务详情也走同一补齐逻辑。
- 生成进度及记录详情优先使用接口名称，兼容选择器缓存及缺失数据的原有 ID 展示。
- 无迁移，不改变金额、渠道归属、任务调度或历史账单。

## 验证与状态

- 12 项前端生成测试通过，新增空 subjects 恢复进度及切换站点后迟到响应隔离回归。
- 隔离本地 MySQL 上游完整套件通过；新增排队名称、JSON字段、归档、跨站点/类型隔离、旧冻结记录补齐及快照保留检查。临时测试库自动清理，未修改业务账单。
- 前端 vue-tsc 与 Vite 构建通过，仅现有大包提示。
- Go billing/mysqlstore/dashboard 三个包非实库回归通过。
- 2026-10-12 按用户要求纳入 main 提交与推送，本次提交只包含该名称修复及测试文档。未远程打包、未部署；浏览器视觉未验收，未重启现有本地服务。

## 文件

后端 billing/automation.go、mysqlstore/billing_generation_name.go、billing_batch_generation.go、billing_generation_history.go；前端 BillingGenerationBatch.vue、BillingGenerationHistory.vue 及共享接口。
