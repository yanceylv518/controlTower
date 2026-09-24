# Agent API Contracts


## NewAPI model square (2026-09-18)

- `GET /api/dashboard/model-square?instance_id=<site>` reads NewAPI `/api/pricing`; `POST` refreshes the same read-only cache. Requires existing admin `models.manage` access. Returns `items`, `vendors`, `group_ratios`, `usable_groups`, `source`, `updated_at`, `expires_at`, `stale`, optional `warning`.
- Process-local cache TTL 5 minutes, keyed by site and API configuration fingerprint; successful refresh cooldown 10 seconds, failure backoff 60 seconds. On upstream failure, last successful data is returned with stale/warning; no prior data returns 502. Missing site API configuration returns 422. Restart clears cache; no model records are independently maintained.
- Explicit retirement requested by user: legacy `/billing/models` now aliases the square response/refresh (GET shape changed); model PUT and price/group-ratio writes are disabled. `/billing/group-ratios` GET reads the same NewAPI cache in legacy items shape. `/billing/prices` GET remains historical configuration read only. Historical invoice records and recalculation behavior are preserved. Upgrade frontend and Server together; external consumers of legacy model response must migrate.

## Global menu visibility (2026-09-18)

- `GET /api/dashboard/menu-visibility`: authenticated admins and viewers receive `{ "items": { "/tuning": false } }`. Missing menu paths default to visible. Response uses `Cache-Control: no-store`.
- `PUT /api/dashboard/menu-visibility`: session requires `settings.manage` (existing trusted dashboard bearer token also supported). Body has the same `items` map; replaces the whole configuration. Unknown paths, non-booleans, null items/values, extra fields, and trailing JSON are rejected with 400. Successful updates create `menu_visibility.update` audit records.
- Hidden entries are absent from every role's sidebar regardless of permissions. Showing an entry does not grant access. This API does not change page/API authorization; authorized admins can directly access `/settings` to restore hidden settings navigation.
- Persisted in the singleton `menu_visibility` table introduced by migration `083_menu_visibility.sql`. General system-setting replacement cannot overwrite it. Clients refresh on navigation/focus and every 30 seconds while visible; this is not push delivery.

> **Dashboard API v1 — 契约冻结（2026-07-13）：此后仅允许向后兼容的新增，禁止修改既有字段语义。**

Control Tower Agent reports to Control Tower Server through outbound HTTPS. The Agent does not expose an inbound port.

## Authentication

Phase 1 uses an HTTP `Authorization: Bearer <agent-token>` header. Tokens are generated and stored by Control Tower Server in later phases. Tokens must never be logged or returned to frontend clients.

## Common Fields

Every Agent request includes:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `instance_id` | string | yes | Control Tower instance ID. |
| `agent_id` | string | yes | Stable Agent ID. |
| `agent_version` | string | yes | Agent binary version. |
| `reported_at` | RFC3339 timestamp | yes | Agent-side report time. |
| `sequence` | integer | yes | Monotonic Agent sequence. |

## POST `/api/agent/heartbeat`

```json
{
  "instance_id": "inst-hdu",
  "agent_id": "agent-hdu-01",
  "agent_version": "0.1.0",
  "reported_at": "2026-07-02T12:00:00Z",
  "sequence": 7,
  "last_log_id": 12345
}
```

## POST `/api/agent/report`

```json
{
  "instance_id": "inst-hdu",
  "agent_id": "agent-hdu-01",
  "agent_version": "0.1.0",
  "reported_at": "2026-07-02T12:00:00Z",
  "sequence": 42,
  "last_log_id": 1001,
  "metric_batch_id": "agent-hdu-01:1001:1001",
  "log_events": [
    {
      "source_log_id": 1001,
      "created_at": "2026-07-02T11:59:00Z",
      "log_type": "consume",
      "user_id": 7,
      "username": "alice",
      "channel_id": 18,
      "model_name": "gpt-4o",
      "token_id": 9,
      "token_name": "prod-token",
      "prompt_tokens": 30,
      "completion_tokens": 70,
      "total_tokens": 100,
      "quota": 500,
      "use_time": 3.2,
      "is_stream": true,
      "group": "default",
      "request_id": "req-1",
      "upstream_request_id": "up-1",
      "error_summary": "",
      "cache_tokens": 128,
      "cache_field_present": true
    }
  ],
  "server_metrics": [
    {
      "collected_at": "2026-07-02T12:00:00Z",
      "cpu_percent": 20.5,
      "memory_used_percent": 66.1,
      "disk_used_percent": 71.2,
      "network_rx_bytes_per_second": 1000,
      "network_tx_bytes_per_second": 2000,
      "load_1m": 0.7
    }
  ],
  "docker_statuses": [
    {
      "collected_at": "2026-07-02T12:00:00Z",
      "container_name": "new-api",
      "status": "running",
      "running": true
    }
  ],
  "health_checks": [
    {
      "checked_at": "2026-07-02T12:00:00Z",
      "target": "new-api",
      "status": "healthy",
      "http_status_code": 200,
      "latency_ms": 15,
      "error_summary": ""
    }
  ]
}
```

