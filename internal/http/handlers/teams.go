package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"gomail/internal/auth"
	"gomail/internal/db"
	mw "gomail/internal/http/middleware"
	"gomail/internal/mail/outbound"
	"gomail/internal/teams"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ── Team CRUD ──────────────────────────────────────────────────────────────

func (a App) createTeam(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if !bind(c, &req) {
		return
	}

	user := mw.CurrentUser(c)
	team, err := a.Teams.CreateTeam(c.Request.Context(), user.ID, req.Name)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "create_team_failed", err.Error())
		return
	}
	response.Created(c, team)
}

func (a App) listTeams(c *gin.Context) {
	user := mw.CurrentUser(c)
	list, err := a.Teams.ListTeams(c.Request.Context(), user.ID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "list_teams_failed", err.Error())
		return
	}
	if list == nil {
		list = []teams.TeamSummary{}
	}
	response.OK(c, list)
}

func (a App) getTeam(c *gin.Context) {
	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}

	team, err := a.Teams.GetTeam(c.Request.Context(), user.ID, teamID)
	if err != nil {
		response.Error(c, http.StatusNotFound, "team_not_found", err.Error())
		return
	}
	response.OK(c, team)
}

func (a App) updateTeam(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if !bind(c, &req) {
		return
	}

	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}

	if err := a.Teams.UpdateTeam(c.Request.Context(), user.ID, teamID, req.Name); err != nil {
		response.Error(c, http.StatusBadRequest, "update_team_failed", err.Error())
		return
	}
	response.OK(c, gin.H{"ok": true})
}

func (a App) deleteTeam(c *gin.Context) {
	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}

	if err := a.Teams.DeleteTeam(c.Request.Context(), user.ID, teamID); err != nil {
		response.Error(c, http.StatusBadRequest, "delete_team_failed", err.Error())
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// ── Members ────────────────────────────────────────────────────────────────

func (a App) listMembers(c *gin.Context) {
	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}

	members, err := a.Teams.ListMembers(c.Request.Context(), user.ID, teamID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "list_members_failed", err.Error())
		return
	}
	if members == nil {
		members = []teams.TeamMemberView{}
	}
	response.OK(c, members)
}

func (a App) updateMember(c *gin.Context) {
	var req struct {
		Role   *string   `json:"role"`
		Scopes *[]string `json:"scopes"`
	}
	if !bind(c, &req) {
		return
	}

	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}
	memberUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_user_id", "invalid user id")
		return
	}

	if err := a.Teams.UpdateMember(c.Request.Context(), user.ID, teamID, memberUserID, req.Role, req.Scopes); err != nil {
		response.Error(c, http.StatusBadRequest, "update_member_failed", err.Error())
		return
	}
	response.OK(c, gin.H{"ok": true})
}

func (a App) removeMember(c *gin.Context) {
	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}
	memberUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_user_id", "invalid user id")
		return
	}

	if err := a.Teams.RemoveMember(c.Request.Context(), user.ID, teamID, memberUserID); err != nil {
		response.Error(c, http.StatusBadRequest, "remove_member_failed", err.Error())
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// ── Invites ────────────────────────────────────────────────────────────────

func (a App) inviteMember(c *gin.Context) {
	var req struct {
		Email  string   `json:"email"`
		Role   string   `json:"role"`
		Scopes []string `json:"scopes"`
	}
	if !bind(c, &req) {
		return
	}

	if req.Role == "" {
		req.Role = db.TeamRoleMember
	}

	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}

	invite, err := a.Teams.InviteMember(c.Request.Context(), user.ID, teamID, req.Email, req.Role, req.Scopes)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invite_failed", err.Error())
		return
	}
	a.sendTeamInviteEmail(c, user, teamID, invite, req.Email)
	response.Created(c, invite)
}

func (a App) sendTeamInviteEmail(c *gin.Context, inviter db.User, teamID uuid.UUID, invite *teams.InviteCreated, inviteEmail string) {
	if invite == nil || a.SendOutbound == nil {
		return
	}
	from := strings.TrimSpace(a.Config.SaaSDomain)
	from = strings.TrimSuffix(strings.ToLower(from), ".")
	if from == "" {
		invite.EmailError = "SAAS_DOMAIN is not configured"
		return
	}
	from = "no-reply@" + from

	teamName := "your workspace"
	if team, err := a.Teams.GetTeam(c.Request.Context(), inviter.ID, teamID); err == nil && strings.TrimSpace(team.Name) != "" {
		teamName = team.Name
	}
	inviterName := strings.TrimSpace(inviter.Name)
	if inviterName == "" {
		inviterName = inviter.Email
	}
	link := strings.TrimRight(a.Config.AppBaseURL, "/") + "/app/join.html?token=" + invite.Token
	if strings.HasPrefix(link, "/app/") {
		link = requestOrigin(c) + link
	}

	subject := fmt.Sprintf("You're invited to join %s", teamName)
	body := fmt.Sprintf(`%s invited you to join %s on GoMail.

Accept the invite:
%s

This invite expires at %s.
`, inviterName, teamName, link, invite.ExpiresAt.Format("2006-01-02 15:04 MST"))

	log := db.SentEmailLog{
		UserID:  inviter.ID,
		TeamID:  &teamID,
		Mode:    "team_invite",
		Subject: subject,
	}
	err := a.SendOutbound(inviter.ID, outbound.Message{
		From:     from,
		To:       []string{inviteEmail},
		Subject:  subject,
		TextBody: body,
		Headers: map[string]string{
			"X-GoMail-Notification": "team-invite",
		},
	}, log)
	if err != nil {
		invite.EmailError = err.Error()
		return
	}
	invite.EmailSent = true
}

