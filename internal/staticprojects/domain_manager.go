package staticprojects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gomail/internal/db"

	"github.com/google/uuid"
)

// AvailableDomains returns verified domains owned by the user that are not bound to another project.
func (s *Service) AvailableDomains(userID uuid.UUID) ([]db.Domain, error) {
	var domains []db.Domain
	s.DB.Where("user_id = ? AND status = ?", userID, "verified").Find(&domains)

	// Filter out domains already bound to other projects
	var bound []uuid.UUID
	s.DB.Model(&db.StaticProject{}).Where("domain_id IS NOT NULL").Pluck("domain_id", &bound)
	boundMap := map[uuid.UUID]bool{}
	for _, id := range bound {
		boundMap[id] = true
	}

	var available []db.Domain
	for _, d := range domains {
		if !boundMap[d.ID] && !s.isSaaSDomain(d.Name) {
			available = append(available, d)
		}
	}
	return available, nil
}

// AssignDomain assigns a verified domain to a project.
func (s *Service) AssignDomain(userID uuid.UUID, projectID uuid.UUID, domainID uuid.UUID) (*ProjectResponse, error) {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ? AND user_id = ?", projectID, userID).Error; err != nil {
		return nil, ErrNotFound
	}

	var domain db.Domain
	if err := s.DB.First(&domain, "id = ? AND user_id = ? AND status = ?", domainID, userID, "verified").Error; err != nil {
		return nil, ErrDomainNotVerified
	}
	if s.isSaaSDomain(domain.Name) {
		return nil, ErrDomainReserved
	}

	// Check if domain is already bound
	var existing int64
	s.DB.Model(&db.StaticProject{}).Where("domain_id = ? AND id != ?", domainID, projectID).Count(&existing)
	if existing > 0 {
		return nil, ErrDomainAlreadyBound
	}

	s.DB.Model(&project).Updates(map[string]any{
		"domain_id":             domainID,
		"assigned_domain":       domain.Name,
		"domain_binding_status": "assigned",
	})
	s.DB.First(&project, "id = ?", project.ID)
	return s.reloadAndRespond(projectID, OwnerContext{UserID: userID})
}

func (s *Service) isSaaSDomain(domainName string) bool {
	return normalizeDomainName(domainName) != "" && normalizeDomainName(domainName) == normalizeDomainName(s.Config.SaaSDomain)
}

func normalizeDomainName(domainName string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domainName)), ".")
}

// UnassignDomain removes domain binding from a project.
func (s *Service) UnassignDomain(userID uuid.UUID, projectID uuid.UUID) (*ProjectResponse, error) {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ? AND user_id = ?", projectID, userID).Error; err != nil {
		return nil, ErrNotFound
	}

	s.cleanupCustomDomainTLS(project)

	s.DB.Model(&project).Updates(map[string]any{
		"domain_id":                nil,
		"assigned_domain":          "",
		"domain_binding_status":    "",
		"domain_last_dns_check_at": nil,
		"domain_last_dns_result":   "",
		"domain_tls_enabled_at":    nil,
	})
	s.DB.First(&project, "id = ?", project.ID)
	return s.reloadAndRespond(projectID, OwnerContext{UserID: userID})
}

// CheckDomainIP checks if the domain's A/AAAA record points to the configured IP.
func (s *Service) CheckDomainIP(projectID uuid.UUID, userID uuid.UUID) (bool, string, error) {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ? AND user_id = ?", projectID, userID).Error; err != nil {
		return false, "", ErrNotFound
	}
	if project.AssignedDomain == "" {
		return false, "", ErrDomainNotAvailable
	}

	// Use net.LookupIP to check A/AAAA records
	ips, err := net.LookupIP(project.AssignedDomain)
	if err != nil {
		now := time.Now()
		s.DB.Model(&project).Updates(map[string]any{
			"domain_last_dns_check_at": &now,
			"domain_last_dns_result":   err.Error(),
		})
		return false, err.Error(), nil
	}

	targetIP := s.Config.TraefikPublicIP
	if targetIP == "" {
		return false, "TRAEFIK_PUBLIC_IP not configured", nil
	}

	var ipStrs []string
	for _, ip := range ips {
		ipStrs = append(ipStrs, ip.String())
		if ip.String() == targetIP {
			now := time.Now()
			s.DB.Model(&project).Updates(map[string]any{
				"domain_last_dns_check_at": &now,
				"domain_last_dns_result":   "ok",
			})
			return true, "", nil
		}
	}

	now := time.Now()
	result := fmt.Sprintf("domain resolves to %v, expected %s", ipStrs, targetIP)
	s.DB.Model(&project).Updates(map[string]any{
		"domain_last_dns_check_at": &now,
		"domain_last_dns_result":   result,
	})
	return false, result, nil
}