## Safety Rules

- Do not send full request bodies.
- Do not send full response bodies.
- Do not send full `Authorization`, API Key, Cookie, or upstream secret values.
- `cache_tokens` is `null` when the field is unavailable.
- `cache_field_present=false` means cache fields were not present, not that the value was zero.
- `metric_batch_id` is stable for the same source-log range; retries reuse it so metric ingestion is idempotent.
- Compressed request bodies are limited to 2 MiB and decoded bodies to 8 MiB.
- Report arrays have server-side item limits; oversized reports return HTTP 413.

## Dashboard Auth API

- `POST /api/auth/login` accepts username/password and sets the HttpOnly, SameSite=Strict `ct_session` cookie.
- `POST /api/auth/logout` deletes the session and clears the cookie.
- `GET /api/auth/me` returns the current username and role.
- `POST /api/auth/password` changes the password and invalidates the current session; new passwords require at least eight characters.

Cookie-authenticated non-GET Dashboard requests require `X-Requested-With: XMLHttpRequest`. Legacy `Authorization: Bearer <dashboard-token>` remains supported without this browser CSRF header.

## Instance Management API

Dashboard-authenticated endpoints provide `GET/POST /api/dashboard/instances`, `PUT /api/dashboard/instances/{id}`, and `POST /api/dashboard/instances/{id}/rotate-token`. Creation and rotation return the new Agent token exactly once; lists never expose token plaintext or hashes. Rotation keeps previous active tokens valid for 24 hours. Disabling an instance rejects all of its instance tokens immediately.

## Per-instance Agent Authentication

Instance tokens are stored only as `SHA-256(pepper + token)` hashes. A token may report only the matching `instance_id`; mismatch returns HTTP 403 `instance_mismatch`. Invalid, expired, or disabled-instance tokens return HTTP 401. The global `CT_AGENT_TOKEN` remains accepted temporarily as an unbound compatibility path.

## Alert Timeline and Notification Operations

- `GET /api/dashboard/alerts/{id}/events?limit=100` returns chronological lifecycle events with `event_type`, `actor`, `note`, and `created_at`.
- Alert actions accept an optional `note` of at most 500 characters; session users and legacy token callers are recorded as the event actor.
- `POST /api/dashboard/notification-deliveries/{id}/resend` resets a failed or exhausted delivery for the notification runner.
- Notification channels accept an optional DingTalk `secret`. List responses expose only `has_secret`; secret values are never returned. DingTalk requests include the timestamp/HMAC signature query parameters when configured.

## Dashboard API v1 Endpoint Catalog

除登录接口外均需 Session Cookie 或 Dashboard Bearer Token；Cookie 写请求还需 `X-Requested-With: XMLHttpRequest`。列表统一响应 `{"items":[]}`，时间为 RFC3339。

### 认证

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `POST /api/auth/login` | JSON `username,password`；同 IP 每分钟最多 10 次 | `{"username":"admin","role":"admin"}` + `ct_session` |
| `POST /api/auth/logout` | 无 | `{"ok":true}` |
| `GET /api/auth/me` | 无 | `{"username":"admin","role":"admin"}` |
| `POST /api/auth/password` | JSON `old_password,new_password` | `{"ok":true}` |

### 实例与 Agent

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `GET /api/dashboard/instances` | 无 | `{"items":[{"instance_id":"inst-x","name":"prod","enabled":true,"agents":[]}]}` |
| `POST /api/dashboard/instances` | JSON `instance_id,name` | `{"instance":{...},"token":"仅返回一次"}` |
| `PUT /api/dashboard/instances/{id}` | JSON `name,enabled` | `{"instance_id":"inst-x","enabled":true}` |
| `POST /api/dashboard/instances/{id}/rotate-token` | 无 | `{"token":"仅返回一次"}` |
| `GET /api/dashboard/agents` | Query `instance_id,limit,offset` | `{"items":[{"id":"agent-1","instance_id":"inst-x"}]}` |

### 指标、历史与用量

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `GET /api/dashboard/overview` | Query `instance_id`, optional `site` | `{"cards":[],"runtime":{...}}` |
| `GET /api/dashboard/metrics` | Query `window,instance_id,site,dimension_type,dimension_key` | `{"items":[{"window":"1m","request_count":10}]}` |
| `GET /api/dashboard/metric-history` | Query `window,instance_id,site,dimension_type,dimension_key,since` | `{"items":[{"bucket_time":"...","request_count":10}]}` |
| `GET /api/dashboard/usage` | Query `window,instance_id,limit` | `{"items":[{"dimension_key":"user:7","quota":100}]}` |

