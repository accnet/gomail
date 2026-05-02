#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-}"
DOMAIN="${2:-}"
ENV_FILE="${ENV_FILE:-/opt/gomail/.env}"
SITES_AVAILABLE_DIR="${SITES_AVAILABLE_DIR:-/etc/nginx/sites-available}"
SITES_ENABLED_DIR="${SITES_ENABLED_DIR:-/etc/nginx/sites-enabled}"
NGINX_SITE_BASENAME="${NGINX_SITE_BASENAME:-}"
STATIC_SERVER_UPSTREAM="${STATIC_SERVER_UPSTREAM:-}"
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-}"
DELETE_CERT_ON_REMOVE="${DELETE_CERT_ON_REMOVE:-false}"

managed_marker="# Managed by GoMail custom domain SSL"

log() {
  printf '[custom-domain-ssl] %s\n' "$*"
}

fail() {
  printf '[custom-domain-ssl] ERROR: %s\n' "$*" >&2
  exit 1
}

require_root() {
  if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
    fail "run this script as root"
  fi
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

load_env_file() {
  [[ -f "$ENV_FILE" ]] || return 0

  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%%$'\r'}"
    [[ -n "$line" ]] || continue
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    [[ "$line" == *=* ]] || continue

    local key value
    key="${line%%=*}"
    value="${line#*=}"

    case "$key" in
      DEFAULT_ADMIN_EMAIL)
        DEFAULT_ADMIN_EMAIL="$value"
        ;;
      STATIC_SERVER_ADDR)
        STATIC_SERVER_ADDR="$value"
        ;;
    esac
  done < "$ENV_FILE"
}

validate_inputs() {
  [[ -n "$ACTION" ]] || fail "missing action"
  [[ -n "$DOMAIN" ]] || fail "missing domain"
  [[ "$DOMAIN" =~ ^[A-Za-z0-9.-]+$ ]] || fail "invalid domain"
  [[ "$DOMAIN" == *.* ]] || fail "domain must contain a dot"
  case "$ACTION" in
    provision|remove) ;;
    *) fail "unsupported action: $ACTION" ;;
  esac
}

site_basename() {
  if [[ -n "$NGINX_SITE_BASENAME" ]]; then
    printf '%s\n' "$NGINX_SITE_BASENAME"
    return 0
  fi
  printf '%s.conf\n' "$DOMAIN"
}

site_path() {
  printf '%s/%s\n' "$SITES_AVAILABLE_DIR" "$(site_basename)"
}

enabled_path() {
  printf '%s/%s\n' "$SITES_ENABLED_DIR" "$(site_basename)"
}

ensure_prerequisites() {
  command_exists nginx || fail "nginx is required"
  command_exists certbot || fail "certbot is required"
  install -d -m 0755 "$SITES_AVAILABLE_DIR" "$SITES_ENABLED_DIR"
}

ensure_upstream() {
  if [[ -z "$STATIC_SERVER_UPSTREAM" ]]; then
    if [[ -n "${STATIC_SERVER_ADDR:-}" ]]; then
      case "$STATIC_SERVER_ADDR" in
        http://*|https://*)
          STATIC_SERVER_UPSTREAM="$STATIC_SERVER_ADDR"
          ;;
        :*)
          STATIC_SERVER_UPSTREAM="http://127.0.0.1${STATIC_SERVER_ADDR}"
          ;;
        *)
          STATIC_SERVER_UPSTREAM="http://${STATIC_SERVER_ADDR}"
          ;;
      esac
    else
      STATIC_SERVER_UPSTREAM="http://127.0.0.1:8090"
    fi
  fi
}

ensure_letsencrypt_email() {
  if [[ -n "$LETSENCRYPT_EMAIL" ]]; then
    return 0
  fi
  LETSENCRYPT_EMAIL="${DEFAULT_ADMIN_EMAIL:-}"
  [[ -n "$LETSENCRYPT_EMAIL" ]] || fail "LETSENCRYPT_EMAIL or DEFAULT_ADMIN_EMAIL is required"
}

ensure_managed_or_missing() {
  local file
  file="$(site_path)"
  if [[ ! -f "$file" ]]; then
    return 0
  fi
  grep -Fqx "$managed_marker" "$file" || fail "refusing to modify unmanaged nginx site: $file"
}

write_site_file() {
  local file
  file="$(site_path)"
  ensure_managed_or_missing
  cat >"$file" <<EOF
$managed_marker
server {
  listen 80;
  listen [::]:80;
  server_name $DOMAIN;

  client_max_body_size 50m;

  location /.well-known/acme-challenge/ {
    root /var/www/html;
  }

  location / {
    proxy_http_version 1.1;
    proxy_set_header Host \$host;
    proxy_set_header X-Real-IP \$remote_addr;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;
    proxy_read_timeout 300s;
    proxy_pass $STATIC_SERVER_UPSTREAM;
  }
}
EOF
  ln -sf "$file" "$(enabled_path)"
}

reload_nginx() {
  nginx -t
  systemctl reload nginx
}

provision() {
  ensure_upstream
  ensure_letsencrypt_email
  log "writing nginx site for $DOMAIN"
  write_site_file
  reload_nginx
  log "requesting certificate for $DOMAIN"
  certbot --nginx --non-interactive --agree-tos -m "$LETSENCRYPT_EMAIL" -d "$DOMAIN" --redirect
  reload_nginx
}

remove() {
  local file link
  file="$(site_path)"
  link="$(enabled_path)"
  if [[ -f "$file" ]]; then
    grep -Fqx "$managed_marker" "$file" || fail "refusing to remove unmanaged nginx site: $file"
  fi
  rm -f "$link"
  rm -f "$file"
  reload_nginx
  if [[ "$DELETE_CERT_ON_REMOVE" == "true" ]]; then
    certbot delete --non-interactive --cert-name "$DOMAIN" || true
  fi
}

main() {
  require_root
  load_env_file
  validate_inputs
  ensure_prerequisites

  case "$ACTION" in
    provision)
      provision
      ;;
    remove)
      remove
      ;;
  esac
}

main "$@"