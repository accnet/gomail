package teams

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gomail/internal/db"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ── DTOs ───────────────────────────────────────────────────────────────────

type TeamSummary struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	OwnerID     uuid.UUID `json:"owner_id"`
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions"`
	MemberCount int       `json:"member_count"`
	IsDefault   bool      `json:"is_default"`
	CanDelete   bool      `json:"can_delete"`
	CreatedAt   time.Time `json:"created_at"`
}

type TeamMemberView struct {
	db.TeamMember
	UserName  string `json:"user_name"`
	UserEmail string `json:"user_email"`
}

type InviteCreated struct {
	ID         uuid.UUID `json:"id"`
	Token      string    `json:"token"` // one-time reveal
	ExpiresAt  time.Time `json:"expires_at"`
	EmailSent  bool      `json:"email_sent"`
	EmailError string    `json:"email_error,omitempty"`
}

type InviteInfo struct {
	ID          uuid.UUID `json:"id"`
	TeamID      uuid.UUID `json:"team_id"`
	TeamName    string    `json:"team_name"`
	InviterName string    `json:"inviter_name"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	Token       string    `json:"token,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// ── Errors ─────────────────────────────────────────────────────────────────

var (
	ErrTeamNotFound          = errors.New("team not found")
	ErrNotOwner              = errors.New("only the team owner can perform this action")
	ErrOwnerCannotLeave      = errors.New("owner cannot leave the team")
	ErrOwnerCannotBeRemoved  = errors.New("owner cannot be removed")
	ErrOwnerCannotBeDemoted  = errors.New("owner cannot be demoted")
	ErrDefaultCannotDelete   = errors.New("default workspace cannot be deleted")
	ErrAlreadyMember         = errors.New("user is already a member of this team")
	ErrNotMember             = errors.New("user is not a member of this team")
	ErrInviteNotFound        = errors.New("invite not found")
	ErrInviteExpired         = errors.New("invite has expired")
	ErrInviteEmailMismatch   = errors.New("invite email does not match current user email")
	ErrInviteNotPending      = errors.New("invite is not in pending status")
	ErrDuplicateInvite       = errors.New("a pending invite for this email already exists")
	ErrInvalidRole           = errors.New("invalid role")
	ErrMissingScope          = errors.New("missing required scope")
	ErrMemberQuotaExceeded   = errors.New("member quota exceeded")
	ErrCreateWorkspaceDenied = errors.New("workspace creation is not allowed for this account")
)

// ── Service ────────────────────────────────────────────────────────────────

type Service struct {
	DB *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{DB: db}
}

func (s *Service) EnsureDefaultWorkspace(ctx context.Context, user db.User) (*db.Team, error) {
	if !user.CanCreateWorkspaces {
		return nil, nil
	}
	var team db.Team
	err := s.DB.WithContext(ctx).
		Joins("JOIN team_members ON team_members.team_id = teams.id AND team_members.deleted_at IS NULL").
		Where("teams.owner_id = ? AND teams.is_default = true AND team_members.user_id = ? AND team_members.role = ? AND teams.deleted_at IS NULL", user.ID, user.ID, db.TeamRoleOwner).
		Order("teams.created_at ASC").
		First(&team).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = s.DB.WithContext(ctx).
			Joins("JOIN team_members ON team_members.team_id = teams.id AND team_members.deleted_at IS NULL").
			Where("teams.owner_id = ? AND team_members.user_id = ? AND team_members.role = ? AND teams.deleted_at IS NULL", user.ID, user.ID, db.TeamRoleOwner).
			Order("teams.created_at ASC").
			First(&team).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			if updateErr := s.DB.WithContext(ctx).Model(&team).Update("is_default", true).Error; updateErr != nil {
				return nil, updateErr
			}
			team.IsDefault = true
		} else {
			name := strings.TrimSpace(user.Name)
			if name == "" {
				name = strings.Split(user.Email, "@")[0]
			}
			if name == "" {
				name = "Default"
			}
			created, createErr := s.CreateTeam(ctx, user.ID, name+"'s Workspace")
			if createErr != nil {
				return nil, createErr
			}
			if updateErr := s.DB.WithContext(ctx).Model(created).Update("is_default", true).Error; updateErr != nil {
				return nil, updateErr
			}
			created.IsDefault = true
			team = *created
		}
	}
	return &team, nil
}