### 日志与运行态

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `GET /api/dashboard/logs` | Query `instance_id,user_id,channel_id,model_name,log_type,request_id,start_time,end_time,limit,offset` | `{"items":[{"source_log_id":1,"log_type":"error"}]}` |
| `GET /api/dashboard/log-samples` | 同日志过滤，另含 `sample_kind` | `{"items":[{"sample_kind":"error"}]}` |
| `GET /api/dashboard/server-metrics` | Query `instance_id,start_time,end_time,limit,offset` | `{"items":[{"cpu_percent":12.5}]}` |
| `GET /api/dashboard/health-checks` | Query `instance_id,target,status,limit,offset` | `{"items":[{"target":"new-api","status":"healthy"}]}` |
| `GET /api/dashboard/docker-statuses` | Query `instance_id,container_name,running,limit,offset` | `{"items":[{"container_name":"new-api","running":true}]}` |
| `GET /api/dashboard/channel-snapshots` | Query `instance_id,channel_id,start_time,end_time,limit,offset`; rc20 returns current channel state only | `{"items":[{"channel_id":7,"status":"enabled"}]}` |

### 告警与时间线

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `GET /api/dashboard/alerts` | Query `instance_id,status,severity,active_only,limit,offset` | `{"items":[{"id":"a1","status":"firing"}]}` |
| `POST /api/dashboard/alerts/action` | JSON `id,action,note,silence_until` | `{"ok":true}` |
| `GET /api/dashboard/alerts/{id}/events` | Query `limit` | `{"items":[{"event_type":"acknowledged","actor":"admin","note":"checked"}]}` |

### 通知

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `GET /api/dashboard/notification-channels` | 必填 Query `site_id`；旧渠道待分配列表使用 `unassigned=true` | `{"items":[{"id":"c1","site_id":"site-a","rule_keys":["user_low_balance"],"channel_type":"dingtalk","has_secret":true}]}` |
| `POST /api/dashboard/notification-channels` | JSON `id,site_id,rule_keys,channel_type,name,webhook_url,enabled,secret` | `{"items":[{"id":"c1","site_id":"site-a","rule_keys":["user_low_balance"],"has_secret":true}]}` |
| `GET /api/dashboard/notification-deliveries` | 必填 Query `site_id`；可选 `alert_id,channel_id,status,limit,offset,start_time,end_time,search`；时间为 RFC3339、起点包含/终点不含，search 匹配告警标题或摘要 | `{"filters_supported":true,"items":[{"id":"d1","status":"failed","attempts":1,"alert_title":"告警标题","alert_summary":"告警摘要"}]}` |
| `POST /api/dashboard/notification-deliveries/{id}/resend` | 必填 Query `site_id`，其他站点记录返回 404 | `{"ok":true}` |

通知渠道必须绑定单个有效站点，已绑定渠道不能跨站点更新（409）。站点和 `rule_keys` 同时匹配才投递；空 `rule_keys` 表示该站点全部告警类型，未知类型返回 400。多渠道匹配则分别投递，未匹配不回退到其他站点。前端按余额、系统、请求三类选择，保存时展开成具体规则。旧的部分规则选择保持原样直到编辑保存，并明确标为“部分规则”。

三个全局开关 CT_NOTIFICATIONS_ENABLED、CT_NOTIFY_BALANCE_ONLY、CT_BALANCE_ALERT_ENABLED 已退役，数据库和环境中的旧值不再影响告警。余额仍仅为显式启用的用户计算，通知由各站点渠道启用状态和类型决定。

金额显示不再使用 CT_QUOTA_PER_UNIT、CT_CURRENCY_SYMBOL。GET `/api/dashboard/passthrough/currency?site=...` 读取 NewAPI 站点配置，返回有效 `quota_per_unit`（已含站点显示汇率）、`price_multiplier`（美元单价转站点显示单位）、`symbol`、`type`。viewer 固定到授权站点。失败返回 503，前端金额显示“—”；余额通知回退明确标识的原始 quota。历史账单继续使用其定价快照。需同步升级前后端，无新增迁移。

