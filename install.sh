#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_USER="${APP_USER:-gomail}"
APP_GROUP="${APP_GROUP:-gomail}"
INSTALL_ROOT="${INSTALL_ROOT:-/opt/gomail}"
APP_ROOT="${APP_ROOT:-$INSTALL_ROOT/current}"
BIN_DIR="${BIN_DIR:-$INSTALL_ROOT/bin}"
ENV_FILE="${ENV_FILE:-$INSTALL_ROOT/.env}"
LOG_DIR="${LOG_DIR:-/var/log/gomail}"
DATA_ROOT="${DATA_ROOT:-/var/lib/gomail/data}"
COMPOSE_FILE="${COMPOSE_FILE:-$INSTALL_ROOT/docker-compose.infra.yml}"
NGINX_SITE="${NGINX_SITE:-/etc/nginx/sites-available/gomail.conf}"
SYSTEMD_DIR="/etc/systemd/system"
GO_VERSION="${GO_VERSION:-1.26.1}"
SSH_PORT="${SSH_PORT:-22}"
ENABLE_TLS="${ENABLE_TLS:-auto}"
ENABLE_UFW="${ENABLE_UFW:-true}"
FORCE_WRITE_ENV="${FORCE_WRITE_ENV:-false}"

APP_DOMAIN="${APP_DOMAIN:-}"
SAAS_DOMAIN="${SAAS_DOMAIN:-}"
STATIC_SITES_BASE_DOMAIN="${STATIC_SITES_BASE_DOMAIN:-}"
SMTP_HOSTNAME="${SMTP_HOSTNAME:-}"
MX_TARGET="${MX_TARGET:-}"
SMTP_AUTH_HOSTNAME="${SMTP_AUTH_HOSTNAME:-}"
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-}"
PUBLIC_IP="${PUBLIC_IP:-}"
DEFAULT_ADMIN_EMAIL="${DEFAULT_ADMIN_EMAIL:-}"
DEFAULT_ADMIN_PASSWORD="${DEFAULT_ADMIN_PASSWORD:-}"
JWT_SECRET="${JWT_SECRET:-}"
DB_PASSWORD="${DB_PASSWORD:-}"
DKIM_KEY_ENCRYPTION_SECRET="${DKIM_KEY_ENCRYPTION_SECRET:-}"
DATABASE_NAME="${DATABASE_NAME:-gomail}"
DATABASE_USER="${DATABASE_USER:-gomail}"

log() {
  printf '[install] %s\n' "$*"
}

fail() {
  printf '[install] ERROR: %s\n' "$*" >&2
  exit 1
}

require_root() {
  if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
    fail "run this script as root or with sudo"
  fi
}

require_debian_family() {
  if [[ ! -r /etc/os-release ]]; then
    fail "cannot detect operating system"
  fi
  . /etc/os-release
  case "${ID:-}" in
    ubuntu|debian) ;;
    *)
      fail "this installer currently supports Ubuntu/Debian only"
      ;;
  esac
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
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

prompt_secret() {
  local var_name="$1"
  local label="$2"
  local current_value="${!var_name:-}"
  if [[ -n "$current_value" ]]; then
    return 0
  fi
  if [[ ! -t 0 ]]; then
    fail "missing required environment variable: $var_name"
  fi
  read -r -s -p "$label: " current_value
  printf '\n'
  if [[ -z "$current_value" ]]; then
    fail "$var_name cannot be empty"
  fi
  printf -v "$var_name" '%s' "$current_value"
}

random_secret() {
  openssl rand -hex "$1"
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      printf 'amd64'
      ;;
    aarch64|arm64)
      printf 'arm64'
      ;;
    *)
      fail "unsupported CPU architecture: $(uname -m)"
      ;;
  esac
}

ensure_base_packages() {
  log "installing base packages"
  apt-get update
  apt-get install -y ca-certificates curl gnupg lsb-release ufw git make rsync tar xz-utils nginx openssl certbot python3-certbot-nginx
}

