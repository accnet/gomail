#!/usr/bin/env bash
set -euo pipefail

STATIC_SITES_BASE_DOMAIN="${STATIC_SITES_BASE_DOMAIN:-}"
WILDCARD_SSL_MODE="${WILDCARD_SSL_MODE:-existing}"
WILDCARD_CERT_FILE="${WILDCARD_CERT_FILE:-}"
WILDCARD_KEY_FILE="${WILDCARD_KEY_FILE:-}"
WILDCARD_CERT_NAME="${WILDCARD_CERT_NAME:-}"
CF_API_TOKEN="${CF_API_TOKEN:-}"
ENV_FILE="${ENV_FILE:-/opt/gomail/.env}"
NGINX_WILDCARD_SITE="${NGINX_WILDCARD_SITE:-/etc/nginx/sites-available/gomail-wildcard.conf}"
NGINX_CERT_DIR="${NGINX_CERT_DIR:-/etc/nginx/certs}"
CERTBOT_CREDENTIALS_DIR="${CERTBOT_CREDENTIALS_DIR:-/root/.secrets/certbot}"
CERTBOT_CLOUDFLARE_CREDS="${CERTBOT_CLOUDFLARE_CREDS:-$CERTBOT_CREDENTIALS_DIR/cloudflare.ini}"

log() {
  printf '[wildcard-ssl] %s\n' "$*"
}

fail() {
  printf '[wildcard-ssl] ERROR: %s\n' "$*" >&2
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
    while IFS= read -r line || [[ -n "$line" ]]; do
      line="${line%$'\r'}"
      [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
      if [[ "$line" =~ ^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]]; then
        export "${BASH_REMATCH[1]}=${BASH_REMATCH[2]}"
      fi
    done <"$ENV_FILE"
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
  command_exists openssl || fail "openssl is required"
  case "$WILDCARD_SSL_MODE" in
    existing)
      [[ -f "$WILDCARD_CERT_FILE" ]] || fail "certificate file not found: $WILDCARD_CERT_FILE"
      [[ -f "$WILDCARD_KEY_FILE" ]] || fail "key file not found: $WILDCARD_KEY_FILE"
      ;;
    cloudflare)
      command_exists certbot || fail "certbot is required"
      [[ -n "$CF_API_TOKEN" ]] || fail "CF_API_TOKEN is required for WILDCARD_SSL_MODE=cloudflare"
      ;;
    *)
      fail "WILDCARD_SSL_MODE must be one of: existing, cloudflare"
      ;;
  esac
}

validate_certificate() {
  log "validating wildcard certificate"
  openssl x509 -in "$WILDCARD_CERT_FILE" -noout >/dev/null
  openssl pkey -in "$WILDCARD_KEY_FILE" -noout >/dev/null
}

ensure_certbot_cloudflare_plugin() {
  if certbot plugins 2>/dev/null | grep -qi dns-cloudflare; then
    return 0
  fi

  log "installing certbot Cloudflare DNS plugin"
  apt-get update
  apt-get install -y python3-certbot-dns-cloudflare
}

write_cloudflare_credentials() {
  install -d -m 0700 "$CERTBOT_CREDENTIALS_DIR"
  cat >"$CERTBOT_CLOUDFLARE_CREDS" <<EOF
dns_cloudflare_api_token = $CF_API_TOKEN
EOF
  chmod 0600 "$CERTBOT_CLOUDFLARE_CREDS"
}

issue_cloudflare_certificate() {
  if [[ -z "$WILDCARD_CERT_NAME" ]]; then
    WILDCARD_CERT_NAME="wildcard-${STATIC_SITES_BASE_DOMAIN//./-}"
  fi

  ensure_certbot_cloudflare_plugin
  write_cloudflare_credentials

  log "requesting wildcard certificate for *.${STATIC_SITES_BASE_DOMAIN} via Cloudflare DNS"
  certbot certonly \
    --non-interactive \
    --agree-tos \
    --dns-cloudflare \
    --dns-cloudflare-credentials "$CERTBOT_CLOUDFLARE_CREDS" \
    -m "${DEFAULT_ADMIN_EMAIL:-admin@${STATIC_SITES_BASE_DOMAIN}}" \
    --cert-name "$WILDCARD_CERT_NAME" \
    -d "$STATIC_SITES_BASE_DOMAIN" \
    -d "*.${STATIC_SITES_BASE_DOMAIN}"

  WILDCARD_CERT_FILE="/etc/letsencrypt/live/$WILDCARD_CERT_NAME/fullchain.pem"
  WILDCARD_KEY_FILE="/etc/letsencrypt/live/$WILDCARD_CERT_NAME/privkey.pem"
}

