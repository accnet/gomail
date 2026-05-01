# GoMail Project Context

## Purpose

GoMail is a self-hosted email SaaS that combines inbound mailbox hosting, SMTP submission/relay, API key management, realtime mailbox updates, and static website hosting.

## Runtime Components

| Component | Entry point | Responsibility |
|---|---|---|
| HTTP API | `cmd/api/main.go` | Gin API, auth/session lifecycle, domains/inboxes/emails, API keys, SSE, static project APIs |
| Inbound SMTP | `cmd/smtp/main.go` | Accepts incoming mail, parses messages, stores raw mail/attachments, publishes realtime events |
| Static server | `cmd/static-server/main.go` | Serves published static sites and resolves hostnames/custom domains |
| Frontend | `web/` | Vanilla JS application served from `/app` |

## Core Packages

| Package | Role |
|---|---|
| `internal/config` | Environment loading, defaults, production validation |
| `internal/db` | GORM connection, migrations, models, seed data |
| `internal/auth` | Password hashing and JWT/refresh-token lifecycle |
| `internal/http/handlers` | HTTP route registration and request handlers |
| `internal/http/middleware` | Session auth, API key auth, admin guards, rate limiting |
| `internal/mail/service` | Inbound message processing pipeline |
| `internal/realtime` | Redis-backed event publishing/SSE support |
| `internal/smtp/server` | Inbound SMTP and SMTP AUTH server adapters |
| `internal/smtp/relay` | Outbound direct/relay SMTP sender |
| `internal/staticprojects` | Static site project lifecycle, DNS binding, audits, thumbnails |
| `internal/storage` | Local attachment/raw email/static site storage |

## Data Stores

- PostgreSQL is the source of truth for users, domains, inboxes, messages, attachments, static projects, API keys, and send logs.
- Redis is used for realtime pub/sub.
- Local filesystem storage holds raw email files, attachments, and static site assets.

## Important Flows

### Inbound Email

1. `cmd/smtp` starts the SMTP server with `internal/smtp/server.Backend`.
2. Accepted messages enter `internal/mail/service.Pipeline`.
3. The pipeline validates recipient/domain state, persists email metadata, stores raw/attachment files, sanitizes HTML, and publishes realtime updates.
4. HTTP clients consume email data through `/api/emails` and realtime events through `/api/events/stream`.

### Authenticated API

1. `cmd/api` loads config, opens DB, migrates schema, seeds admin/demo data, and builds `handlers.App`.
2. `handlers.App.Router()` registers public auth routes, protected routes, admin routes, API key routes, static project routes, and SSE.
3. Session-protected routes use `middleware.Auth`.
4. API-key protected routes use `middleware.ApiKeyAuth`.

### Static Website Hosting

1. Users upload ZIP archives through static project routes.
2. `internal/staticprojects.Service` validates size/file limits, blocks risky extensions, extracts to staging, and atomically publishes live content.
3. Custom domain verification uses `internal/dns.Verifier`.
4. `cmd/static-server` resolves hostnames and serves published assets.

### SMTP Relay / Submission

1. Users create API keys with `send_email` or `full_access` scope.
2. SMTP AUTH validates API keys and delegates outbound delivery to `internal/smtp/relay.Sender`.
3. Delivery supports direct MX lookup or an upstream relay depending on outbound config.
4. When `DKIM_ENABLED=true`, outbound messages are DKIM-signed if the sender domain has verified DKIM config.

### Domain Email Authentication

Domain email-auth endpoints:

```txt
GET  /api/domains/:id/email-auth
POST /api/domains/:id/email-auth/dkim/generate
POST /api/domains/:id/email-auth/verify
```

The API returns SPF and DKIM TXT instructions for each user-owned domain. SPF verification checks for exactly one `v=spf1` TXT record and confirms it includes the GoMail-required mechanism. DKIM generation creates a per-domain RSA keypair, encrypts the private key with `DKIM_KEY_ENCRYPTION_SECRET`, and returns only the DNS TXT record containing the public key.

## Local Development

Common commands:

```sh
make dev-up
make api
make smtp
make check
make e2e
make dev-down
```

`./start.sh` builds and starts the local API/SMTP stack, reading `.env.dev` when present.

## Configuration Notes

- `.env.dev` is intended for local host overrides.
- `.env` is intended for production/deploy defaults.
- Production validation rejects sample JWT/admin secrets.
- Production validation requires `DKIM_KEY_ENCRYPTION_SECRET` with at least 32 characters when `DKIM_ENABLED=true`.
- `SEED_DEMO_DATA=false` disables local demo seeding.
- Static site, attachment, and raw email roots are configurable independently.

## Current Refactor Notes

- The HTTP app now has an explicit `SendEmail` dependency for `/api/send-email`.
- `/api/send-email` returns a controlled server error when no sender is configured instead of panicking on a nil function.
- Domain-level SPF/DKIM readiness is stored in `domain_email_auths`.
- Outbound DKIM signing is implemented in `internal/smtp/relay` and uses per-domain verified DKIM keys. Encrypted DKIM keys use the `enc:v1:` storage format; legacy plaintext PEM values can still be read for development/backward compatibility.
- `RegisterApiKeyRoutes` and `RegisterTestSendRoute` still accept middleware parameters for compatibility with existing tests, but some call sites wrap groups that already have the same middleware. This is a candidate for a later route-registration cleanup.

## Recommended Next Refactors

1. Introduce a small outbound email application service that can be shared by HTTP `/api/send-email` and SMTP AUTH relay. It should accept API key/user context, build a valid RFC 5322 message, call `relay.Sender`, and centralize send logging.
2. Split `internal/http/handlers/app.go` by domain further if route groups continue to grow: auth/session, mailboxes, admin, realtime, and route wiring.
3. Replace raw string statuses/scopes in handlers with typed constants from `internal/db` where available.
4. Move route registration to focused functions so middleware is applied exactly once per route group.
5. Add integration tests for `handlers.App.Router()` covering `/api/send-email` when `SendEmail` is nil and when a sender is injected.
