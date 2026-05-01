package server

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"gomail/internal/auth"
	"gomail/internal/config"
	"gomail/internal/db"
	mailservice "gomail/internal/mail/service"
	"gomail/internal/storage"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testSMTPDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestSessionPersistsMailAndRejectsInactiveUser(t *testing.T) {
	database := testSMTPDB(t)
	cfg := config.Config{
		AppEnv:                "test",
		MXTarget:              "mx.test.local",
		SMTPHostname:          "mx.test.local",
		MaxMessageSizeMB:      25,
		RawEmailStorageRoot:   t.TempDir() + "/raw",
		AttachmentStorageRoot: t.TempDir() + "/att",
	}
	store := storage.NewLocal(cfg.AttachmentStorageRoot, cfg.RawEmailStorageRoot)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	user := db.User{
		Email:               "user@test.local",
		Name:                "User",
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
	domain := db.Domain{UserID: user.ID, Name: "example.test", Status: "verified", MXTarget: cfg.MXTarget, VerificationMethod: "mx", LastVerifiedAt: ptrTime(time.Now())}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@example.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}

	backend := Backend{
		DB:       database,
		Config:   cfg,
		Pipeline: mailservice.Pipeline{DB: database, Config: cfg, Store: store},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	session := &Session{backend: backend}
	if err := session.Rcpt("hello@example.test", nil); err != nil {
		t.Fatalf("rcpt failed: %v", err)
	}
	raw := strings.NewReader("From: sender@test.local\r\nTo: hello@example.test\r\nSubject: Test\r\nMessage-ID: <smtp-test@test.local>\r\n\r\nhello from smtp")
	if err := session.Data(raw); err != nil {
		t.Fatalf("data failed: %v", err)
	}

	var count int64
	if err := database.Model(&db.Email{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 email, got %d", count)
	}

	if err := database.Model(&user).Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	session = &Session{backend: backend}
	if err := session.Rcpt("hello@example.test", nil); err == nil {
		t.Fatal("expected inactive user rcpt rejection")
	}

	if err := os.RemoveAll(cfg.AttachmentStorageRoot); err != nil {
		t.Fatal(err)
	}
}

func ptrTime(v time.Time) *time.Time {
	return &v
}
