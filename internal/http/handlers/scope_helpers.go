package handlers

import (
	"gomail/internal/teams"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ── Query Scoping Helpers ──────────────────────────────────────────────────
// Each helper restricts a GORM query to only resources visible in the
// current active context (personal or team).

func ScopeDomains(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB {
	if ctx.Personal {
		return q.Where("domains.user_id = ? AND domains.team_id IS NULL", ctx.UserID)
	}
	return q.Where("domains.team_id = ?", ctx.TeamID)
}

func ScopeInboxes(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB {
	if ctx.Personal {
		return q.Where("inboxes.user_id = ? AND inboxes.team_id IS NULL", ctx.UserID)
	}
	return q.Where("inboxes.team_id = ?", ctx.TeamID)
}

func ScopeApiKeys(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB {
	if ctx.Personal {
		return q.Where("api_keys.user_id = ? AND api_keys.team_id IS NULL", ctx.UserID)
	}
	return q.Where("api_keys.team_id = ?", ctx.TeamID)
}

func ScopeStaticProjects(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB {
	if ctx.Personal {
		return q.Where("static_projects.user_id = ? AND static_projects.team_id IS NULL", ctx.UserID)
	}
	return q.Where("static_projects.team_id = ?", ctx.TeamID)
}

func ScopeSentEmailLogs(q *gorm.DB, ctx teams.ActiveContext) *gorm.DB {
	if ctx.Personal {
		return q.Where("sent_email_logs.user_id = ? AND sent_email_logs.team_id IS NULL", ctx.UserID)
	}
	return q.Where("sent_email_logs.team_id = ?", ctx.TeamID)
}

// ── Ownership Helpers ─────────────────────────────────────────────────────

// ShouldSetTeamID returns the TeamID to set when creating a resource,
// based on the active context.
func ShouldSetTeamID(ctx teams.ActiveContext) *uuid.UUID {
	if ctx.Personal {
		return nil
	}
	return ctx.TeamID
}

// ShouldSetUserID returns the UserID to set as creator/quota anchor.
func ShouldSetUserID(ctx teams.ActiveContext) uuid.UUID {
	return ctx.UserID
}