只读使用日志 `GET /api/dashboard/passthrough/logs` 兼容 rc35 的 `type`、`channel`、`p`、`page_size` 参数以及 CT 旧的 `log_type`、`channel_id`、`offset`、`limit` 参数；另支持 `username`、`token_name`、`model_name`、`group`、`request_id`、`upstream_request_id`，时间可用 RFC3339 `start_time/end_time` 或 rc35 秒级 `start_timestamp/end_timestamp`。响应保留 `content_summary` 脱敏摘要，同时提供兼容字段 `content`、`channel`、`channel_id`、`channel_name`、`token_id`、`other`、`has_more`、`page/page_size`；`fallback` 表示同一请求是否实际尝试过多个渠道，`fallback_channels`（仅管理员）保留可确认的有序渠道链，通常只有最终尝试记录带完整的 `191 → 141` 链路。服务端同时兼容 `other.fallback`/`other.is_fallback`/`other.fallback_flag` 和 `other.admin_info.use_channel`，并按当前页请求 ID 批量关联其它尝试记录，因此首个错误尝试也不会丢失 fallback 标志。管理员渠道名称在 `channels` 权限不可用时退化为 ID。`GET /api/dashboard/passthrough/logs/count` 与 `/stat` 复用同一筛选口径，统计完整小时优先使用只读日志聚合，带请求 ID 或模糊模型/用户名筛选时回源原表。viewer 站点/用户范围由服务端强制注入，同一用户同一 Request ID 只保留最后结果，`other` 按角色剥离特权元数据。

编辑时 Webhook 地址留空保留原值；同类型渠道 Secret 留空保留原密钥。省略 `rule_keys` 保留原选择，显式 `[]` 改为全部类型。响应始终只返回脱敏地址和是否有密钥。

升级需应用 `075_notification_routing.sql` 并同步更新前后端。历史渠道 `site_id` 为空，暂停投递，在通知设置“旧渠道待分配”中确认归属并保存后恢复。旧投递记录按原告警所属实例的站点查询，避免随渠道分配而串站点。Agent 独立企微直推不使用此路由配置。

### 渠道命令与审计

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `POST /api/dashboard/channels/{channelID}/commands` | JSON `instance_id,confirm,status?,weight?,priority?,group?`；`confirm` 必须为 `true`；提供 `group` 时只能使用该站点渠道快照中已有的分组 | `201 {"id":"...","instance_id":"inst-x","channel_id":7,"status":"pending","payload":{"status":2},"created_by":"admin","created_at":"..."}` |
| `GET /api/dashboard/channel-commands` | Query `instance_id,status,limit,offset` | `{"items":[{"id":"...","status":"succeeded","payload":{"status":2}}]}` |
| `GET /api/dashboard/operation-audits` | Query `instance_id,site_id,actor,operation_type,status,q,from,to,limit,offset`；`from,to` 为 RFC3339，时间范围左闭右开；`actor_options=true` 时仅返回最多 100 个按 `actor` 模糊匹配的去重操作人；需管理员的 `audits.read` 权限 | `{"items":[],"total":0,"operation_types":["settings.update","auth.login","auth.logout"],"actors":null}`；操作类型由固定目录返回，不依赖当前记录；候选查询返回 `actors` 数组 |

调权中心分组操作使用以下站点级接口：

操作审计的 `actor_exact=true` 可与 `actor` 配合精确匹配账号；默认仍保持模糊匹配兼容。关键词 `q` 和操作人模糊匹配中的 `%`、`_` 不作为 SQL 通配符。

操作审计性能扩展（095迁移）：

- `list_only=true`：只查当前页及一条探测记录，不执行总数统计，返回 `total=-1` 与 `has_more`。不传此参数仍兼容原分页与精确总数。
- `count_only=true`：仅返回当前筛选的总数（`items=[]`），忽略分页条件；与 `list_only=true` 互斥。MySQL按完整筛选条件缓存30秒，每个Store最多128项且同时只执行一个计数，等待可取消。
- `before_time`（RFC3339，可含微秒）和 `before_id` 必须成对提供，用于按 `created_at DESC,id DESC` 查询游标之后的记录；不能与非零 `offset` 同用。`has_more`决定下一页是否可用，与缓存总数无关。
- `request_id` 为精确筛选；`correlation_id`、`source`、`trigger` 沿用原筛选语义。页面提供独立“请求ID精确”模式，内容搜索仍用 `q`。
- 查询继承HTTP取消，列表/计数最多8秒，操作人候选最多5秒。计数失败不影响独立列表请求。
- 页面默认浏览器本地当天00:00:00至23:59:59，转换为UTC传递 `[from,to)`（to为次日零点）；重置恢复当天。分页使用上一页/下一页，不再按任意页码执行大OFFSET。

| 方法与路径 | 参数 | 响应 |
| --- | --- | --- |
| `GET /api/dashboard/tuning/channels?site_id=` | 返回站点内每个渠道的最新名称、状态、模型、权重、优先级和 `group_name`；包含禁用渠道及多模型渠道 | `200 {"items":[{"channel_id":7,"channel_name":"primary","status":"enabled","models":["gpt-4o"],"group_name":"default,vip"}]}` |
| `GET /api/dashboard/tuning/groups?site_id=` | 返回当前站点可选的完整分组名称；直连站点读取 New API `/api/group/`，无直连站点从全渠道快照汇总 | `200 {"items":["default","vip"]}` |
| `PUT /api/dashboard/tuning/channels/{channelID}/group?site_id=` | JSON `{"confirm":true,"group":"default,vip"}`；`group` 是逗号分隔的组合，服务端去首尾空格并去重，且每个分组必须已出现在当前站点分组目录中。空字符串表示清空全部分组；组合不得包含空项、控制字符，规范化后最多 128 个 Unicode 字符；未知分组返回 `400 group_not_found` | 直连站点成功返回 `200`，Agent 站点排队返回 `202`，两者均返回 `command_id,channel_id,group,status,created_at` |

