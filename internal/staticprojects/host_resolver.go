package staticprojects

import (
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ResolvedProject contains the info needed to serve a static site.
type ResolvedProject struct {
	ID         uuid.UUID
	RootFolder string
	IsActive   bool
	Status     string
}

// HostResolver resolves Host headers to static projects.
type HostResolver struct {
	DB         *gorm.DB
	BaseDomain string
	SaaSDomain string
}

// NewHostResolver creates a new HostResolver.
func NewHostResolver(database *gorm.DB, baseDomain, saasDomain string) *HostResolver {
	return &HostResolver{DB: database, BaseDomain: baseDomain, SaaSDomain: saasDomain}
}

// Resolve looks up a project by the Host header.
// It matches by:
//  1. Custom domain (exact match on assigned_domain)
//  2. Subdomain (e.g., myproject.basedomain.com → subdomain="myproject")
func (r *HostResolver) Resolve(host string) (*ResolvedProject, error) {
	// Strip port from host
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	host = normalizeDomainName(host)

	var project ResolvedProject

	// Try 1: Match by assigned custom domain
	if host != "" && host != normalizeDomainName(r.SaaSDomain) {
		err := r.DB.Table("static_projects").
			Select("id, root_folder, is_active, status").
			Where("assigned_domain = ? AND domain_binding_status = ? AND deleted_at IS NULL", host, "ssl_active").
			Scan(&project).Error
		if err == nil && project.ID != uuid.Nil {
			return &project, nil
		}
	}

	// Try 2: Match by subdomain (e.g., "myproject.basedomain.com")
	var subdomain string
	if r.BaseDomain != "" && strings.HasSuffix(host, "."+r.BaseDomain) {
		subdomain = strings.TrimSuffix(host, "."+r.BaseDomain)
	} else if r.SaaSDomain != "" && strings.HasSuffix(host, "."+r.SaaSDomain) {
		subdomain = strings.TrimSuffix(host, "."+r.SaaSDomain)
	}

	if subdomain != "" {

		subdomain = strings.TrimSpace(subdomain)
		err := r.DB.Table("static_projects").
			Select("id, root_folder, is_active, status").
			Where("subdomain = ? AND deleted_at IS NULL", subdomain).
			Scan(&project).Error
		if err == nil && project.ID != uuid.Nil {
			return &project, nil
		}
	}

	return nil, nil
}

// IsStaticHost returns true if the host might be a static site.
func (r *HostResolver) IsStaticHost(host string) bool {
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	host = normalizeDomainName(host)
	if r.BaseDomain != "" && strings.HasSuffix(host, "."+r.BaseDomain) {
		return true
	}
	if r.SaaSDomain != "" && strings.HasSuffix(host, "."+r.SaaSDomain) {
		return true
	}
	return false
}
