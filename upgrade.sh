#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_USER="${APP_USER:-gomail}"
APP_GROUP="${APP_GROUP:-gomail}"
INSTALL_ROOT="${INSTALL_ROOT:-/opt/gomail}"
APP_ROOT="${APP_ROOT:-$INSTALL_ROOT/current}"
BIN_DIR="${BIN_DIR:-$INSTALL_ROOT/bin}"
ENV_FILE="${ENV_FILE:-$INSTALL_ROOT/.env}"
COMPOSE_FILE="${COMPOSE_FILE:-$INSTALL_ROOT/docker-compose.infra.yml}"
RUN_TESTS="${RUN_TESTS:-false}"
RESTART_INFRA="${RESTART_INFRA:-false}"

log() {
  printf '[upgrade] %s\n' "$*"
}

fail() {
  printf '[upgrade] ERROR: %s\n' "$*" >&2
  exit 1
}

require_root() {
  if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
    fail "run this script as root or with sudo"
  fi
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

require_file() {
  local path="$1"
  [[ -e "$path" ]] || fail "required path not found: $path"
}

ensure_prerequisites() {
  command_exists rsync || fail "rsync is required"
  command_exists go || fail "go is required"
  command_exists systemctl || fail "systemctl is required"
  command_exists curl || fail "curl is required"
  require_file "$ENV_FILE"
  require_file "$COMPOSE_FILE"
  require_file "$SCRIPT_DIR/go.mod"
}

ensure_layout() {
  require_file "$APP_ROOT"
  install -d -m 0755 "$APP_ROOT" "$BIN_DIR"
}

sync_source() {
  log "syncing repository into $APP_ROOT"
  rsync -a --delete \
    --exclude '.git' \
    --exclude '.github' \
    --exclude '.run' \
    --exclude 'data/' \
    --exclude '.env' \
    --exclude '.env.dev' \
    "$SCRIPT_DIR/" "$APP_ROOT/"
  chown -R "$APP_USER:$APP_GROUP" "$APP_ROOT"
}

maybe_run_tests() {
  if [[ "$RUN_TESTS" != "true" ]]; then
    return 0
  fi
  log "running focused pre-deploy checks"
  pushd "$APP_ROOT" >/dev/null
  go test ./...
  popd >/dev/null
}

build_release() {
  log "building fresh binaries"
  local release_dir
  release_dir="$(mktemp -d "$INSTALL_ROOT/release.XXXXXX")"

  pushd "$APP_ROOT" >/dev/null
  export PATH="/usr/local/go/bin:$PATH"
  export CGO_ENABLED=0
  go mod download
  go build -o "$release_dir/api" ./cmd/api
  go build -o "$release_dir/smtp" ./cmd/smtp
  go build -o "$release_dir/static-server" ./cmd/static-server
  popd >/dev/null

  install -o "$APP_USER" -g "$APP_GROUP" -m 0755 "$release_dir/api" "$BIN_DIR/api"
  install -o "$APP_USER" -g "$APP_GROUP" -m 0755 "$release_dir/smtp" "$BIN_DIR/smtp"
  install -o "$APP_USER" -g "$APP_GROUP" -m 0755 "$release_dir/static-server" "$BIN_DIR/static-server"
  rm -rf "$release_dir"
}

restart_services() {
  if [[ "$RESTART_INFRA" == "true" ]]; then
    log "restarting infrastructure containers"
    systemctl restart gomail-infra.service
  else
    log "reloading infrastructure containers"
    docker compose -f "$COMPOSE_FILE" up -d >/dev/null
  fi

  log "restarting application services"
  systemctl restart gomail-api.service
  systemctl restart gomail-static.service
  systemctl restart gomail-smtp.service
}

wait_for_health() {
  local attempts="${1:-15}"
  local delay_seconds="${2:-2}"

  for ((attempt = 1; attempt <= attempts; attempt++)); do
    if curl -fsS http://127.0.0.1:8080/healthz >/dev/null \
      && curl -fsS http://127.0.0.1:8090/healthz >/dev/null \
      && systemctl --no-pager --quiet is-active gomail-api.service \
      && systemctl --no-pager --quiet is-active gomail-static.service \
      && systemctl --no-pager --quiet is-active gomail-smtp.service; then
      return 0
    fi

    if (( attempt < attempts )); then
      sleep "$delay_seconds"
    fi
  done

  return 1
}

health_check() {
  log "checking service health"
  if wait_for_health 20 2; then
    return 0
  fi

  systemctl --no-pager status gomail-api.service gomail-static.service gomail-smtp.service || true
  fail "services did not become healthy in time"
}

print_summary() {
  cat <<EOF

GoMail upgrade completed.

Updated app root:  $APP_ROOT
Updated binaries:  $BIN_DIR
Environment file:  $ENV_FILE

If something looks wrong, inspect:
  journalctl -u gomail-api.service -n 100 --no-pager
  journalctl -u gomail-static.service -n 100 --no-pager
  journalctl -u gomail-smtp.service -n 100 --no-pager
EOF
}

main() {
  require_root
  ensure_prerequisites
  ensure_layout
  sync_source
  maybe_run_tests
  build_release
  restart_services
  health_check
  print_summary
}

main "$@"