分组更新沿用 `tuning.manage` 权限和命令状态机 `pending → delivered → succeeded|failed`。直连写入成功后立即同步 `channel_current`；Agent 只有在成功回报后才同步。队列命令保留 `before_group` 内部值，完成审计包含操作人、旧分组、新分组和执行结果；失败不会覆盖当前分组。`site_id` 必须与渠道的最新快照归属一致，未知渠道返回 `404 channel_not_found`。

Agent 需要与 Server 一起升级到支持 `group` 字段的版本；旧 Agent 会忽略该字段，不应领取新的分组命令。

## v2.9-B2 Duty-Rotation Tuning (observe and confirm)

### Server channel inventory source (2026-09-16)

- A nonempty site `logs_readonly_dsn` makes Server the channel inventory owner. Server selects channel ID/name/status/weight/models/group/priority from the NewAPI `channels` table at startup and about once per minute (operational runners only), and when the existing channel refresh endpoint is called. The readonly account must have SELECT access to these columns. No channel keys are read and no source database writes are issued.
- Agent requests and binaries are unchanged. Server ignores both complete and partial Agent channel snapshots for configured sites, including when the readonly query fails; logs, metrics and command results retain their existing paths. Removing the readonly configuration re-enables Agent inventories. Sites without readonly configuration retain the existing Agent/HTTP refresh behavior.
- Query errors, cancellation and lists exceeding 5,000 channels preserve the previous snapshot. Server checks source configuration again inside the persistence transaction. Successful complete lists update all enabled instance views of the site from one source and preserve writes newer than the collection start; a genuinely empty successful list removes absent channels.
- Same-model base values and circuit state are retained during refresh. Agent list cleanup checks all enabled collectors in the site before deleting shared anchors/state. Existing model reassignment and mixed-model eligibility rules remain in effect. This change does not recover already-lost manual base values and does not change channel write/control behavior.

- `GET|PUT /api/dashboard/tuning/policy?instance_id=` reads or writes the instance policy. Supported modes are `observe`, `confirm`, and `auto`. In `auto`, action recommendations are persisted first and then atomically converted into auditable channel commands.
- `GET /api/dashboard/tuning/base-values?instance_id=&model=` lists the saved v3.0 channel anchors together with the latest new-api weight and priority. `model` is optional.
- `PUT /api/dashboard/tuning/base-values?instance_id=` saves `{ "items": ChannelBaseValue[] }` in one transaction. Weights and priorities must be non-negative; each changed channel writes a `tuning.base_update` operation audit containing before/after values.
- `POST /api/dashboard/tuning/base-values/sync?instance_id=` accepts `{ "models": string[] }` and previews the current single-model channel values from the latest snapshot. It never writes the base-value table; the UI must issue the explicit PUT after the operator reviews the preview.
- The tuning policy includes `dispatch_modes`, a map from model name to `off`, `observe`, or `auto`. Missing entries are treated as `off`; v3.0-B1 persists this configuration but does not connect it to the v2.9 engine.
- Policy uses the structured v2.9-B2.5 shape:
  - `scheduling`: `window_minutes`, `min_samples`, `sparse_min_samples`, `sparse_lookback_minutes`, `trial_initial_minutes`, `trial_backoff_factor`, `trial_max_minutes`, `trial_windows`, `cooldown_minutes`, and `daily_action_limit`.
    - `sparse_min_samples` and `sparse_lookback_minutes` provide count-based fallback for low-traffic channels. The fallback only affects attributed error-rate decisions and trial recovery checks; latency degradation and dynamic weighting still require the normal current-window sample count. At least one current-window request is required by the freshness guard.
  - `criteria`: named degradation standards containing `name`, `error_rate_threshold`, `severe_threshold`, `latency_multiplier`, `latency_floor_seconds`, and `sustained_windows`. A `default` criterion is required.
  - `assignments`: model name to criterion name mappings. Unassigned models use `default`.
