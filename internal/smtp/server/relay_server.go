package server

import (
	"context"
	"errors"
	"time"

	smtp "github.com/emersion/go-smtp"
)

// RunRelay starts the SMTP authentication/relay submission server.
// It listens on the configured address and handles client authentication via API keys.
func RunRelay(ctx context.Context, backend *RelayBackend) error {
	s := smtp.NewServer(backend)
	addr := backend.Config.SMTPAuthHostname + ":" + backend.Config.SMTPAuthPort
	if backend.Config.SMTPAuthPort == "" {
		addr = backend.Config.SMTPAuthHostname + ":587"
	}
	s.Addr = addr
	s.Domain = backend.Config.SMTPAuthHostname
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