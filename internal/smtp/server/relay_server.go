package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"time"

	smtp "github.com/emersion/go-smtp"
)

func relayListenHost(backend *RelayBackend) string {
	host := strings.TrimSpace(backend.Config.SMTPHost)
	if host == "" {
		return "0.0.0.0"
	}
	return host
}

func relayTLSConfig(backend *RelayBackend) (*tls.Config, error) {
	if strings.EqualFold(strings.TrimSpace(backend.Config.SMTPAuthTLSMode), "none") {
		return nil, nil
	}
	certFile := strings.TrimSpace(backend.Config.SMTPAuthCertFile)
	keyFile := strings.TrimSpace(backend.Config.SMTPAuthKeyFile)
	if certFile == "" || keyFile == "" {
		return nil, nil
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func newRelayServer(backend *RelayBackend, port string, tlsConfig *tls.Config) *smtp.Server {
	if strings.TrimSpace(port) == "" {
		port = "587"
	}
	s := smtp.NewServer(backend)
	s.Addr = net.JoinHostPort(relayListenHost(backend), port)
	s.Domain = backend.Config.SMTPAuthHostname
	s.TLSConfig = tlsConfig
	s.ReadTimeout = 10 * time.Minute
	s.WriteTimeout = 10 * time.Minute
	s.MaxMessageBytes = int64(backend.Config.MaxMessageSizeMB) * 1024 * 1024
	return s
}

// RunRelay starts the SMTP authentication/relay submission server.
// It listens on the configured address and handles client authentication via API keys.
func RunRelay(ctx context.Context, backend *RelayBackend) error {
	tlsConfig, err := relayTLSConfig(backend)
	if err != nil {
		return err
	}

	plainServer := newRelayServer(backend, backend.Config.SMTPAuthPort, tlsConfig)
	servers := []*smtp.Server{plainServer}
	serveFns := []func() error{plainServer.ListenAndServe}

	if tlsConfig != nil {
		tlsPort := strings.TrimSpace(backend.Config.SMTPAuthTLSPort)
		plainPort := strings.TrimSpace(backend.Config.SMTPAuthPort)
		if tlsPort != "" && tlsPort != plainPort {
			tlsServer := newRelayServer(backend, tlsPort, tlsConfig)
			servers = append(servers, tlsServer)
			serveFns = append(serveFns, tlsServer.ListenAndServeTLS)
		}
	}

	errCh := make(chan error, len(serveFns))
	for _, serve := range serveFns {
		go func(run func() error) {
			errCh <- run()
		}(serve)
	}
	select {
	case <-ctx.Done():
		var closeErr error
		for _, server := range servers {
			if err := server.Close(); err != nil && !errors.Is(err, smtp.ErrServerClosed) && closeErr == nil {
				closeErr = err
			}
		}
		return closeErr
	case err := <-errCh:
		if errors.Is(err, smtp.ErrServerClosed) {
			return nil
		}
		return err
	}
}
