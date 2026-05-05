package staticprojects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/storage"
	"log/slog"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Public errors
// ---------------------------------------------------------------------------

var (
	ErrNotFound             = errors.New("project not found")
	ErrQuotaExceeded        = errors.New("you have reached the website limit")
	ErrInvalidArchive       = errors.New("invalid archive")
	ErrArchiveTooLarge      = errors.New("archive exceeds maximum size")
	ErrPublishRootNotFound  = errors.New("no index.html found in the archive root")
	ErrMultiplePublishRoot  = errors.New("multiple top-level folders containing index.html – please ensure only one deployment folder")
	ErrForbiddenFileType    = errors.New("archive contains executable or server-side scripts, which are not allowed")
	ErrDomainNotAvailable   = errors.New("no custom domain assigned")
	ErrDomainNotVerified    = errors.New("domain is not verified")
	ErrDomainAlreadyBound   = errors.New("domain is already bound to another project")
	ErrSSLConditionNotMet   = errors.New("SSL conditions not met – domain must be verified and DNS must point to this server")
	ErrPublishFailed        = errors.New("publish failed")
)

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

type Service struct {
	DB          *gorm.DB
	Storage     *storage.StaticSitesManager
	Config      *config.Config
	AuditLogger *AuditLogger
	Logger      *slog.Logger
}

func NewService(db *gorm.DB, st *storage.StaticSitesManager, cfg *config.Config, auditLogger *AuditLogger, logger *slog.Logger) *Service {
	return &Service{DB: db, Storage: st, Config: cfg, AuditLogger: auditLogger, Logger: logger}
}

// ---------------------------------------------------------------------------
// Quota
// ---------------------------------------------------------------------------

func (s *Service) QuotaInfo(userID uuid.UUID) (used int, max int, err error) {
	var count int64
	if err := s.DB.Model(&db.StaticProject{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return 0, 0, err
	}

	var user db.User
	if err := s.DB.First(&user, "id = ?", userID).Error; err != nil {
		return 0, 0, err
	}
	return int(count), user.MaxWebsites, nil
}

// ---------------------------------------------------------------------------
// Subdomain generation
// ---------------------------------------------------------------------------

var subdomainRe = regexp.MustCompile(`[^a-z0-9-]`)

func (s *Service) generateSubdomain(name string) (string, error) {
	base := strings.ToLower(name)
	base = subdomainRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "site"
	}

	// If available, use base as-is
	var count int64
	s.DB.Model(&db.StaticProject{}).Where("subdomain = ?", base).Count(&count)
	if count == 0 {
		return base, nil
	}

	// Append suffix
	for i := 1; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		var n int64
		s.DB.Model(&db.StaticProject{}).Where("subdomain = ?", candidate).Count(&n)
		if n == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to generate unique subdomain")
}

// ---------------------------------------------------------------------------
// Deploy (new + re-deploy)
// ---------------------------------------------------------------------------

// DeployStream deploys a new static project from an uploaded zip stream.
func (s *Service) DeployStream(ctx context.Context, userID uuid.UUID, name string, r io.Reader, filename string) (*ProjectResponse, error) {
	// Check quota
	used, max, _ := s.QuotaInfo(userID)
	if used >= max {
		return nil, ErrQuotaExceeded
	}

	archivePath, archiveSize, err := s.storeArchiveTemp(r)
	if err != nil {
		return nil, err
	}
	defer os.Remove(archivePath)

	return s.publishArchive(ctx, userID, name, archivePath, archiveSize, filename, nil)
}

// publishArchive is the core deploy/redeploy orchestrator.
// If existing is non-nil, it's a redeploy; otherwise a new project is created.
func (s *Service) publishArchive(ctx context.Context, userID uuid.UUID, name string, archivePath string, archiveSize int64, filename string, existing *db.StaticProject) (*ProjectResponse, error) {
	// Check archive size limit
	if archiveSize > s.Config.StaticSitesMaxArchiveBytes {
		return nil, ErrArchiveTooLarge
	}

	var project db.StaticProject
	var isNew bool

	if existing != nil {
		project = *existing
		isNew = false
		// Set status to deploying
		s.DB.Model(&project).Updates(map[string]any{
			"status":          "deploying",
			"deploy_error":    "",
			"archive_size_bytes": archiveSize,
			"upload_filename": filename,
		})
	} else {
		subdomain, err := s.generateSubdomain(name)
		if err != nil {
			return nil, err
		}
		project = db.StaticProject{
			UserID:          userID,
			Name:            name,
			Subdomain:       subdomain,
			Status:          "deploying",
			UploadFilename:  filename,
			ArchiveSizeBytes: archiveSize,
		}
		if err := s.DB.Create(&project).Error; err != nil {
			return nil, fmt.Errorf("create project: %w", err)
		}
		isNew = true
	}

	// Log audit
	if isNew {
		s.AuditLogger.LogDeploy(userID, project.ID, project.Name)
	} else {
		s.AuditLogger.LogRedeploy(userID, project.ID, project.Name)
	}

	// Ensure staging directory
	paths := s.Storage.ProjectPaths(project.ID)
	stagingDir := paths.Staging
	liveDir := paths.Live
	if err := os.RemoveAll(stagingDir); err != nil {
		return s.failProject(project.ID, userID, "cleanup staging dir: "+err.Error())
	}
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return s.failProject(project.ID, userID, "create staging dir: "+err.Error())
	}

	// Extract and validate archive to staging
	_, rootFolder, fileCount, err := s.extractAndValidateArchive(archivePath, stagingDir)
	if err != nil {
		return s.failProject(project.ID, userID, err.Error())
	}

	// Publish (atomic with retry)
	publishDir := stagingDir
	if rootFolder != "" {
		publishDir = stagingDir + "/" + rootFolder // filepath not used here for simplicity; stagingDir is clean
	}

	if err := s.publishRetry(publishDir, liveDir); err != nil {
		return s.failProject(project.ID, userID, "publish: "+err.Error())
	}

	// Publish success – update project
	now := time.Now()
	s.DB.Model(&project).Updates(map[string]any{
		"status":        "published",
		"root_folder":   rootFolder,
		"staging_folder": stagingDir,
		"detected_root": rootFolder,
		"file_count":    fileCount,
		"published_at":  &now,
	})

	// Reload project for response
	var reloaded db.StaticProject
	if err := s.DB.First(&reloaded, "id = ?", project.ID).Error; err != nil {
		return s.failProject(project.ID, userID, "reload after publish: "+err.Error())
	}

	// Schedule thumbnail generation
	s.enqueueThumbnailGeneration(reloaded)

	// If project was already published before this deploy, reprovision TLS if domain was active
	if !isNew && reloaded.DomainBindingStatus == "ssl_active" && reloaded.AssignedDomain != "" {
		s.provisionCustomDomainTLS(reloaded)
	}

	return s.reloadAndRespond(project.ID, userID)
}

