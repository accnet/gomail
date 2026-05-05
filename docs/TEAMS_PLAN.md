# GoMail Teams — Implementation Plan

## Overview

Add multi-user teams so a group of users can share domains, inboxes, API keys, and static projects. Currently everything is single-user (`user_id` ownership).

---

## Phase 1: Database

### 1.1 New Tables

```sql
-- teams
CREATE TABLE teams (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    owner_id    UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);
CREATE INDEX idx_teams_owner ON teams(owner_id) WHERE deleted_at IS NULL;

-- team_members
CREATE TABLE team_members (
    id          UUID PRIMARY KEY,
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id),
    role        TEXT NOT NULL DEFAULT 'member',  -- owner | admin | member
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    UNIQUE(team_id, user_id)
);
CREATE INDEX idx_team_members_user ON team_members(user_id) WHERE deleted_at IS NULL;

-- team_invites
CREATE TABLE team_invites (
    id          UUID PRIMARY KEY,
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'member',
    inviter_id  UUID NOT NULL REFERENCES users(id),
    status      TEXT NOT NULL DEFAULT 'pending',  -- pending | accepted | declined | expired
    token       TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_team_invites_team ON team_invites(team_id);
CREATE INDEX idx_team_invites_email ON team_invites(email);
CREATE INDEX idx_team_invites_token ON team_invites(token);
```

### 1.2 Alter Existing Tables

Add nullable `team_id` to resources that can be team-owned:

```sql
ALTER TABLE domains      ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE inboxes      ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE api_keys     ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE static_projects ADD COLUMN team_id UUID REFERENCES teams(id);
ALTER TABLE sent_email_logs ADD COLUMN team_id UUID REFERENCES teams(id);

-- Indexes
CREATE INDEX idx_domains_team      ON domains(team_id)      WHERE deleted_at IS NULL AND team_id IS NOT NULL;
CREATE INDEX idx_inboxes_team      ON inboxes(team_id)      WHERE deleted_at IS NULL AND team_id IS NOT NULL;
CREATE INDEX idx_api_keys_team     ON api_keys(team_id)     WHERE deleted_at IS NULL AND team_id IS NOT NULL;
CREATE INDEX idx_static_projects_team ON static_projects(team_id) WHERE deleted_at IS NULL AND team_id IS NOT NULL;
```

**Rule:** A resource belongs to EITHER a user (`team_id IS NULL`) OR a team (`team_id IS NOT NULL`), never both.

### 1.3 GORM Models

File: `internal/db/models.go` — add:

