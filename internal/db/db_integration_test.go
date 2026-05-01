package db

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAutoMigrateCreatesSchema(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(database); err != nil {
		t.Fatal(err)
	}

	for _, model := range []any{&User{}, &RefreshToken{}, &Domain{}, &Inbox{}, &Email{}, &Attachment{}, &DomainEvent{}, &AuditLog{}} {
		if !database.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T", model)
		}
	}
	if !database.Migrator().HasIndex(&Inbox{}, "idx_domain_local_active") {
		t.Fatal("expected partial unique index idx_domain_local_active")
	}
	if !database.Migrator().HasIndex(&Domain{}, "idx_domains_name_active") {
		t.Fatal("expected partial unique index idx_domains_name_active")
	}
}
