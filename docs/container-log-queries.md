# 容器日志查询（第一版）

## 使用

数据查询 → 容器日志。跟随顶部所选站点，自动查询该站点全部在线可用日志来源，无需选择服务器或容器，默认最近 15 分钟。
结果按来源分别显示；离线实例、不可用来源和提交失败会提示查询不完整。
顶部工具栏提供时间及关键词，“更多筛选”展开 Request ID 和错误码；所有条件按 AND 匹配。
关键词按日志原文包含匹配（区分大小写、最多 128 字），不支持正则或命令。历史记录在抽屉中查看。
Request ID 按完整标识边界匹配，不做部分字符串匹配。错误码只匹配文本或 JSON
中的 `code`、`error_code`、`status`、`status_code`、`status code` 字段及 GIN 访问日志的 HTTP 状态位，不匹配正文中任意数字。
只有日志实际包含标识时才会匹配；数据库使用记录与应用日志文件不是同一份日志。

权限 `logs.query` 与现有“使用日志”权限分开。已有全权限管理员可用，受限管理员需
显式授权。第一版该权限可查询所有已接入站点的允许容器，站点下拉只是查询目标选择，
不是访问授权边界；不包含按管理员分配服务器范围的功能。仅查询人本人可查看任务和
日志正文，包括全权限管理员也不能通过任务 ID 读取别人的结果。`audits.read` 可以
查看查询人、查询条件、目标及状态，不获得日志正文。viewer 权限保持不变。

## 架构与限制

- 独立日志读取服务仅监听本机 Unix Socket；只提供 GET `/containers` 和 POST `/query`。
- 日志读取服务拥有 Docker 访问权限，属于可信高权限组件；Agent 不因此加入 docker 组，
  不向浏览器开放 Socket 或通用 Docker API。默认自动发现 new-api，容器限定名单可在本机配置。
- 只用固定参数执行 `/usr/bin/docker ps -a` 和 `docker inspect --format`，检查启动程序、
  镜像、容器名、启动参数和挂载信息；不读取 Config.Env，也不执行 Shell 或 docker exec。
- 自动模式要求启动程序确认为 new-api/newapi；仅名称或镜像相似的候选会显示不可用原因。
  按 `--log-dir` 启动参数解析目录，未指定则探测 `/app/logs`，再解析最具体的 bind/volume
  挂载。没有可读挂载时明确失败，不退回 docker logs，不读取容器 writable layer。
- 每分钟刷新发现信息，每次执行查询前强制重新发现；来源 ID 绑定容器 ID、目录及时区。
  容器重建或来源变化后拒绝旧任务，刷新页面目标后可重新查询。多个候选在页面选择。
- 文件读取限定在发现的目录内，跳过符号链接和非普通文件；用户不能指定路径或命令。
  读取当前 `.log`、轮转日志与 `.gz`，每条返回内容带文件名及行号。
- Agent 配置后每 5 秒单独轮询，不占用指标采集循环；使用实例专属 Agent Token，拒绝
  旧的全局 Token。Token 按实例鉴权，同一实例的 Agent 属于同一信任范围。
- 最近 7 天内，单次时间跨度 ≤1 小时；按修改时间从新到旧扫描最多 256 个文件、
  64 MiB 解压后内容；目录遍历最多 4096 项、有限层级，单行/记录最多 256 KiB；
  最多返回 2,000 行 / 512 KiB。达到任一上限标记截断，结果不能视为完整历史搜索。
  大文件从头扫描，可能在到达近期内容前触及扫描上限；缩小查询范围不会降低全部文件的
  扫描量，必要时先在服务器轮转日志。此版本不建立全文索引。
