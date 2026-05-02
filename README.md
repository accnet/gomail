# GoMail

GoMail is a self-hosted inbound email SaaS platform with static website hosting.

## Features

### 📧 Inbound Email Hosting
- SMTP inbound (port 25/2525) with MIME parsing
- Attachment storage with virus scanning (extension + content-type)
- HTML sanitization (bluemonday) + remote image scrubbing
- Real-time SSE push via Redis pub/sub
- Refresh token rotation with session chain revocation
- Rate-limited auth endpoints (token bucket)

### 📤 SMTP Relay / Email Submission
- API key management with scoped permissions (send_email, full_access)
- One-time key reveal on creation (SHA-256 hashed at rest)
- SMTP AUTH relay support (port 587 STARTTLS, port 465 TLS)
- Key lifecycle: create, list, get, patch, revoke, delete
- Real-time usage counter per API key
- Configurable SMTP auth hostname and ports

### 🌐 Static Website Hosting
- Upload ZIP archives → auto-publish to subdomain
- Atomic publish (staging → live rename, rollback on failure)
- Custom domain binding with DNS verification
- Automatic Let's Encrypt SSL via Traefik
- ZIP bomb protection (max size, max extracted size, max file count)
- Forbidden file extension blocking (.php, .sh, .exe...)
- Path traversal protection (zip-slip prevention)
- Thumbnail generation for published sites

## Quick Start

1. Prepare environment files:

```sh
cp .env.example .env        # production / deploy defaults
cp .env.dev.example .env.dev # optional local host overrides for ./start.sh
```

2. Start Postgres and Redis:

```sh
docker compose up -d
```

3. Build and start the local SaaS stack:

```sh
./start.sh
```

Local dev API runs on `http://localhost:8080` by default.

`start.sh` reads `.env.dev`, builds `api` and `smtp`, ensures Postgres/Redis are reachable on localhost, then starts API on `8080` and SMTP on `2525` with logs in `.run/`.

`docker compose up` reuses `.env.dev`, and `docker-compose.yaml` applies the container-only overrides such as `postgres`, `redis`, and `/app/data` paths.

Production deploys should use `.env`.

## VPS Production Install

The repository now includes [install.sh](./install.sh) for first-time VPS setup on Ubuntu/Debian. It installs Docker, Go, nginx, ufw, builds the GoMail binaries, provisions `systemd` services, and writes an nginx reverse proxy for the main SaaS domain.

Run it as root from the repository checkout:

```sh
sudo APP_DOMAIN=mail.example.com \
  SAAS_DOMAIN=example.com \
  SMTP_HOSTNAME=mx.example.com \
  SMTP_AUTH_HOSTNAME=smtp.example.com \
  DEFAULT_ADMIN_EMAIL=admin@example.com \
  ./install.sh
```

The script prompts for any missing secrets and now attempts a Let's Encrypt certificate for the primary app domain by default. Use `ENABLE_TLS=false` to skip TLS during first install, or `ENABLE_TLS=true` to require certificate provisioning success.

If you want hosted static sites to live under a different wildcard base domain than the main SaaS domain, set `STATIC_SITES_BASE_DOMAIN` separately when running the installer. Example: app on `example.com`, hosted sites on `*.sites.example.net`.

```sh
sudo APP_DOMAIN=example.com \
  SAAS_DOMAIN=example.com \
  STATIC_SITES_BASE_DOMAIN=sites.example.net \
  SMTP_HOSTNAME=mx.example.com \
  SMTP_AUTH_HOSTNAME=smtp.example.com \
  DEFAULT_ADMIN_EMAIL=admin@example.com \
  ./install.sh
```

This nginx-based install covers the main app domain and routes all other HTTP hosts to `static-server`, which is enough for HTTP custom domains and hosted subdomains.

For wildcard HTTPS on hosted static sites, use [wildcard-ssl.sh](./wildcard-ssl.sh). It supports two modes:

1. Install an existing wildcard certificate you already issued elsewhere.
2. Automatically request a wildcard certificate through Cloudflare DNS-01 and then apply it to nginx.

Existing certificate mode:

```sh
sudo STATIC_SITES_BASE_DOMAIN=sites.example.net \
  WILDCARD_SSL_MODE=existing \
  WILDCARD_CERT_FILE=/root/certs/sites.example.net.fullchain.pem \
  WILDCARD_KEY_FILE=/root/certs/sites.example.net.key \
  ./wildcard-ssl.sh
```

Cloudflare auto-issue mode:

```sh
sudo STATIC_SITES_BASE_DOMAIN=sites.example.net \
  WILDCARD_SSL_MODE=cloudflare \
  CF_API_TOKEN=your_cloudflare_dns_token \
  ./wildcard-ssl.sh
```

This script enables nginx HTTPS for `*.STATIC_SITES_BASE_DOMAIN` and redirects wildcard HTTP traffic to HTTPS. The `cloudflare` mode installs `python3-certbot-dns-cloudflare` if needed, requests a certificate for both `STATIC_SITES_BASE_DOMAIN` and `*.STATIC_SITES_BASE_DOMAIN`, then wires it into nginx.

For later code updates on the VPS, use [upgrade.sh](./upgrade.sh). It syncs the new checkout into the deployed app directory, rebuilds binaries, refreshes Docker infra, restarts the `systemd` services, and verifies local health checks.