// ── Teams CRUD ─────────────────────────────────────────────────────────────

func (s *Service) CreateTeam(ctx context.Context, userID uuid.UUID, name string) (*db.Team, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("team name is required")
	}
	var user db.User
	if err := s.DB.WithContext(ctx).First(&user, "id = ? AND deleted_at IS NULL", userID).Error; err != nil {
		return nil, err
	}
	if !user.CanCreateWorkspaces {
		return nil, ErrCreateWorkspaceDenied
	}

	team := &db.Team{
		Name:    name,
		OwnerID: userID,
	}

	scopes := MarshalScopes(DefaultScopes(db.TeamRoleOwner))

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(team).Error; err != nil {
			return err
		}
		member := &db.TeamMember{
			TeamID:      team.ID,
			UserID:      userID,
			Role:        db.TeamRoleOwner,
			Permissions: scopes,
		}
		return tx.Create(member).Error
	})
	if err != nil {
		return nil, err
	}
	return team, nil
}

func (s *Service) GetTeam(ctx context.Context, userID, teamID uuid.UUID) (*db.Team, error) {
	var team db.Team
	// User must be a non-deleted member
	err := s.DB.WithContext(ctx).
		Joins("JOIN team_members ON team_members.team_id = teams.id AND team_members.deleted_at IS NULL").
		Where("teams.id = ? AND team_members.user_id = ? AND teams.deleted_at IS NULL", teamID, userID).
		First(&team).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTeamNotFound
	}
	return &team, err
}

func (s *Service) ListTeams(ctx context.Context, userID uuid.UUID) ([]TeamSummary, error) {
	var user db.User
	if err := s.DB.WithContext(ctx).First(&user, "id = ? AND deleted_at IS NULL", userID).Error; err != nil {
		return nil, err
	}

	var results []struct {
		db.Team
		Role        string `json:"role"`
		Permissions datatypes.JSON
		MemberCount int `json:"member_count"`
	}

	q := s.DB.WithContext(ctx).
		Table("teams").
		Select("teams.*, team_members.role, team_members.permissions, (SELECT COUNT(*) FROM team_members tm2 WHERE tm2.team_id = teams.id AND tm2.deleted_at IS NULL) as member_count").
		Joins("JOIN team_members ON team_members.team_id = teams.id AND team_members.deleted_at IS NULL").
		Where("team_members.user_id = ? AND teams.deleted_at IS NULL", userID)
	if !user.CanCreateWorkspaces {
		q = q.Where("team_members.role != ?", db.TeamRoleOwner)
	}
	if err := q.Scan(&results).Error; err != nil {
		return nil, err
	}

	summaries := make([]TeamSummary, len(results))
	for i, r := range results {
		scopes, _ := ParseScopes(r.Permissions)
		if r.Role == db.TeamRoleOwner {
			scopes = DefaultScopes(db.TeamRoleOwner)
		}
		summaries[i] = TeamSummary{
			ID:          r.ID,
			Name:        r.Name,
			OwnerID:     r.OwnerID,
			Role:        r.Role,
			Permissions: scopes,
			MemberCount: r.MemberCount,
			IsDefault:   r.IsDefault,
			CanDelete:   !r.IsDefault && r.Role == db.TeamRoleOwner,
			CreatedAt:   r.CreatedAt,
		}
	}
	return summaries, nil
}