// ActiveSSL provisions HTTPS for the assigned custom domain.
func (s *Service) ActiveSSL(userID uuid.UUID, projectID uuid.UUID) (*ProjectResponse, error) {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ? AND user_id = ?", projectID, userID).Error; err != nil {
		return nil, ErrNotFound
	}
	if project.AssignedDomain == "" {
		return nil, ErrDomainNotAvailable
	}
	if !s.checkSSLCondition(userID, &project) {
		return nil, ErrSSLConditionNotMet
	}
	s.recordDNSOK(&project)

	if err := s.provisionCustomDomainTLS(project); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPublishFailed, err)
	}

	now := time.Now()
	s.DB.Model(&project).Updates(map[string]any{
		"domain_binding_status": "ssl_active",
		"domain_tls_enabled_at": &now,
	})
	s.DB.First(&project, "id = ?", project.ID)
	return s.reloadAndRespond(projectID, OwnerContext{UserID: userID})
}

// checkSSLCondition verifies whether SSL can be provisioned for the project.
func (s *Service) checkSSLCondition(userID uuid.UUID, project *db.StaticProject) bool {
	if project.DomainLastDNSResult == "ok" {
		return true
	}

	var domain db.Domain
	query := s.DB.Where("user_id = ?", userID)
	if project.DomainID != nil {
		query = query.Where("id = ?", *project.DomainID)
	} else {
		query = query.Where("name = ?", project.AssignedDomain)
	}
	if err := query.First(&domain).Error; err != nil {
		return false
	}
	return domain.ARecordStatus == db.ARecordStatusVerified
}

// recordDNSOK persists the DNS verification result on the project.
func (s *Service) recordDNSOK(project *db.StaticProject) {
	now := time.Now()
	project.DomainLastDNSResult = "ok"
	project.DomainLastDNSCheckAt = &now
	s.DB.Model(project).Updates(map[string]any{
		"domain_last_dns_result":   project.DomainLastDNSResult,
		"domain_last_dns_check_at": project.DomainLastDNSCheckAt,
	})
}

func (s *Service) provisionCustomDomainTLS(project db.StaticProject) error {
	switch s.customDomainSSLProvider() {
	case "command":
		return s.runCustomDomainSSLCommand(s.Config.StaticSitesSSLIssueCommand, project.AssignedDomain)
	case "traefik":
		return s.writeTraefikConfig(project)
	default:
		return fmt.Errorf("unsupported custom domain SSL provider: %s", s.customDomainSSLProvider())
	}
}

func (s *Service) cleanupCustomDomainTLS(project db.StaticProject) {
	switch s.customDomainSSLProvider() {
	case "command":
		_ = s.runCustomDomainSSLCommand(s.Config.StaticSitesSSLCleanupCommand, project.AssignedDomain)
	case "traefik":
		s.cleanupTraefikConfig(project)
	default:
		s.cleanupTraefikConfig(project)
	}
}

func (s *Service) customDomainSSLProvider() string {
	provider := strings.ToLower(strings.TrimSpace(s.Config.StaticSitesSSLProvider))
	if provider == "" || provider == "auto" {
		if strings.TrimSpace(s.Config.StaticSitesSSLIssueCommand) != "" || strings.TrimSpace(s.Config.StaticSitesSSLCleanupCommand) != "" {
			return "command"
		}
		return "traefik"
	}
	return provider
}

func (s *Service) runCustomDomainSSLCommand(commandText string, domain string) error {
	commandText = strings.TrimSpace(commandText)
	if commandText == "" {
		return errors.New("custom domain SSL command is not configured")
	}

	fields := strings.Fields(commandText)
	if len(fields) == 0 {
		return errors.New("custom domain SSL command is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, fields[0], append(fields[1:], domain)...)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return err
		}
		return fmt.Errorf("%s: %w", message, err)
	}
	return nil
}

// writeTraefikConfig writes a Traefik dynamic config file for the custom domain.
func (s *Service) writeTraefikConfig(project db.StaticProject) error {
	if s.Config.TraefikDynamicConfDir == "" {
		return nil // no-op if not configured
	}
	if err := os.MkdirAll(s.Config.TraefikDynamicConfDir, 0755); err != nil {
		return err
	}

	config := map[string]any{
		"http": map[string]any{
			"routers": map[string]any{
				fmt.Sprintf("static-%s", project.ID.String()): map[string]any{
					"rule":    fmt.Sprintf("Host(`%s`)", project.AssignedDomain),
					"service": fmt.Sprintf("static-%s", project.ID.String()),
					"tls": map[string]any{
						"certResolver": "letsencrypt",
					},
				},
			},
			"services": map[string]any{
				fmt.Sprintf("static-%s", project.ID.String()): map[string]any{
					"loadBalancer": map[string]any{
						"servers": []map[string]any{
							{"url": fmt.Sprintf("http://static-server%s", s.Config.StaticServerAddr)},
						},
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	filename := filepath.Join(s.Config.TraefikDynamicConfDir, fmt.Sprintf("static-%s.yaml", project.ID.String()))
	return os.WriteFile(filename, data, 0644)
}

// cleanupTraefikConfig removes the Traefik config file for a project.
func (s *Service) cleanupTraefikConfig(project db.StaticProject) {
	if s.Config.TraefikDynamicConfDir == "" {
		return
	}
	filename := filepath.Join(s.Config.TraefikDynamicConfDir, fmt.Sprintf("static-%s.yaml", project.ID.String()))
	os.Remove(filename)
}
