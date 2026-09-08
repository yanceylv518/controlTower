# 验收记录：站点级只读容器日志查询（2026-09-08）

- 范围：`43f94ae feat(logs): add site-scoped read-only application log queries`
- 结论：**通过，验收修正一处 P2**（成功结果携带陈旧错误文案）。设计说明
  `docs/container-log-queries.md` 与实现一致，安全边界核对无误。

## 变更概要

- 新增高权限本机组件 `control-tower-log-reader`（root，仅监听本机 Unix socket，
  root:ct-agent 0660），只提供 `GET /containers` 与 `POST /query`；以固定参数执行
  `/usr/bin/docker ps -a` / `docker inspect --format`（投影不含 Config.Env），只识别启动
  程序为 new-api 的容器（或本机名单），按 `--log-dir` 解析并要求目录为 bind/volume 挂载；
  文件读取用 `os.OpenRoot` 限定在发现目录内、跳过符号链接、只读 `.log`/轮转/`.gz`，
  扫描/行数/字节/深度/超时上限齐全；回传前脱敏 Bearer、api key、password、cookie 等。
- Agent 配置 `CT_CONTAINER_LOG_SOCKET` 后独立 5 秒轮询 Server 领取任务、经 socket 执行、
  下轮回传；Server 网关只接受实例专属 Token（全局 Token 401）。
- Server：069 迁移两表（targets / tasks）；任务 24 小时清理、排队 2 分钟/领取 90 秒超时；
  结果只有查询人本人可读（全权限管理员也不能凭 ID 读别人的）；审计 `logs.query` 只含
  身份与条件不含正文。新权限 `logs.query`（与"使用日志"分开）。
- 前端"数据查询 → 容器日志"：跟随站点、默认最近 15 分钟、关键词/Request ID/错误码
  AND 匹配、历史抽屉。

## 缺陷与修正（P2）

**成功结果携带"本机日志读取服务不可用"**：worker 把 reader 的回复解码进一个预填了
失败状态与错误文案的 `Result`，reader 成功时省略 `error` 字段（omitempty），陈旧文案
原样留下并随结果上传——页面在"已完成"的结果上方同时显示红色错误条。修正：解码进
全新结构，请求失败或回复不完整才回填失败结果；回归 `TestExecuteQueryUsesFreshResult`。

## 实证

- `go vet`、`go test ./...` 全绿（含真库任务领取/隔离/回传/超时/审计）；
  `pnpm typecheck`/`build` 通过；069 首启套用（编号跳过 068）。
- **Server↔Agent 端到端**（本机无 docker：用真实 `Reader`/`ReadFiles`/`Redact` 代码、仅把
  docker 发现替换为指向临时目录的假清单）：签发 demo_1 实例 Token，真跑 agent →
  目标注册；灌 6 行 new-api/GIN 风格日志 + 1 个 gz 归档 + 1 个指向 /etc/passwd 的符号链接：
  无筛选返回 6 行、扫描 2 个文件（符号链接跳过、归档因时间范围排除）、Bearer/password/
  cookie 均 [REDACTED]；`request_id=req-abc123` 精确 2 行、`abc123` 部分匹配 0 行；
  `error_code=503` 命中 GIN 状态位与 `error_code=` 字段共 2 行、`900`（耗时）0 行；
  关键词与错误码 AND 正确；跨度 3 小时 400、过期 source id 409。
- 隔离与鉴权：另一个持 `logs.query` 的管理员读他人任务 404、自己列表为空；无
  `audits.read` 读审计 403；审计正文不含日志行；全局 Agent Token 轮询 401。
- 停止 reader：下一轮上报 discovery_error，目标不可用、提交 409；恢复后自愈。
- 页面 1366：来源计数、查询结果代码块、已扫描文件数渲染正常；修正前复现红色错误条。

## 记档（P3）

- `logs.query` 可查所有站点的允许容器，站点下拉只是目标选择，非授权边界（文档已写明）。
- 时间选择器按浏览器本地时区，结果行保留日志原文时间（reader 按 Asia/Shanghai 解释
  无时区时间）；浏览器不在东八区时两者会不一致。
- 每次查询至少两轮轮询（领取一轮、回传一轮），常态 5–10 秒。
- 本机无 docker，真容器发现（`docker inspect` 投影、挂载解析）只由单测覆盖，
  上线后按文档在 Linux Docker 上做一次已知日志检查。

## 部署要点

- 先 Server + 前端（069 自动套用），再 Agent；**本批动了 Agent**，Agent 包新增
  `control-tower-log-reader`、service 与安装脚本；仅更新程序不会启用，需在目标服务器
  `sudo bash ./install-log-reader.sh` 并给 agent.config 加 `CT_CONTAINER_LOG_SOCKET`。
- 该实例的 Agent 必须使用实例专属 Token。
- rc99 打在 000aff2 不含本批，上线需重打 rc100（server + 前端 + agent，069 迁移）。
