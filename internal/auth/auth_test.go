package auth

import (
	"fmt"
	"testing"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "wrong"); err == nil {
		t.Fatal("expected wrong password to fail")
	}
}

func TestRefreshTokenRotateReuseRevoke(t *testing.T) {
	database, svc, user := newAuthTestService(t)

	token, err := svc.NewRefreshToken(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	rotatedUser, nextToken, err := svc.RotateRefreshToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if rotatedUser.ID != user.ID {
		t.Fatalf("rotated user mismatch: got %s want %s", rotatedUser.ID, user.ID)
	}
	if nextToken == token || nextToken == "" {
		t.Fatalf("expected new refresh token, got %q", nextToken)
	}

	if _, _, err := svc.RotateRefreshToken(token); err == nil {
		t.Fatal("expected rotated token reuse to fail")
	}

	var tokens []db.RefreshToken
	if err := database.Order("created_at asc").Find(&tokens).Error; err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2 refresh tokens, got %d", len(tokens))
	}
	if tokens[0].RevokedAt == nil || tokens[1].RevokedAt == nil {
		t.Fatalf("expected family revoked after token reuse: %+v", tokens)
	}

	freshToken, err := svc.NewRefreshToken(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeRefreshToken(freshToken); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.RotateRefreshToken(freshToken); err == nil {
		t.Fatal("expected revoked refresh token to fail")
	}
}

func TestRevokeUserRefreshTokens(t *testing.T) {
	database, svc, user := newAuthTestService(t)

	first, err := svc.NewRefreshToken(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.NewRefreshToken(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.RevokeUserRefreshTokens(user.ID); err != nil {
		t.Fatal(err)
	}

	var active int64
	if err := database.Model(&db.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", user.ID).Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("expected all user refresh tokens revoked, active=%d", active)
	}

	if _, _, err := svc.RotateRefreshToken(first); err == nil {
		t.Fatal("expected revoked first token to fail")
	}
	if _, _, err := svc.RotateRefreshToken(second); err == nil {
		t.Fatal("expected revoked second token to fail")
	}
}

func newAuthTestService(t *testing.T) (*gorm.DB, *Service, db.User) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	user := db.User{
		Email:               fmt.Sprintf("%s@test.local", t.Name()),
		Name:                "Auth User",
		PasswordHash:        hash,
		IsActive:            true,
		MaxDomains:          5,
		MaxInboxes:          5,
		MaxMessageSizeMB:    25,
		MaxAttachmentSizeMB: 25,
		MaxStorageBytes:     1 << 30,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewService(database, config.Config{
		JWTSecret:       "test-secret-123456",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
	})
	return database, svc, user
}
