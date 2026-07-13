# Agent API Contracts

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
