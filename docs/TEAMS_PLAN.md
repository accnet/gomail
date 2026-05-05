# GoMail Teams - Implementation Plan

## Trạng thái hiện tại (baseline)

Codebase là single-user. Mọi resource (`Domain`, `Inbox`, `ApiKey`, `StaticProject`, `SentEmailLog`, `ApiKeyUsageLog`, `AuditLog`) đều scope bằng `user_id`. Chưa có migration 005, chưa có package `internal/teams`, chưa có field `team_id` trên bất kỳ bảng nào.

Các file cần sửa chính:
- `internal/db/models.go`
- `internal/db/migrations/` (thêm `005_teams.sql`)
- `internal/http/handlers/app.go` (`App` struct + routes)
- `internal/http/middleware/auth.go` và `apikey_auth.go`
- `internal/realtime/realtime.go` (`Event` struct)
- `internal/staticprojects/service.go`
- `web/` (frontend)

## Quyết định thiết kế

### Ownership model

Giữ nguyên `user_id NOT NULL` trên tất cả bảng hiện tại — không viết lại quota/admin/SMTP logic. Thêm `team_id UUID NULL` vào các bảng chia sẻ được.

- Personal resource: `team_id IS NULL`, `user_id` là chủ sở hữu.
- Team resource: `team_id IS NOT NULL`, `user_id` là người tạo / quota anchor.

### Active context

Mỗi request đi vào một trong hai chế độ:

- **Personal**: không có `X-Team-Id`.
- **Team**: header `X-Team-Id` hợp lệ và user là member active.

Backend không được tin `X-Team-Id` cho đến khi đã verify membership.

### Permission model

- `owner`: tất cả scopes, không thể giảm.
- `admin`: tất cả scopes trừ `team:delete`.
- `member`: chỉ đọc mặc định.

Scopes lưu trong `team_members.permissions JSONB`. Dùng `datatypes.JSON` (gorm.io/datatypes) cho phù hợp với pattern hiện tại của codebase (xem `Email.ReferencesMessageIDs`, `AuditLog.Payload`).

## Phase 1: Database

### 1.1 New Migration

Create `internal/db/migrations/005_teams.sql`.

```sql
CREATE TABLE teams (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    owner_id    UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX idx_teams_owner
    ON teams(owner_id)
    WHERE deleted_at IS NULL;

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

CREATE UNIQUE INDEX idx_team_members_team_user_active
    ON team_members(team_id, user_id)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_team_members_user_active
    ON team_members(user_id)
    WHERE deleted_at IS NULL;

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
    CONSTRAINT chk_team_invite_role CHECK (role IN ('admin', 'member')),
    CONSTRAINT chk_team_invite_status CHECK (status IN ('pending', 'accepted', 'declined', 'expired', 'cancelled'))
);

CREATE INDEX idx_team_invites_team
    ON team_invites(team_id);

CREATE INDEX idx_team_invites_email
    ON team_invites(lower(email));

CREATE UNIQUE INDEX idx_team_invites_pending_email
    ON team_invites(team_id, lower(email))
    WHERE status = 'pending';
```

Use `token_hash`, not a plaintext token. The API can return the plaintext token once when the invite is created, similar to API key one-time reveal.

### 1.2 Alter Existing Tables

```sql
ALTER TABLE domains ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE inboxes ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE api_keys ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE static_projects ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE sent_email_logs ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE api_key_usage_logs ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE audit_logs ADD COLUMN team_id UUID REFERENCES teams(id);

CREATE INDEX idx_domains_team_active
    ON domains(team_id)
    WHERE deleted_at IS NULL AND team_id IS NOT NULL;

CREATE INDEX idx_inboxes_team_active
    ON inboxes(team_id)
    WHERE deleted_at IS NULL AND team_id IS NOT NULL;

CREATE INDEX idx_api_keys_team_active
    ON api_keys(team_id)
    WHERE deleted_at IS NULL AND team_id IS NOT NULL;

CREATE INDEX idx_static_projects_team_active
    ON static_projects(team_id)
    WHERE deleted_at IS NULL AND team_id IS NOT NULL;

CREATE INDEX idx_sent_email_logs_team
    ON sent_email_logs(team_id)
    WHERE team_id IS NOT NULL;
```

