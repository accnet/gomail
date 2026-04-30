package config

import "testing"

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
