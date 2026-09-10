# 验收记录：Nginx 日志来源、查询历史分页、实例逻辑删除（2026-09-09）

- 范围：`7158de7 feat(logs): add nginx sources and paginated query history`
- 结论：**通过，零缺陷**。设计说明 `docs/container-log-queries.md` 与实现一致。

## 变更概要

- **Nginx 来源**：reader 自动读取宿主机常见 nginx.conf（/etc/nginx、/usr/local/nginx、
  /www/server/nginx）并从 master 进程 -c/-p 补充；Docker Nginx 通过 inspect 的 PID 经
  `/proc/<pid>/root` 定位容器文件系统（`os.OpenRoot` 限定），解析 include/server_name/
  log_format/access_log/error_log 继承与 off；只上报域名与支持的字段，不上传配置与路径；
  不执行 shell、nginx -T 或 reload。CT 以实例 Base URL 的 hostname 匹配 server_name，提交
  时再次校验；共享日志缺 host 字段或共享错误日志标不可用。访问日志支持 combined 与
  log_format 描述的文本/JSON，筛选：HTTP 状态码、Request ID、路径包含、耗时下限；错误日志：
  级别与关键词。service 不再 Requires docker，安装脚本不再要求 docker。
- **历史分页**：服务端按查询批次分页（20/50/100），点开才加载正文；可按时间、Request ID
  筛选；**超级管理员（全权限管理员）可查看其他管理员的记录与结果并按发起人筛选**
  （此前任何人都不能读别人的结果，属有意放宽）。074 迁移为 tasks 加生成列
  `history_batch`（JSON_EXTRACT 生成列，MariaDB 11.4 实测可用）+ 索引。
- **实例逻辑删除**（070 迁移加 `deleted`）：仅已停用实例可删，删除后从列表移除、全部
  实例 Token 立即失效、不可更新/轮换/恢复、同 ID 不可重建；历史与审计保留；日志目标
  只算启用且未删除的实例。
- 结果区改为多来源合并展示（每行带来源标识，保留各来源原始行序），条件与统计收进
  "查询详情"。

## 实证

- `go vet`、`go test ./...` 全绿（nginx 发现/共享日志隔离/不支持项/JSON 格式与敏感字段、
  历史分页、实例删除、nginx 站点绑定均含真库）；前端三组 node 单测通过；
  `pnpm typecheck`/`build` 通过；烟测库 070/074 首启套用（编号跳过 071–073，台账按文件名）。
- **端到端**（真实 reader 代码 + 假 docker 发现返回一个应用来源和一个 combined 格式的
  nginx_access 来源，真跑 agent）：实例 Base URL 设为 api.example.com 后 nginx 来源可用且
  query_host 匹配；无筛选返回 4 行、UA 里的 Bearer 已脱敏；`error_code=502` 1 行、
  `path=/v1/chat` 2 行；host 改为其它域名提交 409；对应用来源带 nginx 筛选提交 409。
- 历史：`paged=1&page_size=2` 返回 total 3、两页、正文为空数组；`request_id=nope` total 0；
  受限管理员（仅 logs.query）自己历史为空、按发起人筛选 403、读超级管理员任务 404；
  超级管理员读受限管理员的任务 200 并可按 `actor=logviewer` 筛选。
- 实例删除：启用状态删除 409 `disable_instance_first`；停用后删除 200；删除后 Agent
  心跳 401、列表不显示、轮换/更新 404、同 ID 重建 409。
- 页面 1366：日志类型下拉、时区标签、合并结果区、"查询详情/关闭换行"渲染正常。

## 记档（P3）

- 超级管理员可读他人日志结果是本批的权限放宽，与 09-08 文档"任何人不能读别人结果"
  相反；已在设计文档写明，部署后请知会管理员。
- 应用日志来源按目录递归选"最新两个文件"，若 nginx 日志与应用日志同目录，可能挤占
  两个名额之一（我的夹具即如此，页面提示"部分条目已跳过"）；生产里 new-api 日志目录
  通常独立，留意即可。
- 宿主机 Nginx 发现以 root 读 `/proc/*/cmdline` 与配置引用的日志路径；配置指向的任何
  文件都会被读取——能改 nginx.conf 的人本就是服务器管理员，边界未扩大。
- 本机无 docker/nginx，真实 nginx.conf 解析与 `/proc/<pid>/root` 路径只由单测覆盖；上线
  后按文档在有 Nginx 的服务器上核一次。

## 部署要点

- 先 Server + 前端（070/074 自动套用），再 Agent 与 reader；**需重新运行发布包的
  `install-log-reader.sh`**（service 文件变更、不再依赖 docker），保留原 agent.config 与
  log-reader.env。实例删除功能不需要升级 Agent。
- rc101 打在 4e3996c 不含本批，上线需重打 rc102（server + 前端 + agent + reader，两个迁移）。