```sh
sudo ./upgrade.sh
```

If you upload a release archive to the VPS instead of updating the working tree first, deploy from that archive explicitly so the rebuild uses the same code you copied:

```sh
sudo DEPLOY_ARCHIVE=/home/admin/gomail-deploy.tgz ./upgrade.sh
```

`upgrade.sh` now fails fast when it detects a newer `gomail-deploy.tgz` beside the checkout, which helps prevent rebuilding from stale source by accident.

Set `RUN_TESTS=true` if you want `go test ./...` before the restart, or `RESTART_INFRA=true` if you want a full `gomail-infra.service` restart instead of `docker compose up -d`.

If the main SaaS domain certificate was issued incorrectly or DNS was fixed later, use [ssl-fix.sh](./ssl-fix.sh) to request the certificate again without rerunning the full install:

```sh
sudo ./ssl-fix.sh
```

It reads `APP_DOMAIN` and `DEFAULT_ADMIN_EMAIL` from `/opt/gomail/.env` by default. Set `DELETE_OLD_CERT=true` if you want to remove the old certificate lineage before requesting a fresh one.

If you need to reapply or rotate the wildcard certificate used by hosted sites, rerun [wildcard-ssl.sh](./wildcard-ssl.sh) with either updated certificate/key file paths or the same Cloudflare token.

## Developer Commands

```sh
make dev-up    # start Postgres and Redis
make api       # run the HTTP API
make smtp      # run the SMTP server
make check     # go test, go vet, and JS syntax checks
make e2e       # local manual E2E flow via API + SMTP
make dev-down  # stop local services
```

In development, demo data is seeded by default for the super admin account. Set `SEED_DEMO_DATA=false` to disable it.

`make e2e` assumes the local Docker Postgres container is `gomail-postgres-1` and uses a local-only shortcut to mark the created domain as verified in the database before sending a test mail to SMTP.

For VPS deploy, set `SMTP_PORT=25`, open port 25 in the firewall/provider security group, and point `MX_TARGET` to the public SMTP hostname, for example `mx.example.com`.

## SaaS Domain

Recommended DNS:

- `mail.example.com` -> web/API reverse proxy.
- `mx.example.com` -> VPS running SMTP port 25.
- User domains: `MX 10 mx.example.com`.

## SMTP Relay Configuration

Users generate API keys with `send_email` scope to submit emails through the platform's SMTP relay. Each key is hashed with SHA-256 and only revealed once on creation.

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `SMTP_AUTH_HOSTNAME` | `smtp.your-domain.com` | SMTP relay hostname advertised to API key holders |
| `SMTP_AUTH_PORT` | `587` | SMTP AUTH submission port (STARTTLS) |
| `SMTP_AUTH_TLS_PORT` | `465` | SMTP AUTH submission port (implicit TLS) |

### API Endpoints

All endpoints require Bearer token authentication.

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/api-keys` | Create API key (returns full key once) |
| `GET` | `/api/api-keys` | List all API keys (key prefix only) |
| `GET` | `/api/api-keys/:id` | Get single API key |
| `PATCH` | `/api/api-keys/:id` | Update key name/scope |
| `DELETE` | `/api/api-keys/:id` | Delete API key |
| `POST` | `/api/api-keys/:id/revoke` | Revoke API key (sets is_active=false) |
| `GET` | `/api/api-keys/:id/usage` | Get usage count |
| `POST` | `/api/send-email` | Send email (requires `X-Api-Key` header with `send_email` scope) |

### Send Email Request

```json
POST /api/send-email
X-Api-Key: go_xxxxxxxxxxxx

{
  "to": "recipient@example.com",
  "subject": "Hello",
  "body": "Email content"
}
```

### SMTP AUTH Credentials

When creating a key with `send_email` scope, the response includes SMTP credentials:

```json
{
  "smtp_settings": {
    "host": "smtp.your-domain.com",
    "port_587": "587",
    "port_465": "465",
    "username_format": "api"
  }
}
```

Users authenticate to the SMTP relay using their full API key as the password and `api` as the username.

## Default Super Admin

The first API startup seeds `DEFAULT_ADMIN_EMAIL` with `DEFAULT_ADMIN_PASSWORD`. In production the app refuses sample secrets from `.env.example`.

## Architecture

```
cmd/api              → HTTP API + SSE (port 8080/8080)
cmd/smtp             → SMTP inbound (port 25/2525)
cmd/static-server    → Static file serving (port 8090)
```

### Stack

| Component | Technology |
|---|---|
| Backend | Go + Gin + GORM |
| Database | PostgreSQL 16 |
| Cache/Realtime | Redis 7 |
| SMTP | emersion/go-smtp |
| Reverse Proxy | Traefik v3.3 |
| Frontend | Vanilla JS SPA |

## Project Status

| Product | Status |
|---|---|
| Inbound Email Hosting | ✅ Complete |
| SMTP Relay / Submission | ✅ Complete |
| Static Website Hosting | ✅ Complete |
| Rate Limiting | ✅ Implemented |
| Graceful Shutdown | ✅ All services |
| Typed Status Constants | ✅ Refactored |
| Connection Pool | ✅ Configured |
| Code Duplication | ✅ Cleaned up |

See [report.md](./report.md) for full analysis.
