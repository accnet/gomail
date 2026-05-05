package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gomail/internal/config"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func Open(databaseURL string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// Connection pool defaults
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	sqlDB.SetConnMaxIdleTime(1 * time.Minute)
	return db, nil
}

func AutoMigrate(database *gorm.DB) error {
	return database.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(8217600042)").Error; err != nil {
				return err
			}
		}
		return autoMigrateLocked(tx)
	})
}

func autoMigrateLocked(database *gorm.DB) error {
	if err := database.AutoMigrate(
		&User{},
		&RefreshToken{},
		&Domain{},
		&DomainEmailAuth{},
		&Inbox{},
		&Email{},
		&Attachment{},
		&DomainEvent{},
		&AuditLog{},
		&StaticProject{},
		&ApiKey{},
		&ApiKeyUsageLog{},
		&SentEmailLog{},
	); err != nil {
		return err
	}
	// Create partial unique indexes for soft-delete support.
	// These ensure uniqueness only for non-deleted rows, allowing
	// soft-deleted records with the same email/name/address to coexist.
	partialIndexes := []struct {
		table   string
		name    string
		columns string
		where   string
	}{
		{"users", "idx_users_email_active", "email", "deleted_at IS NULL"},
		{"domains", "idx_domains_name_active", "name", "deleted_at IS NULL"},
		{"inboxes", "idx_inboxes_address_active", "address", "deleted_at IS NULL"},
		{"inboxes", "idx_domain_local_active", "domain_id, local_part", "deleted_at IS NULL"},
	}
	for _, idx := range partialIndexes {
		stmt := fmt.Sprintf(
			"CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s (%s) WHERE %s",
			idx.name, idx.table, idx.columns, idx.where,
		)
		if err := database.Exec(stmt).Error; err != nil {
			return err
		}
	}
	if err := ensureThreadingSchema(database); err != nil {
		return err
	}
	return BackfillEmailThreading(database, 500)
}