func (s *Service) UpdateTeam(ctx context.Context, actorID, teamID uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("team name is required")
	}

	// Must be owner
	member, err := s.getActiveMember(ctx, teamID, actorID)
	if err != nil {
		return err
	}
	if member.Role != db.TeamRoleOwner {
		return ErrNotOwner
	}

	return s.DB.WithContext(ctx).
		Model(&db.Team{}).
		Where("id = ? AND deleted_at IS NULL", teamID).
		Update("name", name).Error
}

func (s *Service) DeleteTeam(ctx context.Context, actorID, teamID uuid.UUID) error {
	// Must be owner
	member, err := s.getActiveMember(ctx, teamID, actorID)
	if err != nil {
		return err
	}
	if member.Role != db.TeamRoleOwner {
		return ErrNotOwner
	}
	var team db.Team
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", teamID).First(&team).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTeamNotFound
		}
		return err
	}
	if team.IsDefault {
		return ErrDefaultCannotDelete
	}

	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Soft-delete team
		if err := tx.Model(&db.Team{}).Where("id = ?", teamID).Update("deleted_at", time.Now()).Error; err != nil {
			return err
		}
		// Soft-delete all active members
		if err := tx.Model(&db.TeamMember{}).
			Where("team_id = ? AND deleted_at IS NULL", teamID).
			Update("deleted_at", time.Now()).Error; err != nil {
			return err
		}
		return nil
	})
}

// ── Members ────────────────────────────────────────────────────────────────

func (s *Service) GetMember(ctx context.Context, teamID, userID uuid.UUID) (*db.TeamMember, error) {
	return s.getActiveMember(ctx, teamID, userID)
}

func (s *Service) ListMembers(ctx context.Context, actorID, teamID uuid.UUID) ([]TeamMemberView, error) {
	actor, err := s.getActiveMember(ctx, teamID, actorID)
	if err != nil {
		return nil, err
	}
	if actor.Role != db.TeamRoleOwner && !MemberHasScope(*actor, ScopeMemberManage) {
		return nil, ErrMissingScope
	}

	var views []TeamMemberView
	err = s.DB.WithContext(ctx).
		Table("team_members").
		Select("team_members.*, users.name as user_name, users.email as user_email").
		Joins("JOIN users ON users.id = team_members.user_id").
		Where("team_members.team_id = ? AND team_members.deleted_at IS NULL", teamID).
		Scan(&views).Error
	return views, err
}

func (s *Service) UpdateMember(ctx context.Context, actorID, teamID, targetUserID uuid.UUID, role *string, scopes *[]string) error {
	// Actor must have member:manage scope (or be owner)
	actor, err := s.getActiveMember(ctx, teamID, actorID)
	if err != nil {
		return err
	}
	if actor.Role != db.TeamRoleOwner && !MemberHasScope(*actor, ScopeMemberManage) {
		return ErrMissingScope
	}

	target, err := s.getActiveMember(ctx, teamID, targetUserID)
	if err != nil {
		return err
	}

	// Owner protections
	if target.Role == db.TeamRoleOwner {
		if role != nil && *role != db.TeamRoleOwner {
			return ErrOwnerCannotBeDemoted
		}
	}

	updates := map[string]interface{}{}
	if role != nil {
		if err := ValidateRole(*role, false); err != nil {
			return err
		}
		updates["role"] = *role

		// If demoting admin to member without explicit scopes, reset to defaults
		if scopes == nil && target.Role == db.TeamRoleAdmin && *role == db.TeamRoleMember {
			defaultScopes := MarshalScopes(DefaultScopes(db.TeamRoleMember))
			updates["permissions"] = defaultScopes
		}
	}
	if scopes != nil {
		if err := ValidateScopes(*scopes); err != nil {
			return err
		}
		updates["permissions"] = MarshalScopes(*scopes)
	}

	if len(updates) == 0 {
		return nil
	}

	return s.DB.WithContext(ctx).
		Model(&db.TeamMember{}).
		Where("id = ? AND deleted_at IS NULL", target.ID).
		Updates(updates).Error
}

