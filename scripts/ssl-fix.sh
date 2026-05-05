#!/usr/bin/env bash
set -euo pipefail

APP_DOMAIN="${APP_DOMAIN:-}"
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-}"
DELETE_OLD_CERT="${DELETE_OLD_CERT:-false}"
ENV_FILE="${ENV_FILE:-/opt/gomail/.env}"

log() {
  printf '[ssl-fix] %s\n' "$*"
}

fail() {
  printf '[ssl-fix] ERROR: %s\n' "$*" >&2
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

load_env_file() {
  if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
  fi
}

prompt_value() {
  local var_name="$1"
  local label="$2"
  local default_value="${3:-}"
  local current_value="${!var_name:-}"

  if [[ -n "$current_value" ]]; then
    return 0
  fi
  if [[ ! -t 0 ]]; then
    if [[ -n "$default_value" ]]; then
      printf -v "$var_name" '%s' "$default_value"
      return 0
    fi
    fail "missing required environment variable: $var_name"
  fi

  local prompt="$label"
  if [[ -n "$default_value" ]]; then
    prompt+=" [$default_value]"
  fi
  prompt+=": "
  read -r -p "$prompt" current_value
  if [[ -z "$current_value" ]]; then
    current_value="$default_value"
  fi
  if [[ -z "$current_value" ]]; then
    fail "$var_name cannot be empty"
  fi
  printf -v "$var_name" '%s' "$current_value"
}

ensure_prerequisites() {
  command_exists nginx || fail "nginx is required"
  command_exists certbot || fail "certbot is required"
  nginx -t >/dev/null
}

maybe_delete_old_cert() {
  if [[ "$DELETE_OLD_CERT" != "true" ]]; then
    return 0
  fi

  log "deleting existing certificate lineage for $APP_DOMAIN"
  certbot delete --non-interactive --cert-name "$APP_DOMAIN" || true
}

issue_certificate() {
  log "requesting certificate for $APP_DOMAIN"
  certbot --nginx --non-interactive --agree-tos -m "$LETSENCRYPT_EMAIL" -d "$APP_DOMAIN" --redirect
}

show_status() {
  log "installed certificates"
  certbot certificates || true
}

main() {
  require_root
  load_env_file
  prompt_value APP_DOMAIN "Primary SaaS domain"
  prompt_value LETSENCRYPT_EMAIL "Let's Encrypt email" "${DEFAULT_ADMIN_EMAIL:-}"
  ensure_prerequisites
  maybe_delete_old_cert
  issue_certificate
  systemctl reload nginx
  show_status
}

main "$@"