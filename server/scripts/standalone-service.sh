#!/usr/bin/env bash
# Shared by the legacy build entry points. Existing installations are preserved.

preflight_filehub_service() {
  if systemctl cat filehub.service >/dev/null 2>&1; then
    local service_user
    service_user=$(systemctl show filehub.service --property=User --value)
    if [[ -z "$service_user" || "$service_user" == root || "$service_user" == 0 ]]; then
      echo 'Existing FileHub runs as root. Migrate the service account before restarting it.' >&2
      return 1
    fi
  elif [[ -e /opt/filehub/index.db || -e /var/lib/filehub/index.db || -e /opt/filehub/secret ]]; then
    echo 'Existing FileHub state found. Restore its service unit before deploying.' >&2
    return 1
  fi
}

install_filehub_service_if_missing() {
  if systemctl cat filehub.service >/dev/null 2>&1; then
    return
  fi
  if ! getent passwd filehub >/dev/null; then
    useradd --system --user-group --home-dir /var/lib/filehub --no-create-home --shell /usr/sbin/nologin filehub
  fi
  install -m 0644 -- distribution/filehub.service /etc/systemd/system/filehub.service
  systemctl daemon-reload
}
