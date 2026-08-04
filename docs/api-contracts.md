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

## v2.9-B2 Duty-Rotation Tuning (observe and confirm)

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

- `GET /api/dashboard/billing/summary?instance_id=&month=&page=&page_size=&search=&sort=&format=` monthly per-user consumption (amount via CT prices with newapi-ratio fallback, `price_sources`, unpriced model list, quota cross-check, balance snapshot). Viewer requests are pinned to their scope site and user set by the session gate. `format=csv` streams a BOM-prefixed CSV. Results are cached until the next daily rollup or price/ratio change.
- `GET /api/dashboard/billing/detail?instance_id=&user_id=&month=&format=` per-user model/tier/day breakdown; same scope rules; CSV export supported.
- `GET /api/dashboard/billing/import-prices?instance_id=` converts the site's current newapi ModelRatio config into editable CT price rows (form backfill only, saving goes through the prices API).
- `GET|PUT /api/dashboard/billing/prices?instance_id=` and `GET|PUT /api/dashboard/billing/group-ratios?instance_id=` admin-only, effective-dated tiered price schedules and group ratios; changes are audited and invalidate the summary cache.
- `GET|PUT /api/dashboard/billing/models?instance_id=` admin-only model catalog. The list merges models discovered from new-api ratios, CT price schedules, and CT model metadata; PUT maintains the per-site maximum context-token length.
- `POST /api/dashboard/billing/backfill {instance_id, from, to}` admin-only, day-segmented and rate-limited, audited.
## Billing background jobs

- `POST /api/dashboard/billing/jobs` (also available at the compatibility path `POST /api/dashboard/billing/backfill`) creates an admin-only, hourly segmented billing job and returns HTTP 202 with the job record.
- `GET /api/dashboard/billing/jobs?id=...` returns progress (`completed_steps / total_steps`), anomaly count, and terminal error. Scoped users may only read jobs for their site.
- Each hour is read from new-api in pages of 2,000 using the `(created_at,id)` keyset. No billing generation query uses `OFFSET`.
- Completed data is written as an immutable version. `billing_active_versions` is switched only after every step succeeds, so an interrupted generation never replaces the currently visible bill.
- A failed step is resumed from its persisted cursor and retried up to three times.

## Billing validation and user pricing

- `GET|PUT /api/dashboard/billing/user-settings` controls `use_tiered_pricing` per site/user; writes are admin-only and default to enabled when no row exists.
- `GET /api/dashboard/billing/anomalies` lists or exports (`format=csv`) rejected source orders by keyset cursor.
- A source order is excluded when input or output tokens are NULL/zero, or input tokens exceed the configured model maximum context. Unknown/zero model maximum context skips only the context-limit validation.
