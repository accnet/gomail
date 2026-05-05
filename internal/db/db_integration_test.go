package db

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
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

func TestBackfillEmailThreadingFromHeaders(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(database); err != nil {
		t.Fatal(err)
	}
	user := User{Email: "backfill@test.local", PasswordHash: "x", IsActive: true}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	domain := Domain{UserID: user.ID, Name: "backfill.test", Status: DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	inbox := Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@backfill.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	parent := Email{
		ID:          uuid.New(),
		InboxID:     inbox.ID,
		MessageID:   "<parent@example.net>",
		FromAddress: "sender@example.net",
		ToAddress:   inbox.Address,
		Subject:     "Thread",
		ReceivedAt:  time.Now().Add(-time.Minute),
		HeadersJSON: datatypes.JSON([]byte(`{"Message-ID":["<parent@example.net>"]}`)),
	}
	child := Email{
		ID:          uuid.New(),
		InboxID:     inbox.ID,
		MessageID:   "<child@example.net>",
		FromAddress: "sender@example.net",
		ToAddress:   inbox.Address,
		Subject:     "Re: Thread",
		ReceivedAt:  time.Now(),
		HeadersJSON: datatypes.JSON([]byte(`{"In-Reply-To":["<parent@example.net>"],"References":["<parent@example.net>"]}`)),
	}
	if err := database.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&child).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&Email{}).Where("id IN ?", []uuid.UUID{parent.ID, child.ID}).Updates(map[string]any{
		"conversation_id":        "",
		"in_reply_to_message_id": "",
		"parent_email_id":        nil,
		"root_email_id":          nil,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := BackfillEmailThreading(database, 1); err != nil {
		t.Fatal(err)
	}
	var gotParent, gotChild Email
	if err := database.First(&gotParent, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.First(&gotChild, "id = ?", child.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotParent.MessageID != "parent@example.net" || gotParent.ConversationID != "parent@example.net" {
		t.Fatalf("unexpected parent threading: %+v", gotParent)
	}
	if gotChild.InReplyToMessageID != "parent@example.net" {
		t.Fatalf("child in-reply-to = %q", gotChild.InReplyToMessageID)
	}
	if gotChild.ParentEmailID == nil || *gotChild.ParentEmailID != gotParent.ID {
		t.Fatalf("child parent id = %v want %s", gotChild.ParentEmailID, gotParent.ID)
	}
	if gotChild.RootEmailID == nil || *gotChild.RootEmailID != gotParent.ID {
		t.Fatalf("child root id = %v want %s", gotChild.RootEmailID, gotParent.ID)
	}
	if gotChild.ConversationID != gotParent.ConversationID {
		t.Fatalf("child conversation = %q want %q", gotChild.ConversationID, gotParent.ConversationID)
	}
}