- 根据每条记录时间筛选，包含开始时间、不包含结束时间。支持 new-api/GIN 文本时间、
  RFC3339 和 JSON 字符串 time/timestamp/ts/created_at。无时间的续行继承上文时间；
  无可识别上文的行会跳过并提示。不支持任意自定义时间格式。
  无时区时间默认按 Asia/Shanghai 解释，页面显示此时区；可配置 CT_LOG_TIMEZONE。
  文件已删除、未挂载或日志未实际记录时，不能恢复对应历史。
- 执行限时 30 秒。单个目标最多 5 个排队 / 执行任务，同时只执行 1 个；等待超过
  2 分钟或领取超过 90 秒未回传标记超时。领取响应丢失或进程退出不会自动重新执行，
  用户看到超时后可重新提交。结果上传支持幂等重试，不覆盖已完成任务。
- 查询正文和结果保留 24 小时，在查询或轮询时清理；停止服务期间不会定时清理。
  审计元数据遵循现有审计保留规则，查询结果不写入审计。
- 常见 authorization、API key、password、token、cookie 等内容会在回传前脱敏。
  这不等于任意业务日志的完整敏感信息识别；容器允许范围应由管理员按内容确定。
- 发现错误、目录不可读、压缩损坏及跳过内容会明确显示；不回传 Docker 原始错误正文。

## 部署

先更新 Server + 前端，启动时自动执行 `069_container_log_queries.sql`；然后更新 Agent。
Agent 发布包新增 `control-tower-log-reader`、对应 service 和安装脚本。旧 Agent 不支持
日志轮询，因此不会出现在可查询列表中。仅更新程序不会自动启用日志读取。

在需要查询的 Linux amd64 Docker 服务器上，进入解压后的新版 Agent 包目录：

```bash
sudo bash ./install-log-reader.sh
```

无需预先填写容器名。安装脚本保留现有 Agent 配置和日志读取配置。
通常能直接发现如 `new-api-prod` 的容器，并解析挂载到 `/app/logs` 的命名卷。
日志目录必须已挂载，安装脚本不会修改容器或 Compose。

可选配置文件 `/etc/control-tower/log-reader.env`（编辑已有文件，保留其他项）：

```ini
# 仅自动识别失败或需限定容器时设置；不设置则自动发现。
CT_LOG_CONTAINERS=new-api-prod
# 无时区日志的实际时区，默认 Asia/Shanghai。
CT_LOG_TIMEZONE=Asia/Shanghai
```

手动指定容器仍要求目录为可读日志挂载，不开放任意命令或文件路径。
修改此文件后重启 `control-tower-log-reader`。

在现有 `/etc/control-tower/agent.config` 增加一行，保留其他配置：

```ini
CT_CONTAINER_LOG_SOCKET=/run/control-tower-log-reader/reader.sock
```

确认 `CT_AGENT_TOKEN` 使用该实例的专属 Token，然后：

```bash
sudo systemctl restart control-tower-agent
sudo systemctl status control-tower-log-reader --no-pager
sudo journalctl -u control-tower-agent -n 30 --no-pager
```

完成首次发现后约 5 秒页面出现目标。停用时删除 Agent 的 Socket 配置并重启 Agent，再停止 reader。
Socket 权限为 root:ct-agent 0660，服务目录为 0750。不要给查询 Agent 增加 sudo 或 docker
组权限；不要将读取服务绑定公网。读取服务使用 systemd 管理，配置改动需重启读取服务。

## 验证

自动测试覆盖固定 Docker 发现参数、容器重建校验、挂载遮蔽、时间与压缩历史日志、
非法容器与命令字符拒绝、AND / 精确匹配、常见凭据脱敏、
扫描结果限制、实例 Token 要求，以及真实 MySQL 的任务领取、跨用户结果隔离、跨 Agent
结果隔离、重复回传、队列超时与审计。测试不更改真实账号密码或查询生产日志。

Linux Docker 上线后还需用已知测试日志检查：时间范围、请求 ID、错误码匹配，停止
读取服务后的失败提示，以及不允许容器被拒绝。Windows 开发环境不代表已验证生产 Docker。