install_certificate_files() {
  local cert_target key_target
  cert_target="$NGINX_CERT_DIR/${STATIC_SITES_BASE_DOMAIN}.crt"
  key_target="$NGINX_CERT_DIR/${STATIC_SITES_BASE_DOMAIN}.key"

  install -d -m 0755 "$NGINX_CERT_DIR"
  install -m 0644 "$WILDCARD_CERT_FILE" "$cert_target"
  install -m 0600 "$WILDCARD_KEY_FILE" "$key_target"
}

write_nginx_site() {
  local cert_target key_target
  cert_target="$NGINX_CERT_DIR/${STATIC_SITES_BASE_DOMAIN}.crt"
  key_target="$NGINX_CERT_DIR/${STATIC_SITES_BASE_DOMAIN}.key"

  log "writing nginx wildcard SSL site"
  cat >"$NGINX_WILDCARD_SITE" <<EOF
server {
  listen 80;
  listen [::]:80;
  server_name *.$STATIC_SITES_BASE_DOMAIN;

  return 301 https://\$host\$request_uri;
}

server {
  listen 443 ssl http2;
  listen [::]:443 ssl http2;
  server_name *.$STATIC_SITES_BASE_DOMAIN;

  ssl_certificate $cert_target;
  ssl_certificate_key $key_target;
  ssl_session_timeout 1d;
  ssl_session_cache shared:GoMailWildcardSSL:10m;
  ssl_protocols TLSv1.2 TLSv1.3;

  client_max_body_size 50m;

  location / {
    proxy_http_version 1.1;
    proxy_set_header Host \$host;
    proxy_set_header X-Real-IP \$remote_addr;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
    proxy_read_timeout 300s;
    proxy_pass http://127.0.0.1:8090;
  }
}
EOF

  ln -sf "$NGINX_WILDCARD_SITE" /etc/nginx/sites-enabled/gomail-wildcard.conf
}

reload_nginx() {
  nginx -t
  systemctl reload nginx
}

print_summary() {
  cat <<EOF

Wildcard SSL enabled for *.$STATIC_SITES_BASE_DOMAIN

Installed cert: $NGINX_CERT_DIR/${STATIC_SITES_BASE_DOMAIN}.crt
Installed key:  $NGINX_CERT_DIR/${STATIC_SITES_BASE_DOMAIN}.key
nginx site:     $NGINX_WILDCARD_SITE

Notes:
  1. This expects DNS wildcard *.${STATIC_SITES_BASE_DOMAIN} to point to this VPS.
  2. The wildcard certificate itself must already cover *.${STATIC_SITES_BASE_DOMAIN}.
  3. Exact hosts such as the main app domain still use their own nginx server block.
EOF
}

main() {
  require_root
  load_env_file
  prompt_value STATIC_SITES_BASE_DOMAIN "Wildcard/static sites base domain"

  if [[ ! -t 0 && -z "$WILDCARD_SSL_MODE" ]]; then
    WILDCARD_SSL_MODE="existing"
  fi
  prompt_value WILDCARD_SSL_MODE "Wildcard SSL mode (existing or cloudflare)" "$WILDCARD_SSL_MODE"

  ensure_prerequisites

  case "$WILDCARD_SSL_MODE" in
    existing)
      prompt_value WILDCARD_CERT_FILE "Wildcard certificate file path"
      prompt_value WILDCARD_KEY_FILE "Wildcard private key file path"
      validate_certificate
      ;;
    cloudflare)
      prompt_value CF_API_TOKEN "Cloudflare API token"
      issue_cloudflare_certificate
      validate_certificate
      ;;
  esac

  install_certificate_files
  write_nginx_site
  reload_nginx
  print_summary
}

main "$@"