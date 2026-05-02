package db

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Status constants for domain/email/attachment lifecycle.
const (
	DomainStatusPending  = "pending"
	DomainStatusVerified = "verified"
	DomainStatusFailed   = "failed"

	DomainAuthStatusPending  = "pending"
	DomainAuthStatusVerified = "verified"
	DomainAuthStatusFailed   = "failed"

	ARecordStatusPending  = "pending"
	ARecordStatusVerified = "verified"
	ARecordStatusFailed   = "failed"

	EmailStatusRead   = "read"
	EmailStatusUnread = "unread"

	AttachmentScanStatusPending  = "pending"
	AttachmentScanStatusScanning = "scanning"
	AttachmentScanStatusClean    = "clean"
	AttachmentScanStatusInfected = "infected"
	AttachmentScanStatusSkipped  = "skipped"

	StaticProjectStatusDraft         = "draft"
	StaticProjectStatusDeploying     = "deploying"
	StaticProjectStatusPublished     = "published"
	StaticProjectStatusPublishFailed = "publish_failed"

	ThumbnailStatusPending    = "pending"
	ThumbnailStatusReady      = "ready"
	ThumbnailStatusFailed     = "failed"
	ThumbnailStatusProcessing = "processing"

	DomainBindingStatusAssigned  = "assigned"
	DomainBindingStatusSSLActive = "ssl_active"

	// API Key scopes
	ApiKeyScopeSendEmail     = "send_email"
	ApiKeyScopeReadInbox     = "read_inbox"
	ApiKeyScopeManageDomains = "manage_domains"
	ApiKeyScopeManageInboxes = "manage_inboxes"
	ApiKeyScopeReadSent      = "read_sent"
	ApiKeyScopeFullAccess    = "full_access"

	// Sent email statuses
	SentEmailStatusSent   = "sent"
	SentEmailStatusFailed = "failed"
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
	MaxWebsites         int            `gorm:"not null;default:5" json:"max_websites"`
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
	ARecordCheckAt     *time.Time     `json:"a_record_check_at"`
	ARecordStatus      string         `gorm:"not null;default:pending" json:"a_record_status"`
	ARecordResult      string         `json:"a_record_result"`
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

type DomainEmailAuth struct {
	ID                  uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	DomainID            uuid.UUID      `gorm:"type:uuid;uniqueIndex;not null" json:"domain_id"`
	SPFStatus           string         `gorm:"index;not null;default:pending" json:"spf_status"`
	SPFRecord           string         `json:"spf_record"`
	SPFLastCheckedAt    *time.Time     `json:"spf_last_checked_at"`
	SPFError            string         `json:"spf_error"`
	DKIMSelector        string         `gorm:"not null" json:"dkim_selector"`
	DKIMPublicKey       string         `gorm:"type:text" json:"dkim_public_key"`
	DKIMPrivateKeyPEM   string         `gorm:"type:text" json:"-"`
	DKIMStatus          string         `gorm:"index;not null;default:pending" json:"dkim_status"`
	DKIMRecordName      string         `json:"dkim_record_name"`
	DKIMRecordValue     string         `gorm:"type:text" json:"dkim_record_value"`
	DKIMLastCheckedAt   *time.Time     `json:"dkim_last_checked_at"`
	DKIMError           string         `json:"dkim_error"`
	DKIMLastGeneratedAt *time.Time     `json:"dkim_last_generated_at"`
	DKIMLastVerifiedAt  *time.Time     `json:"dkim_last_verified_at"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

func (a *DomainEmailAuth) BeforeCreate(tx *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
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

type StaticProject struct {
	ID                   uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID               uuid.UUID      `gorm:"type:uuid;index;not null" json:"user_id"`
	Name                 string         `gorm:"not null" json:"name"`
	Subdomain            string         `gorm:"uniqueIndex;not null" json:"subdomain"`
	DomainID             *uuid.UUID     `gorm:"type:uuid;uniqueIndex:idx_static_project_domain;index" json:"domain_id"`
	AssignedDomain       string         `json:"assigned_domain"`
	DomainBindingStatus  string         `gorm:"not null;default:''" json:"domain_binding_status"`
	DomainLastDNSCheckAt *time.Time     `json:"domain_last_dns_check_at"`
	DomainLastDNSResult  string         `json:"domain_last_dns_result"`
	DomainTLSEnabledAt   *time.Time     `json:"domain_tls_enabled_at"`
	RootFolder           string         `gorm:"not null" json:"root_folder"`
	StagingFolder        string         `gorm:"not null" json:"-"`
	UploadFilename       string         `json:"upload_filename"`
	DetectedRoot         string         `json:"detected_root"`
	ArchiveSizeBytes     int64          `json:"archive_size_bytes"`
	FileCount            int            `json:"file_count"`
	Status               string         `gorm:"index;not null;default:draft" json:"status"`
	DeployError          string         `json:"deploy_error"`
	ThumbnailPath        string         `json:"thumbnail_path"`
	ThumbnailStatus      string         `gorm:"not null;default:pending" json:"thumbnail_status"`
	IsActive             bool           `gorm:"not null;default:true" json:"is_active"`
	PublishedAt          *time.Time     `json:"published_at"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	DeletedAt            gorm.DeletedAt `gorm:"index" json:"-"`
}

func (p *StaticProject) BeforeCreate(tx *gorm.DB) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return nil
}