Keep global uniqueness where required by product behavior:

- `domains.name` remains globally unique.
- `inboxes.address` remains globally unique.
- `static_projects.subdomain` remains globally unique.
- API key hash remains globally unique.

### 1.3 GORM Models

Add `Team`, `TeamMember`, and `TeamInvite` to `internal/db/models.go`.

Important model details:

- Add `BeforeCreate` hooks to assign UUIDs.
- Use constants for team roles, invite statuses, and team permission scopes.
- Store permissions as `datatypes.JSON`.
- Do not define methods on `db.TeamMember` from another package. Permission methods must live in package `db`, or be plain functions in package `teams`.

Add to existing resource models:

```go
TeamID *uuid.UUID `gorm:"type:uuid;index" json:"team_id,omitempty"`
```

Required models:

- `Domain`
- `Inbox`
- `ApiKey`
- `StaticProject`
- `SentEmailLog`
- `ApiKeyUsageLog`
- `AuditLog`

## Phase 2: Team Package

Create `internal/teams`.

### 2.1 Permissions

File: `internal/teams/permissions.go`

```go
const (
    RoleOwner  = "owner"
    RoleAdmin  = "admin"
    RoleMember = "member"

    ScopeEmailAccess   = "email:access"
    ScopeEmailManage   = "email:manage"
    ScopeApiKeyRead    = "apikey:read"
    ScopeApiKeyCreate  = "apikey:create"
    ScopeApiKeyManage  = "apikey:manage"
    ScopeWebsiteRead   = "website:read"
    ScopeWebsiteDeploy = "website:deploy"
    ScopeWebsiteManage = "website:manage"
    ScopeDomainManage  = "domain:manage"
    ScopeMemberManage  = "member:manage"
    ScopeTeamDelete    = "team:delete"
)
```

Helpers:

- `DefaultScopes(role string) []string`
- `ValidateRole(role string, allowOwner bool) error`
- `ValidateScopes(scopes []string) error`
- `MarshalScopes(scopes []string) datatypes.JSON`
- `ParseScopes(raw datatypes.JSON) ([]string, error)`
- `MemberHasScope(member db.TeamMember, scope string) bool`
- `MemberHasAnyScope(member db.TeamMember, scopes ...string) bool`

Owner always returns true for scope checks.

### 2.2 Active Context

File: `internal/teams/context.go`

```go
type ActiveContext struct {
    UserID   uuid.UUID
    TeamID   *uuid.UUID
    Role     string
    Scopes   []string
    Personal bool
}
```

Helpers:

- `FromGin(c *gin.Context) ActiveContext`
- `SetGin(c *gin.Context, ctx ActiveContext)`
- `TeamIDFromRequest(c *gin.Context) (*uuid.UUID, error)`

Context rules:

- Empty team id means personal context.
- Invalid UUID returns `400 invalid_team_id`.
- Unknown team or non-member returns `403 forbidden`.
- Deleted team/member returns `403 forbidden`.

### 2.3 Service

File: `internal/teams/service.go`

Core methods:

