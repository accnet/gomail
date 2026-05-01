package db

import (
	"context"
	"errors"
	"fmt"
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
	return nil
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
		if err := database.WithContext(ctx).Where("address = ?", inboxes[i].Address).FirstOrCreate(&inboxes[i]).Error; err != nil {
			return err
		}
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
		demoEmail(inboxes[0].ID, "<demo-dns@gomail.local>", "dns-check@gomail.local", "hello@site1.localhost", "DNS verification complete", "MX record is pointing to your GoMail server.", "MX verification succeeded for site1.localhost.", now.Add(-2*time.Hour), true),
		demoEmail(inboxes[1].ID, "<demo-support@gomail.local>", "customer@example.net", "support@site2.localhost", "Question about inbound routing", "Can you confirm this mailbox receives support tickets?", "Can you confirm this mailbox receives support tickets from external senders?", now.Add(-5*time.Hour), false),
		demoEmail(inboxes[1].ID, "<demo-report@gomail.local>", "reports@example.net", "support@site2.localhost", "Daily delivery summary", "All test messages were accepted successfully.", "All test messages were accepted successfully. No blocked attachments were found.", now.Add(-26*time.Hour), true),
	}
	var used int64
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range emails {
			used += emails[i].RawSizeBytes
			if err := tx.Create(&emails[i]).Error; err != nil {
				return err
			}
		}
		return tx.Model(&User{}).Where("id = ?", user.ID).Update("storage_used_bytes", gorm.Expr("storage_used_bytes + ?", used)).Error
	})
}

func demoEmail(inboxID uuid.UUID, messageID, from, to, subject, snippetText, body string, receivedAt time.Time, read bool) Email {
	html := "<p>" + body + "</p>"
	size := int64(len(body) + len(subject) + len(from) + len(to) + 160)
	return Email{
		InboxID:           inboxID,
		MessageID:         messageID,
		FromAddress:       from,
		ToAddress:         to,
		Subject:           subject,
		ReceivedAt:        receivedAt,
		RawSizeBytes:      size,
		Snippet:           snippetText,
		TextBody:          body,
		HTMLBody:          html,
		HTMLBodySanitized: html,
		HeadersJSON:       datatypes.JSON([]byte(`{"From":["` + from + `"],"To":["` + to + `"]}`)),
		AuthResultsJSON:   datatypes.JSON([]byte(`{"spf":["pass"],"dkim":["pass"],"dmarc":["pass"]}`)),
		IsRead:            read,
	}
}
