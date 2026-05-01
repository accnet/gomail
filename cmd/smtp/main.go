package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gomail/internal/config"
	"gomail/internal/db"
	mailservice "gomail/internal/mail/service"
	"gomail/internal/realtime"
	relay "gomail/internal/smtp/relay"
	smtpserver "gomail/internal/smtp/server"
	"gomail/internal/storage"
	"gomail/pkg/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	logg := logger.New(cfg.AppEnv)

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		log.Fatal(err)
	}

	store := storage.NewLocal(cfg.AttachmentStorageRoot, cfg.RawEmailStorageRoot)
	if err := store.Ensure(); err != nil {
		log.Fatal(err)
	}
	redisClient := realtime.NewRedis(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	pipeline := mailservice.Pipeline{
		DB:        database,
		Config:    cfg,
		Store:     store,
		Publisher: realtime.NewPublisher(redisClient),
	}

	// Shared daily counts map for relay (shared between Sender and RelayBackend)
	relayCounts := make(map[string]int)

	// Create outbound sender for relay
	sender := relay.NewSender(database, cfg, logg, relayCounts)

	// Create relay/auth SMTP backend (submission)
	relayBackend := smtpserver.NewRelayBackend(database, cfg, logg, sender)

	// Create inbound SMTP backend
	backend := smtpserver.Backend{DB: database, Config: cfg, Pipeline: pipeline, Logger: logg}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Count active servers to know how many goroutines to wait for
	serverCount := 1 // inbound SMTP is always active
	if cfg.SMTPAuthEnabled {
		serverCount++
	}
	errCh := make(chan error, serverCount)

	// Start inbound SMTP server
	go func() {
		logg.Info("inbound smtp listening", "addr", cfg.SMTPHost+":"+cfg.SMTPPort)
		errCh <- smtpserver.Run(ctx, backend)
	}()

	// Start relay/auth SMTP submission server
	if cfg.SMTPAuthEnabled {
		go func() {
			port := cfg.SMTPAuthPort
			if port == "" {
				port = "587"
			}
			logg.Info("relay smtp listening", "addr", cfg.SMTPAuthHostname+":"+port)
			errCh <- smtpserver.RunRelay(ctx, relayBackend)
		}()
	}

	// Wait for shutdown signal or first server error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logg.Info("shutting down...", "signal", sig.String())
		cancel()
	case err := <-errCh:
		if err != nil {
			logg.Error("server error, shutting down", "error", err)
		}
		cancel()
	}

	// Wait for remaining servers to finish
	for i := 0; i < serverCount-1; i++ {
		<-errCh
	}
	logg.Info("all servers stopped")
}