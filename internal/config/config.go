package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
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
	DefaultAdminMaxWebsites         int

	StorageRoot           string
	AttachmentStorageRoot string
	RawEmailStorageRoot   string
	StaticSitesRoot       string

	DomainVerifyTimeout time.Duration
	DomainRecheckEvery  time.Duration
	DomainDNSResolvers  []string
	BlockFlagged        bool
	AllowAdminOverride  bool
	MaxMessageSizeMB    int
	ClamAVEnabled       bool
	ClamAVAddr          string
	TrustedProxies      []string
	SeedDemoData        bool

	StaticSitesMaxArchiveBytes   int64
	StaticSitesMaxExtractedBytes int64
	StaticSitesMaxFileCount      int
	StaticSitesBaseDomain        string
	StaticServerAddr             string
	StaticSitesSSLProvider       string
	StaticSitesSSLIssueCommand   string
	StaticSitesSSLCleanupCommand string
	TraefikDynamicConfDir        string
	TraefikPublicIP              string

	// SMTP relay / submission server
	SMTPAuthEnabled         bool
	SMTPAuthHostname        string
	SMTPAuthPort            string
	SMTPAuthTLSPort         string
	SMTPAuthTLSMode         string // "starttls", "tls", or "none"
	SMTPAuthCertFile        string
	SMTPAuthKeyFile         string
	SMTPRelayHostname       string
	SMTPRelayPublicIP       string
	DKIMEnabled             bool
	DKIMSelector            string
	DKIMPrivateKeyPath      string
	DKIMKeyEncryptionSecret string
	OutboundMode            string // "direct" or "relay"
	OutboundRelayHost       string
	OutboundRelayPort       string
	OutboundRelayUser       string
	OutboundRelayPass       string
	MaxDailySendPerKey      int
}

