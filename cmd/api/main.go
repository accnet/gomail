package main

import (
	"context"
	"log"

	"gomail/internal/auth"
	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dns"
	"gomail/internal/http/handlers"
	"gomail/internal/realtime"
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
		logg.Error("db open failed", "error", err)
		log.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		logg.Error("migration failed", "error", err)
		log.Fatal(err)
	}
	if err := db.SeedSuperAdmin(context.Background(), database, cfg); err != nil {
		logg.Error("seed super admin failed", "error", err)
		log.Fatal(err)
	}
	if cfg.SeedDemoData && cfg.AppEnv != "production" {
		if err := db.SeedDemoData(context.Background(), database, cfg); err != nil {
			logg.Error("seed demo data failed", "error", err)
			log.Fatal(err)
		}
	}
	redisClient := realtime.NewRedis(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	authSvc := auth.NewService(database, cfg)
	verifier := dns.Verifier{Timeout: cfg.DomainVerifyTimeout, MXTarget: cfg.MXTarget}
	go handlers.BackgroundDomainRecheck(context.Background(), database, verifier, cfg.DomainRecheckEvery)
	app := handlers.App{DB: database, Auth: authSvc, Config: cfg, Redis: redisClient, Verifier: verifier}
	router := app.Router()
	if len(cfg.TrustedProxies) > 0 {
		_ = router.SetTrustedProxies(cfg.TrustedProxies)
	} else {
		_ = router.SetTrustedProxies(nil)
	}
	addr := cfg.HTTPHost + ":" + cfg.HTTPPort
	logg.Info("api listening", "addr", addr)
	if err := router.Run(addr); err != nil {
		log.Fatal(err)
	}
}
