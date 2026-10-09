# ALB 访问日志接入配置（2026-10-09）

## 范围与当前状态

用户确认将连接配置保存到 CT 数据库，并将名称明确为“ALB 访问日志接入”，与 CT 自身系统日志区分。本次实现配置页面、加密保存与只读连接测试；没有启动定时采集、请求趋势、渠道榜或在途请求监控。单个全局 ALB 数据源，包含该实例下全部 Host，不关联客户。

线上用户截图为 Docker Compose 的 compose-server-1 / v2.0.0-rc157；CT 不在阿里云，使用 SLS 公网端点与专用 RAM 用户 AccessKey。截图只能确认运行镜像标签，不代表本次新功能已经上线。

当前代码在 main 工作区，未提交、推送、发布、部署；保留既有错误统计、监控设计及其他本地修改。

## 页面与存储

- 入口：系统设置 → 外部数据源 → 阿里云 ALB 访问日志接入。
- 字段：SLS 公网 Endpoint、Project、Logstore、ALB 实例 ID、AccessKey ID、AccessKey Secret。
- 独立表 alb_access_log_config，迁移 124_alb_access_log_config.sql；不写通用 system_settings，避免其整体替换与公开设置回显。
- 复用 server/internal/secrets 的 AES-256-GCM，主密钥来自既有 CT_SECRET_KEY。Secret 单独密文列；JSON/API/审计不返回或记录 Secret 明文，也不返回密文。
- 已保存 Secret 留空沿用；变更 AccessKey ID 必须提供匹配 Secret。主密钥必须保持稳定并与数据库分开备份。
- version 乐观锁防止并发覆盖，成功保存/已保存配置的测试会递增版本；冲突提示重新加载。

## API 与测试语义

- GET /api/dashboard/alb-access-log：读取脱敏配置与最近测试结果。
- POST /api/dashboard/alb-access-log/test：测试表单；若连接与已保存配置完全一致，则持久化测试结果。草稿测试不替换原配置。
- PUT /api/dashboard/alb-access-log：重新测试并保存配置和结果；云端测试失败时仍允许保存，并明确显示连接失败，而非伪装已连通。
- 三个接口均需登录管理员的 settings.manage 权限，沿用会话/CSRF/操作审计机制，不开放给旧版 Dashboard Bearer。
- 只允许阿里云标准公网 HTTPS SLS 域名；可填写带协议或 Project 前缀的 Endpoint，服务端规范化。禁止内网端点、路径、端口及跳转。
- 调用 GetLogsV2，近 15 分钟固定 SQL：按 app_lb_id 过滤的 count(*) / max(__time__) / count(request_length)，LIMIT 1。不返回原始日志，亦不读取请求体内容。
- 使用官方文档描述的 SLS V1 签名；gzip 解压后响应最大 256 KiB；总超时 15 秒，每进程最多两个并发测试。
- Incomplete 最多三次重试（间隔 200ms），最终仍未完成则单独显示，不显示部分请求数。
- success、no_data、incomplete、failed 分别展示；无匹配日志不能证明 ALB ID 存在，提示核对 ID、日志投递和时间范围。
- 保存和查询配置不会改变云端日志保留期、索引、ALB 或 NewAPI 配置。
- 当前字段 request_length 包含请求行/请求头/请求体；未来图表应保留该口径说明。ALB 完成日志不能枚举在途/卡住请求。

## 验证

已通过：
- Go test：alblog、dashboard、httpapi、auth、ingest、mysqlstore、storage。
- 同上七个 Go 包的 go vet；正式 Server 入口 go test ./server/cmd/control-tower-server 通过。
- 签名独立固定向量；gzip、无数据、未完成重试、权限/限流/不存在/异常响应、重定向、取消、危险端点及 SQL 输入校验。
- 管理权限、密文保存、明文不回显、Secret 留空保留、ID 更换、草稿隔离、版本冲突、失败结果保存、审计脱敏。
- 独立本机 MySQL 8 临时库专项 TestALBLogConfigPersistence：迁移、读写、插入/更新冲突、加密字段分离、测试结果重载。测试结束清理本次临时库，未修改既有库。
- Web 4 项行为回归：修改后失效旧结果、保存清除 Secret、失败状态、乱序/卸载保护、加载失败与冲突。
- pnpm typecheck / pnpm build；仍有既有大 chunk 提示。
- Chrome 正式构建 + 模拟 API：测试/保存两次请求、1440/390 视口、无脚本异常或横向溢出。截图在 outputs/alb-access-log/，已检查。

待验证：
- 尚未使用真实 AccessKey 调用用户 SLS；公网网络、RAM 权限、实际 SLS 签名/响应与字段索引，必须上线后点击“测试连接”验收。
- 浏览器结果是模拟数据，不能当作真实 SLS 已连通。
- 无本次生产部署或压力测试；未执行周期性查询。

## 上线要求与后续

更新 Server/Web，正常启动执行 124 迁移；Agent 不变。不需要新增 SLS 环境变量，但 CT_SECRET_KEY 必须已配置。线上 rc157 尚不含本次功能，需要后续发布。

用户在页面自行填入凭证。配置示例 Project=pinducloud-alb-logs、Logstore=alb-access-log；Endpoint 和 ALB ID 从实际控制台复制，不将截图猜测值作为默认值。

下一阶段另行实现分钟聚合采集及四档趋势（<5、5–10、10–20、≥20 MiB），同时展示数据新鲜度。该方案不能替代业务渠道统计或在途事件。

官方协议参考：
- [GetLogsV2](https://help.aliyun.com/zh/sls/developer-reference/api-sls-2020-12-30-getlogsv2)
- [SLS V1 请求签名](https://help.aliyun.com/zh/sls/developer-reference/request-signatures/)
