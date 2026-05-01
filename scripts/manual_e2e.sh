#!/usr/bin/env bash
set -euo pipefail

API_BASE_URL="${API_BASE_URL:-http://localhost:8089/api}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-change-me-before-deploy}"
USER_EMAIL="${USER_EMAIL:-e2e-user@example.com}"
USER_PASSWORD="${USER_PASSWORD:-password123}"
USER_NAME="${USER_NAME:-E2E User}"
DOMAIN_NAME="${DOMAIN_NAME:-e2e.localhost}"
LOCAL_PART="${LOCAL_PART:-hello}"
SMTP_TARGET_HOST="${SMTP_TARGET_HOST:-127.0.0.1}"
SMTP_TARGET_PORT="${SMTP_TARGET_PORT:-2525}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-gomail-postgres-1}"
POSTGRES_DB="${POSTGRES_DB:-gomail}"
POSTGRES_USER="${POSTGRES_USER:-gomail}"

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 1; }
}

need curl
need docker
need nc

json_field() {
  local field="$1"
  node -e "const fs=require('fs'); const data=JSON.parse(fs.readFileSync(0,'utf8')); const keys='$field'.split('.'); let cur=data; for (const k of keys) cur=cur?.[k]; if (cur===undefined) process.exit(2); process.stdout.write(String(cur));"
}

echo "1. Admin login"
ADMIN_LOGIN="$(curl -sS -X POST "$API_BASE_URL/auth/login" -H 'Content-Type: application/json' -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}")"
ADMIN_TOKEN="$(printf '%s' "$ADMIN_LOGIN" | json_field access_token)"

echo "2. Register user"
curl -sS -X POST "$API_BASE_URL/auth/register" -H 'Content-Type: application/json' -d "{\"name\":\"$USER_NAME\",\"email\":\"$USER_EMAIL\",\"password\":\"$USER_PASSWORD\"}" >/dev/null || true
USER_ID="$(docker exec "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "select id from users where email='${USER_EMAIL}' limit 1")"
[[ -n "$USER_ID" ]] || { echo "user not found after registration" >&2; exit 1; }

echo "3. Activate user via admin API"
curl -sS -X PATCH "$API_BASE_URL/admin/users/$USER_ID/status" -H 'Content-Type: application/json' -H "Authorization: Bearer $ADMIN_TOKEN" -d '{"is_active":true}' >/dev/null

echo "4. User login"
USER_LOGIN="$(curl -sS -X POST "$API_BASE_URL/auth/login" -H 'Content-Type: application/json' -d "{\"email\":\"$USER_EMAIL\",\"password\":\"$USER_PASSWORD\"}")"
USER_TOKEN="$(printf '%s' "$USER_LOGIN" | json_field access_token)"

echo "5. Create domain"
DOMAIN_RESP="$(curl -sS -X POST "$API_BASE_URL/domains" -H 'Content-Type: application/json' -H "Authorization: Bearer $USER_TOKEN" -d "{\"name\":\"$DOMAIN_NAME\"}")"
DOMAIN_ID="$(printf '%s' "$DOMAIN_RESP" | json_field id)"

echo "6. Force verify domain for local E2E"
docker exec "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "update domains set status='verified', warning_status='', verification_error='', last_verified_at=now() where id='${DOMAIN_ID}';" >/dev/null

echo "7. Create inbox"
INBOX_RESP="$(curl -sS -X POST "$API_BASE_URL/inboxes" -H 'Content-Type: application/json' -H "Authorization: Bearer $USER_TOKEN" -d "{\"domain_id\":\"$DOMAIN_ID\",\"local_part\":\"$LOCAL_PART\"}")"
INBOX_ID="$(printf '%s' "$INBOX_RESP" | json_field id)"
ADDRESS="$(printf '%s' "$INBOX_RESP" | json_field address)"

echo "8. Send test mail over SMTP"
SMTP_MSG="$(mktemp)"
trap 'rm -f "$SMTP_MSG"' EXIT
cat >"$SMTP_MSG" <<EOF
From: sender@example.net
To: $ADDRESS
Subject: Manual E2E
Message-ID: <manual-e2e-$(date +%s)@example.net>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8

hello from manual e2e
EOF
{
  printf 'HELO localhost\r\n'
  printf 'MAIL FROM:<sender@example.net>\r\n'
  printf 'RCPT TO:<%s>\r\n' "$ADDRESS"
  printf 'DATA\r\n'
  sed 's/$/\r/' "$SMTP_MSG"
  printf '.\r\nQUIT\r\n'
} | nc "$SMTP_TARGET_HOST" "$SMTP_TARGET_PORT" >/dev/null

echo "9. Fetch emails"
EMAIL_LIST="$(curl -sS "$API_BASE_URL/emails?page=1&page_size=10" -H "Authorization: Bearer $USER_TOKEN")"
EMAIL_COUNT="$(printf '%s' "$EMAIL_LIST" | node -e "const fs=require('fs'); const data=JSON.parse(fs.readFileSync(0,'utf8')); process.stdout.write(String(data.items.length));")"
echo "Inbox ID: $INBOX_ID"
echo "Email count on first page: $EMAIL_COUNT"
printf '%s\n' "$EMAIL_LIST"
