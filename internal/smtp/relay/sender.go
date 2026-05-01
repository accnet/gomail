package relay

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/smtp"
	"strings"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dkimkeys"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Sender handles outbound SMTP delivery for the relay service.
type Sender struct {
	DB          *gorm.DB
	Config      config.Config
	Logger      *slog.Logger
	DailyCounts map[string]int // api_key_id -> count today
}

// NewSender creates a new relay outbound sender.
func NewSender(database *gorm.DB, cfg config.Config, logger *slog.Logger, counts map[string]int) *Sender {
	return &Sender{
		DB:          database,
		Config:      cfg,
		Logger:      logger,
		DailyCounts: counts,
	}
}

// Send processes and delivers an outbound email.
// It reads the raw message, delivers via direct MX or external relay,
// logs the result to SentEmailLog, and updates daily usage counts.
func (s *Sender) Send(apiKeyID uuid.UUID, userID uuid.UUID, from string, rcpts []string, data io.Reader) error {
	body, err := io.ReadAll(data)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}
	if s.Config.DKIMEnabled {
		body, err = s.signMessage(userID, from, body)
		if err != nil {
			return fmt.Errorf("dkim sign: %w", err)
		}
	}

	// Determine delivery mode
	channel := "direct"
	if s.Config.OutboundMode == "relay" && s.Config.OutboundRelayHost != "" {
		channel = "relay"
		err = s.deliverViaRelay(from, rcpts, body)
	} else {
		err = s.deliverDirect(from, rcpts, body)
	}

	// Log to SentEmailLog
	status := db.SentEmailStatusSent
	if err != nil {
		status = db.SentEmailStatusFailed
	}
	log := db.SentEmailLog{
		ApiKeyID:    &apiKeyID,
		UserID:      userID,
		FromAddress: strings.Trim(from, "<>"),
		ToAddress:   strings.Join(rcpts, ","),
		Channel:     channel,
		Status:      status,
	}
	if err != nil {
		errMsg := err.Error()
		if len(errMsg) > 500 {
			errMsg = errMsg[:500]
		}
		log.ErrorMessage = errMsg
	} else {
		// Update daily count
		keyStr := apiKeyID.String()
		if s.DailyCounts != nil {
			s.DailyCounts[keyStr]++
		}
	}
	if dbErr := s.DB.Create(&log).Error; dbErr != nil {
		s.Logger.Warn("failed to log sent email", "error", dbErr)
	}

	return err
}

func (s *Sender) signMessage(userID uuid.UUID, from string, body []byte) ([]byte, error) {
	domain := extractDomain(strings.Trim(from, "<>"))
	if domain == "" {
		return body, nil
	}
	var row db.Domain
	if err := s.DB.Where("user_id = ? AND name = ? AND status = ?", userID, domain, db.DomainStatusVerified).First(&row).Error; err != nil {
		s.Logger.Warn("dkim signing skipped: verified from domain not found", "domain", domain, "error", err)
		return body, nil
	}
	var auth db.DomainEmailAuth
	if err := s.DB.Where("domain_id = ? AND dkim_status = ?", row.ID, db.DomainAuthStatusVerified).First(&auth).Error; err != nil {
		s.Logger.Warn("dkim signing skipped: verified dkim config not found", "domain", domain, "error", err)
		return body, nil
	}
	if auth.DKIMSelector == "" || auth.DKIMPrivateKeyPEM == "" {
		s.Logger.Warn("dkim signing skipped: incomplete dkim config", "domain", domain)
		return body, nil
	}
	privateKeyPEM, err := dkimkeys.DecryptPrivateKeyPEM(auth.DKIMPrivateKeyPEM, s.Config.DKIMKeyEncryptionSecret)
	if err != nil {
		return nil, err
	}
	return signDKIM(body, domain, auth.DKIMSelector, privateKeyPEM)
}

// deliverDirect delivers email by looking up MX records for each recipient domain.
func (s *Sender) deliverDirect(from string, rcpts []string, body []byte) error {
	cleanFrom := strings.Trim(from, "<>")
	for _, rcpt := range rcpts {
		cleanRcpt := strings.Trim(rcpt, "<>")
		domain := extractDomain(cleanRcpt)
		if domain == "" {
			return fmt.Errorf("invalid recipient: %s", rcpt)
		}

		mxs, err := net.LookupMX(domain)
		if err != nil {
			s.Logger.Warn("mx lookup failed", "domain", domain, "error", err)
			return fmt.Errorf("mx lookup %s: %w", domain, err)
		}
		if len(mxs) == 0 {
			return fmt.Errorf("no mx records for %s", domain)
		}

		// Try MX servers in priority order (sorted by net.LookupMX)
		var lastErr error
		for _, mx := range mxs {
			host := strings.TrimSuffix(mx.Host, ".")
			addr := fmt.Sprintf("%s:25", host)
			err := smtp.SendMail(addr, nil, cleanFrom, []string{cleanRcpt}, body)
			if err == nil {
				s.Logger.Debug("delivered direct", "rcpt", cleanRcpt, "mx", host)
				lastErr = nil
				break
			}
			s.Logger.Warn("direct delivery attempt failed", "mx", host, "error", err)
			lastErr = err
		}
		if lastErr != nil {
			return fmt.Errorf("deliver to %s: %w", cleanRcpt, lastErr)
		}
	}
	return nil
}

// deliverViaRelay delivers email through a configured external SMTP relay.
func (s *Sender) deliverViaRelay(from string, rcpts []string, body []byte) error {
	cleanFrom := strings.Trim(from, "<>")
	host := s.Config.OutboundRelayHost
	port := s.Config.OutboundRelayPort
	if port == "" {
		port = "587"
	}
	addr := fmt.Sprintf("%s:%s", host, port)

	var auth smtp.Auth
	if s.Config.OutboundRelayUser != "" {
		auth = smtp.PlainAuth("", s.Config.OutboundRelayUser, s.Config.OutboundRelayPass, host)
	}

	// Implicit TLS for port 465
	if port == "465" {
		tlsConfig := &tls.Config{ServerName: host}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("tls dial %s: %w", addr, err)
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("smtp client: %w", err)
		}
		defer c.Close()
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
		if err := c.Mail(cleanFrom); err != nil {
			return fmt.Errorf("mail from: %w", err)
		}
		for _, rcpt := range rcpts {
			cleanRcpt := strings.Trim(rcpt, "<>")
			if err := c.Rcpt(cleanRcpt); err != nil {
				return fmt.Errorf("rcpt %s: %w", cleanRcpt, err)
			}
		}
		w, err := c.Data()
		if err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if _, err := w.Write(body); err != nil {
			return fmt.Errorf("write body: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("close body: %w", err)
		}
		if err := c.Quit(); err != nil {
			return fmt.Errorf("quit: %w", err)
		}
		s.Logger.Debug("delivered via relay (tls)", "host", addr, "rcpts", len(rcpts))
		return nil
	}

	// Plain SMTP relay (go smtp.SendMail may upgrade to STARTTLS if server supports it)
	return smtp.SendMail(addr, auth, cleanFrom, cleanRcpts(rcpts), body)
}

func cleanRcpts(rcpts []string) []string {
	cleaned := make([]string, len(rcpts))
	for i, r := range rcpts {
		cleaned[i] = strings.Trim(r, "<>")
	}
	return cleaned
}

// extractDomain extracts the domain part from an email address.
func extractDomain(addr string) string {
	parts := strings.Split(addr, "@")
	if len(parts) == 2 {
		return strings.ToLower(parts[1])
	}
	return ""
}