func requestOrigin(c *gin.Context) string {
	proto := c.GetHeader("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
	}
	host := c.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = c.Request.Host
	}
	if host == "" {
		return ""
	}
	return proto + "://" + host
}

func (a App) listInvites(c *gin.Context) {
	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}

	invites, err := a.Teams.ListPendingInvites(c.Request.Context(), user.ID, teamID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "list_invites_failed", err.Error())
		return
	}
	if invites == nil {
		invites = []teams.InviteInfo{}
	}
	response.OK(c, invites)
}

func (a App) cancelInvite(c *gin.Context) {
	user := mw.CurrentUser(c)
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id")
		return
	}
	inviteID, err := uuid.Parse(c.Param("inviteId"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_invite_id", "invalid invite id")
		return
	}

	if err := a.Teams.CancelInvite(c.Request.Context(), user.ID, teamID, inviteID); err != nil {
		response.Error(c, http.StatusBadRequest, "cancel_invite_failed", err.Error())
		return
	}
	response.OK(c, gin.H{"ok": true})
}

func (a App) getInviteInfo(c *gin.Context) {
	token := c.Param("token")
	info, err := a.Teams.GetInviteInfo(c.Request.Context(), token)
	if err != nil {
		response.Error(c, http.StatusNotFound, "invite_not_found", err.Error())
		return
	}
	response.OK(c, info)
}

func (a App) acceptInvite(c *gin.Context) {
	user := mw.CurrentUser(c)
	token := c.Param("token")

	_, err := a.Teams.AcceptInvite(c.Request.Context(), user.ID, token)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "accept_invite_failed", err.Error())
		return
	}
	response.OK(c, gin.H{"ok": true})
}

func (a App) declineInvite(c *gin.Context) {
	user := mw.CurrentUser(c)
	token := c.Param("token")

	if err := a.Teams.DeclineInvite(c.Request.Context(), user.ID, token); err != nil {
		response.Error(c, http.StatusBadRequest, "decline_invite_failed", err.Error())
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// ── Invite Register (creates new active user + accepts invite) ─────────────

func (a App) inviteRegister(c *gin.Context) {
	token := c.Param("token")

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if !bind(c, &req) {
		return
	}

	if len(req.Password) < 8 {
		response.Error(c, http.StatusBadRequest, "invalid_input", "password length >= 8 required")
		return
	}

	// Validate invite first
	info, err := a.Teams.GetInviteInfo(c.Request.Context(), token)
	if err != nil {
		response.Error(c, http.StatusNotFound, "invite_not_found", err.Error())
		return
	}
	if info.Status != db.TeamInviteStatusPending {
		response.Error(c, http.StatusBadRequest, "invite_not_pending", "invite is no longer pending")
		return
	}
	if !strings.EqualFold(req.Email, info.Email) {
		response.Error(c, http.StatusBadRequest, "email_mismatch", "email does not match invite")
		return
	}

	// Create user (active) + accept invite in one transaction
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "hash_failed", "could not hash password")
		return
	}

	user := db.User{
		Email:               strings.ToLower(req.Email),
		Name:                req.Name,
		PasswordHash:        hash,
		IsActive:            true,
		MaxDomains:          5,
		MaxInboxes:          50,
		MaxMembers:          5,
		MaxAttachmentSizeMB: 25,
		MaxMessageSizeMB:    25,
		MaxStorageBytes:     10 * 1024 * 1024 * 1024,
	}

	err = a.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		// Temporarily set Teams DB to this tx for AcceptInvite
		origDB := a.Teams.DB
		a.Teams.DB = tx
		if _, ensureErr := a.Teams.EnsureDefaultWorkspace(c.Request.Context(), user); ensureErr != nil {
			a.Teams.DB = origDB
			return ensureErr
		}
		_, acceptErr := a.Teams.AcceptInvite(c.Request.Context(), user.ID, token)
		a.Teams.DB = origDB
		return acceptErr
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			response.Error(c, http.StatusConflict, "email_exists", "email already exists")
		} else {
			response.Error(c, http.StatusBadRequest, "register_failed", err.Error())
		}
		return
	}

	// Issue tokens
	access, err := a.Auth.AccessToken(user)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "token_failed", "could not issue access token")
		return
	}
	refresh, err := a.Auth.NewRefreshToken(user.ID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "token_failed", "could not issue refresh token")
		return
	}
	response.Created(c, gin.H{
		"access_token":  access,
		"refresh_token": refresh,
		"user":          user,
	})
}
