#!/usr/bin/env bash
# Control Tower Agent one-command installer (standalone DingTalk alert mode).
#
# Usage:
#   sudo ./install-agent.sh                          # interactive
#   sudo ./install-agent.sh --config my-agent.config # use a prepared config file
#   sudo ./install-agent.sh --dsn 'user:pass@tcp(127.0.0.1:3306)/newapi' \
#                           --webhook 'https://oapi.dingtalk.com/robot/send?access_token=...'
#
# Optional flags: --binary PATH   agent binary (default: auto-detect next to this script)
#                 --window N      alert window   (default 10)
#                 --threshold N   alert threshold (default 3)
#
# --config installs your file as-is (start from deploy/agent.standalone.config.example);
# the other modes generate the config for you. Either way the live config ends up
# at /etc/control-tower/agent.config. Re-running overwrites it and restarts the service.
set -euo pipefail

BIN_DIR=/usr/local/bin
CONF_DIR=/etc/control-tower
DATA_DIR=/var/lib/control-tower-agent
UNIT=/etc/systemd/system/control-tower-agent.service
RUN_USER=ct-agent

DSN="" WEBHOOK="" BINARY="" WINDOW=10 THRESHOLD=3 CONFIG_SRC=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config) CONFIG_SRC="$2"; shift 2 ;;
    --dsn) DSN="$2"; shift 2 ;;
    --webhook) WEBHOOK="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --window) WINDOW="$2"; shift 2 ;;
    --threshold) THRESHOLD="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

if [[ -n "$CONFIG_SRC" && ! -f "$CONFIG_SRC" ]]; then
  echo "config file not found: $CONFIG_SRC" >&2
  exit 1
fi

[[ $EUID -eq 0 ]] || { echo "please run with sudo/root" >&2; exit 1; }
command -v systemctl >/dev/null || { echo "systemd is required" >&2; exit 1; }

# Locate the agent binary.
if [[ -z "$BINARY" ]]; then
  here="$(cd "$(dirname "$0")" && pwd)"
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "unsupported arch $(uname -m); pass --binary" >&2; exit 1 ;;
  esac
  for candidate in "$here/control-tower-agent-linux-$arch" "$here/control-tower-agent" "$here/../dist/control-tower-agent-linux-$arch"; do
    [[ -f "$candidate" ]] && BINARY="$candidate" && break
  done
  [[ -n "$BINARY" ]] || { echo "agent binary not found next to installer; pass --binary PATH" >&2; exit 1; }
fi

# Interactive prompts for anything missing (skipped when --config is used).
if [[ -z "$CONFIG_SRC" ]]; then
  if [[ -z "$DSN" ]]; then
    echo "MySQL 只读 DSN，格式: user:password@tcp(127.0.0.1:3306)/newapi"
    echo "(只读账号只需要 logs 表: GRANT SELECT ON newapi.logs TO 'ct_readonly'@'%';)"
    read -rp "DSN: " DSN
  fi
  if [[ -z "$WEBHOOK" ]]; then
    echo "钉钉群机器人 Webhook (机器人安全设置: 自定义关键词 填 告警)"
    read -rp "Webhook URL: " WEBHOOK
  fi
  [[ -n "$DSN" && -n "$WEBHOOK" ]] || { echo "DSN and webhook are required" >&2; exit 1; }
fi

echo "==> installing binary to $BIN_DIR/control-tower-agent"
install -m 0755 "$BINARY" "$BIN_DIR/control-tower-agent"

echo "==> creating user and directories"
id -u "$RUN_USER" >/dev/null 2>&1 || useradd -r -s /usr/sbin/nologin "$RUN_USER"
mkdir -p "$CONF_DIR" "$DATA_DIR"
chown -R "$RUN_USER:$RUN_USER" "$DATA_DIR"

echo "==> writing $CONF_DIR/agent.config"
if [[ -n "$CONFIG_SRC" ]]; then
  install -m 0600 "$CONFIG_SRC" "$CONF_DIR/agent.config"
else
  host_tag="$(hostname -s 2>/dev/null || echo host)"
  cat > "$CONF_DIR/agent.config" <<EOF
CT_AGENT_ID=agent-$host_tag
CT_INSTANCE_ID=inst-$host_tag
CT_LOG_DSN=$DSN
CT_DATA_DIR=$DATA_DIR
CT_DINGTALK_WEBHOOK_URL=$WEBHOOK
CT_ALERT_ERROR_WINDOW=$WINDOW
CT_ALERT_ERROR_THRESHOLD=$THRESHOLD
CT_LOG_POLL_INTERVAL_SECONDS=30
EOF
  chmod 600 "$CONF_DIR/agent.config"
fi
chown "$RUN_USER:$RUN_USER" "$CONF_DIR/agent.config"

echo "==> running preflight"
if ! sudo -u "$RUN_USER" "$BIN_DIR/control-tower-agent" -config "$CONF_DIR/agent.config" -preflight; then
  echo "preflight failed; fix the config ($CONF_DIR/agent.config) and re-run" >&2
  exit 1
fi

echo "==> installing systemd unit"
cat > "$UNIT" <<EOF
[Unit]
Description=Control Tower Agent (new-api monitoring)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$RUN_USER
Group=$RUN_USER
ExecStart=$BIN_DIR/control-tower-agent -config $CONF_DIR/agent.config
Restart=on-failure
RestartSec=10
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$DATA_DIR
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now control-tower-agent
sleep 1
systemctl --no-pager --lines=5 status control-tower-agent || true

echo
echo "done. 首次启动自动从 logs 表当前位置开始监控（不回放历史）。"
echo "查看日志: journalctl -u control-tower-agent -f"
echo "卸载:     systemctl disable --now control-tower-agent; rm -f $UNIT $BIN_DIR/control-tower-agent; rm -rf $CONF_DIR $DATA_DIR"
