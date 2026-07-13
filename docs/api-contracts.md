# Agent API Contracts

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
| `GET /api/dashboard/overview` | Query `instance_id` | `{"cards":[],"runtime":{...}}` |
| `GET /api/dashboard/metrics` | Query `window,instance_id,dimension_type,dimension_key` | `{"items":[{"window":"1m","request_count":10}]}` |
| `GET /api/dashboard/metric-history` | Query `window,dimension_type,dimension_key,since` | `{"items":[{"bucket_time":"...","request_count":10}]}` |
| `GET /api/dashboard/usage` | Query `window,instance_id,limit` | `{"items":[{"dimension_key":"user:7","quota":100}]}` |

### 日志与运行态

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `GET /api/dashboard/logs` | Query `instance_id,user_id,channel_id,model_name,log_type,request_id,start_time,end_time,limit,offset` | `{"items":[{"source_log_id":1,"log_type":"error"}]}` |
| `GET /api/dashboard/log-samples` | 同日志过滤，另含 `sample_kind` | `{"items":[{"sample_kind":"error"}]}` |
| `GET /api/dashboard/server-metrics` | Query `instance_id,start_time,end_time,limit,offset` | `{"items":[{"cpu_percent":12.5}]}` |
| `GET /api/dashboard/health-checks` | Query `instance_id,target,status,limit,offset` | `{"items":[{"target":"new-api","status":"healthy"}]}` |
| `GET /api/dashboard/docker-statuses` | Query `instance_id,container_name,running,limit,offset` | `{"items":[{"container_name":"new-api","running":true}]}` |
| `GET /api/dashboard/channel-snapshots` | Query `instance_id,channel_id,start_time,end_time,limit,offset` | `{"items":[{"channel_id":7,"status":"enabled"}]}` |

### 告警与时间线

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `GET /api/dashboard/alerts` | Query `instance_id,status,severity,active_only,limit,offset` | `{"items":[{"id":"a1","status":"firing"}]}` |
| `POST /api/dashboard/alerts/action` | JSON `id,action,note,silence_until` | `{"ok":true}` |
| `GET /api/dashboard/alerts/{id}/events` | Query `limit` | `{"items":[{"event_type":"acknowledged","actor":"admin","note":"checked"}]}` |

### 通知

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `GET /api/dashboard/notification-channels` | 无 | `{"items":[{"id":"c1","channel_type":"dingtalk","has_secret":true}]}` |
| `POST /api/dashboard/notification-channels` | JSON `id,channel_type,name,webhook_url,enabled,secret` | `{"items":[{"id":"c1","has_secret":true}]}` |
| `GET /api/dashboard/notification-deliveries` | Query `alert_id,channel_id,status,limit,offset` | `{"items":[{"id":"d1","status":"failed","attempts":1}]}` |
| `POST /api/dashboard/notification-deliveries/{id}/resend` | 无 | `{"ok":true}` |

### 渠道命令与审计

| 方法与路径 | 参数 | 成功响应示例 |
| --- | --- | --- |
| `POST /api/dashboard/channels/{channelID}/commands` | JSON `instance_id,confirm,status?,weight?,priority?`；`confirm` 必须为 `true` | `201 {"id":"...","instance_id":"inst-x","channel_id":7,"status":"pending","payload":{"status":2},"created_by":"admin","created_at":"..."}` |
| `GET /api/dashboard/channel-commands` | Query `instance_id,status,limit,offset` | `{"items":[{"id":"...","status":"succeeded","payload":{"status":2}}]}` |
| `GET /api/dashboard/operation-audits` | Query `instance_id,limit,offset` | `{"items":[{"operation_type":"channel.update","target_type":"channel","target_id":"7","actor_id":"admin","after_summary":"...","created_at":"..."}]}` |

命令状态机固定为 `pending → delivered → succeeded|failed`，或 `pending → expired`。缺少人工确认返回 `400 confirm_required`，实例不存在返回 `404 instance_not_found`，空更新返回 `400 invalid_command`。
