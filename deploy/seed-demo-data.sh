#!/usr/bin/env bash
set -euo pipefail
# Seeds a running Control Tower Server with rich demo data for the M2 stage
# browser walkthrough: two instances, per-dimension 1m metrics across the last
# hour, error/slow log samples, channel snapshots with names and models, and
# fresh error events that raise a firing alert. Requires: CT_ADMIN_USER/PASS.
base="${CT_BASE:-http://127.0.0.1:8080}"
jar="$(mktemp)"; trap 'rm -f "$jar"' EXIT
step(){ echo "[seed] $1"; }

step login
curl -fsS -c "$jar" -H 'Content-Type: application/json' -d "{\"username\":\"$CT_ADMIN_USER\",\"password\":\"$CT_ADMIN_PASS\"}" "$base/api/auth/login" >/dev/null

make_instance(){ # $1 = instance id
  response=$(curl -sS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -d "{\"instance_id\":\"$1\",\"name\":\"Demo $1\"}" "$base/api/dashboard/instances")
  token=$(printf '%s' "$response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
  if [ -z "$token" ]; then echo "[seed] instance $1 exists already; recreate the test db for a clean seed" >&2; exit 1; fi
  printf '%s' "$token"
}

post_report(){ # $1 instance, $2 token, $3 json body
  printf '%s' "$3" | gzip -c | curl -fsS -H "Authorization: Bearer $2" -H 'Content-Encoding: gzip' -H 'Content-Type: application/json' --data-binary @- "$base/api/agent/report" >/dev/null
}

metric(){ # $1 minutes-ago, $2 dim_type, $3 dim_key, $4 requests, $5 errors, $6 p95
  local bt; bt=$(date -u -d "-$1 minutes" +%FT%TZ 2>/dev/null || date -u -v-"$1"M +%FT%TZ)
  local ok=$(($4 - $5))
  printf '{"bucket_time":"%s","window_seconds":60,"dimension_type":"%s","dimension_key":"%s","request_count":%d,"success_count":%d,"error_count":%d,"success_rate":%s,"error_rate":%s,"tpm":%d,"prompt_tokens":%d,"completion_tokens":%d,"quota":%d,"avg_use_time":2.1,"p95_use_time":%s,"stream_rate":0.6,"use_time_sum":%d,"stream_count":%d,"latency_buckets":[1,2,%d,%d,2,1,1,0,0,0]}' \
    "$bt" "$2" "$3" "$4" "$ok" "$5" "$(awk "BEGIN{printf \"%.3f\", $ok/$4}")" "$(awk "BEGIN{printf \"%.3f\", $5/$4}")" $(($4 * 900)) $(($4 * 600)) $(($4 * 300)) $(($4 * 1500)) "$6" $(($4 * 2)) $(($4 / 2)) $(($4 / 3)) $(($4 / 3))
}

seed_instance(){ # $1 instance id, $2 token, $3 channel id, $4 channel name, $5 user id, $6 user name, $7 model
  local inst="$1" tok="$2" ch="$3" chname="$4" uid="$5" uname="$6" model="$7"
  step "metrics for $inst (12 buckets x 4 dimensions)"
  local metrics="" m sep=""
  for ago in 60 55 50 45 40 35 30 25 20 15 10 5; do
    local req=$((20 + ago % 7 * 3)) err=$((ago % 5 == 0 ? 3 : 1))
    for dim in "instance|$inst" "instance_channel|$inst:channel:$ch" "instance_user|$inst:user:$uid" "instance_model|$inst:model:$model"; do
      m=$(metric "$ago" "${dim%%|*}" "${dim##*|}" "$req" "$err" "3.$((ago % 9))")
      metrics="$metrics$sep$m"; sep=","
    done
  done
  local now; now=$(date -u +%FT%TZ)
  post_report "$inst" "$tok" "{\"instance_id\":\"$inst\",\"agent_id\":\"seed-$inst\",\"agent_version\":\"seed\",\"reported_at\":\"$now\",\"sequence\":10,\"last_log_id\":500,\"metric_batch_id\":\"seed-$inst-metrics-$(date +%s)\",\"aggregated_metrics\":[$metrics]}"

  step "samples + snapshots + runtime for $inst"
  local samples="{\"sample_kind\":\"error\",\"source_log_id\":401,\"created_at\":\"$now\",\"log_type\":\"error\",\"user_id\":$uid,\"username\":\"$uname\",\"channel_id\":$ch,\"model_name\":\"$model\",\"total_tokens\":120,\"use_time\":4.2,\"request_id\":\"seed-err-$inst\",\"error_summary\":\"upstream timeout (seeded)\"},{\"sample_kind\":\"slow\",\"source_log_id\":402,\"created_at\":\"$now\",\"log_type\":\"consume\",\"user_id\":$uid,\"username\":\"$uname\",\"channel_id\":$ch,\"model_name\":\"$model\",\"total_tokens\":900,\"use_time\":42.5,\"request_id\":\"seed-slow-$inst\"}"
  local snapshots="{\"channel_id\":$ch,\"channel_name\":\"$chname\",\"status\":\"enabled\",\"weight\":10,\"models_text\":\"$model,gpt-4o-mini,claude-sonnet\",\"captured_at\":\"$now\"},{\"channel_id\":$((ch + 1)),\"channel_name\":\"$chname-备用\",\"status\":\"disabled\",\"weight\":2,\"models_text\":\"$model\",\"captured_at\":\"$now\"}"
  local health="{\"checked_at\":\"$now\",\"target\":\"new-api\",\"status\":\"up\",\"http_status_code\":200,\"latency_ms\":18}"
  local sysm="{\"collected_at\":\"$now\",\"cpu_percent\":23.5,\"memory_used_percent\":61.2,\"disk_used_percent\":48.7,\"network_rx_bytes_per_second\":125000,\"network_tx_bytes_per_second\":88000,\"load_1m\":0.8}"
  local docker="{\"collected_at\":\"$now\",\"container_name\":\"new-api\",\"status\":\"Up 3 days\",\"running\":true}"
  post_report "$inst" "$tok" "{\"instance_id\":\"$inst\",\"agent_id\":\"seed-$inst\",\"agent_version\":\"seed\",\"reported_at\":\"$now\",\"sequence\":11,\"last_log_id\":510,\"log_samples\":[$samples],\"channel_snapshots\":[$snapshots],\"health_checks\":[$health],\"server_metrics\":[$sysm],\"docker_statuses\":[$docker]}"

  step "fresh error burst for $inst (raises recent_errors alert)"
  local events="" e sep2=""
  for n in 1 2 3; do
    e="{\"source_log_id\":$((520 + n)),\"created_at\":\"$now\",\"log_type\":\"error\",\"channel_id\":$ch,\"user_id\":$uid,\"username\":\"$uname\",\"model_name\":\"$model\",\"request_id\":\"seed-burst-$inst-$n\",\"error_summary\":\"seeded burst\"}"
    events="$events$sep2$e"; sep2=","
  done
  post_report "$inst" "$tok" "{\"instance_id\":\"$inst\",\"agent_id\":\"seed-$inst\",\"agent_version\":\"seed\",\"reported_at\":\"$now\",\"sequence\":12,\"last_log_id\":523,\"log_events\":[$events]}"
}

step "create instance inst-demo-a"
token_a=$(make_instance inst-demo-a)
step "create instance inst-demo-b"
token_b=$(make_instance inst-demo-b)

seed_instance inst-demo-a "$token_a" 77 "OpenAI-主力" 9 "alice" "gpt-4o"
seed_instance inst-demo-b "$token_b" 88 "Claude-备用" 12 "bob" "claude-sonnet"

step "notification channel that always fails (feeds failed deliveries for resend test)"
curl -fsS -b "$jar" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' -d '{"id":"seed-failing","channel_type":"dingtalk","name":"演示-必失败","webhook_url":"http://127.0.0.1:1","enabled":true,"secret":"seed"}' "$base/api/dashboard/notification-channels" >/dev/null

echo '[seed] done. 打开 / 走查：两实例切换、渠道快照、样本、告警(触发后由通知 runner 产出 failed 投递供重发)。'
