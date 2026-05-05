package teams

import (
	"context"
	"errors"
	"testing"

	"gomail/internal/db"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEnsureDefaultWorkspaceCreatesDefaultWorkspace(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}

	user := db.User{
		Email:               "newuser@test.local",
		Name:                "NewUser",
		PasswordHash:        "hash",
		IsActive:            true,
		CanCreateWorkspaces: true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	team, err := NewService(database).EnsureDefaultWorkspace(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if team == nil {
		t.Fatal("expected non-nil default workspace")
	}
	if !team.IsDefault {
		t.Fatal("expected default workspace")
	}
	if team.OwnerID != user.ID {
		t.Fatalf("expected owner %s, got %s", user.ID, team.OwnerID)
	}

	// Calling again should return the same workspace, not create a new one
	team2, err := NewService(database).EnsureDefaultWorkspace(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if team2.ID != team.ID {
		t.Fatalf("expected same workspace ID, got different: %s vs %s", team.ID, team2.ID)
	}
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
		Email:               "owner@test.local",
		PasswordHash:        "hash",
		IsActive:            true,
		CanCreateWorkspaces: true,
		MaxMembers:          0,
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
