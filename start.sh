#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_DIR="$ROOT_DIR/.run"
API_PID_FILE="$RUN_DIR/api.pid"
SMTP_PID_FILE="$RUN_DIR/smtp.pid"
API_LOG_FILE="$RUN_DIR/api.log"
SMTP_LOG_FILE="$RUN_DIR/smtp.log"

mkdir -p "$RUN_DIR" "$ROOT_DIR/data/attachments" "$ROOT_DIR/data/raw-eml"

if [[ -f "$ROOT_DIR/.env.dev" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ROOT_DIR/.env.dev"
  set +a
fi

export APP_ENV="${APP_ENV:-development}"
export APP_NAME="${APP_NAME:-GoMail}"
export SAAS_DOMAIN="${SAAS_DOMAIN:-localhost}"
export APP_BASE_URL="${APP_BASE_URL:-http://localhost:8080}"
export API_BASE_URL="${API_BASE_URL:-http://localhost:8080/api}"
export HTTP_HOST="${HTTP_HOST:-0.0.0.0}"
export HTTP_PORT="${HTTP_PORT:-8080}"
export SMTP_HOST="${SMTP_HOST:-0.0.0.0}"
export SMTP_PORT="${SMTP_PORT:-2525}"
export SMTP_HOSTNAME="${SMTP_HOSTNAME:-mx.localhost}"
export MX_TARGET="${MX_TARGET:-mx.localhost}"
export DATABASE_URL="${DATABASE_URL:-postgres://gomail:gomail_password@127.0.0.1:5432/gomail?sslmode=disable}"
export REDIS_ADDR="${REDIS_ADDR:-127.0.0.1:6379}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-}"
export REDIS_DB="${REDIS_DB:-0}"
export JWT_SECRET="${JWT_SECRET:-dev-secret-change-me}"
export ACCESS_TOKEN_TTL_MINUTES="${ACCESS_TOKEN_TTL_MINUTES:-15}"
export REFRESH_TOKEN_TTL_DAYS="${REFRESH_TOKEN_TTL_DAYS:-30}"
export DEFAULT_ADMIN_EMAIL="${DEFAULT_ADMIN_EMAIL:-admin@example.com}"
export DEFAULT_ADMIN_PASSWORD="${DEFAULT_ADMIN_PASSWORD:-change-me-before-deploy}"
export DEFAULT_ADMIN_NAME="${DEFAULT_ADMIN_NAME:-Super Admin}"
export DEFAULT_ADMIN_MAX_DOMAINS="${DEFAULT_ADMIN_MAX_DOMAINS:-100}"
export DEFAULT_ADMIN_MAX_INBOXES="${DEFAULT_ADMIN_MAX_INBOXES:-1000}"
export DEFAULT_ADMIN_MAX_MESSAGE_SIZE_MB="${DEFAULT_ADMIN_MAX_MESSAGE_SIZE_MB:-25}"
export DEFAULT_ADMIN_MAX_ATTACHMENT_SIZE_MB="${DEFAULT_ADMIN_MAX_ATTACHMENT_SIZE_MB:-25}"
export DEFAULT_ADMIN_MAX_STORAGE_GB="${DEFAULT_ADMIN_MAX_STORAGE_GB:-100}"
export STORAGE_ROOT="${STORAGE_ROOT:-$ROOT_DIR/data}"
export ATTACHMENT_STORAGE_ROOT="${ATTACHMENT_STORAGE_ROOT:-$ROOT_DIR/data/attachments}"
export RAW_EMAIL_STORAGE_ROOT="${RAW_EMAIL_STORAGE_ROOT:-$ROOT_DIR/data/raw-eml}"
export DOMAIN_VERIFY_TIMEOUT_SECONDS="${DOMAIN_VERIFY_TIMEOUT_SECONDS:-10}"
export DOMAIN_RECHECK_INTERVAL_MINUTES="${DOMAIN_RECHECK_INTERVAL_MINUTES:-30}"
export CLAMAV_ENABLED="${CLAMAV_ENABLED:-false}"
export CLAMAV_ADDR="${CLAMAV_ADDR:-clamav:3310}"
export BLOCK_FLAGGED_ATTACHMENTS="${BLOCK_FLAGGED_ATTACHMENTS:-true}"
export ALLOW_ADMIN_ATTACHMENT_OVERRIDE="${ALLOW_ADMIN_ATTACHMENT_OVERRIDE:-true}"

port_open() {
  local host="$1"
  local port="$2"
  if command -v nc >/dev/null 2>&1; then
    nc -z "$host" "$port" >/dev/null 2>&1
  else
    bash -c ">/dev/tcp/$host/$port" >/dev/null 2>&1
  fi
}

pid_is_running() {
  local pid="$1"
  kill -0 "$pid" >/dev/null 2>&1
}

cleanup_pid() {
  local pid_file="$1"
  if [[ -f "$pid_file" ]]; then
    local pid
    pid="$(cat "$pid_file")"
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      sleep 1
    fi
    rm -f "$pid_file"
  fi
}

port_pids() {
  local port="$1"
  ss -ltnp "( sport = :$port )" 2>/dev/null | sed -n 's/.*pid=\([0-9]\+\).*/\1/p' | sort -u
}

ensure_port_available() {
  local port="$1"
  local label="$2"
  if ! port_open 127.0.0.1 "$port"; then
    return 0
  fi
  local pids
  pids="$(port_pids "$port" | tr '\n' ' ' | xargs || true)"
  echo "$label port $port is already in use${pids:+ by PID(s): $pids}" >&2
  echo "Stop the conflicting process or change the port before running ./start.sh" >&2
  exit 1
}

start_dependency() {
  local service="$1"
  if command -v docker >/dev/null 2>&1; then
    docker compose up -d "$service" >/dev/null
  else
    echo "docker is required to start missing dependency: $service" >&2
    exit 1
  fi
}

wait_for_port() {
  local host="$1"
  local port="$2"
  local label="$3"
  local retries="${4:-30}"
  local i
  for ((i=0; i<retries; i++)); do
    if port_open "$host" "$port"; then
      return 0
    fi
    sleep 1
  done
  echo "$label did not become ready on $host:$port" >&2
  exit 1
}

launch_process() {
  local pid_file="$1"
  local log_file="$2"
  shift 2
  if command -v setsid >/dev/null 2>&1; then
    (
      cd "$ROOT_DIR"
      setsid "$@" >"$log_file" 2>&1 < /dev/null &
      echo $! >"$pid_file"
    )
  else
    (
      cd "$ROOT_DIR"
      nohup "$@" >"$log_file" 2>&1 < /dev/null &
      echo $! >"$pid_file"
    )
  fi
}

echo "Preparing dependencies..."
if ! port_open 127.0.0.1 5432; then
  start_dependency postgres
fi
if ! port_open 127.0.0.1 6379; then
  start_dependency redis
fi
wait_for_port 127.0.0.1 5432 "Postgres"
wait_for_port 127.0.0.1 6379 "Redis"

echo "Building binaries..."
(cd "$ROOT_DIR" && go build -o api ./cmd/api && go build -o smtp ./cmd/smtp)

echo "Restarting local processes..."
cleanup_pid "$API_PID_FILE"
cleanup_pid "$SMTP_PID_FILE"
ensure_port_available "$HTTP_PORT" "API"
ensure_port_available "$SMTP_PORT" "SMTP"

launch_process "$API_PID_FILE" "$API_LOG_FILE" ./api
launch_process "$SMTP_PID_FILE" "$SMTP_LOG_FILE" ./smtp

sleep 1
if ! pid_is_running "$(cat "$API_PID_FILE")"; then
  echo "API failed to stay up. Check $API_LOG_FILE" >&2
  exit 1
fi
if ! pid_is_running "$(cat "$SMTP_PID_FILE")"; then
  echo "SMTP failed to stay up. Check $SMTP_LOG_FILE" >&2
  exit 1
fi

wait_for_port 127.0.0.1 "$HTTP_PORT" "API"
wait_for_port 127.0.0.1 "$SMTP_PORT" "SMTP"

echo "GoMail started"
echo "API:   $APP_BASE_URL/app/"
echo "Health: $APP_BASE_URL/healthz"
echo "SMTP:  localhost:$SMTP_PORT"
echo "Admin: $DEFAULT_ADMIN_EMAIL"
echo "Logs:  $API_LOG_FILE $SMTP_LOG_FILE"
