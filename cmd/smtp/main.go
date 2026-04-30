package main

import (
	"context"
	"log"

	"gomail/internal/config"
	"gomail/internal/db"
	mailservice "gomail/internal/mail/service"
	"gomail/internal/realtime"
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
	backend := smtpserver.Backend{DB: database, Config: cfg, Pipeline: pipeline, Logger: logg}
	logg.Info("smtp listening", "addr", cfg.SMTPHost+":"+cfg.SMTPPort)
	if err := smtpserver.Run(context.Background(), backend); err != nil {
		log.Fatal(err)
	}
}