```go
type Team struct {
    ID        uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
    Name      string         `gorm:"not null" json:"name"`
    OwnerID   uuid.UUID      `gorm:"type:uuid;index;not null" json:"owner_id"`
    Owner     *User          `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
    Members   []TeamMember   `gorm:"foreignKey:TeamID" json:"members,omitempty"`
    CreatedAt time.Time      `json:"created_at"`
    UpdatedAt time.Time      `json:"updated_at"`
    DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type TeamMember struct {
    ID        uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
    TeamID    uuid.UUID      `gorm:"type:uuid;uniqueIndex:idx_team_user;not null" json:"team_id"`
    UserID    uuid.UUID      `gorm:"type:uuid;uniqueIndex:idx_team_user;not null" json:"user_id"`
    Role      string         `gorm:"not null;default:member" json:"role"` // owner | admin | member
    JoinedAt  time.Time      `json:"joined_at"`
    CreatedAt time.Time      `json:"created_at"`
    UpdatedAt time.Time      `json:"updated_at"`
    DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type TeamInvite struct {
    ID        uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
    TeamID    uuid.UUID  `gorm:"type:uuid;index;not null" json:"team_id"`
    Team      *Team      `gorm:"foreignKey:TeamID" json:"team,omitempty"`
    Email     string     `gorm:"not null" json:"email"`
    Role      string     `gorm:"not null;default:member" json:"role"`
    InviterID uuid.UUID  `gorm:"type:uuid;not null" json:"inviter_id"`
    Status    string     `gorm:"not null;default:pending" json:"status"` // pending | accepted | declined
    Token     string     `gorm:"uniqueIndex;not null" json:"token"`
    ExpiresAt time.Time  `gorm:"not null" json:"expires_at"`
    CreatedAt time.Time  `json:"created_at"`
    UpdatedAt time.Time  `json:"updated_at"`
}
```

Add to Domain, Inbox, ApiKey, StaticProject, SentEmailLog:

```go
TeamID *uuid.UUID `gorm:"type:uuid;index" json:"team_id,omitempty"`
```

---

## Phase 2: Backend — Service Layer

### 2.1 File: `internal/teams/service.go`

```go
package teams

type Service struct {
    DB *gorm.DB
}

func NewService(db *gorm.DB) *Service { ... }

// CreateTeam creates a team and adds creator as owner.
func (s *Service) CreateTeam(userID uuid.UUID, name string) (*db.Team, error)

// GetTeam returns team if user is a member.
func (s *Service) GetTeam(userID, teamID uuid.UUID) (*db.Team, error)

// ListTeams returns all teams the user belongs to.
func (s *Service) ListTeams(userID uuid.UUID) ([]db.Team, error)

// UpdateTeam (owner/admin only).
func (s *Service) UpdateTeam(userID, teamID uuid.UUID, name string) error

// DeleteTeam (owner only).
func (s *Service) DeleteTeam(userID, teamID uuid.UUID) error

// ListMembers returns members of a team.
func (s *Service) ListMembers(userID, teamID uuid.UUID) ([]db.TeamMember, error)

// ChangeMemberRole (owner/admin only, cannot change owner).
func (s *Service) ChangeMemberRole(actorID, teamID, memberID uuid.UUID, role string) error

// RemoveMember (owner/admin, or self-leave).
func (s *Service) RemoveMember(actorID, teamID, memberID uuid.UUID) error

// InviteMember sends invite email + creates TeamInvite record.
func (s *Service) InviteMember(actorID, teamID uuid.UUID, email, role string) (*db.TeamInvite, error)

// AcceptInvite accepts an invite via token, creates TeamMember.
func (s *Service) AcceptInvite(userID uuid.UUID, token string) (*db.TeamMember, error)

// DeclineInvite declines an invite.
func (s *Service) DeclineInvite(userID uuid.UUID, token string) error

// ListPendingInvites for a team (owner/admin only).
func (s *Service) ListPendingInvites(userID, teamID uuid.UUID) ([]db.TeamInvite, error)

// GetUserRole returns the role of user in team (or empty string if not member).
func (s *Service) GetUserRole(userID, teamID uuid.UUID) string
```

### 2.2 Authorization Helpers

File: `internal/teams/authz.go`

```go
// CanManageTeam returns true if user is owner or admin of the team.
func CanManageTeam(role string) bool { return role == "owner" || role == "admin" }

// CanOwnTeam returns true if user is the owner.
func CanOwnTeam(role string) bool { return role == "owner" }

// RequireTeamRole is middleware factory — checks user belongs to team with required role.
func RequireTeamRole(svc *Service, teamIDParam string, roles ...string) gin.HandlerFunc
```

---

## Phase 3: Backend — API Endpoints

### 3.1 Routes (in `app.go` Router)

```go
teamsGroup := protected.Group("/teams")
teamsGroup.POST("", a.createTeam)
teamsGroup.GET("", a.listTeams)
teamsGroup.GET("/:id", a.getTeam)
teamsGroup.PATCH("/:id", a.updateTeam)
teamsGroup.DELETE("/:id", a.deleteTeam)

teamsGroup.GET("/:id/members", a.listMembers)
teamsGroup.PATCH("/:id/members/:memberId", a.updateMember)
teamsGroup.DELETE("/:id/members/:memberId", a.removeMember)

teamsGroup.POST("/:id/invites", a.inviteMember)
teamsGroup.GET("/:id/invites", a.listInvites)
teamsGroup.DELETE("/:id/invites/:inviteId", a.cancelInvite)
```

Public endpoints (no auth needed to accept):

```go
api.GET("/team-invites/:token", a.getInviteInfo)      // preview invite
api.POST("/team-invites/:token/accept", a.acceptInvite) // accept (requires auth)
api.POST("/team-invites/:token/decline", a.declineInvite)
```

### 3.2 Handlers

File: `internal/http/handlers/teams.go`

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/api/teams` | Bearer | Create team (creator becomes owner) |
| GET | `/api/teams` | Bearer | List my teams |
| GET | `/api/teams/:id` | Bearer + member | Get team detail |
| PATCH | `/api/teams/:id` | Bearer + admin | Update team name |
| DELETE | `/api/teams/:id` | Bearer + owner | Delete team |
| GET | `/api/teams/:id/members` | Bearer + member | List members |
| PATCH | `/api/teams/:id/members/:mid` | Bearer + admin | Change role |
| DELETE | `/api/teams/:id/members/:mid` | Bearer + admin/self | Remove member |
| POST | `/api/teams/:id/invites` | Bearer + admin | Invite user |
| GET | `/api/teams/:id/invites` | Bearer + admin | List pending invites |
| DELETE | `/api/teams/:id/invites/:iid` | Bearer + admin | Cancel invite |
| GET | `/api/team-invites/:token` | Public | Preview invite info |
| POST | `/api/team-invites/:token/accept` | Bearer | Accept invite |
| POST | `/api/team-invites/:token/decline` | Bearer | Decline invite |

---

## Phase 4: Resource Ownership Changes

### 4.1 Context Enrichment

Add to auth middleware (`internal/http/middleware/auth.go`):

```go
// After extracting current user, also attach their team context
c.Set("team_id", getTeamIDFromRequest(c))
c.Set("team_role", getTeamRole(c))
```

### 4.2 Query Scoping

All list/create handlers for Domain, Inbox, ApiKey, StaticProject need to support `team_id`:

```go
// Before (single user):
db.Where("user_id = ?", user.ID).Find(&domains)

// After (user or team):
if teamID := c.GetString("team_id"); teamID != "" {
    db.Where("team_id = ?", teamID).Find(&domains)
} else {
    db.Where("user_id = ? AND team_id IS NULL", user.ID).Find(&domains)
}
```

### 4.3 Create with Team

When creating a resource, accept optional `team_id`:

```json
POST /api/domains
{
    "name": "mycompany.com",
    "team_id": "uuid-here"   // optional
}
```

Validate that the user belongs to the team and has at least `admin` role before allowing team-scoped creation.

### 4.4 Quota Checks

Team resources count against the team owner's quota (simplest approach). Or introduce `max_team_domains`, `max_team_inboxes` on User model.

---

## Phase 5: Frontend

### 5.1 Team Switcher

In sidebar/topbar, show current context:

```
[GoMail]  ▼ MyCompany Team
          ──────────────
          Personal
          MyCompany Team
          ──────────────
          + Create Team
```

When "Personal" is selected → show user's own resources. When a team is selected → show team resources.

### 5.2 Team Management Page

New page `/app/` section or new route:

- **Team list** — cards with team name, member count, my role
- **Team detail** — members table (avatar, email, role, actions), pending invites, team settings
- **Invite modal** — email input + role dropdown → send invite
- **Accept invite** — from email link or notification badge

### 5.3 UI Files

| File | Purpose |
|---|---|
| `web/teams.js` | Team API calls, list/detail rendering |
| `web/main.js` | Team switcher in sidebar, context switching |
| `web/styles.css` | Team cards, member table, invite badges |

### 5.4 Context Switching Behavior

When user switches team context:
1. `state.activeTeamId` is set
2. All API calls include `?team_id=xxx` or `X-Team-Id` header
3. Domain, Inbox, API Key, Project lists reload with team scope
4. Sidebar shows team name instead of "Personal"

---

## Phase 6: Edge Cases & Polish

| Case | Handling |
|---|---|
| Owner leaves team | Must transfer ownership or delete team first |
| Delete team | Soft-delete team + cascade soft-delete all team members. Resources with `team_id` become orphaned (or reassign to owner) |
| User deleted from system | Cascade remove from all teams |
| Invite expired | Cron or on-access check `expires_at < now()` → mark `expired` |
| Duplicate invite | Upsert or reject if pending invite exists for same email+team |
| Cross-team resource access | Middleware rejects unless user is member of the resource's team |
| Rate limiting | Team-shared API keys share rate limit counters |
| Audit log | `DomainEvent` already exists — extend to log team_id and actor_id |

---

## Implementation Order

1. **DB migration** (new tables + alter existing) 
2. **GORM models** in `internal/db/models.go`
3. **Service layer** `internal/teams/service.go` + `authz.go`
4. **Handlers** `internal/http/handlers/teams.go`
5. **Wire routes** in `app.go`
6. **Middleware update** — team context in auth
7. **Resource handler updates** — domain, inbox, apikey, projects support team_id
8. **Frontend** — team switcher, management page, invite flow
9. **Tests**
10. **Deploy**

## Estimated Effort

| Phase | Hours |
|---|---|
| DB + Models | 2-3 |
| Service Layer | 3-4 |
| API Handlers | 4-5 |
| Resource Handler Updates | 3-4 |
| Frontend | 6-8 |
| Tests | 2-3 |
| **Total** | **20-27h** (~3-4 days) |
