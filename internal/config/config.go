package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv     string
	AppName    string
	AppBaseURL string
	APIBaseURL string
	SaaSDomain string

	HTTPHost string
	HTTPPort string

	SMTPHost     string
	SMTPPort     string
	SMTPHostname string
	MXTarget     string

	DatabaseURL string
	RedisAddr   string
	RedisPass   string
	RedisDB     int

	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	DefaultAdminEmail               string
	DefaultAdminPassword            string
	DefaultAdminName                string
	DefaultAdminMaxDomains          int
	DefaultAdminMaxInboxes          int
	DefaultAdminMaxMessageSizeMB    int
	DefaultAdminMaxAttachmentSizeMB int
	DefaultAdminMaxStorageGB        int

	StorageRoot           string
	AttachmentStorageRoot string
	RawEmailStorageRoot   string

	DomainVerifyTimeout time.Duration
	DomainRecheckEvery  time.Duration
	BlockFlagged        bool
	AllowAdminOverride  bool
	MaxMessageSizeMB    int
	ClamAVEnabled       bool
	ClamAVAddr          string
	TrustedProxies      []string
	SeedDemoData        bool
}

func Load() (Config, error) {
	cfg := Config{
		AppEnv:                          env("APP_ENV", "development"),
		AppName:                         env("APP_NAME", "GoMail"),
		AppBaseURL:                      env("APP_BASE_URL", "http://localhost:8089"),
		APIBaseURL:                      env("API_BASE_URL", "http://localhost:8089/api"),
		SaaSDomain:                      env("SAAS_DOMAIN", "localhost"),
		HTTPHost:                        env("HTTP_HOST", "0.0.0.0"),
		HTTPPort:                        env("HTTP_PORT", "8089"),
		SMTPHost:                        env("SMTP_HOST", "0.0.0.0"),
		SMTPPort:                        env("SMTP_PORT", "2525"),
		SMTPHostname:                    env("SMTP_HOSTNAME", "mx.localhost"),
		MXTarget:                        env("MX_TARGET", "mx.localhost"),
		DatabaseURL:                     env("DATABASE_URL", "postgres://gomail:gomail_password@localhost:5432/gomail?sslmode=disable"),
		RedisAddr:                       env("REDIS_ADDR", "localhost:6379"),
		RedisPass:                       env("REDIS_PASSWORD", ""),
		RedisDB:                         envInt("REDIS_DB", 0),
		JWTSecret:                       env("JWT_SECRET", "dev-secret-change-me"),
		AccessTokenTTL:                  time.Duration(envInt("ACCESS_TOKEN_TTL_MINUTES", 15)) * time.Minute,
		RefreshTokenTTL:                 time.Duration(envInt("REFRESH_TOKEN_TTL_DAYS", 30)) * 24 * time.Hour,
		DefaultAdminEmail:               env("DEFAULT_ADMIN_EMAIL", "admin@example.com"),
		DefaultAdminPassword:            env("DEFAULT_ADMIN_PASSWORD", "change-me-before-deploy"),
		DefaultAdminName:                env("DEFAULT_ADMIN_NAME", "Super Admin"),
		DefaultAdminMaxDomains:          envInt("DEFAULT_ADMIN_MAX_DOMAINS", 100),
		DefaultAdminMaxInboxes:          envInt("DEFAULT_ADMIN_MAX_INBOXES", 1000),
		DefaultAdminMaxMessageSizeMB:    envInt("DEFAULT_ADMIN_MAX_MESSAGE_SIZE_MB", 25),
		DefaultAdminMaxAttachmentSizeMB: envInt("DEFAULT_ADMIN_MAX_ATTACHMENT_SIZE_MB", 25),
		DefaultAdminMaxStorageGB:        envInt("DEFAULT_ADMIN_MAX_STORAGE_GB", 100),
		StorageRoot:                     env("STORAGE_ROOT", "./data"),
		AttachmentStorageRoot:           env("ATTACHMENT_STORAGE_ROOT", "./data/attachments"),
		RawEmailStorageRoot:             env("RAW_EMAIL_STORAGE_ROOT", "./data/raw-eml"),
		DomainVerifyTimeout:             time.Duration(envInt("DOMAIN_VERIFY_TIMEOUT_SECONDS", 10)) * time.Second,
		DomainRecheckEvery:              time.Duration(envInt("DOMAIN_RECHECK_INTERVAL_MINUTES", 30)) * time.Minute,
		BlockFlagged:                    envBool("BLOCK_FLAGGED_ATTACHMENTS", true),
		AllowAdminOverride:              envBool("ALLOW_ADMIN_ATTACHMENT_OVERRIDE", true),
		MaxMessageSizeMB:                envInt("MAX_MESSAGE_SIZE_MB", 25),
		ClamAVEnabled:                   envBool("CLAMAV_ENABLED", false),
		ClamAVAddr:                      env("CLAMAV_ADDR", "clamav:3310"),
		SeedDemoData:                    envBool("SEED_DEMO_DATA", true),
	}
	if proxies := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); proxies != "" {
		cfg.TrustedProxies = strings.Split(proxies, ",")
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var missing []string
	required := map[string]string{
		"SAAS_DOMAIN":         c.SaaSDomain,
		"APP_BASE_URL":        c.AppBaseURL,
		"API_BASE_URL":        c.APIBaseURL,
		"SMTP_HOSTNAME":       c.SMTPHostname,
		"MX_TARGET":           c.MXTarget,
		"DATABASE_URL":        c.DatabaseURL,
		"REDIS_ADDR":          c.RedisAddr,
		"JWT_SECRET":          c.JWTSecret,
		"DEFAULT_ADMIN_EMAIL": c.DefaultAdminEmail,
	}
	for k, v := range required {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	if _, err := url.ParseRequestURI(c.AppBaseURL); err != nil {
		return fmt.Errorf("invalid APP_BASE_URL: %w", err)
	}
	if _, err := url.ParseRequestURI(c.APIBaseURL); err != nil {
		return fmt.Errorf("invalid API_BASE_URL: %w", err)
	}
	if c.AppEnv == "production" {
		if c.JWTSecret == "change-me-use-a-long-random-secret" || c.JWTSecret == "dev-secret-change-me" {
			return errors.New("JWT_SECRET must be changed for production")
		}
		if c.DefaultAdminPassword == "change-me-before-deploy" || len(c.DefaultAdminPassword) < 12 {
			return errors.New("DEFAULT_ADMIN_PASSWORD must be changed and at least 12 characters for production")
		}
	}
	return nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "true", "1", "yes", "y":
		return true
	case "false", "0", "no", "n":
		return false
	default:
		return fallback
	}
}