ensure_docker() {
  if command_exists docker && docker compose version >/dev/null 2>&1; then
    systemctl enable --now docker
    return 0
  fi

  log "installing Docker Engine and Compose plugin"
  install -m 0755 -d /etc/apt/keyrings
  if [[ ! -f /etc/apt/keyrings/docker.asc ]]; then
    curl -fsSL https://download.docker.com/linux/$(. /etc/os-release && echo "$ID")/gpg -o /etc/apt/keyrings/docker.asc
    chmod a+r /etc/apt/keyrings/docker.asc
  fi

  local codename
  codename="$(. /etc/os-release && echo "${VERSION_CODENAME:-}")"
  if [[ -z "$codename" ]]; then
    fail "cannot determine distro codename for Docker repository"
  fi

  cat >/etc/apt/sources.list.d/docker.list <<EOF
deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$(. /etc/os-release && echo "$ID") $codename stable
EOF

  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
}

ensure_go() {
  if command_exists go; then
    local installed
    installed="$(go version | awk '{print $3}')"
    if [[ "$installed" == "go$GO_VERSION" ]]; then
      return 0
    fi
  fi

  log "installing Go $GO_VERSION"
  local arch tmp_tar
  arch="$(detect_arch)"
  tmp_tar="/tmp/go${GO_VERSION}.linux-${arch}.tar.gz"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" -o "$tmp_tar"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tmp_tar"
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
}

ensure_app_user() {
  if ! id -u "$APP_USER" >/dev/null 2>&1; then
    log "creating system user $APP_USER"
    useradd --system --home-dir /var/lib/gomail --create-home --shell /usr/sbin/nologin "$APP_USER"
  fi
}

detect_public_ip() {
  if [[ -n "$PUBLIC_IP" ]]; then
    return 0
  fi
  if command_exists curl; then
    PUBLIC_IP="$(curl -fsSL https://api.ipify.org || true)"
  fi
}