func (s *Service) failProject(projectID, userID uuid.UUID, errMsg string) (*ProjectResponse, error) {
	s.DB.Model(&db.StaticProject{}).Where("id = ?", projectID).Updates(map[string]any{
		"status":       "publish_failed",
		"deploy_error": errMsg,
	})
	s.Logger.Error("publish failed", "project_id", projectID, "error", errMsg)
	return s.reloadAndRespond(projectID, userID)
}

// ---------------------------------------------------------------------------
// Redeploy
// ---------------------------------------------------------------------------

// RedeployStream streams and re-publishes an existing project.
func (s *Service) RedeployStream(ctx context.Context, userID uuid.UUID, projectID uuid.UUID, r io.Reader, filename string) (*ProjectResponse, error) {
	archivePath, archiveSize, err := s.storeArchiveTemp(r)
	if err != nil {
		return nil, err
	}
	defer os.Remove(archivePath)
	return s.redeployArchive(ctx, userID, projectID, archivePath, archiveSize, filename)
}

func (s *Service) redeployArchive(ctx context.Context, userID uuid.UUID, projectID uuid.UUID, archivePath string, archiveSize int64, filename string) (*ProjectResponse, error) {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ? AND user_id = ?", projectID, userID).Error; err != nil {
		return nil, ErrNotFound
	}
	return s.publishArchive(ctx, userID, project.Name, archivePath, archiveSize, filename, &project)
}

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

// List returns all static projects for a user.
func (s *Service) List(userID uuid.UUID) ([]ProjectResponse, error) {
	var projects []db.StaticProject
	s.DB.Where("user_id = ?", userID).Order("created_at desc").Find(&projects)

	used, max, _ := s.QuotaInfo(userID)
	responses := make([]ProjectResponse, len(projects))
	for i := range projects {
		responses[i] = ProjectResponse{
			StaticProject: projects[i],
			UIState:       ComputeUIState(&projects[i]),
			WebsitesUsed:  used,
			MaxWebsites:   max,
		}
	}
	return responses, nil
}

// Get returns a single project by ID, checking ownership.
func (s *Service) Get(userID uuid.UUID, projectID uuid.UUID) (*ProjectResponse, error) {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ? AND user_id = ?", projectID, userID).Error; err != nil {
		return nil, ErrNotFound
	}
	used, max, _ := s.QuotaInfo(userID)
	return toProjectResponse(&project, used, max), nil
}

// Delete soft-deletes a project and cleans up its files.
func (s *Service) Delete(userID uuid.UUID, projectID uuid.UUID) error {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ? AND user_id = ?", projectID, userID).Error; err != nil {
		return ErrNotFound
	}
	// Cleanup domain binding if any
	if project.DomainID != nil {
		s.cleanupCustomDomainTLS(project)
	}
	// Cleanup files
	s.Storage.CleanupProjectDirs(project.ID)
	// Soft delete
	s.DB.Delete(&project)
	return nil
}

// ToggleStatus enables or disables a project.
func (s *Service) ToggleStatus(userID uuid.UUID, projectID uuid.UUID, isActive bool) (*ProjectResponse, error) {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ? AND user_id = ?", projectID, userID).Error; err != nil {
		return nil, ErrNotFound
	}
	s.DB.Model(&project).Update("is_active", isActive)
	return s.reloadAndRespond(projectID, userID)
}

func (s *Service) enqueueThumbnailGeneration(project db.StaticProject) {
	// Thumbnail generation is handled by thumbnail_worker.go
	// This is a no-op placeholder; in production the worker polls the DB.
}