func ensureThreadingSchema(database *gorm.DB) error {
	dialect := database.Dialector.Name()
	uuidType := "TEXT"
	jsonType := "JSON"
	if dialect == "postgres" {
		uuidType = "UUID"
		jsonType = "JSONB"
	}
	columns := []struct {
		table string
		name  string
		typ   string
	}{
		{"emails", "conversation_id", "VARCHAR"},
		{"emails", "root_email_id", uuidType},
		{"emails", "parent_email_id", uuidType},
		{"emails", "in_reply_to_message_id", "VARCHAR"},
		{"emails", "references_message_ids", jsonType},
		{"sent_email_logs", "original_email_id", uuidType},
		{"sent_email_logs", "parent_email_id", uuidType},
		{"sent_email_logs", "conversation_id", "VARCHAR"},
		{"sent_email_logs", "mode", "VARCHAR"},
		{"sent_email_logs", "in_reply_to_message_id", "VARCHAR"},
		{"sent_email_logs", "references_message_ids", jsonType},
	}
	for _, col := range columns {
		if dialect == "postgres" {
			if err := database.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s", col.table, col.name, col.typ)).Error; err != nil {
				return err
			}
			continue
		}
		if database.Migrator().HasColumn(col.table, col.name) {
			continue
		}
		if err := database.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", col.table, col.name, col.typ)).Error; err != nil {
			return err
		}
	}
	indexes := []struct {
		table   string
		name    string
		columns string
	}{
		{"emails", "idx_emails_conversation_id", "conversation_id"},
		{"emails", "idx_emails_root_email_id", "root_email_id"},
		{"emails", "idx_emails_parent_email_id", "parent_email_id"},
		{"emails", "idx_emails_in_reply_to_message_id", "in_reply_to_message_id"},
		{"emails", "idx_emails_inbox_message_id", "inbox_id, message_id"},
		{"sent_email_logs", "idx_sent_email_logs_original_email_id", "original_email_id"},
		{"sent_email_logs", "idx_sent_email_logs_parent_email_id", "parent_email_id"},
		{"sent_email_logs", "idx_sent_email_logs_conversation_id", "conversation_id"},
		{"sent_email_logs", "idx_sent_email_logs_mode", "mode"},
		{"sent_email_logs", "idx_sent_email_logs_in_reply_to_message_id", "in_reply_to_message_id"},
	}
	for _, idx := range indexes {
		stmt := fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", idx.name, idx.table, idx.columns)
		if err := database.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

func BackfillEmailThreading(database *gorm.DB, batchSize int) error {
	if batchSize <= 0 {
		batchSize = 500
	}
	var rows []Email
	if err := database.Where("conversation_id = '' OR conversation_id IS NULL").Order("received_at ASC").Limit(batchSize).Find(&rows).Error; err != nil {
		return err
	}
	for len(rows) > 0 {
		for _, row := range rows {
			inReplyTo, refs := threadingFromHeaders(row.HeadersJSON)
			messageID := normalizeThreadMessageID(row.MessageID)
			conversationID := inferBackfillConversationID(messageID, inReplyTo, refs, row.Subject)
			updates := map[string]any{
				"message_id":             messageID,
				"conversation_id":        conversationID,
				"in_reply_to_message_id": inReplyTo,
				"references_message_ids": datatypes.JSON(mustMarshalJSON(refs)),
			}
			if inReplyTo != "" {
				var parent Email
				err := database.Joins("JOIN inboxes child_inbox ON child_inbox.id = ? JOIN inboxes parent_inbox ON parent_inbox.id = emails.inbox_id", row.InboxID).
					Where("parent_inbox.user_id = child_inbox.user_id AND emails.message_id IN ?", []string{inReplyTo, "<" + inReplyTo + ">"}).
					Order("emails.received_at DESC").
					First(&parent).Error
				if err == nil {
					updates["parent_email_id"] = parent.ID
					if parent.RootEmailID != nil {
						updates["root_email_id"] = *parent.RootEmailID
					} else {
						updates["root_email_id"] = parent.ID
					}
					if parent.ConversationID != "" && len(refs) == 0 {
						updates["conversation_id"] = parent.ConversationID
					}
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if err := database.Model(&Email{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		rows = rows[:0]
		if err := database.Where("conversation_id = '' OR conversation_id IS NULL").Order("received_at ASC").Limit(batchSize).Find(&rows).Error; err != nil {
			return err
		}
	}
	return nil
}

func threadingFromHeaders(raw datatypes.JSON) (string, []string) {
	headers := map[string][]string{}
	_ = json.Unmarshal(raw, &headers)
	get := func(name string) string {
		for key, values := range headers {
			if strings.EqualFold(key, name) && len(values) > 0 {
				return values[0]
			}
		}
		return ""
	}
	return firstThreadMessageID(get("In-Reply-To")), parseThreadMessageIDList(get("References"))
}

func inferBackfillConversationID(messageID, inReplyTo string, refs []string, subject string) string {
	if len(refs) > 0 {
		return refs[0]
	}
	if inReplyTo != "" {
		return inReplyTo
	}
	if messageID != "" {
		return messageID
	}
	subject = strings.ToLower(strings.TrimSpace(subject))
	if subject == "" {
		return "missing-message-id"
	}
	return "subject:" + subject
}

func firstThreadMessageID(value string) string {
	ids := parseThreadMessageIDList(value)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func parseThreadMessageIDList(value string) []string {
	fields := strings.Fields(value)
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		id := normalizeThreadMessageID(field)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		if id := normalizeThreadMessageID(value); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func normalizeThreadMessageID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "<>")
	return strings.ToLower(strings.TrimSpace(value))
}

func mustMarshalJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func SeedSuperAdmin(ctx context.Context, database *gorm.DB, cfg config.Config) error {
	var existing User
	err := database.WithContext(ctx).Where("email = ?", cfg.DefaultAdminEmail).First(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.DefaultAdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := User{
		Email:               cfg.DefaultAdminEmail,
		Name:                cfg.DefaultAdminName,
		PasswordHash:        string(hash),
		IsAdmin:             true,
		IsSuperAdmin:        true,
		IsActive:            true,
		MaxDomains:          cfg.DefaultAdminMaxDomains,
		MaxInboxes:          cfg.DefaultAdminMaxInboxes,
		MaxMessageSizeMB:    cfg.DefaultAdminMaxMessageSizeMB,
		MaxAttachmentSizeMB: cfg.DefaultAdminMaxAttachmentSizeMB,
		MaxStorageBytes:     int64(cfg.DefaultAdminMaxStorageGB) * 1024 * 1024 * 1024,
		MaxWebsites:         cfg.DefaultAdminMaxWebsites,
	}
	return database.WithContext(ctx).Create(&user).Error
}

func SeedDemoData(ctx context.Context, database *gorm.DB, cfg config.Config) error {
	var user User
	if err := database.WithContext(ctx).Where("email = ?", cfg.DefaultAdminEmail).First(&user).Error; err != nil {
		return err
	}

	now := time.Now()
	domains := []Domain{
		{
			UserID:             user.ID,
			Name:               "site1.localhost",
			Status:             "verified",
			VerificationMethod: "mx",
			MXTarget:           cfg.MXTarget,
			LastVerifiedAt:     &now,
		},
		{
			UserID:             user.ID,
			Name:               "site2.localhost",
			Status:             "verified",
			VerificationMethod: "mx",
			MXTarget:           cfg.MXTarget,
			LastVerifiedAt:     &now,
		},
		{
			UserID:             user.ID,
			Name:               "pending.localhost",
			Status:             "pending",
			VerificationMethod: "mx",
			MXTarget:           cfg.MXTarget,
		},
	}
	for i := range domains {
		if err := database.WithContext(ctx).Where("name = ?", domains[i].Name).FirstOrCreate(&domains[i]).Error; err != nil {
			return err
		}
	}

	inboxes := []Inbox{
		{UserID: user.ID, DomainID: domains[0].ID, LocalPart: "hello", Address: "hello@site1.localhost", IsActive: true},
		{UserID: user.ID, DomainID: domains[1].ID, LocalPart: "support", Address: "support@site2.localhost", IsActive: true},
	}
	for i := range inboxes {
		var existingInbox Inbox
		err := database.WithContext(ctx).Unscoped().Where("address = ?", inboxes[i].Address).First(&existingInbox).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := database.WithContext(ctx).Create(&inboxes[i]).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}

		updates := map[string]any{
			"user_id":    inboxes[i].UserID,
			"domain_id":  inboxes[i].DomainID,
			"local_part": inboxes[i].LocalPart,
			"address":    inboxes[i].Address,
			"is_active":  inboxes[i].IsActive,
			"deleted_at": nil,
		}
		if err := database.WithContext(ctx).Unscoped().Model(&existingInbox).Updates(updates).Error; err != nil {
			return err
		}
		inboxes[i].ID = existingInbox.ID
	}

	var existing int64
	if err := database.WithContext(ctx).Model(&Email{}).Where("message_id LIKE ?", "<demo-%").Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}

	emails := []Email{
		demoEmail(inboxes[0].ID, "<demo-welcome@gomail.local>", "team@gomail.local", "hello@site1.localhost", "Welcome to GoMail", "Your verified domain is ready to receive inbound email.", "Your verified domain is ready. You can create more addresses from the Email tab.", now.Add(-35*time.Minute), false),
		demoEmail(inboxes[1].ID, "<demo-support@gomail.local>", "customer@example.net", "support@site2.localhost", "Question about inbound routing", "Can you confirm this mailbox receives support tickets?", "Can you confirm this mailbox receives support tickets from external senders?", now.Add(-5*time.Hour), false),
		demoEmail(inboxes[1].ID, "<demo-report@gomail.local>", "reports@example.net", "support@site2.localhost", "Daily delivery summary", "All test messages were accepted successfully.", "All test messages were accepted successfully. No blocked attachments were found.", now.Add(-26*time.Hour), true),
	}
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range emails {
			if err := tx.Create(&emails[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})

}

func demoEmail(inboxID uuid.UUID, messageID, from, to, subject, snippetText, body string, receivedAt time.Time, read bool) Email {
	html := "<p>" + body + "</p>"
	return Email{

		InboxID:     inboxID,
		MessageID:   messageID,
		FromAddress: from,
		ToAddress:   to,
		Subject:     subject,
		ReceivedAt:  receivedAt,
		Snippet:     snippetText,

		TextBody:          body,
		HTMLBody:          html,
		HTMLBodySanitized: html,
		HeadersJSON:       datatypes.JSON([]byte(`{"From":["` + from + `"],"To":["` + to + `"]}`)),
		AuthResultsJSON:   datatypes.JSON([]byte(`{"spf":["pass"],"dkim":["pass"],"dmarc":["pass"]}`)),
		IsRead:            read,
	}
}
