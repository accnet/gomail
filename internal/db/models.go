package db

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type User struct {
	ID                  uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	Email               string         `gorm:"uniqueIndex;not null" json:"email"`
	Name                string         `json:"name"`
	PasswordHash        string         `gorm:"not null" json:"-"`
	IsAdmin             bool           `gorm:"not null;default:false" json:"is_admin"`
	IsSuperAdmin        bool           `gorm:"not null;default:false" json:"is_super_admin"`
	IsActive            bool           `gorm:"not null;default:false" json:"is_active"`
	MaxDomains          int            `gorm:"not null;default:5" json:"max_domains"`
	MaxInboxes          int            `gorm:"not null;default:50" json:"max_inboxes"`
	MaxAttachmentSizeMB int            `gorm:"not null;default:25" json:"max_attachment_size_mb"`
	MaxMessageSizeMB    int            `gorm:"not null;default:25" json:"max_message_size_mb"`
	MaxStorageBytes     int64          `gorm:"not null;default:10737418240" json:"max_storage_bytes"`
	StorageUsedBytes    int64          `gorm:"not null;default:0" json:"storage_used_bytes"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}

type RefreshToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `gorm:"type:uuid;index;not null"`
	TokenHash string    `gorm:"uniqueIndex;not null"`
	FamilyID  uuid.UUID `gorm:"type:uuid;index;not null"`
	ExpiresAt time.Time `gorm:"index;not null"`
	RevokedAt *time.Time
	CreatedAt time.Time
}

func (t *RefreshToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.FamilyID == uuid.Nil {
		t.FamilyID = uuid.New()
	}
	return nil
}

type Domain struct {
	ID                 uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID             uuid.UUID      `gorm:"type:uuid;index;not null" json:"user_id"`
	Name               string         `gorm:"uniqueIndex;not null" json:"name"`
	Status             string         `gorm:"index;not null;default:pending" json:"status"`
	WarningStatus      string         `json:"warning_status"`
	VerificationMethod string         `json:"verification_method"`
	MXTarget           string         `json:"mx_target"`
	LastVerifiedAt     *time.Time     `json:"last_verified_at"`
	VerificationError  string         `json:"verification_error"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `gorm:"index" json:"-"`
}

func (d *Domain) BeforeCreate(tx *gorm.DB) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	return nil
}

type Inbox struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID    uuid.UUID      `gorm:"type:uuid;index;not null" json:"user_id"`
	DomainID  uuid.UUID      `gorm:"type:uuid;uniqueIndex:idx_domain_local;index;not null" json:"domain_id"`
	LocalPart string         `gorm:"uniqueIndex:idx_domain_local;not null" json:"local_part"`
	Address   string         `gorm:"uniqueIndex;not null" json:"address"`
	IsActive  bool           `gorm:"not null;default:true" json:"is_active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (i *Inbox) BeforeCreate(tx *gorm.DB) error {
	if i.ID == uuid.Nil {
		i.ID = uuid.New()
	}
	return nil
}

type Email struct {
	ID                uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	InboxID           uuid.UUID      `gorm:"type:uuid;index;not null" json:"inbox_id"`
	MessageID         string         `gorm:"index" json:"message_id"`
	FromAddress       string         `json:"from_address"`
	ToAddress         string         `json:"to_address"`
	Subject           string         `json:"subject"`
	ReceivedAt        time.Time      `gorm:"index" json:"received_at"`
	RawSizeBytes      int64          `json:"raw_size_bytes"`
	RawStoragePath    string         `json:"-"`
	Snippet           string         `json:"snippet"`
	TextBody          string         `json:"text_body,omitempty"`
	HTMLBody          string         `json:"-"`
	HTMLBodySanitized string         `json:"html_body_sanitized,omitempty"`
	HeadersJSON       datatypes.JSON `json:"headers_json"`
	AuthResultsJSON   datatypes.JSON `json:"auth_results_json"`
	IsRead            bool           `gorm:"not null;default:false" json:"is_read"`
	CreatedAt         time.Time      `json:"created_at"`
	DeletedAt         gorm.DeletedAt `gorm:"index" json:"-"`
	Attachments       []Attachment   `json:"attachments,omitempty"`
}

func (e *Email) BeforeCreate(tx *gorm.DB) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	return nil
}

type Attachment struct {
	ID                    uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	EmailID               uuid.UUID  `gorm:"type:uuid;index;not null" json:"email_id"`
	Filename              string     `json:"filename"`
	ContentType           string     `json:"content_type"`
	SizeBytes             int64      `json:"size_bytes"`
	StoragePath           string     `json:"-"`
	SHA256                string     `json:"sha256"`
	ScanStatus            string     `gorm:"index;not null;default:pending" json:"scan_status"`
	ScanResult            string     `json:"scan_result"`
	IsBlocked             bool       `gorm:"not null;default:false" json:"is_blocked"`
	AdminOverrideDownload bool       `gorm:"not null;default:false" json:"admin_override_download"`
	AdminOverrideBy       *uuid.UUID `gorm:"type:uuid" json:"admin_override_by"`
	AdminOverrideAt       *time.Time `json:"admin_override_at"`
	ContentID             string     `json:"content_id"`
	Disposition           string     `json:"disposition"`
	CreatedAt             time.Time  `json:"created_at"`
}

func (a *Attachment) BeforeCreate(tx *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

type DomainEvent struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	DomainID  uuid.UUID      `gorm:"type:uuid;index;not null" json:"domain_id"`
	Type      string         `gorm:"index;not null" json:"type"`
	Payload   datatypes.JSON `json:"payload_json"`
	CreatedAt time.Time      `json:"created_at"`
}

func (e *DomainEvent) BeforeCreate(tx *gorm.DB) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	return nil
}

type AuditLog struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	ActorID   *uuid.UUID     `gorm:"type:uuid;index" json:"actor_id"`
	Type      string         `gorm:"index;not null" json:"type"`
	Payload   datatypes.JSON `json:"payload_json"`
	CreatedAt time.Time      `json:"created_at"`
}

func (l *AuditLog) BeforeCreate(tx *gorm.DB) error {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	return nil
}