func (s *Service) RemoveMember(ctx context.Context, actorID, teamID, targetUserID uuid.UUID) error {
	// Actor must have member:manage scope (or be owner)
	actor, err := s.getActiveMember(ctx, teamID, actorID)
	if err != nil {
		return err
	}
	if actor.Role != db.TeamRoleOwner && !MemberHasScope(*actor, ScopeMemberManage) {
		return ErrMissingScope
	}

	target, err := s.getActiveMember(ctx, teamID, targetUserID)
	if err != nil {
		return err
	}

	if target.Role == db.TeamRoleOwner {
		return ErrOwnerCannotBeRemoved
	}

	if target.UserID == actorID && actor.Role == db.TeamRoleOwner {
		return ErrOwnerCannotLeave
	}

	return s.DB.WithContext(ctx).
		Model(&db.TeamMember{}).
		Where("id = ?", target.ID).
		Update("deleted_at", time.Now()).Error
}

// ── Invites ────────────────────────────────────────────────────────────────

func (s *Service) InviteMember(ctx context.Context, actorID, teamID uuid.UUID, email, role string, scopes []string) (*InviteCreated, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	// Actor must have member:manage scope (or be owner)
	actor, err := s.getActiveMember(ctx, teamID, actorID)
	if err != nil {
		return nil, err
	}
	if actor.Role != db.TeamRoleOwner && !MemberHasScope(*actor, ScopeMemberManage) {
		return nil, ErrMissingScope
	}

	if err := ValidateRole(role, false); err != nil {
		return nil, err
	}
	if role != db.TeamRoleMember {
		return nil, errors.New("invites can only be created for member role")
	}

	if len(scopes) == 0 {
		scopes = DefaultScopes(role)
	}
	if err := ValidateScopes(scopes); err != nil {
		return nil, err
	}

	// Generate token
	plainToken, tokenHash, err := generateToken()
	if err != nil {
		return nil, err
	}

	invite := &db.TeamInvite{
		TeamID:      teamID,
		Email:       email,
		Role:        role,
		Permissions: MarshalScopes(scopes),
		InviterID:   actorID,
		Status:      db.TeamInviteStatusPending,
		TokenHash:   tokenHash,
		TokenPlain:  plainToken,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour), // 7 days
	}

	// Wrap in transaction to prevent race conditions on quota
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureInviteMemberQuota(tx, ctx, teamID); err != nil {
			return err
		}

		// Check for duplicate pending invite
		var existing int64
		if err := tx.Model(&db.TeamInvite{}).
			Where("team_id = ? AND lower(email) = ? AND status = ?", teamID, email, db.TeamInviteStatusPending).
			Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return ErrDuplicateInvite
		}

		// Check if user is already a member
		var memberCount int64
		if err := tx.
			Table("team_members").
			Joins("JOIN users ON users.id = team_members.user_id").
			Where("team_members.team_id = ? AND lower(users.email) = ? AND team_members.deleted_at IS NULL", teamID, email).
			Count(&memberCount).Error; err != nil {
			return err
		}
		if memberCount > 0 {
			return ErrAlreadyMember
		}

		return tx.Create(invite).Error
	})
	if err != nil {
		return nil, err
	}

	return &InviteCreated{
		ID:        invite.ID,
		Token:     plainToken,
		ExpiresAt: invite.ExpiresAt,
	}, nil
}