- Example policy:
  ```json
  {
    "scheduling": {
      "window_minutes": 15,
      "min_samples": 20,
      "sparse_min_samples": 10,
      "sparse_lookback_minutes": 360,
      "trial_initial_minutes": 60,
      "trial_backoff_factor": 2,
      "trial_max_minutes": 1440,
      "trial_windows": 2,
      "cooldown_minutes": 10,
      "daily_action_limit": 6
    },
    "criteria": [{
      "name": "default",
      "error_rate_threshold": 0.15,
      "severe_threshold": 0.5,
      "latency_multiplier": 2,
      "latency_floor_seconds": 10,
      "sustained_windows": 2
    }],
    "assignments": {}
  }
  ```
- Legacy flat policy JSON is treated as the complete default policy. Every subsequent write uses the structured shape.
- `GET /api/dashboard/tuning/ladders?instance_id=` returns the current channel ladder and dispatch states.
- `GET /api/dashboard/tuning/recommendations?instance_id=&limit=&before=&rule=` returns duty-rotation and dynamic-weight recommendations. `rule` is optional and filters to one rule such as `rebalance`. Global confirm/auto controls `demote` and `trial`; `dynamic_weighting.mode` independently controls `rebalance` (`off`, `observe`, or `auto`). Informational rules (`mixed_channel`, `no_backup`, and `ladder_exhausted`) remain recorded.
- `POST /api/dashboard/tuning/recommendations/{id}/adopt` atomically adopts a pending action recommendation, creates a `channel.update` command for the first enabled instance in the same site, records the command ID and actor, and writes an operation audit.
- `POST /api/dashboard/tuning/recommendations/{id}/dismiss` dismisses a pending action recommendation and writes an operation audit. Pending recommendations expire after 60 minutes.
- `GET /api/dashboard/tuning/report?instance_id=&days=7|30` reports adoption and hit rates using only `demote` and `trial`.

命令状态机固定为 `pending → delivered → succeeded|failed`，或 `pending → expired`。缺少人工确认返回 `400 confirm_required`，实例不存在返回 `404 instance_not_found`，空更新返回 `400 invalid_command`。

## v3.1-B3 User Billing

- `GET /api/dashboard/billing/summary?instance_id=&month=&job_id=&covered=1&page=&page_size=&search=&sort=&format=` monthly per-user consumption (amount via CT prices with newapi-ratio fallback, `price_sources`, unpriced model list, quota cross-check, balance snapshot). Optional `job_id` pins the read to one completed immutable version. Without `job_id`, ordinary reads retain the latest completed bill until a new bill completes. `covered=1` requires the selected half-open range to be fully covered by a completed version and slices that immutable version to the selected hours; otherwise HTTP 409 `billing_range_not_covered` includes `latest_job` when available. Viewer requests are pinned to their scope site and user set by the session gate. `format=csv` streams a BOM-prefixed CSV. Results are cached until the next daily rollup or price/ratio change.
- `GET /api/dashboard/billing/detail?instance_id=&user_id=&month=&format=` per-user model/tier/day breakdown; same scope rules; CSV export supported.
- `GET /api/dashboard/billing/import-prices?instance_id=` converts the site's current newapi ModelRatio config into editable CT price rows (form backfill only, saving goes through the prices API).
- `GET|PUT /api/dashboard/billing/prices?instance_id=` and `GET|PUT /api/dashboard/billing/group-ratios?instance_id=` admin-only, effective-dated tiered price schedules and group ratios; changes are audited and invalidate the summary cache.
- `GET|PUT /api/dashboard/billing/models?instance_id=` admin-only model catalog. The list merges models discovered from new-api ratios, CT price schedules, and CT model metadata; PUT maintains the per-site maximum context-token length.
- `POST /api/dashboard/billing/backfill {instance_id, from, to}` admin-only, day-segmented and rate-limited, audited.
## Billing background jobs

- `POST /api/dashboard/billing/jobs` (also available at the compatibility path `POST /api/dashboard/billing/backfill`) creates an admin-only, hourly segmented billing job and returns HTTP 202 with the job record.
- `GET /api/dashboard/billing/jobs?id=...` returns progress (`completed_steps / total_steps`), anomaly count, and terminal error. Scoped users may only read jobs for their site.
- `GET /api/dashboard/billing/jobs?instance_id=&status=&limit=` lists recent billing generation and verification jobs for the admin task center; `status` optionally filters `pending|running|complete|failed`, and active jobs are ordered first when unfiltered.
- `DELETE /api/dashboard/billing/jobs?id=...` stops an admin-owned pending/running task, marks its unfinished steps failed, and preserves all previously completed bill versions. Creating a generation job for a range already fully covered by a completed version returns HTTP 409 `billing_range_already_covered` with `covering_job`.
- Billing generation and verification are globally serialized. A request for a different new job while any job is `pending` or `running` returns HTTP 409 `billing_job_busy` with `active_job`; an idempotent request for the same existing job is still reused.
- Each hour is read from new-api in pages of 2,000 using the `(created_at,id)` keyset. No billing generation query uses `OFFSET`.
- Completed data is written as an immutable version. `billing_active_versions` is switched only after every step succeeds, so an interrupted generation never replaces the currently visible bill.
- A failed step is resumed from its persisted cursor and retried up to three times.

