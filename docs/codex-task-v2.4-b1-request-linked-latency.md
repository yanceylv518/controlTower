# Codex 任务：v2.4-B1——Request ID 关联的延时分诊

## 目标

把 Nginx timing 慢样本与 new-api 使用日志可靠关联，使延时分诊能够回答“哪个实例、用户、渠道、模型、令牌发生了慢请求”。关联键使用生产验证过的 `instance_id + request_id`，禁止按时间邻近做模糊匹配。

本批不进入 new-api 请求链路，不修改 new-api 源码、路由或数据库结构；Nginx 仍由运维侧配置，Control Tower 只读取日志。

## 已验证前提（2026-07-16）

生产环境已完成以下验证：

- new-api 响应头 `X-Oneapi-Request-Id` 与使用日志详情中的 `Request ID` 完全一致；
- 通用 `X-Request-Id` 是另一套 ID，不能作为 new-api 使用日志的关联依据；
- Nginx `timed` 格式使用 `request_id=$upstream_http_x_oneapi_request_id` 后，可在 access log 中记录业务 Request ID；
- 部分非业务接口或未携带该响应头的请求会记录 `request_id=-`，必须作为“未关联”样本处理；
- Agent 停止与否不影响 new-api 写入使用日志，也不影响上述 Nginx 验证。

生产 Nginx 目标格式：

```nginx
log_format timed '$remote_addr - "$request" [$time_local] '
                 'status=$status request_id=$upstream_http_x_oneapi_request_id '
                 'rt=$request_time '
                 'uct=$upstream_connect_time '
                 'uht=$upstream_header_time '
                 'urt=$upstream_response_time '
                 'bytes=$body_bytes_sent req_len=$request_length';
```

## 设计原则

1. **精确关联**：只按 `instance_id + request_id` 关联，绝不按时间戳猜测。
2. **失败安全**：缺失、无效或无法关联的 request_id 不得导致 Agent 退出、上报失败或企业微信告警受影响。
3. **只加不改**：上报/API 字段均为可选增量；旧 Agent、旧 Server 和无 request_id 日志继续兼容。
4. **隐私最小化**：不采集 Authorization、请求体、响应体、IP 或 query；path 继续剥离 query。
5. **语义准确**：`uht` 标为“上游首响应时间”，不是精确模型 TTFT；若后续从 new-api 日志取得精确首字字段，另起批次。

## 工作项

### 任务 1：Agent 解析与上报

修改 `agent/internal/nginxtiming`：

- `Entry`、`SlowSample` 增加 `RequestID string`；
- `ParseLine` 解析 `request_id=`，`-`、空值或异常超长值归一为空；
- request_id 设置合理长度上限，超限只丢弃该字段，不丢整行 timing 数据；
- 聚合桶不按 request_id 拆分，只有慢样本携带 request_id，避免基数膨胀；
- `NginxSlowSamplePayload` 增加可选 `request_id`，沿用本地缓冲和成功出队机制；
- 不改变独立告警、MySQL logs 采集、企业微信提醒或现有游标。

测试覆盖：正常 ID、`-`、缺字段、超长值、字段顺序变化、旧格式兼容、慢样本透传。

### 任务 2：Server 契约、迁移与存储

- Agent Gateway 合约增加可选 `request_id`；
- 新增迁移 `009_nginx_sample_request_id.sql`：为 `nginx_slow_samples` 增加可空 `request_id`，并增加 `(instance_id, request_id)` 索引；
- MySQL Store 和 Memory Store 写入、查询时保留该字段；
- 原有慢样本幂等规则保持不变，重复上报不得产生重复记录；
- request_id 为空时正常保存 timing 样本，API 标记为未关联。

迁移必须可在已有生产数据上执行；历史行保持 NULL，不做回填。

### 任务 3：关联 new-api 日志维度

关联来源使用 Control Tower 已采集的日志数据，不反向连接生产 new-api 数据库。**关联源修正（2026-07-16 验收发现）**：生产默认的 `aggregate_with_samples` 模式下 `log_events` 表为空（仅 `full_debug` 模式填充），**主关联源必须是 `log_samples`**——该表已含 user_id/username/channel_id/model_name/token_name/request_id 且有 `idx_log_samples_request_id` 索引；`log_events` 作为次级源（full_debug 部署时优先，条数更全）。两表都查、按来源合并去重：