- `CreateTeam(ctx context.Context, userID uuid.UUID, name string) (*db.Team, error)`
- `GetTeam(ctx context.Context, userID, teamID uuid.UUID) (*db.Team, error)`
- `ListTeams(ctx context.Context, userID uuid.UUID) ([]TeamSummary, error)`
- `UpdateTeam(ctx context.Context, actorID, teamID uuid.UUID, name string) error`
- `DeleteTeam(ctx context.Context, actorID, teamID uuid.UUID) error`
- `ListMembers(ctx context.Context, actorID, teamID uuid.UUID) ([]TeamMemberView, error)`
- `UpdateMember(ctx context.Context, actorID, teamID, memberUserID uuid.UUID, role *string, scopes *[]string) error`
- `RemoveMember(ctx context.Context, actorID, teamID, memberUserID uuid.UUID) error`
- `InviteMember(ctx context.Context, actorID, teamID uuid.UUID, email, role string, scopes []string) (*InviteCreated, error)`
- `GetInviteInfo(ctx context.Context, token string) (*InviteInfo, error)`
- `AcceptInvite(ctx context.Context, userID uuid.UUID, token string) (*db.TeamMember, error)`
- `DeclineInvite(ctx context.Context, userID uuid.UUID, token string) error`
- `CancelInvite(ctx context.Context, actorID, teamID, inviteID uuid.UUID) error`
- `ListPendingInvites(ctx context.Context, actorID, teamID uuid.UUID) ([]InviteInfo, error)`
- `GetMember(ctx context.Context, teamID, userID uuid.UUID) (*db.TeamMember, error)`

Transactional requirements:

- `CreateTeam` inserts team and owner membership in one transaction.
- `AcceptInvite` checks token hash, status, expiry, invite email, existing member, and inserts membership plus updates invite status in one transaction.
- `DeleteTeam` soft-deletes team and active members in one transaction.
- Owner cannot leave, be removed, or be demoted unless ownership transfer is implemented first.

## Phase 3: Middleware

### 3.1 Auth Context Enrichment

Update `internal/http/middleware/auth.go` only enough to keep auth focused:

- Authenticate user as today.
- Optionally attach user to gin context as today.

Do not make auth middleware import handlers. Either:

- Create a separate `teams.RequireContext(teamSvc)` middleware after auth, or
- Add a small middleware package function that depends on `internal/teams`.

Recommended route wiring:

```go
protected := api.Group("")
protected.Use(mw.Auth(a.Auth, a.DB))
protected.Use(teamsmw.RequireContext(a.Teams))
```

### 3.2 Scope Middleware

File: `internal/http/middleware/team_scope.go` or `internal/teams/middleware.go`.

```go
func RequireTeamScope(scope string) gin.HandlerFunc
```

Behavior:

- Personal context passes. Existing personal ownership checks still apply.
- Team context requires the member to have the scope.
- Missing scope returns `403` with code `missing_scope`.

Use this on team-aware resource routes:

- domain create/verify/delete: `domain:manage`
- inbox create/update/delete: `email:manage`
- inbox/email list/read/reply/download: `email:access`
- API key list/get/settings: `apikey:read`
- API key create: `apikey:create`
- API key patch/revoke/delete/usage: `apikey:manage`
- static project list/get: `website:read`
- static project deploy/create: `website:deploy`
- static project delete/domain/SSL actions: `website:manage`

## Phase 4: API Endpoints

### 4.1 Team Routes

Add `Teams *teams.Service` to `handlers.App`.

Wire in `App.Router()`:

```go
teamsGroup := protected.Group("/teams")
teamsGroup.POST("", a.createTeam)
teamsGroup.GET("", a.listTeams)
teamsGroup.GET("/:id", a.getTeam)
teamsGroup.PATCH("/:id", a.updateTeam)
teamsGroup.DELETE("/:id", a.deleteTeam)

teamsGroup.GET("/:id/members", a.listMembers)
teamsGroup.PATCH("/:id/members/:memberUserId", a.updateMember)
teamsGroup.DELETE("/:id/members/:memberUserId", a.removeMember)

teamsGroup.POST("/:id/invites", a.inviteMember)
teamsGroup.GET("/:id/invites", a.listInvites)
teamsGroup.DELETE("/:id/invites/:inviteId", a.cancelInvite)

api.GET("/team-invites/:token", a.getInviteInfo)
protected.POST("/team-invites/:token/accept", a.acceptInvite)
protected.POST("/team-invites/:token/decline", a.declineInvite)
```

