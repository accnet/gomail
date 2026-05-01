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

	for _, model := range []any{&User{}, &RefreshToken{}, &Domain{}, &Inbox{}, &Email{}, &Attachment{}, &DomainEvent{}, &AuditLog{}, &StaticProject{}, &ApiKey{}, &ApiKeyUsageLog{}, &SentEmailLog{}} {
		if !database.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T", model)
		}
	}
	if !database.Migrator().HasColumn(&User{}, "max_websites") {
		t.Fatal("expected users.max_websites column")
	}
	if !database.Migrator().HasIndex(&StaticProject{}, "idx_static_projects_user_id") {
		t.Fatal("expected static_projects user_id index")
	}
	if !database.Migrator().HasIndex(&StaticProject{}, "idx_static_projects_status") {
		t.Fatal("expected static_projects status index")
	}
	if !database.Migrator().HasIndex(&StaticProject{}, "idx_static_projects_deleted_at") {
		t.Fatal("expected static_projects deleted_at index")
	}
	if !database.Migrator().HasIndex(&Inbox{}, "idx_domain_local_active") {
		t.Fatal("expected partial unique index idx_domain_local_active")
	}
	if !database.Migrator().HasIndex(&Domain{}, "idx_domains_name_active") {
		t.Fatal("expected partial unique index idx_domains_name_active")
	}
	if !database.Migrator().HasIndex(&ApiKey{}, "idx_api_keys_user_id") {
		t.Fatal("expected api_keys user_id index")
	}
	if !database.Migrator().HasIndex(&ApiKey{}, "idx_api_keys_key_hash") || !database.Migrator().HasIndex(&ApiKey{}, "idx_api_keys_key_prefix") {
		t.Fatal("expected api_keys key_hash or key_prefix index")
	}
	if !database.Migrator().HasIndex(&SentEmailLog{}, "idx_sent_email_logs_user_id") {
		t.Fatal("expected sent_email_logs user_id index")
	}
	if !database.Migrator().HasIndex(&SentEmailLog{}, "idx_sent_email_logs_status") {
		t.Fatal("expected sent_email_logs status index")
	}
}
