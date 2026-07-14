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
- `full_debug`: upload every collected log event; needed only for the server-side `recent_errors` rule or local debugging.

## Agent-Side WeCom Error Alert

Set `CT_WECOM_WEBHOOK_URL` to a WeCom group robot webhook to enable the
built-in error alert: for any channel and any user, when
`CT_ALERT_ERROR_THRESHOLD` (default 3) or more of that dimension's most recent
`CT_ALERT_ERROR_WINDOW` (default 10) requests are errors, the agent sends one
group message. The dimension does not notify again until it first recovers
(errors drop below the threshold), so a continuously failing dimension does not
spam the group. Failed sends are retried on the next collector pass.

- The rule is evaluated directly on rows read from the source `logs` table, so
  it works in every log event mode and does not require the server.
- Alert messages show the channel name next to the id (`渠道 18(OpenAI-主力)`)
  when the read-only account also has `GRANT SELECT ON newapi.channels`; the
  mapping refreshes every 10 minutes. Without that grant the agent logs one
  warning and falls back to id-only labels.
- With the webhook configured, `CT_SERVER_URL` becomes optional: leaving it
  empty runs the agent in standalone alert-only mode (collect + alert, no
  heartbeat/report).
- Configure the robot's security setting with custom keyword `告警` — every
  message starts with `【Control Tower 告警】`.
- Window entries older than `CT_ALERT_WINDOW_MAX_AGE_MINUTES` (default 60)
  leave the window, so a sparse channel with stale errors re-arms and a new
  error burst alerts again instead of being deduplicated forever.
- While an episode keeps firing, a reminder is re-sent every
  `CT_ALERT_REMIND_MINUTES` (default 60) with the episode start time and
  cumulative error count, so a channel that never recovers is not silent
  after its first alert. Reminders only continue while new errors keep
  arriving: a dimension quiet for the decay window ends its episode.
- Cache-miss monitoring is enabled by default. On each channel, successful
  requests with `prompt_tokens > CT_ALERT_NOCACHE_MIN_PROMPT_TOKENS`
  (default 512) enter a separate window of `CT_ALERT_NOCACHE_WINDOW`
  (default 10); when the window is full and every entry reports zero cached
  tokens, a cache-broken alert fires. Any cache hit re-arms the episode.
  Disable with `CT_ALERT_NOCACHE_ENABLED=false`. Channels whose models never
  report cache usage will alert once and then remind per
  `CT_ALERT_REMIND_MINUTES`; disable the rule or raise the token floor if
  that is noise for your deployment.
- Episode transitions are appended to `CT_DATA_DIR/alert-events.jsonl` as
  JSON lines. Records identify the dimension, rule, alert/remind/rearm kind,
  window count, threshold, and episode totals. The file rotates at 5 MiB and
  retains one `.1` file; logging failures never block alert delivery.
- Disabled channels (new-api status != 1) are excluded from channel-level
  monitoring: their events are ignored, an ongoing episode closes silently
  (recorded as kind=disposed in the event log), and re-enabling starts from a
  fresh window. Channel states refresh every 10 minutes together with names,
  so suppression may lag a disable action by up to 10 minutes. The user
  dimension is unaffected.
- Windows are in-memory; after a restart, counting starts from the next
  collected batch.
- On a fresh install (no state file), standalone mode starts from the current
  end of the logs table instead of replaying history, so old incidents never
  trigger alerts.

### One-Command Install (Linux)

Copy the agent binary and `deploy/install-agent.sh` to the new-api server, then:

```bash
sudo ./install-agent.sh
```

The installer asks for the read-only MySQL DSN and the WeCom webhook, then
installs the binary, config (0600), a hardened systemd unit, runs preflight,
and starts the service. Re-running the installer overwrites the config and
restarts the service. Non-interactive options:

- `--dsn ... --webhook ...`: generate the config from flags.
- `--config my-agent.config`: install a prepared config file as-is; start
  from `deploy/agent.standalone.config.example`.

The live config always ends up at `/etc/control-tower/agent.config`; edit it
and `systemctl restart control-tower-agent` to apply changes.

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

## Nginx Timing 延时分析

完整上报模式可只读 tail Nginx `timed` 访问日志，将 TTFT、传输段、5xx/504 和慢样本按分钟聚合后显示在 Web「延时分诊」页。字段格式与分诊公式见 [`docs/latency-diagnosis.md`](../docs/latency-diagnosis.md)。该模块只做分析，不发送企业微信或任何告警消息。

```ini
CT_NGINX_ACCESS_LOG=/var/log/nginx/newapi-timing.log
CT_NGINX_SLOW_RT_SECONDS=10
```

`CT_NGINX_ACCESS_LOG` 留空时完全禁用。文件暂时不存在或无权限时 Agent 只记录一次 WARN 并每 30 秒重试，不退出且不影响原有采集上报；未配置 `CT_SERVER_URL` 的独立告警模式不会启动 timing 采集。

给 `ct-agent` 日志读取权限可选以下任一方式：

```bash
sudo setfacl -m u:ct-agent:r /var/log/nginx/newapi-timing.log
# 或让 Agent 继承 Nginx 日志常用的 adm 组权限
sudo usermod -aG adm ct-agent
```

修改组成员后需重启 `control-tower-agent` 服务使权限生效。Agent 只保留 URL path，query 会在采集边界剥离。

## Build Standalone Agent

From `tools/control-tower`, build a standalone Windows binary:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\build-agent.ps1
```

The output is written to `tools/control-tower/dist/control-tower-agent.exe`.




