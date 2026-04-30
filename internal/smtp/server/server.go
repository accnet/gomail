package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	mailservice "gomail/internal/mail/service"

	smtp "github.com/emersion/go-smtp"
	"gorm.io/gorm"
)

type Backend struct {
	DB       *gorm.DB
	Config   config.Config
	Pipeline mailservice.Pipeline
	Logger   *slog.Logger
}

func (b Backend) NewSession(conn *smtp.Conn) (smtp.Session, error) {
	return &Session{backend: b, remote: conn.Conn().RemoteAddr().String()}, nil
}

type Session struct {
	backend Backend
	remote  string
	from    string
	rcpt    string
	inbox   db.Inbox
	user    db.User
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	addr, err := mail.ParseAddress(to)
	if err != nil {
		return &smtp.SMTPError{Code: 553, Message: "malformed address"}
	}
	parts := strings.Split(strings.ToLower(addr.Address), "@")
	if len(parts) != 2 {
		return &smtp.SMTPError{Code: 553, Message: "malformed address"}
	}
	var inbox db.Inbox
	err = s.backend.DB.Joins("JOIN domains ON domains.id = inboxes.domain_id").
		Joins("JOIN users ON users.id = inboxes.user_id").
		Where("domains.name = ? AND domains.status = ? AND inboxes.local_part = ? AND inboxes.is_active = true AND users.is_active = true AND domains.deleted_at IS NULL AND inboxes.deleted_at IS NULL", parts[1], "verified", parts[0]).
		First(&inbox).Error
	if err != nil {
		return &smtp.SMTPError{Code: 550, Message: "mailbox unavailable"}
	}
	var user db.User
	if err := s.backend.DB.First(&user, "id = ? AND is_active = ?", inbox.UserID, true).Error; err != nil {
		return &smtp.SMTPError{Code: 550, Message: "mailbox unavailable"}
	}
	s.rcpt = addr.Address
	s.inbox = inbox
	s.user = user
	return nil
}

func (s *Session) Data(r io.Reader) error {
	if s.inbox.ID.String() == "" {
		return &smtp.SMTPError{Code: 550, Message: "mailbox unavailable"}
	}
	limit := int64(s.user.MaxMessageSizeMB)*1024*1024 + 1
	raw, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return err
	}
	if int64(len(raw)) >= limit {
		return &smtp.SMTPError{Code: 552, Message: "message size limit exceeded"}
	}
	_, err = s.backend.Pipeline.Ingest(context.Background(), s.inbox, s.user, s.from, s.rcpt, raw)
	if err != nil {
		if strings.Contains(err.Error(), "quota") || strings.Contains(err.Error(), "limit") {
			return &smtp.SMTPError{Code: 552, Message: err.Error()}
		}
		s.backend.Logger.Error("smtp ingest failed", "error", err, "remote", s.remote)
		return &smtp.SMTPError{Code: 451, Message: "temporary local error"}
	}
	return nil
}

func (s *Session) Reset() {
	s.from = ""
	s.rcpt = ""
	s.inbox = db.Inbox{}
	s.user = db.User{}
}

func (s *Session) Logout() error { return nil }

func Run(ctx context.Context, backend Backend) error {
	s := smtp.NewServer(backend)
	s.Addr = backend.Config.SMTPHost + ":" + backend.Config.SMTPPort
	s.Domain = backend.Config.SMTPHostname
	s.ReadTimeout = 10 * time.Minute
	s.WriteTimeout = 10 * time.Minute
	s.MaxMessageBytes = int64(backend.Config.MaxMessageSizeMB) * 1024 * 1024
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		return s.Close()
	case err := <-errCh:
		if errors.Is(err, smtp.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func NormalizeMessage(raw []byte) []byte {
	return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
}
