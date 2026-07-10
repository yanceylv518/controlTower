# Control Tower Agent

The agent runs on each new-api server and actively reports to Control Tower Server.

Phase 1 agent responsibilities:

- Load safe configuration from environment variables.
- Persist local cursor state.
- Convert new-api `logs` rows into sanitized Control Tower log events.
- Report heartbeat and batch payloads to Control Tower Server.
- Report real server runtime metrics and the configured new-api health check target.
- Report Docker container status when `CT_DOCKER_ENABLED=true` and the Docker CLI is available.

The agent does not expose an inbound port and does not read or modify `.env`.

## Run Modes

By default the agent runs continuously and polls/uploads every 30 seconds (`CT_LOG_POLL_INTERVAL_SECONDS=30`). Increase this to 60 seconds for very low-priority monitoring, or decrease only during local debugging.

Set `CT_AGENT_RUN_ONCE=true` for local validation or scheduled one-shot execution. In that mode the agent collects one batch, reports it, updates local cursor state on success, and exits.

Set `CT_AGENT_FAKE_EVENT=true` only for smoke testing. Fake event mode sends one synthetic heartbeat/report and does not read the new-api database.



## Log Event Mode

Production defaults to `CT_LOG_EVENT_MODE=aggregate_with_samples`.

- `aggregate_only`: upload aggregated metrics only; no log event details.
- `aggregate_with_samples`: upload aggregated metrics plus limited error/slow log samples.
- `full_debug`: upload every collected log event; use only for local debugging.

`CT_LOG_SAMPLE_LIMIT` caps samples per report. `CT_SLOW_LOG_THRESHOLD_SECONDS` controls slow request sampling.
## Health And Metrics

Each real collector pass sends one system metric sample and one health check result.

- System metrics include CPU percent, memory used percent, disk used percent, network byte rates where supported, and 1-minute load where supported.
- `CT_NEW_API_STATUS_URL` controls the HTTP health target.
- Health check errors are summarized and capped before reporting; response bodies are not sent.



## Local Report Buffer

When a report containing log events cannot be delivered, the agent writes it to `report-buffer.json` under `CT_DATA_DIR`. After the buffer write succeeds, the local cursor advances to avoid repeatedly collecting the same source rows.

On the next collector pass, the agent flushes buffered reports before reading new logs. The buffer is capped by `CT_MAX_LOCAL_BUFFER_EVENTS` across queued log events.

Local JSON state files tolerate UTF-8 BOM so PowerShell-created validation files can still be read.

## Docker Status

When `CT_DOCKER_ENABLED=true`, each real collector pass tries to run:

```powershell
docker ps --all --format "{{.Names}}\t{{.Status}}\t{{.State}}"
```

If Docker is unavailable, the agent skips Docker status for that pass and continues reporting logs, metrics, and health checks.

## Local Smoke Report

Start the local Control Tower Server first:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\start-server-local.ps1
```

In another PowerShell window, send one safe fake event to the local Server:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\send-agent-smoke-report.ps1
```

The smoke mode sets `CT_AGENT_FAKE_EVENT=1`. It does not read the new-api database and does not send request bodies, response bodies, API keys, cookies, or upstream secrets.

## Local Real Collector Check

After creating the local source logs test data, run one real collector pass:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\run-agent-collector-local.ps1 -ResetState
```

The local collector script reads MySQL credentials from `local/mysql-test.config.ps1` at runtime, sets the DSN only in-process, and does not print the password.




## Config File

The agent can load an env-style config file. Environment variables still override file values.

```powershell
.\dist\control-tower-agent.exe -config .\deploy\agent.config.example
```

You can also set `CT_AGENT_CONFIG` instead of passing `-config`.

```powershell
$env:CT_AGENT_CONFIG = ".\deploy\agent.config.example"
.\dist\control-tower-agent.exe
```

The config parser accepts `KEY=value`, optional `export KEY=value`, blank lines, and `#` comments. Do not commit real tokens or MySQL passwords.

## Preflight Check

Run preflight before deploying the collector loop:

```powershell
.\dist\control-tower-agent.exe -config .\deploy\agent.config.example -preflight
```

Preflight checks:

- Agent config is complete.
- `CT_DATA_DIR` is writable.
- Control Tower Server `GET /healthz` is reachable.
- MySQL can connect with the configured DSN.
- `logs` can be queried for the collector cursor.
- `channels` can be queried when channel snapshots are enabled.
- `logs.id` has an index, reported as a warning if missing.

Preflight only reads MySQL metadata and source rows needed to verify queryability. It does not modify the new-api database.

## Build Standalone Agent

From `tools/control-tower`, build a standalone Windows binary:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\build-agent.ps1
```

The output is written to `tools/control-tower/dist/control-tower-agent.exe`.




