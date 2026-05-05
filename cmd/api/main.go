package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gomail/internal/auth"
	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dns"
	"gomail/internal/http/handlers"
	"gomail/internal/mail/outbound"
	"gomail/internal/realtime"
	"gomail/internal/smtp/relay"
	"gomail/internal/staticprojects"
	"gomail/internal/storage"
	"gomail/internal/teams"
	"gomail/pkg/logger"

	"log/slog"

	"github.com/google/uuid"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	logg := logger.New(cfg.AppEnv)

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		logg.Error("db open failed", "error", err)
		log.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		logg.Error("migration failed", "error", err)
		log.Fatal(err)
	}

	// Use a cancellable context for background workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := db.SeedSuperAdmin(ctx, database, cfg); err != nil {
		logg.Error("seed super admin failed", "error", err)
		log.Fatal(err)
	}
	if cfg.SeedDemoData && cfg.AppEnv != "production" {
		if err := db.SeedDemoData(ctx, database, cfg); err != nil {
			logg.Error("seed demo data failed", "error", err)
			log.Fatal(err)
		}
	}
	teamSvc := teams.NewService(database)
	if err := teamSvc.EnsureDefaultWorkspaces(ctx); err != nil {
		logg.Error("seed default workspaces failed", "error", err)
		log.Fatal(err)
	}

	redisClient := realtime.NewRedis(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	authSvc := auth.NewService(database, cfg)
	verifier := dns.Verifier{
		Timeout:  cfg.DomainVerifyTimeout,
		MXTarget: cfg.MXTarget,
		Resolver: dns.NewNetResolver(cfg.DomainDNSResolvers),
	}

	go handlers.BackgroundDomainRecheck(ctx, database, verifier, cfg, cfg.DomainRecheckEvery)

	storageMgr := storage.NewStaticSitesManager(cfg.StaticSitesRoot)
	auditLogger := staticprojects.NewAuditLogger(database)
	staticSvc := staticprojects.NewService(database, storageMgr, &cfg, auditLogger, slog.Default())
	thumbnailWorker := staticprojects.NewThumbnailWorker(database, cfg.StaticSitesRoot, func(subdomain string) string {
		if subdomain == "" || cfg.StaticSitesBaseDomain == "" {
			return ""
		}
		return "http://" + subdomain + "." + cfg.StaticSitesBaseDomain
	})
	go thumbnailWorker.Run(ctx, time.Minute)

	staticHandler := handlers.NewStaticProjectsHandler(staticSvc)
	siteMiddleware := handlers.NewStaticSiteMiddleware(database, cfg.SaaSDomain, cfg.LandingRoot)
	app := handlers.App{
		DB:                   database,
		Auth:                 authSvc,
		Teams:                teamSvc,
		Config:               cfg,
		Redis:                redisClient,
		Verifier:             verifier,
		StaticProjects:       staticHandler,
		StaticSiteMiddleware: siteMiddleware,
	}
	if cfg.OutboundSMTPConfigured() {
		relaySender := relay.NewSender(database, cfg, logg, make(map[string]int))
		app.SendOutbound = func(userID uuid.UUID, msg outbound.Message, sentLog db.SentEmailLog) error {
			return relaySender.SendMessage(nil, userID, msg, sentLog)
		}
	} else {
		logg.Warn("reply email disabled: outbound SMTP is not configured")
	}
	router := app.Router()
	if len(cfg.TrustedProxies) > 0 {
		_ = router.SetTrustedProxies(cfg.TrustedProxies)
	} else {
		_ = router.SetTrustedProxies(nil)
	}

	addr := cfg.HTTPHost + ":" + cfg.HTTPPort
	srv := &http.Server{
		Addr:         addr,
		Handler:      router.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logg.Info("api listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logg.Info("shutting down api server...")

	// Cancel background workers (DomainRecheck, ThumbnailWorker)
	cancel()

	// Graceful HTTP shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logg.Error("api server forced shutdown", "error", err)
	}

	logg.Info("api server stopped")
}
