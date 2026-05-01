-- Migration 002: SMTP Relay - API keys, sent email log, usage tracking
-- +migrate Up

CREATE TABLE IF NOT EXISTS api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR NOT NULL,
    key_prefix VARCHAR NOT NULL,
    key_hash VARCHAR NOT NULL UNIQUE,
    scopes VARCHAR NOT NULL DEFAULT 'send_email',
    allowed_ips VARCHAR,
    rate_limit_rpm INT NOT NULL DEFAULT 60,
    max_daily_emails INT NOT NULL DEFAULT 500,
    daily_sent_count INT NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_prefix ON api_keys(key_prefix);
CREATE INDEX IF NOT EXISTS idx_api_keys_deleted_at ON api_keys(deleted_at);

CREATE TABLE IF NOT EXISTS api_key_usage_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    api_key_id UUID NOT NULL,
    user_id UUID NOT NULL,
    endpoint VARCHAR,
    method VARCHAR,
    status_code INT,
    ip_address VARCHAR,
    user_agent VARCHAR,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_key_usage_logs_api_key_id ON api_key_usage_logs(api_key_id);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_logs_user_id ON api_key_usage_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_logs_created_at ON api_key_usage_logs(created_at);

CREATE TABLE IF NOT EXISTS sent_email_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    channel VARCHAR NOT NULL DEFAULT 'smtp_auth',
    from_address VARCHAR,
    to_address VARCHAR,
    cc_address VARCHAR,
    bcc_address VARCHAR,
    subject VARCHAR,
    body_text TEXT,
    body_html TEXT,
    status VARCHAR NOT NULL DEFAULT 'sent',
    error_message TEXT,
    message_id VARCHAR,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sent_email_logs_user_id ON sent_email_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_sent_email_logs_api_key_id ON sent_email_logs(api_key_id);
CREATE INDEX IF NOT EXISTS idx_sent_email_logs_status ON sent_email_logs(status);
CREATE INDEX IF NOT EXISTS idx_sent_email_logs_created_at ON sent_email_logs(created_at);

-- +migrate Down
DROP TABLE IF EXISTS sent_email_logs;
DROP TABLE IF EXISTS api_key_usage_logs;
DROP TABLE IF EXISTS api_keys;