- 按 `instance_id + request_id` 批量查询匹配日志（先 log_samples 后 log_events），避免逐样本 N+1；
- 为匹配样本补充 `user_id`、`channel_id`、`model_name`、令牌显示名（仅已有安全展示字段）；
- 名称优先走现有 name resolver，返回用户/渠道/实例可读名称，同时保留原始 ID；
- 一个 request_id 对应多条日志时，返回匹配数量和明确状态 `multiple`，不得静默任选造成错误归因；
- 关联状态统一为 `matched`、`unmatched`、`multiple`；旧样本无 ID 为 `unmatched`。

**命中率预期（写进交付说明与页面空态文案）**：log_samples 是采样数据（每报最多 50 条,错误优先,慢阈值 10s 与 nginx 慢样本对齐）——正常流量下 rt≥10s 的 nginx 慢样本绝大多数可关联;错误风暴期采样截断会产生 unmatched,这是设计内行为,不是缺陷。不得为提高命中率而放宽为时间邻近匹配。

### 任务 4：Dashboard API

扩展现有：

```text
GET /api/dashboard/nginx-timing/slow-samples
```

每条样本增量返回：

```text
request_id, match_status, match_count,
user_id, user_name,
channel_id, channel_name,
model_name, token_name
```

并增加可选过滤参数：`user_id`、`channel_id`、`model_name`、`match_status`。原参数和旧响应字段不变；非法过滤参数返回 400；limit 上限保持现有约束。

### 任务 5：Web 延时分诊

在 `/latency` 慢样本区完成：

- 展示用户、渠道、模型、令牌、Request ID 和关联状态；
- 支持按用户、渠道、模型及“未匹配”过滤；
- Request ID 可复制，并提供到对应维度页的跳转；
- `matched` 正常展示，`multiple` 显示“多条重试/尝试”，`unmatched` 显示“未匹配”，不得伪造维度；
- 保留 `rt`、`uht`、`urt`，文案分别为“总耗时”“上游首响应”“上游总耗时”；
- 分钟聚合趋势图维持实例级语义，不声称已有维度级全量延时分布。

## 明确不做

- 不修改 new-api 源码、数据库结构或请求中间件；
- 不让 Agent 修改 Nginx 配置；
- 不按时间近邻关联；
- 不把 `uht` 宣称为精确 TTFT；
- 不把所有请求明细持久化为 timing 样本，本批仍以慢样本为主；
- 不新增告警、企业微信消息或自动调权动作。

## 验证要求

### 自动化

1. `go test ./...`、`go vet ./...`；
2. `pnpm build`、`pnpm test`；
3. 迁移契约：009 编号、InnoDB/utf8mb4 约束、索引存在、历史 NULL 行兼容；
4. Agent parser/aggregator/reporter 全链路 request_id 测试；
5. Server 单条匹配、无匹配、多条匹配、批量查询无 N+1、旧 payload 兼容测试；
6. Dashboard 过滤、空态、非法参数和权限测试。

### 手工端到端

1. 生产或脱敏测试日志中追加带 `request_id` 的 timed 行；
2. 确认 Agent 上报成功且不影响既有 logs 采集和企业微信告警；
3. 确认 `nginx_slow_samples.request_id` 入库；
4. 使用同一 ID 在 new-api 使用日志与 Control Tower 延时分诊中定位；
5. 核对用户、渠道、模型、令牌与 new-api 页面一致；
6. 追加 `request_id=-` 和不存在 ID，页面显示未匹配且服务无错误；
7. 制造同 ID 多日志，页面显示 multiple 而非任意归因。

## 实施顺序与提交建议

1. Agent 解析、契约及单测；
2. 009 迁移、Server 存储和关联查询；
3. Dashboard API 与前端；
4. 全量测试、端到端验证和交付文档；
5. 更新 `docs/development-progress.md` 与 `docs/iteration-log.md`，再按发布流程制作下一 RC。

建议功能提交：

```text
feat(latency): correlate nginx samples with request dimensions (v2.4-B1)
```

## 开发前置检查

- [x] 每次修改前 `git pull --ff-only origin main`
- [x] 工作区无无关改动；若有，提交前先向用户确认
- [x] 真实 Token、Webhook、DSN、Request 内容不入库
- [x] 最新生产日志格式仍包含 `request_id=$upstream_http_x_oneapi_request_id`