Accept and decline require auth. The service must require `current_user.email == invite.email`.

### 4.2 Invite Registration Flow

Current registration creates inactive users, and login rejects inactive users. Team invites need one explicit rule.

Recommended first implementation:

- Add `POST /api/team-invites/:token/register`.
- It validates token and invite email.
- It creates the user as active.
- It accepts the invite in the same transaction or immediately after user creation.
- It returns access and refresh tokens.

Existing-user flow:

1. Preview invite with `GET /api/team-invites/:token`.
2. Login normally.
3. Accept invite with Bearer token.

New-user flow:

1. Preview invite.
2. Submit name and password to invite-register endpoint.
3. Backend creates active user, accepts invite, returns tokens.

Avoid frontend logic that tries login, then blind register on `401`. It creates ambiguous errors and conflicts with inactive-account behavior.

## Phase 5: Resource Scoping

### 5.1 Shared Query Helpers

Before editing individual handlers, add helpers so every feature scopes resources consistently.

Recommended helpers:

- `ScopeDomains(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB`
- `ScopeInboxes(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB`
- `ScopeApiKeys(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB`
- `ScopeStaticProjects(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB`
- `ScopeSentEmailLogs(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB`

Personal context:

```sql
user_id = ? AND team_id IS NULL
```

Team context:

```sql
team_id = ?
```

### 5.2 HTTP Handlers

Update these areas:

- Domains in `internal/http/handlers/app.go`
- Domain email auth in `internal/http/handlers/domain_email_auth.go`
- Inboxes and emails in `internal/http/handlers/app.go`
- Sent/reply/conversation queries in `internal/http/handlers/app.go`
- API keys in `internal/http/handlers/apikey.go`
- Static project handlers in `internal/http/handlers/static_projects.go`

When creating team resources:

- Set `TeamID`.
- Keep `UserID` as creator or quota anchor.
- Validate required team scope before creation.
- Count quotas against the team owner for first implementation.

### 5.3 Static Projects Service

`internal/staticprojects.Service` currently accepts `userID`. Introduce a context parameter or a small ownership struct:

```go
type OwnerContext struct {
    UserID uuid.UUID
    TeamID *uuid.UUID
}
```

Update list/get/create/delete/domain assignment methods to scope by `team_id` when present.

Domain assignment must require the domain to be available in the same context:

- Personal project can bind only personal verified domains.
- Team project can bind only team verified domains.

### 5.4 API Keys and SMTP Relay

Team-owned API keys must carry `team_id`.

Update:

- `internal/http/middleware/apikey_auth.go`
- `internal/http/handlers/apikey.go`
- SMTP AUTH path in `internal/smtp/server`
- Outbound sender in `internal/smtp/relay`
- Send logs and usage logs

Rules:

- API key auth sets both user id and optional team id in context.
- Outbound domain lookup uses:
  - personal key: `domains.user_id = ? AND domains.team_id IS NULL`
  - team key: `domains.team_id = ?`
- `sent_email_logs.team_id` is set for team keys.
- Rate limit counters stay per API key. Team-shared keys naturally share the same key counters.

### 5.5 Realtime Events

Inbound email currently publishes by `UserID`. Team inboxes need team fanout.

First implementation:

- Add optional `TeamID` to `realtime.Event`.
- For personal inboxes, publish to user channel as today.
- For team inboxes, publish to team channel.
- SSE subscribes to current user's personal channel plus team channels for teams where the user has `email:access`.

If broad fanout is simpler initially, publish the event to all active team members with `email:access`, but keep the event payload carrying `team_id`.

## Phase 6: Frontend

### 6.1 Team Switcher

Add a context switcher in the existing app shell:

```text
Personal
MyCompany Team
+ Create Team
```

Behavior:

