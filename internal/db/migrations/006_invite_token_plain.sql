-- ── Add token_plain to team_invites ────────────────────────────────────────
ALTER TABLE team_invites ADD COLUMN token_plain TEXT;
CREATE INDEX idx_team_invites_token_plain ON team_invites(token_plain) WHERE status = 'pending';