## Billing full verification

- `POST /api/dashboard/billing/verification` accepts `{ "source_job_id": "..." }` for a completed `generate` job. It creates an admin-only background verification job and returns HTTP 202. Repeated or concurrent submissions reuse the existing non-failed job and return `reused=true`.
- `GET /api/dashboard/billing/verification?source_job_id=&job_id=&page=&page_size=&mismatches_only=` returns resumable progress and, after completion, paginated comparison results. `job_id` is optional but must belong to the supplied source job. Results default to mismatches only; `mismatches_only=false` includes matched dimensions.
- Verification rescans new-api consumption logs through the same hourly, 2,000-row keyset pages as generation. It applies the billing anomaly rules again, then compares `(business day, user, model, group)` across source logs, immutable normal bill rows, and immutable anomaly rows.
- A dimension is matched only when normal count/quota, anomaly count/quota, and the source decomposition (`source = normal + anomaly`) all agree. Keys found only in CT or only in the source are retained as mismatches.
- Verification tables are isolated from active bill versions. Starting or failing a verification never changes billing totals, anomaly orders, discounts, or exports.

## Billing validation and user pricing

- `GET|PUT /api/dashboard/billing/user-settings` controls `use_tiered_pricing` per site/user; writes are admin-only and default to enabled when no row exists.
- `GET /api/dashboard/billing/anomalies` lists or exports (`format=csv`) rejected source orders by keyset cursor.
- A source order is excluded when input or output tokens are NULL/zero, or input tokens exceed the configured model maximum context. Unknown/zero model maximum context skips only the context-limit validation.

### 渠道熔断通知（2026-09-14）

通知类别新增“渠道熔断”，包含 `channel_circuit_opened` 和 `channel_circuit_recovered`。复用调权事件，仅 auto 模式且关联 channel.update 命令 succeeded 后生成；观察模式、失败和待执行命令不通知。按事件 ID 持久去重，恢复使用独立消息及重试记录，并关闭同站点同渠道此前的熔断告警。恢复信息在告警中心以 resolved/info 展示；短时间熔断后恢复仍可分别投递两条带事件时间的消息。

076 迁移记录启用时间，仅消费该时间之后的事件，不补发历史熔断。普通指标扫描不能关闭熔断事件；已发送熔断消息不会因恢复而重新释放。旧渠道显式所选类别不扩大；空 rule_keys 的全部类别渠道会包含新增熔断类别。按站点投递，复用已有重试、确认和静默；不新增周期提醒。事件映射保留以防历史告警清理后重发。需要升级 Server/前端及迁移，Agent 无新增要求。


## 调权专用未重试 TTFT（2026-09-15）

Agent 在原有 `/api/agent/report` 的 `aggregated_metrics` 渠道条目中可附带 `speed_ttft`，不增加上报请求或采集次数。仅 `instance_channel` 维度携带；客户、模型等其他维度及公共 TTFT 字段不变。

```json
{"speed_ttft":{"buckets":[0,0,10,0,0,0,0,0,0,0,0,0,0,0,0],"retry_count":3,"unknown_count":2}}
```

- `buckets`：15 个非累计桶，边界与 `ttft_buckets` V2 相同；只计入有有效 `frt`、输出 Token 大于 0 的成功流式日志，且 `other.admin_info.use_channel` 完整、长度为 1、末渠道与日志渠道相同。
- `retry_count`：有效流式 TTFT 中能确认尝试次数大于 1 的数量，包括同渠道重试。
- `unknown_count`：其余有效流式 TTFT 数量，包括无法确认尝试信息或不符合成功输出条件的记录。
- 三者之和必须等于公共 `ttft_count`，长度、非负性或总数校验失败时仅忽略该可选统计，不中断公共监控上报。
- 缺失对象表示旧 Agent 或无有效新统计；全零对象表示新 Agent 已统计但该条目无有效流式 TTFT。合并仅累加存在的新证据，旧记录不被认定为未重试；公开 TTFT 总数减去新证据总数得到旧口径/缺失覆盖数量。
- 079 迁移新增 1m/5m 可空统计列及调权状态计数/口径版本，不用历史公共 TTFT 回填新字段。原批次 ID 去重事务覆盖新增数据。
- 调权速度分位数、同模型速度基线均使用专用桶；最低样本数使用有效未重试 TTFT 数，不以总请求数代替。缓存、OTPS、错误及其原有资格条件不变。
- 速度样本或速度基线不足时保留相同模型之前的新口径速度系数；首次及旧口径评分使用中性 1。旧版本评分不会被当成新口径可信值继承。
- 状态 API 新增 `speed_sample_count`、`speed_retry_count`、`speed_unknown_count`、`speed_legacy_count`、`speed_stats_version`；变更记录 evidence 同步记录。升级顺序为 Server/079 迁移与前端，然后 Agent；无生产迁移/部署包含在本次代码交付中。


