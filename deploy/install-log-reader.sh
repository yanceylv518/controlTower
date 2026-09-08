#!/usr/bin/env bash
# Opt-in log reader installation. Does not edit the running Agent configuration.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo 'Run with sudo'; exit 1; }
[[ -x /usr/bin/docker ]] || { echo '/usr/bin/docker is required'; exit 1; }
id ct-agent >/dev/null
here="$(cd "$(dirname "$0")" && pwd)"
[[ -f "$here/control-tower-log-reader" && -f "$here/control-tower-log-reader.service" ]]
if systemctl is-active --quiet control-tower-log-reader; then systemctl stop control-tower-log-reader; fi
install -m 0755 "$here/control-tower-log-reader" /usr/local/bin/control-tower-log-reader
install -m 0644 "$here/control-tower-log-reader.service" /etc/systemd/system/control-tower-log-reader.service
systemctl daemon-reload
systemctl enable control-tower-log-reader
systemctl restart control-tower-log-reader
echo 'Set CT_CONTAINER_LOG_SOCKET=/run/control-tower-log-reader/reader.sock in /etc/control-tower/agent.config, then restart control-tower-agent.'
