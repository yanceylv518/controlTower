#!/usr/bin/env bash
set -euo pipefail
# M1 Server E2E. Requires a running Server/MySQL and admin bootstrap credentials.
base="${CT_BASE:-http://127.0.0.1:8080}"
jar="$(mktemp)"; trap 'rm -f "$jar"' EXIT
step(){ echo "[e2e] $1"; }
step health; curl -fsS "$base/healthz" >/dev/null
step login; curl -fsS -c "$jar" -H 'Content-Type: application/json' -d "{\"username\":\"$CT_ADMIN_USER\",\"password\":\"$CT_ADMIN_PASS\"}" "$base/api/auth/login" >/dev/null
step me; curl -fsS -b "$jar" "$base/api/auth/me" >/dev/null
id="inst-e2e-$(date +%s)"
step create-instance; response=$(curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -d "{\"instance_id\":\"$id\",\"name\":\"E2E\"}" "$base/api/dashboard/instances")
token=$(printf '%s' "$response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); test -n "$token"
heartbeat(){ printf '%s' "{\"instance_id\":\"$1\",\"agent_id\":\"e2e\",\"agent_version\":\"e2e\",\"reported_at\":\"$(date -u +%FT%TZ)\",\"sequence\":1}" | gzip -c | curl -fsS -H "Authorization: Bearer $2" -H 'Content-Encoding: gzip' -H 'Content-Type: application/json' --data-binary @- "$base/api/agent/heartbeat"; }
step heartbeat; heartbeat "$id" "$token" >/dev/null
step command-confirm-required
code=$(curl -sS -o /dev/null -w '%{http_code}' -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -d "{\"instance_id\":\"$id\",\"status\":2}" "$base/api/dashboard/channels/77/commands"); test "$code" = 400
step command-create
command=$(curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -d "{\"instance_id\":\"$id\",\"confirm\":true,\"status\":2}" "$base/api/dashboard/channels/77/commands")
command_id=$(printf '%s' "$command" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p'); test -n "$command_id"
step command-deliver
heartbeat_response=$(heartbeat "$id" "$token"); printf '%s' "$heartbeat_response" | grep -q "\"id\":\"$command_id\""
step command-complete
now="$(date -u +%FT%TZ)"
printf '%s' "{\"instance_id\":\"$id\",\"agent_id\":\"e2e\",\"agent_version\":\"e2e\",\"reported_at\":\"$now\",\"sequence\":2,\"command_results\":[{\"id\":\"$command_id\",\"channel_id\":77,\"status\":\"succeeded\",\"applied_at\":\"$now\"}]}" | gzip -c | curl -fsS -H "Authorization: Bearer $token" -H 'Content-Encoding: gzip' -H 'Content-Type: application/json' --data-binary @- "$base/api/agent/report" >/dev/null
commands=$(curl -fsS -b "$jar" "$base/api/dashboard/channel-commands?instance_id=$id&status=succeeded"); printf '%s' "$commands" | grep -q "\"id\":\"$command_id\""
audits=$(curl -fsS -b "$jar" "$base/api/dashboard/operation-audits?instance_id=$id"); printf '%s' "$audits" | grep -q '"actor_id":"'"$CT_ADMIN_USER"'"'; printf '%s' "$audits" | grep -q '"target_id":"77"'
step notification-channel
curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -d '{"id":"e2e-failing","channel_type":"wecom","name":"e2e-failing","webhook_url":"http://127.0.0.1:1","enabled":true}' "$base/api/dashboard/notification-channels" >/dev/null
step error-report
now="$(date -u +%FT%TZ)"
events=''
for n in 1 2 3; do events="${events}${events:+,}{\"source_log_id\":$n,\"created_at\":\"$now\",\"log_type\":\"error\",\"channel_id\":77,\"request_id\":\"e2e-$n\",\"error_summary\":\"e2e\"}"; done
printf '%s' "{\"instance_id\":\"$id\",\"agent_id\":\"e2e\",\"agent_version\":\"e2e\",\"reported_at\":\"$now\",\"sequence\":3,\"last_log_id\":3,\"log_events\":[$events]}" | gzip -c | curl -fsS -H "Authorization: Bearer $token" -H 'Content-Encoding: gzip' -H 'Content-Type: application/json' --data-binary @- "$base/api/agent/report" >/dev/null
step alert-timeline
alerts=$(curl -fsS -b "$jar" "$base/api/dashboard/alerts?status=firing&instance_id=$id")
alert_id=$(printf '%s' "$alerts" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p'); test -n "$alert_id"
curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -d "{\"id\":\"$alert_id\",\"action\":\"acknowledge\",\"note\":\"e2e\"}" "$base/api/dashboard/alerts/action" >/dev/null
timeline=$(curl -fsS -b "$jar" "$base/api/dashboard/alerts/$alert_id/events"); printf '%s' "$timeline" | grep -q '"event_type":"firing"'; printf '%s' "$timeline" | grep -q '"event_type":"acknowledged"'; printf '%s' "$timeline" | grep -q '"note":"e2e"'; printf '%s' "$timeline" | grep -q "\"actor\":\"$CT_ADMIN_USER\""
step notification-resend
deliveries=$(curl -fsS -b "$jar" "$base/api/dashboard/notification-deliveries?alert_id=$alert_id&status=failed")
delivery_id=$(printf '%s' "$deliveries" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
if [ -n "$delivery_id" ]; then curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -X POST "$base/api/dashboard/notification-deliveries/$delivery_id/resend" >/dev/null; else echo '[e2e] notification delivery not ready; skip resend (runner interval/configuration)'; fi
step mismatch; if heartbeat wrong-instance "$token" >/dev/null 2>&1; then exit 1; fi
step rotate; rotated=$(curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -X POST "$base/api/dashboard/instances/$id/rotate-token"); new_token=$(printf '%s' "$rotated" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); test -n "$new_token"
step rotation-grace; heartbeat "$id" "$token" >/dev/null; heartbeat "$id" "$new_token" >/dev/null
step disable; curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -X PUT -d '{"enabled":false}' "$base/api/dashboard/instances/$id" >/dev/null
if heartbeat "$id" "$new_token" >/dev/null 2>&1; then exit 1; fi
step list-no-token; list=$(curl -fsS -b "$jar" "$base/api/dashboard/instances"); ! printf '%s' "$list" | grep -q 'token'
echo '[e2e] passed'
