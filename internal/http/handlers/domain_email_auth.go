package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dkimkeys"
	mw "gomail/internal/http/middleware"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var dkimSelectorPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

type domainEmailAuthResponse struct {
	Auth  db.DomainEmailAuth `json:"auth"`
	SPF   dnsInstruction     `json:"spf"`
	DKIM  dnsInstruction     `json:"dkim"`
	DMARC dnsInstruction     `json:"dmarc"`
}

type dnsInstruction struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (a App) getDomainEmailAuth(c *gin.Context) {
	user := mw.CurrentUser(c)
	domain, auth, err := a.loadDomainEmailAuth(c.Param("id"), user.ID)
	if err != nil {
		response.Error(c, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	response.OK(c, buildDomainEmailAuthResponse(domain, auth))
}

func (a App) generateDomainDKIM(c *gin.Context) {
	user := mw.CurrentUser(c)
	domain, auth, err := a.loadDomainEmailAuth(c.Param("id"), user.ID)
	if err != nil {
		response.Error(c, http.StatusNotFound, "not_found", "domain not found")
		return
	}

	var req struct {
		Selector string `json:"selector"`
	}
	_ = c.ShouldBindJSON(&req)
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		selector = a.Config.DKIMSelector
	}
	if selector == "" {
		selector = "gomail"
	}
	if !dkimSelectorPattern.MatchString(selector) {
		response.Error(c, http.StatusBadRequest, "invalid_selector", "selector must be 1-63 DNS-safe characters")
		return
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "dkim_generate_failed", "could not generate DKIM key")
		return
	}
	privatePEM, publicKey, err := encodeDKIMKey(privateKey)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "dkim_generate_failed", "could not encode DKIM key")
		return
	}
	encryptedPrivatePEM, err := dkimkeys.EncryptPrivateKeyPEM(privatePEM, a.Config.DKIMKeyEncryptionSecret)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "dkim_generate_failed", "could not encrypt DKIM key")
		return
	}

	now := time.Now()
	recordName := selector + "._domainkey." + domain.Name
	recordValue := dkimRecordValue(publicKey)
	updates := map[string]any{
		"dkim_selector":          selector,
		"dkim_public_key":        publicKey,
		"dkim_private_key_pem":   encryptedPrivatePEM,
		"dkim_status":            db.DomainAuthStatusPending,
		"dkim_record_name":       recordName,
		"dkim_record_value":      recordValue,
		"dkim_error":             "",
		"dkim_last_generated_at": &now,
		"dkim_last_checked_at":   nil,
		"dkim_last_verified_at":  nil,
	}
	if err := a.DB.Model(&auth).Updates(updates).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "dkim_generate_failed", "could not save DKIM key")
		return
	}
	a.DB.First(&auth, "id = ?", auth.ID)
	response.OK(c, buildDomainEmailAuthResponse(domain, auth))
}

func (a App) verifyDomainEmailAuth(c *gin.Context) {
	user := mw.CurrentUser(c)
	domain, auth, err := a.loadDomainEmailAuth(c.Param("id"), user.ID)
	if err != nil {
		response.Error(c, http.StatusNotFound, "not_found", "domain not found")
		return
	}

	now := time.Now()
	requiredSPF := spfRequiredMechanism(a.Config)
	spfOK, spfErr := a.Verifier.VerifySPF(c.Request.Context(), domain.Name, requiredSPF)
	dkimOK := false
	dkimErr := "DKIM key has not been generated"
	if auth.DKIMRecordName != "" && auth.DKIMPublicKey != "" {
		dkimOK, dkimErr = a.Verifier.VerifyDKIM(c.Request.Context(), auth.DKIMRecordName, auth.DKIMPublicKey)
	}

	updates := map[string]any{
		"spf_status":           authStatus(spfOK),
		"spf_record":           expectedSPFRecord(a.Config),
		"spf_last_checked_at":  &now,
		"spf_error":            spfErr,
		"dkim_status":          authStatus(dkimOK),
		"dkim_last_checked_at": &now,
		"dkim_error":           dkimErr,
	}
	if dkimOK {
		updates["dkim_last_verified_at"] = &now
	}
	if err := a.DB.Model(&auth).Updates(updates).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "email_auth_verify_failed", "could not save verification result")
		return
	}
	domain.WarningStatus = deriveDomainWarningStatus(domain, &db.DomainEmailAuth{
		SPFStatus:  authStatus(spfOK),
		DKIMStatus: authStatus(dkimOK),
	})
	if err := a.DB.Model(&domain).Update("warning_status", domain.WarningStatus).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "email_auth_verify_failed", "could not update domain warning status")
		return
	}
	a.DB.First(&auth, "id = ?", auth.ID)
	response.OK(c, buildDomainEmailAuthResponse(domain, auth))
}