## Continuous tuning: current-window OTPS fallback (2026-09-17)

This rule requires the total-request-time output statistics implementation and migration 081 bundled in this worktree. Average OTPS includes eligible streaming and non-streaming requests and has independent evidence readiness; historical generation-time statistics must not be substituted for that input.

When current TTFT evidence and its peer baseline are ready, the performance product is `Kspeed * Kotps * Kcache * Kerror`. Otherwise, if current output evidence and its baseline are ready, set `Kspeed = Kotps` and use `Kotps * Kotps * Kcache * Kerror`. Never reuse the prior TTFT coefficient for a new calculation. Cache scoring remains unchanged; existing combined bounds and dispatch protections continue to apply.

An insufficient total request window, or absence of both usable current TTFT and output evidence, holds the effective current weight without a normal weight write. Circuit, probe and recovery decisions take precedence. Dashboard status distinguishes current output substitution from skipped normal tuning. This fallback itself adds no Agent payload or migration; deploy its Server and frontend with the average-output dependency (Server/migration before Agent).


## 使用日志查询优化（2026-09-18）

- `GET /api/dashboard/passthrough/logs` 新增可选 `cursor`，响应新增 `next_cursor` / `previous_cursor`。游标是不可依赖内部格式的字符串，绑定站点、角色、有效用户范围、时间及筛选条件；不替代后端权限检查。无游标继续支持原 `offset`/页码契约；无效或不同筛选范围的游标返回 400 `invalid_cursor`。
- 游标排序键为 `(created_at,id)`。下一页使用严格小于条件降序读取，上一页严格大于升序读取再倒序返回；同秒记录以 ID 决定边界。游标请求中的 offset 仅用于响应页码和展示位置，SQL offset 为 0。前端仅相邻页使用游标，跳页/改页大小/新筛选回到普通分页；旧 Server 未返回游标时自动兼容。
- 列表先加载，返回后才启动总数与统计；新搜索取消旧附属请求，卸载/切站不启动已失效请求。翻页/失败重试暂停本页面未完成的统计，列表请求结束后仅恢复被暂停的请求，过期查询不恢复。Server 每个站点连接池只允许一项 count/stat 计算同时执行，为列表保留第二个连接；不保证源库本身无锁等待或其他业务查询无竞争。
- 前端保存当前成功页面的请求游标、offset、limit；翻页或改页大小失败时一起恢复，错误提示的重试入口经过同一分页加载逻辑，避免游标与页码错配。
- 固定时间窗口的 count/stat 使用进程内 5 秒缓存与相同请求合并，最多 256 个条目。键包含有效站点/用户范围/角色、连接配置身份和查询条件；分页参数不影响统计键。失败不缓存；所有等待者取消时终止底层查询，单个等待者取消不影响其他等待者。隐式滚动窗口不缓存。响应禁止浏览器缓存，并以 `X-CT-Statistics-Max-Age: 5` 标明服务端最大缓存时间。
- Viewer 去重计数、模糊筛选、请求 ID 筛选仍维持原表语义，可复用上述短时结果缓存。小时汇总为空时整段回源校验继续保留，避免将源库重置或覆盖异常误判成零条。
- 管理员渠道名称按数据库连接身份与渠道 ID 缓存一分钟，最多 2048 条，成功确认的空名称/不存在也缓存，查询失败不缓存；Viewer 不执行名称补查。重试标记、未命中名称各有 1 秒补查预算，失败保留原基础日志及已有元数据。
- 列表新增 `fallback_checked`：true 表示已有肯定的重试证据或本轮补查成功，false 表示未确认；必须结合 `fallback` 判断，不能把 false 当成没有重试。前端对具备请求ID/用户ID的未知状态显示“待确认”，管理员可独立打开请求链路查询（含渠道ID缺失场景）；Viewer 不新增链路入口，旧接口缺少该字段时维持原显示。
- count/stat 的审计集中在每次 HTTP 请求返回处，冷查询、缓存命中、合并等待者各记录一次，使用原操作类型。按实际 HTTP 状态记录 succeeded/failed，取消记 failed/http_status=499；后台共享计算不重复记录审计。
- 小时汇总每轮最多两个站点并行，同站点仍去重并保留 10 秒稳定等待和每轮 10×5000 条预算；一轮完成后再等 30 秒，不重叠执行。未修改 NewAPI 表、计数口径或 Agent。
- 游标分页不提供跨页数据库快照；历史记录删除或迟到写入时页码/短时总数可能变化。生产执行计划与性能收益需现场验证。
