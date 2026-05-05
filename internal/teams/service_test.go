package teams

import (
	"context"
	"errors"
	"testing"
	"time"

	"gomail/internal/db"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEnsureDefaultWorkspaceMigratesPersonalResources(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}

	user := db.User{
		Email:        "legacy@test.local",
		Name:         "Legacy",
		PasswordHash: "hash",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	domain := db.Domain{UserID: user.ID, Name: "legacy.test"}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@legacy.test"}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	apiKey := db.ApiKey{UserID: user.ID, Name: "Legacy Key", KeyPrefix: "legacy", KeyHash: "legacy-hash", Scopes: "send_email"}
	if err := database.Create(&apiKey).Error; err != nil {
		t.Fatal(err)
	}
	project := db.StaticProject{UserID: user.ID, Name: "Legacy Site", Subdomain: "legacy-site", RootFolder: "/tmp/root", StagingFolder: "/tmp/staging"}
	if err := database.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	sentLog := db.SentEmailLog{UserID: user.ID, Status: db.SentEmailStatusSent}
	if err := database.Create(&sentLog).Error; err != nil {
		t.Fatal(err)
	}
	usageLog := db.ApiKeyUsageLog{ApiKeyID: apiKey.ID, UserID: user.ID, Endpoint: "/api/send-email"}
	if err := database.Create(&usageLog).Error; err != nil {
		t.Fatal(err)
	}
	auditLog := db.AuditLog{ActorID: &user.ID, Type: "legacy.audit"}
	if err := database.Create(&auditLog).Error; err != nil {
		t.Fatal(err)
	}
	deletedTeam := db.Team{Name: "Deleted Workspace", OwnerID: user.ID, DeletedAt: gorm.DeletedAt{Time: time.Now(), Valid: true}}
	if err := database.Create(&deletedTeam).Error; err != nil {
		t.Fatal(err)
	}
	deletedTeamDomain := db.Domain{UserID: user.ID, TeamID: &deletedTeam.ID, Name: "deleted-workspace.test"}
	if err := database.Create(&deletedTeamDomain).Error; err != nil {
		t.Fatal(err)
	}

	team, err := NewService(database).EnsureDefaultWorkspace(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if !team.IsDefault {
		t.Fatal("expected default workspace")
	}

	assertTeamID(t, database, &db.Domain{}, domain.ID, team.ID.String())
	assertTeamID(t, database, &db.Inbox{}, inbox.ID, team.ID.String())
	assertTeamID(t, database, &db.ApiKey{}, apiKey.ID, team.ID.String())
	assertTeamID(t, database, &db.StaticProject{}, project.ID, team.ID.String())
	assertTeamID(t, database, &db.SentEmailLog{}, sentLog.ID, team.ID.String())
	assertTeamID(t, database, &db.ApiKeyUsageLog{}, usageLog.ID, team.ID.String())
	assertTeamID(t, database, &db.AuditLog{}, auditLog.ID, team.ID.String())
	assertTeamID(t, database, &db.Domain{}, deletedTeamDomain.ID, team.ID.String())
}

func TestInviteMemberRespectsOwnerMemberQuota(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}

	owner := db.User{
		Email:        "owner@test.local",
		PasswordHash: "hash",
		IsActive:     true,
		MaxMembers:   0,
	}
	if err := database.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&owner).Update("max_members", 0).Error; err != nil {
		t.Fatal(err)
	}
	team, err := NewService(database).CreateTeam(context.Background(), owner.ID, "Quota Team")
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewService(database).InviteMember(context.Background(), owner.ID, team.ID, "member@test.local", db.TeamRoleMember, nil)
	if !errors.Is(err, ErrMemberQuotaExceeded) {
		t.Fatalf("InviteMember error = %v, want %v", err, ErrMemberQuotaExceeded)
	}
}

func assertTeamID(t *testing.T, database *gorm.DB, model any, id any, want string) {
	t.Helper()
	var got string
	if err := database.Model(model).Select("team_id").Where("id = ?", id).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("team_id = %q, want %q", got, want)
	}
}