func (a App) loadDomainEmailAuth(domainID string, userID uuid.UUID) (db.Domain, db.DomainEmailAuth, error) {
	var domain db.Domain
	if err := a.DB.Where("id = ? AND user_id = ?", domainID, userID).First(&domain).Error; err != nil {
		return db.Domain{}, db.DomainEmailAuth{}, err
	}
	auth, err := ensureDomainEmailAuth(a.DB, domain, a.Config)
	return domain, auth, err
}

func ensureDomainEmailAuth(database *gorm.DB, domain db.Domain, cfg config.Config) (db.DomainEmailAuth, error) {
	var auth db.DomainEmailAuth
	err := database.Where("domain_id = ?", domain.ID).First(&auth).Error
	if err == nil {
		updates := map[string]any{}
		if auth.SPFRecord == "" {
			updates["spf_record"] = expectedSPFRecord(cfg)
		}
		if auth.DKIMSelector == "" {
			updates["dkim_selector"] = defaultDKIMSelector(cfg)
		}
		if len(updates) > 0 {
			if err := database.Model(&auth).Updates(updates).Error; err != nil {
				return auth, err
			}
			database.First(&auth, "id = ?", auth.ID)
		}
		return auth, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return db.DomainEmailAuth{}, err
	}
	auth = db.DomainEmailAuth{
		DomainID:     domain.ID,
		SPFStatus:    db.DomainAuthStatusPending,
		SPFRecord:    expectedSPFRecord(cfg),
		DKIMSelector: defaultDKIMSelector(cfg),
		DKIMStatus:   db.DomainAuthStatusPending,
	}
	return auth, database.Create(&auth).Error
}

func buildDomainEmailAuthResponse(domain db.Domain, auth db.DomainEmailAuth) domainEmailAuthResponse {
	return domainEmailAuthResponse{
		Auth: auth,
		SPF: dnsInstruction{
			Name:  domain.Name,
			Type:  "TXT",
			Value: auth.SPFRecord,
		},
		DKIM: dnsInstruction{
			Name:  auth.DKIMRecordName,
			Type:  "TXT",
			Value: auth.DKIMRecordValue,
		},
		DMARC: dnsInstruction{
			Name:  "_dmarc." + domain.Name,
			Type:  "TXT",
			Value: expectedDMARCRecord(),
		},
	}
}

func encodeDKIMKey(privateKey *rsa.PrivateKey) (string, string, error) {
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	publicKey := base64.StdEncoding.EncodeToString(publicDER)
	return privatePEM, publicKey, nil
}

func dkimRecordValue(publicKey string) string {
	return "v=DKIM1; k=rsa; p=" + publicKey
}

func authStatus(ok bool) string {
	if ok {
		return db.DomainAuthStatusVerified
	}
	return db.DomainAuthStatusFailed
}

func defaultDKIMSelector(cfg config.Config) string {
	if cfg.DKIMSelector != "" {
		return cfg.DKIMSelector
	}
	return "gomail"
}

func expectedSPFRecord(cfg config.Config) string {
	mechanism := spfRequiredMechanism(cfg)
	if mechanism == "" || mechanism == "mx" {
		return "v=spf1 mx -all"
	}
	return "v=spf1 " + mechanism + " mx -all"
}

func expectedDMARCRecord() string {
	return "v=DMARC1; p=none; adkim=s; aspf=s"
}

func spfRequiredMechanism(cfg config.Config) string {
	if cfg.SMTPRelayPublicIP != "" {
		if ip := net.ParseIP(cfg.SMTPRelayPublicIP); ip != nil {
			if ip.To4() != nil {
				return "ip4:" + cfg.SMTPRelayPublicIP
			}
			return "ip6:" + cfg.SMTPRelayPublicIP
		}
	}
	if cfg.SMTPRelayHostname != "" {
		return "a:" + cfg.SMTPRelayHostname
	}
	if cfg.SMTPAuthHostname != "" {
		return "a:" + cfg.SMTPAuthHostname
	}
	return "mx"
}
