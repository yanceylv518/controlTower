# Control Tower Server

The server receives agent reports, validates them, stores Control Tower-owned monitoring data, aggregates metrics, and exposes read-only Dashboard APIs.

Phase 1 server responsibilities:

- Validate Agent authentication.
- Receive heartbeat and report payloads.
- Store sanitized log summaries and server metrics in the Control Tower MySQL database.
- Run 1m/5m aggregation in a background runner.
- Expose protected Dashboard overview and log query APIs.

The server does not query the new-api database during UI requests and does not write to the new-api database.

## Local Run

1. Fill local MySQL credentials in `local/mysql-test.config.ps1`.
2. Create or verify the local test database and run integration tests:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\create-mysql-test-db.ps1 -RunIntegrationTest
```

3. Start the server:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\start-server-local.ps1
```

Default local server URL:

```text
http://127.0.0.1:18081
```

Health check:

```powershell
Invoke-RestMethod http://127.0.0.1:18081/healthz
```

Dashboard overview example:

```powershell
Invoke-RestMethod http://127.0.0.1:18081/api/dashboard/overview -Headers @{ Authorization = "Bearer local-dashboard-token" }
```

The local startup script sets MySQL DSN only inside its PowerShell process and does not print the password.
## Runtime Dashboard APIs

All Dashboard APIs require `Authorization: Bearer <CT_DASHBOARD_TOKEN>`.

- `GET /api/dashboard/server-metrics`
- `GET /api/dashboard/health-checks`
- `GET /api/dashboard/docker-statuses`

Common query parameters: `instance_id`, `start_time`, `end_time`, `limit`, `offset`.

Additional filters:

- Health checks: `target`, `status`
- Docker statuses: `container_name`, `running`
## Overview Runtime Summary

`GET /api/dashboard/overview` includes the original 1-minute traffic summary plus `runtime`:

- `latest_server_metrics`: latest server metric per instance.
- `health`: latest health check per instance and target, with up/down counts.
- `docker`: latest Docker status per instance and container, with running/stopped counts.

