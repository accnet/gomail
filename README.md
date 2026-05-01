# GoMail

GoMail is a self-hosted inbound email SaaS prototype.

## Quick Start

1. Copy env and change secrets:

```sh
cp .env.example .env
```

2. Start Postgres and Redis:

```sh
docker compose up -d
```

3. Build and start the local SaaS stack:

```sh
./start.sh
```

Local dev API runs on `http://localhost:8089` by default.

`start.sh` builds `api` and `smtp`, ensures Postgres/Redis are reachable on localhost, then starts API on `8089` and SMTP on `2525` with logs in `.run/`.

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

## Default Super Admin

The first API startup seeds `DEFAULT_ADMIN_EMAIL` with `DEFAULT_ADMIN_PASSWORD`. In production the app refuses sample secrets from `.env.example`.
