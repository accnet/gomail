-- ── Teams ──────────────────────────────────────────────────────────────────
CREATE TABLE teams (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    owner_id   UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_teams_owner ON teams(owner_id) WHERE deleted_at IS NULL;

-- ── Members ────────────────────────────────────────────────────────────────
CREATE TABLE team_members (
    id          UUID PRIMARY KEY,
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id),
    role        TEXT NOT NULL DEFAULT 'member',
    permissions JSONB NOT NULL DEFAULT '[]'::jsonb,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT chk_team_member_role CHECK (role IN ('owner', 'admin', 'member'))
);

CREATE UNIQUE INDEX idx_team_members_active
    ON team_members(team_id, user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_team_members_user
    ON team_members(user_id) WHERE deleted_at IS NULL;

-- ── Invites ────────────────────────────────────────────────────────────────
CREATE TABLE team_invites (
    id          UUID PRIMARY KEY,
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'member',
    permissions JSONB NOT NULL DEFAULT '[]'::jsonb,
    inviter_id  UUID NOT NULL REFERENCES users(id),
    status      TEXT NOT NULL DEFAULT 'pending',
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    declined_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_team_invite_role   CHECK (role   IN ('admin', 'member')),
    CONSTRAINT chk_team_invite_status CHECK (status IN ('pending', 'accepted', 'declined', 'expired', 'cancelled'))
);

CREATE INDEX idx_team_invites_team  ON team_invites(team_id);
CREATE INDEX idx_team_invites_email ON team_invites(lower(email));
CREATE UNIQUE INDEX idx_team_invites_pending_email
    ON team_invites(team_id, lower(email)) WHERE status = 'pending';

-- ── Add team_id to existing resource tables ────────────────────────────────
ALTER TABLE domains           ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE inboxes           ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE api_keys          ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE static_projects   ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE sent_email_logs   ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE api_key_usage_logs ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE audit_logs        ADD COLUMN team_id UUID REFERENCES teams(id);

CREATE INDEX idx_domains_team         ON domains(team_id)         WHERE deleted_at IS NULL AND team_id IS NOT NULL;
CREATE INDEX idx_inboxes_team         ON inboxes(team_id)         WHERE deleted_at IS NULL AND team_id IS NOT NULL;
CREATE INDEX idx_api_keys_team        ON api_keys(team_id)        WHERE deleted_at IS NULL AND team_id IS NOT NULL;
CREATE INDEX idx_static_projects_team ON static_projects(team_id) WHERE deleted_at IS NULL AND team_id IS NOT NULL;
CREATE INDEX idx_sent_email_logs_team ON sent_email_logs(team_id) WHERE team_id IS NOT NULL;