collect_config() {
  prompt_value APP_DOMAIN "Primary SaaS domain (for example mail.example.com)"

  local guessed_base=""
  if [[ -n "$APP_DOMAIN" && "$APP_DOMAIN" == *.* ]]; then
    guessed_base="${APP_DOMAIN#*.}"
  fi
  prompt_value SAAS_DOMAIN "Base SaaS domain for hosted sites" "$guessed_base"
  prompt_value STATIC_SITES_BASE_DOMAIN "Wildcard/static sites base domain" "$SAAS_DOMAIN"
  prompt_value SMTP_HOSTNAME "Inbound SMTP hostname" "mx.$SAAS_DOMAIN"
  prompt_value MX_TARGET "MX target hostname" "$SMTP_HOSTNAME"
  prompt_value SMTP_AUTH_HOSTNAME "SMTP relay hostname" "smtp.$SAAS_DOMAIN"
  prompt_value DEFAULT_ADMIN_EMAIL "Initial super admin email" "admin@$SAAS_DOMAIN"

  detect_public_ip
  prompt_value PUBLIC_IP "Public IPv4 of this VPS" "$PUBLIC_IP"

  if [[ -z "$DB_PASSWORD" ]]; then
    DB_PASSWORD="$(random_secret 16)"
  fi
  if [[ -z "$JWT_SECRET" ]]; then
    JWT_SECRET="$(random_secret 32)"
  fi
  if [[ -z "$DKIM_KEY_ENCRYPTION_SECRET" ]]; then
    DKIM_KEY_ENCRYPTION_SECRET="$(random_secret 32)"
  fi
  prompt_secret DEFAULT_ADMIN_PASSWORD "Initial super admin password"

  if [[ ${#DEFAULT_ADMIN_PASSWORD} -lt 12 ]]; then
    fail "DEFAULT_ADMIN_PASSWORD must be at least 12 characters"
  fi
  if [[ ${#JWT_SECRET} -lt 32 ]]; then
    fail "JWT_SECRET must be at least 32 characters"
  fi
  if [[ ${#DKIM_KEY_ENCRYPTION_SECRET} -lt 32 ]]; then
    fail "DKIM_KEY_ENCRYPTION_SECRET must be at least 32 characters"
  fi
}

prepare_directories() {
  log "preparing filesystem layout"
  install -d -m 0755 "$INSTALL_ROOT" "$APP_ROOT" "$BIN_DIR" "$LOG_DIR"
  install -d -m 0755 \
    "$DATA_ROOT" \
    "$DATA_ROOT/attachments" \
    "$DATA_ROOT/raw-eml" \
    "$DATA_ROOT/static-sites/live" \
    "$DATA_ROOT/static-sites/staging" \
    "$DATA_ROOT/static-sites/tmp" \
    "$DATA_ROOT/traefik-dynamic/generated"
  chown -R "$APP_USER:$APP_GROUP" "$INSTALL_ROOT" "$LOG_DIR" /var/lib/gomail
}

sync_source() {
  log "copying repository into $APP_ROOT"
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

write_env_file() {
  if [[ -f "$ENV_FILE" && "$FORCE_WRITE_ENV" != "true" ]]; then
    log "keeping existing environment file at $ENV_FILE"
    return 0
  fi

  log "writing production environment file"
  cat >"$ENV_FILE" <<EOF
APP_ENV=production
APP_NAME=GoMail
APP_BASE_URL=https://$APP_DOMAIN
API_BASE_URL=https://$APP_DOMAIN/api
SAAS_DOMAIN=$SAAS_DOMAIN

HTTP_HOST=127.0.0.1
HTTP_PORT=8080
TRUSTED_PROXIES=127.0.0.1/32

SMTP_HOST=0.0.0.0
SMTP_PORT=25
SMTP_HOSTNAME=$SMTP_HOSTNAME
MX_TARGET=$MX_TARGET
MAX_MESSAGE_SIZE_MB=25

DATABASE_URL=postgres://$DATABASE_USER:$DB_PASSWORD@127.0.0.1:5432/$DATABASE_NAME?sslmode=disable

REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=
REDIS_DB=0

JWT_SECRET=$JWT_SECRET
ACCESS_TOKEN_TTL_MINUTES=15
REFRESH_TOKEN_TTL_DAYS=30

DEFAULT_ADMIN_EMAIL=$DEFAULT_ADMIN_EMAIL
DEFAULT_ADMIN_PASSWORD=$DEFAULT_ADMIN_PASSWORD
DEFAULT_ADMIN_NAME="Super Admin"
DEFAULT_ADMIN_MAX_DOMAINS=100
DEFAULT_ADMIN_MAX_INBOXES=1000
DEFAULT_ADMIN_MAX_MESSAGE_SIZE_MB=25
DEFAULT_ADMIN_MAX_ATTACHMENT_SIZE_MB=25
DEFAULT_ADMIN_MAX_STORAGE_GB=100
DEFAULT_ADMIN_MAX_WEBSITES=100

STORAGE_ROOT=$DATA_ROOT
ATTACHMENT_STORAGE_ROOT=$DATA_ROOT/attachments
RAW_EMAIL_STORAGE_ROOT=$DATA_ROOT/raw-eml
STATIC_SITES_ROOT=$DATA_ROOT/static-sites
STATIC_SITES_MAX_ARCHIVE_BYTES=52428800
STATIC_SITES_MAX_EXTRACTED_BYTES=209715200
STATIC_SITES_MAX_FILE_COUNT=5000
STATIC_SITES_BASE_DOMAIN=$STATIC_SITES_BASE_DOMAIN
STATIC_SERVER_ADDR=127.0.0.1:8090

TRAEFIK_DYNAMIC_CONF_DIR=$DATA_ROOT/traefik-dynamic/generated
TRAEFIK_PUBLIC_IP=$PUBLIC_IP

DOMAIN_VERIFY_TIMEOUT_SECONDS=10
DOMAIN_RECHECK_INTERVAL_MINUTES=30

CLAMAV_ENABLED=false
CLAMAV_ADDR=127.0.0.1:3310
BLOCK_FLAGGED_ATTACHMENTS=true
ALLOW_ADMIN_ATTACHMENT_OVERRIDE=true

SMTP_AUTH_ENABLED=true
SMTP_AUTH_HOSTNAME=$SMTP_AUTH_HOSTNAME
SMTP_AUTH_PORT=587
SMTP_AUTH_TLS_PORT=465
SMTP_RELAY_HOSTNAME=$SMTP_AUTH_HOSTNAME
SMTP_RELAY_PUBLIC_IP=$PUBLIC_IP
DKIM_ENABLED=true
DKIM_SELECTOR=gomail
DKIM_KEY_ENCRYPTION_SECRET=$DKIM_KEY_ENCRYPTION_SECRET
OUTBOUND_MODE=direct
OUTBOUND_RELAY_HOST=
OUTBOUND_RELAY_PORT=587
OUTBOUND_RELAY_USER=
OUTBOUND_RELAY_PASS=
MAX_DAILY_SEND_PER_KEY=500

SEED_DEMO_DATA=false
EOF
  chown "$APP_USER:$APP_GROUP" "$ENV_FILE"
  chmod 0640 "$ENV_FILE"
}

write_infra_compose() {
  log "writing Docker Compose file for Postgres and Redis"
  cat >"$COMPOSE_FILE" <<EOF
services:
  postgres:
    image: postgres:16-alpine
    container_name: gomail-postgres
    restart: unless-stopped
    environment:
      POSTGRES_DB: $DATABASE_NAME
      POSTGRES_USER: $DATABASE_USER
      POSTGRES_PASSWORD: $DB_PASSWORD
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - gomail_postgres:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    container_name: gomail-redis
    restart: unless-stopped
    command: ["redis-server", "--appendonly", "yes"]
    ports:
      - "127.0.0.1:6379:6379"
    volumes:
      - gomail_redis:/data

volumes:
  gomail_postgres:
  gomail_redis:
EOF
}

build_binaries() {
  log "building Go binaries"
  export PATH="/usr/local/go/bin:$PATH"
  export CGO_ENABLED=0
  pushd "$APP_ROOT" >/dev/null
  go mod download
  go build -o "$BIN_DIR/api" ./cmd/api
  go build -o "$BIN_DIR/smtp" ./cmd/smtp
  go build -o "$BIN_DIR/static-server" ./cmd/static-server
  popd >/dev/null
  chown "$APP_USER:$APP_GROUP" "$BIN_DIR/api" "$BIN_DIR/smtp" "$BIN_DIR/static-server"
  chmod 0755 "$BIN_DIR/api" "$BIN_DIR/smtp" "$BIN_DIR/static-server"
}

write_systemd_unit() {
  local service_name="$1"
  local description="$2"
  local exec_start="$3"
  local extra_lines="${4:-}"

  cat >"$SYSTEMD_DIR/$service_name" <<EOF
[Unit]
Description=$description
After=network-online.target docker.service gomail-infra.service
Wants=network-online.target
Requires=gomail-infra.service

[Service]
Type=simple
User=$APP_USER
Group=$APP_GROUP
WorkingDirectory=$APP_ROOT
EnvironmentFile=$ENV_FILE
ExecStart=$exec_start
Restart=always
RestartSec=3
StandardOutput=append:$LOG_DIR/${service_name%.service}.log
StandardError=append:$LOG_DIR/${service_name%.service}.log
$extra_lines

[Install]
WantedBy=multi-user.target
EOF
}

write_systemd_units() {
  log "writing systemd units"
  cat >"$SYSTEMD_DIR/gomail-infra.service" <<EOF
[Unit]
Description=GoMail infrastructure containers
After=docker.service network-online.target
Requires=docker.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=$INSTALL_ROOT
ExecStart=/usr/bin/docker compose -f $COMPOSE_FILE up -d
ExecStop=/usr/bin/docker compose -f $COMPOSE_FILE down

[Install]
WantedBy=multi-user.target
EOF

  write_systemd_unit "gomail-api.service" "GoMail API" "$BIN_DIR/api"
  write_systemd_unit "gomail-static.service" "GoMail static server" "$BIN_DIR/static-server"
  write_systemd_unit "gomail-smtp.service" "GoMail SMTP" "$BIN_DIR/smtp" "AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true"

  systemctl daemon-reload
  systemctl enable gomail-infra.service gomail-api.service gomail-static.service gomail-smtp.service
}

write_nginx_config() {
  log "writing nginx reverse proxy configuration"
  cat >"$NGINX_SITE" <<EOF
map \$http_upgrade \$connection_upgrade {
  default upgrade;
  '' close;
}

server {
  listen 80;
  listen [::]:80;
  server_name $APP_DOMAIN;

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
    proxy_set_header Upgrade \$http_upgrade;
    proxy_set_header Connection \$connection_upgrade;
    proxy_read_timeout 300s;
    proxy_pass http://127.0.0.1:8080;
  }
}

server {
  listen 80 default_server;
  listen [::]:80 default_server;
  server_name _;

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
    proxy_pass http://127.0.0.1:8090;
  }
}
EOF

  rm -f /etc/nginx/sites-enabled/default
  ln -sf "$NGINX_SITE" /etc/nginx/sites-enabled/gomail.conf
  nginx -t
  systemctl enable --now nginx
  systemctl reload nginx
}

configure_firewall() {
  if [[ "$ENABLE_UFW" != "true" ]]; then
    return 0
  fi
  log "configuring firewall rules with ufw"
  ufw allow "$SSH_PORT/tcp"
  ufw allow 80/tcp
  ufw allow 443/tcp
  ufw allow 25/tcp
  ufw allow 465/tcp
  ufw allow 587/tcp
  ufw --force enable
}

start_services() {
  log "starting infrastructure and application services"
  systemctl restart gomail-infra.service
  systemctl restart gomail-api.service
  systemctl restart gomail-static.service
  systemctl restart gomail-smtp.service
}

maybe_enable_tls() {
  case "$ENABLE_TLS" in
    false)
      log "skipping TLS setup because ENABLE_TLS=false"
      return 0
      ;;
    true|auto)
      ;;
    *)
      fail "ENABLE_TLS must be one of: auto, true, false"
      ;;
  esac

  prompt_value LETSENCRYPT_EMAIL "Let's Encrypt email" "$DEFAULT_ADMIN_EMAIL"
  log "requesting Let's Encrypt certificate for $APP_DOMAIN"

  if certbot --nginx --non-interactive --agree-tos -m "$LETSENCRYPT_EMAIL" -d "$APP_DOMAIN" --redirect; then
    systemctl reload nginx
    return 0
  fi

  if [[ "$ENABLE_TLS" == "true" ]]; then
    fail "failed to provision TLS certificate for $APP_DOMAIN"
  fi

  log "automatic TLS provisioning skipped; ensure DNS for $APP_DOMAIN points to this VPS, then rerun certbot or install.sh with ENABLE_TLS=true"
}

health_check() {
  log "verifying local services"
  curl -fsS http://127.0.0.1:8080/healthz >/dev/null
  curl -fsS http://127.0.0.1:8090/healthz >/dev/null
}

print_summary() {
  cat <<EOF

GoMail installation completed.

Deploy root:        $INSTALL_ROOT
Environment file:   $ENV_FILE
Primary app URL:    https://$APP_DOMAIN
Static host mode:   nginx default server -> static-server
SMTP inbound host:  $SMTP_HOSTNAME:25
SMTP relay host:    $SMTP_AUTH_HOSTNAME:587 / 465

systemd services:
  - gomail-infra.service
  - gomail-api.service
  - gomail-static.service
  - gomail-smtp.service

Important follow-up:
  1. Point A records for $APP_DOMAIN, $SMTP_HOSTNAME, and $SMTP_AUTH_HOSTNAME to $PUBLIC_IP.
  2. Point wildcard/static domains for *.$STATIC_SITES_BASE_DOMAIN and any custom domains to $PUBLIC_IP.
  3. TLS for $APP_DOMAIN is attempted automatically by default. Set ENABLE_TLS=false to skip or ENABLE_TLS=true to require success.
  4. If you need HTTPS for wildcard or arbitrary custom domains, this nginx-based install does not automate that yet; keep Traefik or provision wildcard/manual certs separately.
EOF
}

main() {
  require_root
  require_debian_family
  ensure_base_packages
  ensure_docker
  ensure_go
  ensure_app_user
  collect_config
  prepare_directories
  sync_source
  write_env_file
  write_infra_compose
  build_binaries
  write_systemd_units
  write_nginx_config
  configure_firewall
  start_services
  maybe_enable_tls
  health_check
  print_summary
}

main "$@"