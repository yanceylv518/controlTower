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
step mismatch; if heartbeat wrong-instance "$token" >/dev/null 2>&1; then exit 1; fi
step rotate; rotated=$(curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -X POST "$base/api/dashboard/instances/$id/rotate-token"); new_token=$(printf '%s' "$rotated" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); test -n "$new_token"
step rotation-grace; heartbeat "$id" "$token" >/dev/null; heartbeat "$id" "$new_token" >/dev/null
step disable; curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -X PUT -d '{"enabled":false}' "$base/api/dashboard/instances/$id" >/dev/null
if heartbeat "$id" "$new_token" >/dev/null 2>&1; then exit 1; fi
step list-no-token; list=$(curl -fsS -b "$jar" "$base/api/dashboard/instances"); ! printf '%s' "$list" | grep -q 'token'
echo '[e2e] passed'