- Store active team id in app state and localStorage.
- Send `X-Team-Id` on API calls when active team is selected.
- Reload domains, inboxes, emails, API keys, dashboard, and static projects after switching.
- Hide actions when the active member lacks the required scope.

### 6.2 Team Management

Add `web/teams.js` for:

- Team list
- Team detail
- Members table
- Pending invites
- Invite modal
- Role and permission editor

Add styles to `web/styles.css` using the existing dashboard visual language.

### 6.3 Invite Join Page

Add:

- `web/join.html`
- `web/join.js`

Flow:

1. Read token from query string.
2. `GET /api/team-invites/:token`.
3. If already logged in, show accept button.
4. If not logged in, show tabs for login and create account.
5. Existing account: login then accept.
6. New account: call invite-register endpoint.
7. Store returned tokens and redirect to `/app/` with the joined team active.

Do not auto-register after a failed login without showing the user what happened.

## Phase 7: Edge Cases

| Case | Handling |
|---|---|
| Owner leaves team | Reject until ownership transfer exists |
| Owner permissions | Always all scopes |
| Owner demotion/removal | Reject |
| Admin demoted to member | Reset to member default scopes unless request includes explicit scopes |
| Missing scope | `403` code `missing_scope`, include required scope |
| Invalid team id | `400` code `invalid_team_id` |
| Non-member team id | `403` code `forbidden` |
| Duplicate invite | Reject pending duplicate for same lower(email)+team |
| Expired invite | Mark expired on read/accept and reject |
| Invite email mismatch | Reject accept/register |
| Team deletion | Soft-delete team and memberships; leave resources retained with `team_id` for audit/export |
| User deletion | Soft-delete memberships and invites by email where appropriate |
| Cross-context resource access | Always scope by active context, never by id alone |
| Domain uniqueness | Keep global uniqueness |
| Static project custom domain | Project and domain must be in same context |
| Audit log | Include actor user id and team id |

## Phase 8: Tests

Add tests before frontend polish.

### Unit Tests

- Permission default scopes and validation
- `MemberHasScope`
- Invite token hashing and expiry
- Service role restrictions
- Active context parsing and membership rejection

### Integration Tests

- Create team creates owner member
- Admin can invite; member cannot invite without scope
- Existing user accepts invite only for matching email
- New user invite registration creates active account and member
- Team member can list team inboxes with `email:access`
- Member without `email:manage` cannot create inbox
- Team API key can send only from team verified domains
- Personal context cannot see team resources
- Team context cannot see personal resources
- Static project can bind only same-context verified domain
- SSE receives team email events for members with `email:access`

## Implementation Order

1. Add migration and GORM models.
2. Add `internal/teams` permissions, context, and service.
3. Add middleware for validated active context and scope checks.
4. Add team handlers and routes.
5. Add invite preview, accept, decline, and invite-register flow.
6. Add shared resource scoping helpers.
7. Update domain, inbox, email, sent/reply, and dashboard handlers.
8. Update domain email auth handlers.
9. Update API key handlers, API key auth, SMTP AUTH, relay, logs.
10. Update static project handlers and service.
11. Update realtime event model and SSE subscriptions.
12. Add frontend team switcher and team management.
13. Add invite join page.
14. Add tests and run `make check`.

## Estimated Effort

| Area | Hours |
|---|---:|
| DB + models | 3-4 |
| Team service + permissions | 6-8 |
| Middleware + context | 3-4 |
| Team handlers + invite flow | 6-8 |
| Resource handler scoping | 8-12 |
| API key + SMTP relay updates | 5-8 |
| Static projects updates | 4-6 |
| Realtime updates | 3-5 |
| Frontend | 8-12 |
| Tests | 6-10 |
| Total | 52-77 |

The higher estimate reflects the real blast radius: teams touch HTTP authorization, SMTP relay, realtime events, static projects, and existing single-user assumptions.
