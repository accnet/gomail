package config

import (
	"reflect"
	"testing"
)

func TestProductionRejectsSampleSecrets(t *testing.T) {
	cfg := Config{
		AppEnv:               "production",
		AppBaseURL:           "https://mail.example.com",
		APIBaseURL:           "https://mail.example.com/api",
		SaaSDomain:           "example.com",
		SMTPHostname:         "mx.example.com",
		MXTarget:             "mx.example.com",
		DatabaseURL:          "postgres://example",
		RedisAddr:            "redis:6379",
		JWTSecret:            "change-me-use-a-long-random-secret",
		DefaultAdminEmail:    "admin@example.com",
		DefaultAdminPassword: "change-me-before-deploy",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production sample secret validation failure")
	}
}

func TestProductionRejectsDKIMEnabledWithoutEncryptionSecret(t *testing.T) {
	cfg := Config{
		AppEnv:               "production",
		AppBaseURL:           "https://mail.example.com",
		APIBaseURL:           "https://mail.example.com/api",
		SaaSDomain:           "example.com",
		SMTPHostname:         "mx.example.com",
		MXTarget:             "mx.example.com",
		DatabaseURL:          "postgres://example",
		RedisAddr:            "redis:6379",
		JWTSecret:            "a-realistic-production-jwt-secret-value",
		DefaultAdminEmail:    "admin@example.com",
		DefaultAdminPassword: "a-realistic-admin-password",
		DKIMEnabled:          true,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected DKIM encryption secret validation failure")
	}
}

func TestLoadDefaultsProductionDomainDNSResolvers(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_BASE_URL", "https://mail.example.com")
	t.Setenv("API_BASE_URL", "https://mail.example.com/api")
	t.Setenv("SAAS_DOMAIN", "example.com")
	t.Setenv("SMTP_HOSTNAME", "mx.example.com")
	t.Setenv("MX_TARGET", "mx.example.com")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("JWT_SECRET", "a-realistic-production-jwt-secret-value")
	t.Setenv("DEFAULT_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("DEFAULT_ADMIN_PASSWORD", "a-realistic-admin-password")
	t.Setenv("DOMAIN_DNS_RESOLVERS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.1.1.1:53", "8.8.8.8:53"}
	if !reflect.DeepEqual(cfg.DomainDNSResolvers, want) {
		t.Fatalf("DomainDNSResolvers = %v want %v", cfg.DomainDNSResolvers, want)
	}
}

func TestLoadParsesDomainDNSResolvers(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_BASE_URL", "http://localhost:8080")
	t.Setenv("API_BASE_URL", "http://localhost:8080/api")
	t.Setenv("SAAS_DOMAIN", "localhost")
	t.Setenv("SMTP_HOSTNAME", "mx.localhost")
	t.Setenv("MX_TARGET", "mx.localhost")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("JWT_SECRET", "dev-secret-change-me")
	t.Setenv("DEFAULT_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("DOMAIN_DNS_RESOLVERS", "9.9.9.9, 1.1.1.1:5353")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"9.9.9.9:53", "1.1.1.1:5353"}
	if !reflect.DeepEqual(cfg.DomainDNSResolvers, want) {
		t.Fatalf("DomainDNSResolvers = %v want %v", cfg.DomainDNSResolvers, want)
	}
}
