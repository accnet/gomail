package handlers

import (
	"context"
	"errors"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dns"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const domainVerifiedWarningStatus = "verified_warning"

func domainARecordTargetIP(cfg config.Config) string {
	if cfg.TraefikPublicIP != "" {
		return cfg.TraefikPublicIP
	}
	if cfg.SMTPRelayPublicIP != "" {
		return cfg.SMTPRelayPublicIP
	}
	return ""
}

func verifyDomainARecord(ctx context.Context, verifier dns.Verifier, cfg config.Config, domainName string) (string, string, *time.Time) {
	targetIP := domainARecordTargetIP(cfg)
	if targetIP == "" {
		return db.ARecordStatusPending, "", nil
	}
	now := time.Now()
	ok, result := verifier.VerifyA(ctx, domainName, targetIP)
	if ok {
		if result == "" {
			result = targetIP
		}
		return db.ARecordStatusVerified, result, &now
	}
	return db.ARecordStatusFailed, result, &now
}

func loadOptionalDomainEmailAuth(database *gorm.DB, domainID uuid.UUID) (*db.DomainEmailAuth, error) {
	var auth db.DomainEmailAuth
	err := database.Where("domain_id = ?", domainID).First(&auth).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &auth, nil
}

func deriveDomainWarningStatus(domain db.Domain, auth *db.DomainEmailAuth) string {
	if domain.Status != db.DomainStatusVerified {
		return ""
	}
	if domain.VerificationError != "" {
		return domainVerifiedWarningStatus
	}
	if domain.ARecordStatus == db.ARecordStatusFailed {
		return domainVerifiedWarningStatus
	}
	if auth == nil {
		return ""
	}
	if auth.SPFStatus == db.DomainAuthStatusFailed || auth.DKIMStatus == db.DomainAuthStatusFailed {
		return domainVerifiedWarningStatus
	}
	return ""
}