func (s *Service) GetInviteInfo(ctx context.Context, token string) (*InviteInfo, error) {
	tokenHash := hashToken(token)

	var invite db.TeamInvite
	if err := s.DB.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&invite).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}

	// Auto-expire
	if invite.Status == db.TeamInviteStatusPending && time.Now().After(invite.ExpiresAt) {
		s.DB.WithContext(ctx).Model(&invite).Update("status", db.TeamInviteStatusExpired)
		invite.Status = db.TeamInviteStatusExpired
	}

	var team db.Team
	s.DB.WithContext(ctx).Where("id = ?", invite.TeamID).First(&team)
	var inviter db.User
	s.DB.WithContext(ctx).Where("id = ?", invite.InviterID).First(&inviter)

	return &InviteInfo{
		ID:          invite.ID,
		TeamID:      invite.TeamID,
		TeamName:    team.Name,
		InviterName: inviter.Name,
		Email:       invite.Email,
		Role:        invite.Role,
		Status:      invite.Status,
		ExpiresAt:   invite.ExpiresAt,
	}, nil
}

func (s *Service) AcceptInvite(ctx context.Context, userID uuid.UUID, token string) (*db.TeamMember, error) {
	tokenHash := hashToken(token)

	var user db.User
	if err := s.DB.WithContext(ctx).Where("id = ?", userID).First(&user).Error; err != nil {
		return nil, errors.New("user not found")
	}

	var invite db.TeamInvite
	if err := s.DB.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&invite).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}

	// Check expired before status to return the correct error
	if time.Now().After(invite.ExpiresAt) {
		return nil, ErrInviteExpired
	}
	if invite.Status != db.TeamInviteStatusPending {
		return nil, ErrInviteNotPending
	}
	if !strings.EqualFold(user.Email, invite.Email) {
		return nil, ErrInviteEmailMismatch
	}

	// Wrap in transaction to prevent race conditions on quota and membership
	var member *db.TeamMember
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if invite.Role != db.TeamRoleOwner {
			if err := ensureActiveMemberQuota(tx, ctx, invite.TeamID); err != nil {
				return err
			}
		}

		// Check if already a member (inside txn to prevent concurrent accept)
		var existingCount int64
		if err := tx.Model(&db.TeamMember{}).
			Where("team_id = ? AND user_id = ? AND deleted_at IS NULL", invite.TeamID, userID).
			Count(&existingCount).Error; err != nil {
			return err
		}
		if existingCount > 0 {
			return ErrAlreadyMember
		}

		member = &db.TeamMember{
			TeamID:      invite.TeamID,
			UserID:      userID,
			Role:        invite.Role,
			Permissions: invite.Permissions,
		}
		if err := tx.Create(member).Error; err != nil {
			return err
		}
		now := time.Now()
		return tx.Model(&invite).Updates(map[string]interface{}{
			"status":      db.TeamInviteStatusAccepted,
			"accepted_at": now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return member, nil
}

func (s *Service) DeclineInvite(ctx context.Context, userID uuid.UUID, token string) error {
	tokenHash := hashToken(token)

	var user db.User
	if err := s.DB.WithContext(ctx).Where("id = ?", userID).First(&user).Error; err != nil {
		return errors.New("user not found")
	}

	var invite db.TeamInvite
	if err := s.DB.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&invite).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInviteNotFound
		}
		return err
	}

	if invite.Status != db.TeamInviteStatusPending {
		return ErrInviteNotPending
	}
	if !strings.EqualFold(user.Email, invite.Email) {
		return ErrInviteEmailMismatch
	}

	now := time.Now()
	return s.DB.WithContext(ctx).Model(&invite).Updates(map[string]interface{}{
		"status":      db.TeamInviteStatusDeclined,
		"declined_at": now,
	}).Error
}

func (s *Service) CancelInvite(ctx context.Context, actorID, teamID, inviteID uuid.UUID) error {
	_, err := s.getActiveMember(ctx, teamID, actorID)
	if err != nil {
		return err
	}

	result := s.DB.WithContext(ctx).
		Model(&db.TeamInvite{}).
		Where("id = ? AND team_id = ? AND status = ?", inviteID, teamID, db.TeamInviteStatusPending).
		Update("status", db.TeamInviteStatusCancelled)
	if result.RowsAffected == 0 {
		return ErrInviteNotFound
	}
	return result.Error
}

