package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"strings"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/smtp/relay"

	smtp "github.com/emersion/go-smtp"
	"gorm.io/gorm"
)

var (
	errInvalidCredentials = &smtp.SMTPError{Code: 535, Message: "invalid credentials"}
	errKeyInactive        = &smtp.SMTPError{Code: 535, Message: "api key inactive"}
	errKeyExpired         = &smtp.SMTPError{Code: 535, Message: "api key expired"}
	errScopeInsufficient  = &smtp.SMTPError{Code: 535, Message: "api key scope insufficient"}
	errDomainNotVerified  = &smtp.SMTPError{Code: 550, Message: "domain not verified"}
	errMaxSize            = &smtp.SMTPError{Code: 552, Message: "message exceeds max size"}
	errQuotaExceeded      = &smtp.SMTPError{Code: 452, Message: "daily quota exceeded"}
)

type RelayBackend struct {
	DB          *gorm.DB
	Config      config.Config
	Logger      *slog.Logger
	Sender      *relay.Sender
	DailyCounts map[string]int // api_key_id -> count today (in-memory)
}

func NewRelayBackend(database *gorm.DB, cfg config.Config, logger *slog.Logger, sender *relay.Sender) *RelayBackend {
	counts := sender.DailyCounts
	if counts == nil {
		counts = make(map[string]int)
		sender.DailyCounts = counts
	}
	return &RelayBackend{
		DB:          database,
		Config:      cfg,
		Logger:      logger,
		Sender:      sender,
		DailyCounts: counts,
	}
}

func (b *RelayBackend) NewSession(conn *smtp.Conn) (smtp.Session, error) {
	return &RelaySession{backend: b, remote: conn.Conn().RemoteAddr().String()}, nil
}

type RelaySession struct {
	backend  *RelayBackend
	remote   string
	from     string
	rcpts    []string
	apiKey   *db.ApiKey
	user     *db.User
	authDone bool
}

func (s *RelaySession) AuthPlain(username, password string) error {
	apiKeyID := username
	hash := sha256.Sum256([]byte(password))
	keyHash := hex.EncodeToString(hash[:])

	var ak db.ApiKey
	if err := s.backend.DB.Where("id = ? AND key_hash = ?", apiKeyID, keyHash).First(&ak).Error; err != nil {
		s.backend.Logger.Warn("relay auth failed", "api_key_id", apiKeyID, "remote", s.remote)
		return errInvalidCredentials
	}

	if !ak.IsActive {
		return errKeyInactive
	}
	if ak.ExpiresAt != nil && time.Now().After(*ak.ExpiresAt) {
		return errKeyExpired
	}
	if ak.Scopes != "send_email" && ak.Scopes != "full_access" {
		return errScopeInsufficient
	}

	var user db.User
	if err := s.backend.DB.First(&user, "id = ?", ak.UserID).Error; err != nil {
		return errInvalidCredentials
	}

	s.apiKey = &ak
	s.user = &user
	s.authDone = true
	s.backend.Logger.Info("relay auth success", "api_key_id", apiKeyID, "user_id", user.ID, "remote", s.remote)
	return nil
}

func (s *RelaySession) Mail(from string, opts *smtp.MailOptions) error {
	if !s.authDone {
		return &smtp.SMTPError{Code: 530, Message: "authentication required"}
	}
	// Validate MAIL FROM domain belongs to user and is verified
	parts := strings.Split(strings.Trim(from, "<>"), "@")
	if len(parts) != 2 {
		return &smtp.SMTPError{Code: 553, Message: "malformed from address"}
	}
	domain := strings.ToLower(parts[1])

	var d db.Domain
	err := s.backend.DB.Where("name = ? AND user_id = ?", domain, s.user.ID).First(&d).Error
	if err != nil {
		return &smtp.SMTPError{Code: 550, Message: "from domain not found or not owned by you"}
	}
	if d.Status != "verified" {
		return errDomainNotVerified
	}

	// Check message size limit
	if opts != nil && opts.Size > 0 {
		maxSize := int64(s.backend.Config.MaxMessageSizeMB) * 1024 * 1024
		if opts.Size > maxSize {
			return errMaxSize
		}
	}

	// Check daily quota
	if s.backend.Config.MaxDailySendPerKey > 0 {
		count := s.backend.DailyCounts[s.apiKey.ID.String()]
		if count >= s.backend.Config.MaxDailySendPerKey {
			return errQuotaExceeded
		}
	}

	s.from = from
	return nil
}

func (s *RelaySession) Rcpt(to string, opts *smtp.RcptOptions) error {
	// Allow any recipient for relay
	s.rcpts = append(s.rcpts, to)
	return nil
}

func (s *RelaySession) Data(r io.Reader) error {
	if s.backend.Sender == nil {
		return &smtp.SMTPError{Code: 451, Message: "outbound delivery not configured"}
	}
	return s.backend.Sender.Send(s.apiKey.ID, s.user.ID, s.from, s.rcpts, r)
}

func (s *RelaySession) Reset() {
	s.from = ""
	s.rcpts = nil
}

func (s *RelaySession) Logout() error {
	if s.apiKey != nil {
		s.backend.DB.Model(s.apiKey).Update("last_used_at", time.Now())
	}
	return nil
}