func Load() (Config, error) {
	if os.Getenv("APP_ENV") == "" {
		if _, err := os.Stat(".env.dev"); err == nil {
			_ = godotenv.Load(".env.dev")
		} else {
			_ = godotenv.Load(".env")
		}
	}

	appEnv := env("APP_ENV", "development")
	domainDNSResolvers := parseDNSResolverAddrs(os.Getenv("DOMAIN_DNS_RESOLVERS"))
	if len(domainDNSResolvers) == 0 {
		domainDNSResolvers = defaultDomainDNSResolvers(appEnv)
	}

	cfg := Config{
		AppEnv:                          appEnv,
		AppName:                         env("APP_NAME", "GoMail"),
		AppBaseURL:                      env("APP_BASE_URL", "http://localhost:8080"),
		APIBaseURL:                      env("API_BASE_URL", "http://localhost:8080/api"),
		SaaSDomain:                      env("SAAS_DOMAIN", "localhost"),
		HTTPHost:                        env("HTTP_HOST", "0.0.0.0"),
		HTTPPort:                        env("HTTP_PORT", "8080"),
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
		DefaultAdminMaxWebsites:         envInt("DEFAULT_ADMIN_MAX_WEBSITES", 100),
		StorageRoot:                     env("STORAGE_ROOT", "./data"),
		AttachmentStorageRoot:           env("ATTACHMENT_STORAGE_ROOT", "./data/attachments"),
		RawEmailStorageRoot:             env("RAW_EMAIL_STORAGE_ROOT", "./data/raw-eml"),
		StaticSitesRoot:                 env("STATIC_SITES_ROOT", "./data/static-sites"),
		DomainVerifyTimeout:             time.Duration(envInt("DOMAIN_VERIFY_TIMEOUT_SECONDS", 10)) * time.Second,
		DomainRecheckEvery:              time.Duration(envInt("DOMAIN_RECHECK_INTERVAL_MINUTES", 30)) * time.Minute,
		DomainDNSResolvers:              domainDNSResolvers,
		BlockFlagged:                    envBool("BLOCK_FLAGGED_ATTACHMENTS", true),
		AllowAdminOverride:              envBool("ALLOW_ADMIN_ATTACHMENT_OVERRIDE", true),
		MaxMessageSizeMB:                envInt("MAX_MESSAGE_SIZE_MB", 25),
		ClamAVEnabled:                   envBool("CLAMAV_ENABLED", false),
		ClamAVAddr:                      env("CLAMAV_ADDR", "clamav:3310"),
		SeedDemoData:                    envBool("SEED_DEMO_DATA", true),
		StaticSitesMaxArchiveBytes:      envInt64("STATIC_SITES_MAX_ARCHIVE_BYTES", int64(envInt("STATIC_SITES_MAX_ARCHIVE_MB", 50))*1024*1024),
		StaticSitesMaxExtractedBytes:    envInt64("STATIC_SITES_MAX_EXTRACTED_BYTES", int64(envInt("STATIC_SITES_MAX_EXTRACTED_MB", 200))*1024*1024),
		StaticSitesMaxFileCount:         envInt("STATIC_SITES_MAX_FILE_COUNT", 5000),
		StaticSitesBaseDomain:           env("STATIC_SITES_BASE_DOMAIN", "localhost"),
		StaticServerAddr:                env("STATIC_SERVER_ADDR", ":8090"),
		StaticSitesSSLProvider:          env("STATIC_SITES_SSL_PROVIDER", "auto"),
		StaticSitesSSLIssueCommand:      env("STATIC_SITES_SSL_ISSUE_COMMAND", ""),
		StaticSitesSSLCleanupCommand:    env("STATIC_SITES_SSL_CLEANUP_COMMAND", ""),
		TraefikDynamicConfDir:           env("TRAEFIK_DYNAMIC_CONF_DIR", "./data/traefik-dynamic"),
		TraefikPublicIP:                 env("TRAEFIK_PUBLIC_IP", ""),

		// SMTP relay defaults
		SMTPAuthEnabled:         envBool("SMTP_AUTH_ENABLED", false),
		SMTPAuthHostname:        env("SMTP_AUTH_HOSTNAME", "smtp.localhost"),
		SMTPAuthPort:            env("SMTP_AUTH_PORT", "587"),
		SMTPAuthTLSPort:         env("SMTP_AUTH_TLS_PORT", "465"),
		SMTPAuthTLSMode:         env("SMTP_AUTH_TLS_MODE", "starttls"),
		SMTPAuthCertFile:        env("SMTP_AUTH_CERT_FILE", ""),
		SMTPAuthKeyFile:         env("SMTP_AUTH_KEY_FILE", ""),
		SMTPRelayHostname:       env("SMTP_RELAY_HOSTNAME", ""),
		SMTPRelayPublicIP:       env("SMTP_RELAY_PUBLIC_IP", ""),
		DKIMEnabled:             envBool("DKIM_ENABLED", false),
		DKIMSelector:            env("DKIM_SELECTOR", "gomail"),
		DKIMPrivateKeyPath:      env("DKIM_PRIVATE_KEY_PATH", ""),
		DKIMKeyEncryptionSecret: env("DKIM_KEY_ENCRYPTION_SECRET", ""),
		OutboundMode:            env("OUTBOUND_MODE", "direct"),
		OutboundRelayHost:       env("OUTBOUND_RELAY_HOST", ""),
		OutboundRelayPort:       env("OUTBOUND_RELAY_PORT", "25"),
		OutboundRelayUser:       env("OUTBOUND_RELAY_USER", ""),
		OutboundRelayPass:       env("OUTBOUND_RELAY_PASS", ""),
		MaxDailySendPerKey:      envInt("MAX_DAILY_SEND_PER_KEY", 0),
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
	for _, resolver := range c.DomainDNSResolvers {
		if _, _, err := net.SplitHostPort(resolver); err != nil {
			return fmt.Errorf("invalid DOMAIN_DNS_RESOLVERS entry %q: %w", resolver, err)
		}
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
		if c.DKIMEnabled && (len(c.DKIMKeyEncryptionSecret) < 32 || c.DKIMKeyEncryptionSecret == "change-me-use-at-least-32-random-chars") {
			return errors.New("DKIM_KEY_ENCRYPTION_SECRET must be set to at least 32 characters when DKIM is enabled in production")
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

func envInt64(key string, fallback int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
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

func defaultDomainDNSResolvers(appEnv string) []string {
	if strings.EqualFold(strings.TrimSpace(appEnv), "production") {
		return []string{"1.1.1.1:53", "8.8.8.8:53"}
	}
	return nil
}

func parseDNSResolverAddrs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	resolvers := make([]string, 0, len(parts))
	for _, part := range parts {
		resolver := strings.TrimSpace(part)
		if resolver == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(resolver); err != nil {
			resolver = net.JoinHostPort(resolver, "53")
		}
		resolvers = append(resolvers, resolver)
	}
	if len(resolvers) == 0 {
		return nil
	}
	return resolvers
}