// ApiKey represents an API key for external apps to authenticate with the SMTP relay.
type ApiKey struct {
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID         uuid.UUID      `gorm:"type:uuid;index;not null" json:"user_id"`
	Name           string         `gorm:"not null" json:"name"`
	KeyPrefix      string         `gorm:"not null;index" json:"key_prefix"`
	KeyHash        string         `gorm:"not null;uniqueIndex" json:"-"`
	Scopes         string         `gorm:"not null;default:'send_email'" json:"scopes"`
	AllowedIPs     string         `json:"allowed_ips"`
	RateLimitRPM   int            `gorm:"not null;default:60" json:"rate_limit_rpm"`
	MaxDailyEmails int            `gorm:"not null;default:500" json:"max_daily_emails"`
	DailySentCount int            `gorm:"not null;default:0" json:"daily_sent_count"`
	LastUsedAt     *time.Time     `json:"last_used_at"`
	ExpiresAt      *time.Time     `json:"expires_at"`
	IsActive       bool           `gorm:"not null;default:true" json:"is_active"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (a *ApiKey) BeforeCreate(tx *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

// ApiKeyUsageLog tracks API key usage for audit and rate limiting.
type ApiKeyUsageLog struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	ApiKeyID   uuid.UUID `gorm:"type:uuid;index;not null" json:"api_key_id"`
	UserID     uuid.UUID `gorm:"type:uuid;index;not null" json:"user_id"`
	Endpoint   string    `json:"endpoint"`
	Method     string    `json:"method"`
	StatusCode int       `json:"status_code"`
	IPAddress  string    `json:"ip_address"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
}

func (l *ApiKeyUsageLog) BeforeCreate(tx *gorm.DB) error {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	return nil
}

// SentEmailLog records emails sent through the SMTP relay.
type SentEmailLog struct {
	ID           uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID       uuid.UUID  `gorm:"type:uuid;index;not null" json:"user_id"`
	ApiKeyID     *uuid.UUID `gorm:"type:uuid;index" json:"api_key_id,omitempty"`
	Channel      string     `gorm:"not null;default:'smtp_auth'" json:"channel"`
	FromAddress  string     `json:"from_address"`
	ToAddress    string     `json:"to_address"`
	CcAddress    string     `json:"cc_address"`
	BccAddress   string     `json:"bcc_address"`
	Subject      string     `json:"subject"`
	BodyText     string     `json:"body_text,omitempty"`
	BodyHTML     string     `json:"body_html,omitempty"`
	Status       string     `gorm:"index;not null" json:"status"`
	ErrorMessage string     `json:"error_message"`
	MessageID    string     `json:"message_id"`
	SentAt       *time.Time `json:"sent_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (s *SentEmailLog) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return nil
}