func (s *Service) ListPendingInvites(ctx context.Context, actorID, teamID uuid.UUID) ([]InviteInfo, error) {
	actor, err := s.getActiveMember(ctx, teamID, actorID)
	if err != nil {
		return nil, err
	}
	if actor.Role != db.TeamRoleOwner && !MemberHasScope(*actor, ScopeMemberManage) {
		return nil, ErrMissingScope
	}

	var invites []db.TeamInvite
	if err := s.DB.WithContext(ctx).
		Where("team_id = ? AND status = ?", teamID, db.TeamInviteStatusPending).
		Find(&invites).Error; err != nil {
		return nil, err
	}

	var team db.Team
	s.DB.WithContext(ctx).Where("id = ?", teamID).First(&team)

	infos := make([]InviteInfo, len(invites))
	for i, inv := range invites {
		var inviter db.User
		s.DB.WithContext(ctx).Where("id = ?", inv.InviterID).First(&inviter)
		infos[i] = InviteInfo{
			ID:          inv.ID,
			TeamID:      inv.TeamID,
			TeamName:    team.Name,
			InviterName: inviter.Name,
			Email:       inv.Email,
			Role:        inv.Role,
			Status:      inv.Status,
			Token:       inv.TokenPlain,
			ExpiresAt:   inv.ExpiresAt,
		}
	}
	return infos, nil
}

// ── Internal helpers ───────────────────────────────────────────────────────

func ensureInviteMemberQuota(database *gorm.DB, ctx context.Context, teamID uuid.UUID) error {
	maxMembers, err := teamMaxMembers(database, ctx, teamID)
	if err != nil {
		return err
	}
	var activeMembers int64
	if err := database.WithContext(ctx).Model(&db.TeamMember{}).
		Where("team_id = ? AND role != ? AND deleted_at IS NULL", teamID, db.TeamRoleOwner).
		Count(&activeMembers).Error; err != nil {
		return err
	}
	var pendingInvites int64
	if err := database.WithContext(ctx).Model(&db.TeamInvite{}).
		Where("team_id = ? AND role != ? AND status = ?", teamID, db.TeamRoleOwner, db.TeamInviteStatusPending).
		Count(&pendingInvites).Error; err != nil {
		return err
	}
	if int(activeMembers+pendingInvites) >= maxMembers {
		return ErrMemberQuotaExceeded
	}
	return nil
}

func ensureActiveMemberQuota(database *gorm.DB, ctx context.Context, teamID uuid.UUID) error {
	maxMembers, err := teamMaxMembers(database, ctx, teamID)
	if err != nil {
		return err
	}
	var activeMembers int64
	if err := database.WithContext(ctx).Model(&db.TeamMember{}).
		Where("team_id = ? AND role != ? AND deleted_at IS NULL", teamID, db.TeamRoleOwner).
		Count(&activeMembers).Error; err != nil {
		return err
	}
	if int(activeMembers) >= maxMembers {
		return ErrMemberQuotaExceeded
	}
	return nil
}

func teamMaxMembers(database *gorm.DB, ctx context.Context, teamID uuid.UUID) (int, error) {
	var owner db.User
	err := database.WithContext(ctx).
		Joins("JOIN teams ON teams.owner_id = users.id").
		Where("teams.id = ? AND teams.deleted_at IS NULL", teamID).
		First(&owner).Error
	if err != nil {
		return 0, err
	}
	return owner.MaxMembers, nil
}

func (s *Service) getActiveMember(ctx context.Context, teamID, userID uuid.UUID) (*db.TeamMember, error) {
	var member db.TeamMember
	err := s.DB.WithContext(ctx).
		Where("team_id = ? AND user_id = ? AND deleted_at IS NULL", teamID, userID).
		First(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotMember
	}
	return &member, err
}

func generateToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = hex.EncodeToString(b)
	hash = hashToken(plaintext)
	return plaintext, hash, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
