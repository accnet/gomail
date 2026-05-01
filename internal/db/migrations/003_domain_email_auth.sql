-- Migration 003: Domain-level SPF/DKIM readiness and DKIM keys
-- +migrate Up

CREATE TABLE IF NOT EXISTS domain_email_auths (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    spf_status VARCHAR NOT NULL DEFAULT 'pending',
    spf_record TEXT,
    spf_last_checked_at TIMESTAMPTZ,
    spf_error TEXT,
    dkim_selector VARCHAR NOT NULL,
    dkim_public_key TEXT,
    dkim_private_key_pem TEXT,
    dkim_status VARCHAR NOT NULL DEFAULT 'pending',
    dkim_record_name VARCHAR,
    dkim_record_value TEXT,
    dkim_last_checked_at TIMESTAMPTZ,
    dkim_error TEXT,
    dkim_last_generated_at TIMESTAMPTZ,
    dkim_last_verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_domain_email_auths_domain_id ON domain_email_auths(domain_id);
CREATE INDEX IF NOT EXISTS idx_domain_email_auths_spf_status ON domain_email_auths(spf_status);
CREATE INDEX IF NOT EXISTS idx_domain_email_auths_dkim_status ON domain_email_auths(dkim_status);
CREATE INDEX IF NOT EXISTS idx_domain_email_auths_deleted_at ON domain_email_auths(deleted_at);

-- +migrate Down
DROP TABLE IF EXISTS domain_email_auths;
