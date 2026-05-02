package staticprojects

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/storage"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrQuotaExceeded       = errors.New("website_quota_exceeded")
	ErrInvalidArchive      = errors.New("invalid_archive")
	ErrPublishRootNotFound = errors.New("publish_root_not_found")
	ErrMultiplePublishRoot = errors.New("multiple_publish_roots")
	ErrForbiddenFileType   = errors.New("forbidden_file_type")
	ErrPublishFailed       = errors.New("publish_failed")
	ErrNotFound            = errors.New("not_found")
	ErrNotOwner            = errors.New("not_owner")
	ErrDomainNotVerified   = errors.New("domain_not_verified")
	ErrDomainAlreadyBound  = errors.New("domain_already_bound")
	ErrDomainNotAvailable  = errors.New("domain_not_available")
	ErrCheckIPFailed       = errors.New("check_ip_failed")
	ErrSSLConditionNotMet  = errors.New("ssl_condition_not_met")
)

const tempArchivePattern = "static-upload-*.zip"

var allowedExtensions = map[string]bool{
	".html":        true,
	".htm":         true,
	".css":         true,
	".js":          true,
	".json":        true,
	".xml":         true,
	".txt":         true,
	".md":          true,
	".png":         true,
	".jpg":         true,
	".jpeg":        true,
	".gif":         true,
	".svg":         true,
	".ico":         true,
	".webp":        true,
	".woff":        true,
	".woff2":       true,
	".ttf":         true,
	".otf":         true,
	".eot":         true,
	".pdf":         true,
	".mp4":         true,
	".webmanifest": true,
	".map":         true,
}

var forbiddenExtensions = map[string]bool{
	".php":      true,
	".phtml":    true,
	".php3":     true,
	".php4":     true,
	".php5":     true,
	".phps":     true,
	".cgi":      true,
	".pl":       true,
	".py":       true,
	".rb":       true,
	".sh":       true,
	".bash":     true,
	".exe":      true,
	".dll":      true,
	".so":       true,
	".bin":      true,
	".bat":      true,
	".cmd":      true,
	".asp":      true,
	".aspx":     true,
	".jsp":      true,
	".war":      true,
	".jar":      true,
	".htaccess": true,
}

// Service handles static project operations.
type Service struct {
	DB      *gorm.DB
	Config  config.Config
	Storage *storage.StaticSitesManager
	Audit   *AuditLogger
}

// NewService creates a new static project service.
func NewService(database *gorm.DB, cfg config.Config) *Service {
	return &Service{
		DB:      database,
		Config:  cfg,
		Storage: storage.NewStaticSitesManager(cfg.StaticSitesRoot),
		Audit:   NewAuditLogger(database),
	}
}

// UIState represents the computed UI state for a project.
type UIState string

const (
	UIStateDeploying UIState = "deploying"
	UIStateLive      UIState = "live"
	UIStateFailed    UIState = "failed"
	UIStateDisabled  UIState = "disabled"
)

// ProjectResponse is the API response for a static project.
type ProjectResponse struct {
	db.StaticProject
	UIState      UIState `json:"ui_state"`
	WebsitesUsed int     `json:"websites_used"`
	MaxWebsites  int     `json:"max_websites"`
}

// ComputeUIState computes the UI state from the project's raw fields.
func ComputeUIState(p *db.StaticProject) UIState {
	if !p.IsActive {
		return UIStateDisabled
	}
	switch p.Status {
	case "draft", "deploying":
		return UIStateDeploying
	case "published":
		return UIStateLive
	case "publish_failed":
		return UIStateFailed
	default:
		return UIStateFailed
	}
}

// QuotaInfo returns the website quota info for a user.
func (s *Service) QuotaInfo(userID uuid.UUID) (used int, max int, err error) {
	var user db.User
	if err := s.DB.First(&user, "id = ?", userID).Error; err != nil {
		return 0, 0, err
	}
	var count int64
	s.DB.Model(&db.StaticProject{}).Where("user_id = ?", userID).Count(&count)
	return int(count), user.MaxWebsites, nil
}

// checkQuota checks if the user has available website quota.
func (s *Service) checkQuota(userID uuid.UUID) error {
	used, max, err := s.QuotaInfo(userID)
	if err != nil {
		return err
	}
	if used >= max {
		return ErrQuotaExceeded
	}
	return nil
}

// generateSubdomain creates a random unique subdomain.
func (s *Service) generateSubdomain(ctx context.Context) (string, error) {
	chars := "abcdefghijklmnopqrstuvwxyz0123456789"
	for i := 0; i < 10; i++ {
		b := make([]byte, 8)
		for j := range b {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
			if err != nil {
				return "", err
			}
			b[j] = chars[n.Int64()]
		}
		sub := string(b)
		var exists int64
		s.DB.Model(&db.StaticProject{}).Where("subdomain = ?", sub).Count(&exists)
		if exists == 0 {
			return sub, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique subdomain after 10 attempts")
}

// validateArchiveSize checks the uploaded archive size.
func (s *Service) validateArchiveSize(size int64) error {
	if size > s.Config.StaticSitesMaxArchiveBytes {
		return fmt.Errorf("%w: archive size %d exceeds max %d", ErrInvalidArchive, size, s.Config.StaticSitesMaxArchiveBytes)
	}
	return nil
}

func (s *Service) storeArchiveTemp(r io.Reader) (string, int64, error) {
	tempDir := filepath.Join(s.Config.StaticSitesRoot, "tmp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", 0, err
	}

	f, err := os.CreateTemp(tempDir, tempArchivePattern)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	max := s.Config.StaticSitesMaxArchiveBytes
	limited := io.LimitReader(r, max+1)
	size, err := io.Copy(f, limited)
	if err != nil {
		os.Remove(f.Name())
		return "", 0, err
	}
	if err := s.validateArchiveSize(size); err != nil {
		os.Remove(f.Name())
		return "", 0, err
	}
	return f.Name(), size, nil
}

// validateZipEntry checks a single zip entry for safety.
func (s *Service) validateZipEntry(f *zip.File) error {
	name := filepath.ToSlash(f.Name)
	if strings.ContainsRune(name, 0) || len(name) > 255 {
		return fmt.Errorf("%w: invalid file name: %s", ErrInvalidArchive, f.Name)
	}
	// Reject zip-slip
	if strings.Contains(name, "../") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") {
		return fmt.Errorf("%w: path traversal detected: %s", ErrInvalidArchive, f.Name)
	}
	// Reject symlinks
	if f.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symlink not allowed: %s", ErrInvalidArchive, f.Name)
	}
	// Reject directories (they're fine, just skip)
	if f.FileInfo().IsDir() {
		return nil
	}
	// Check extension
	ext := strings.ToLower(filepath.Ext(name))
	if forbiddenExtensions[ext] {
		return fmt.Errorf("%w: forbidden file type: %s", ErrForbiddenFileType, f.Name)
	}
	if !allowedExtensions[ext] && ext != "" {
		return fmt.Errorf("%w: disallowed file type: %s", ErrForbiddenFileType, f.Name)
	}
	return nil
}

// detectPublishRoot determines the root folder to serve from the extracted files.
func detectPublishRoot(files []string) (string, error) {
	// Check if index.html exists at root
	for _, f := range files {
		if f == "index.html" {
			return "", nil // root
		}
	}
	// Check for exactly one top-level directory containing index.html
	dirs := map[string]bool{}
	for _, f := range files {
		parts := strings.SplitN(f, "/", 2)
		if len(parts) == 2 {
			dirs[parts[0]] = true
		}
	}
	var candidates []string
	for dir := range dirs {
		for _, f := range files {
			if f == dir+"/index.html" {
				candidates = append(candidates, dir)
				break
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return "", ErrMultiplePublishRoot
	}
	return "", ErrPublishRootNotFound
}

// extractAndValidateArchive extracts a ZIP archive to the staging directory and validates its contents.
func (s *Service) extractAndValidateArchive(projectID uuid.UUID, archivePath string) (rootFolder string, fileCount int, extractedSize int64, err error) {
	paths := s.Storage.ProjectPaths(projectID)
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", 0, 0, fmt.Errorf("%w: invalid zip: %w", ErrInvalidArchive, err)
	}
	defer reader.Close()

	var extractedFiles []string
	for _, f := range reader.File {
		if err := s.validateZipEntry(f); err != nil {
			return "", 0, 0, err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		fileCount++
		extractedSize += int64(f.UncompressedSize64)
		if extractedSize > s.Config.StaticSitesMaxExtractedBytes {
			return "", 0, 0, fmt.Errorf("%w: extracted size exceeds max %d", ErrInvalidArchive, s.Config.StaticSitesMaxExtractedBytes)
		}
		if fileCount > s.Config.StaticSitesMaxFileCount {
			return "", 0, 0, fmt.Errorf("%w: file count exceeds max %d", ErrInvalidArchive, s.Config.StaticSitesMaxFileCount)
		}
		extractedFiles = append(extractedFiles, filepath.ToSlash(f.Name))
	}

	// Detect publish root
	root, err := detectPublishRoot(extractedFiles)
	if err != nil {
		return "", 0, 0, err
	}
	rootFolder = root

	// Extract files
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		targetPath := filepath.Join(paths.Staging, name)
		// Ensure the target is within staging
		cleanStaging := filepath.Clean(paths.Staging) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(targetPath), cleanStaging) {
			return "", 0, 0, fmt.Errorf("%w: path escape detected: %s", ErrInvalidArchive, f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return "", 0, 0, fmt.Errorf("create dir for %s: %w", name, err)
		}
		rc, err := f.Open()
		if err != nil {
			return "", 0, 0, fmt.Errorf("open %s: %w", name, err)
		}
		out, err := os.Create(targetPath)
		if err != nil {
			rc.Close()
			return "", 0, 0, fmt.Errorf("create %s: %w", name, err)
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return "", 0, 0, fmt.Errorf("write %s: %w", name, err)
		}
	}

	return rootFolder, fileCount, extractedSize, nil
}

// extractAndValidate extracts in-memory ZIP data. Tests use this wrapper; API uploads use extractAndValidateArchive.
func (s *Service) extractAndValidate(projectID uuid.UUID, data []byte) (rootFolder string, fileCount int, extractedSize int64, err error) {
	archivePath, _, err := s.storeArchiveTemp(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, err
	}
	defer os.Remove(archivePath)
	return s.extractAndValidateArchive(projectID, archivePath)
}

// publishAtomic moves files from staging to live atomically.
func (s *Service) publishAtomic(projectID uuid.UUID, rootFolder string) error {
	paths := s.Storage.ProjectPaths(projectID)
	liveRoot := paths.Live
	newLive := liveRoot + ".new"
	oldLive := liveRoot + ".old"

	sourceRoot := paths.Staging
	if rootFolder != "" {
		sourceRoot = filepath.Join(paths.Staging, rootFolder)
	}
	if err := os.RemoveAll(newLive); err != nil {
		return fmt.Errorf("clean new live dir: %w", err)
	}
	if err := os.MkdirAll(newLive, 0755); err != nil {
		return fmt.Errorf("create new live dir: %w", err)
	}

	err := filepath.Walk(sourceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(newLive, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
	if err != nil {
		os.RemoveAll(newLive)
		return fmt.Errorf("%w: publish copy failed: %w", ErrPublishFailed, err)
	}
	oldThumbnail := filepath.Join(liveRoot, "thumbnail.png")
	newThumbnail := filepath.Join(newLive, "thumbnail.png")
	if _, err := os.Stat(newThumbnail); os.IsNotExist(err) {
		if _, err := os.Stat(oldThumbnail); err == nil {
			if err := copyFile(oldThumbnail, newThumbnail); err != nil {
				os.RemoveAll(newLive)
				return fmt.Errorf("%w: preserve thumbnail: %w", ErrPublishFailed, err)
			}
		}
	}

	if err := os.RemoveAll(oldLive); err != nil {
		os.RemoveAll(newLive)
		return fmt.Errorf("%w: clean old live dir: %w", ErrPublishFailed, err)
	}
	if _, err := os.Stat(liveRoot); err == nil {
		if err := os.Rename(liveRoot, oldLive); err != nil {
			os.RemoveAll(newLive)
			return fmt.Errorf("%w: move current live aside: %w", ErrPublishFailed, err)
		}
	} else if !os.IsNotExist(err) {
		os.RemoveAll(newLive)
		return fmt.Errorf("%w: stat current live: %w", ErrPublishFailed, err)
	}
	if err := os.Rename(newLive, liveRoot); err != nil {
		if _, statErr := os.Stat(oldLive); statErr == nil {
			_ = os.Rename(oldLive, liveRoot)
		}
		os.RemoveAll(newLive)
		return fmt.Errorf("%w: activate new live dir: %w", ErrPublishFailed, err)
	}
	os.RemoveAll(oldLive)
	return nil
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// publishArchive is the shared internal method for both deploy and redeploy.
// If project is nil, a new project is created (deploy); otherwise the existing project is updated (redeploy).
func (s *Service) publishArchive(ctx context.Context, userID uuid.UUID, name string, archivePath string, archiveSize int64, filename string, existing *db.StaticProject) (*ProjectResponse, error) {
	var project *db.StaticProject

	if existing != nil {
		// Redeploy: use existing project
		project = existing
		s.DB.Model(project).Updates(map[string]any{
			"status":          "deploying",
			"upload_filename": filename,
			"deploy_error":    "",
		})
	} else {
		// Deploy: create new project
		if err := s.checkQuota(userID); err != nil {
			return nil, err
		}
		if err := s.validateArchiveSize(archiveSize); err != nil {
			return nil, err
		}
		subdomain, err := s.generateSubdomain(ctx)
		if err != nil {
			return nil, err
		}
		project = &db.StaticProject{
			UserID:         userID,
			Name:           name,
			Subdomain:      subdomain,
			Status:         "deploying",
			UploadFilename: filename,
			IsActive:       true,
		}
		if err := s.DB.Create(project).Error; err != nil {
			return nil, err
		}
	}

	if err := s.Storage.EnsureProjectDirs(project.ID); err != nil {
		if existing == nil {
			s.DB.Delete(project)
		} else {
			s.DB.Model(project).Update("status", "publish_failed")
		}
		return nil, err
	}

	rootFolder, fileCount, _, err := s.extractAndValidateArchive(project.ID, archivePath)
	if err != nil {
		s.DB.Model(project).Updates(map[string]any{
			"status":       "publish_failed",
			"deploy_error": err.Error(),
		})
		s.Storage.CleanupStaging(project.ID)
		s.DB.First(project, "id = ?", project.ID)
		return &ProjectResponse{StaticProject: *project, UIState: ComputeUIState(project)}, err
	}

	if err := s.publishAtomic(project.ID, rootFolder); err != nil {
		s.DB.Model(project).Updates(map[string]any{
			"status":       "publish_failed",
			"deploy_error": err.Error(),
		})
		s.Storage.CleanupStaging(project.ID)
		s.DB.First(project, "id = ?", project.ID)
		return &ProjectResponse{StaticProject: *project, UIState: ComputeUIState(project)}, err
	}

	now := time.Now()
	rootFolderStr := rootFolder
	if rootFolderStr == "" {
		rootFolderStr = "."
	}
	s.DB.Model(project).Updates(map[string]any{
		"status":             "published",
		"root_folder":        s.Storage.ProjectPaths(project.ID).Live,
		"staging_folder":     s.Storage.ProjectPaths(project.ID).Staging,
		"detected_root":      rootFolderStr,
		"archive_size_bytes": archiveSize,
		"thumbnail_status":   "pending",
		"file_count":         fileCount,
		"published_at":       &now,
		"deploy_error":       "",
	})

	s.Storage.CleanupStaging(project.ID)
	s.DB.First(project, "id = ?", project.ID)

	used, max, _ := s.QuotaInfo(userID)
	return &ProjectResponse{
		StaticProject: *project,
		UIState:       ComputeUIState(project),
		WebsitesUsed:  used,
		MaxWebsites:   max,
	}, nil
}

func (s *Service) deployArchive(ctx context.Context, userID uuid.UUID, name string, archivePath string, archiveSize int64, filename string) (*ProjectResponse, error) {
	return s.publishArchive(ctx, userID, name, archivePath, archiveSize, filename, nil)
}

// Deploy creates a new static project and publishes it.
func (s *Service) Deploy(ctx context.Context, userID uuid.UUID, name string, data []byte, filename string) (*ProjectResponse, error) {
	archivePath, archiveSize, err := s.storeArchiveTemp(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer os.Remove(archivePath)
	return s.deployArchive(ctx, userID, name, archivePath, archiveSize, filename)
}

// DeployStream streams an uploaded ZIP to a temporary file before publishing it.
func (s *Service) DeployStream(ctx context.Context, userID uuid.UUID, name string, r io.Reader, filename string) (*ProjectResponse, error) {
	archivePath, archiveSize, err := s.storeArchiveTemp(r)
	if err != nil {
		return nil, err
	}
	defer os.Remove(archivePath)
	return s.deployArchive(ctx, userID, name, archivePath, archiveSize, filename)
}

// Redeploy re-uploads and re-publishes an existing project.
func (s *Service) Redeploy(ctx context.Context, userID uuid.UUID, projectID uuid.UUID, data []byte, filename string) (*ProjectResponse, error) {
	archivePath, archiveSize, err := s.storeArchiveTemp(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer os.Remove(archivePath)
	return s.redeployArchive(ctx, userID, projectID, archivePath, archiveSize, filename)
}

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

// List returns all static projects for a user.
func (s *Service) List(userID uuid.UUID) ([]ProjectResponse, error) {
	var projects []db.StaticProject
	s.DB.Where("user_id = ?", userID).Order("created_at desc").Find(&projects)

	used, max, _ := s.QuotaInfo(userID)
	responses := make([]ProjectResponse, len(projects))
	for i, p := range projects {
		responses[i] = ProjectResponse{
			StaticProject: p,
			UIState:       ComputeUIState(&p),
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
	return &ProjectResponse{
		StaticProject: project,
		UIState:       ComputeUIState(&project),
		WebsitesUsed:  used,
		MaxWebsites:   max,
	}, nil
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
	s.DB.First(&project, "id = ?", project.ID)
	used, max, _ := s.QuotaInfo(userID)
	return &ProjectResponse{
		StaticProject: project,
		UIState:       ComputeUIState(&project),
		WebsitesUsed:  used,
		MaxWebsites:   max,
	}, nil
}

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
		if !boundMap[d.ID] {
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
	used, max, _ := s.QuotaInfo(userID)
	return &ProjectResponse{
		StaticProject: project,
		UIState:       ComputeUIState(&project),
		WebsitesUsed:  used,
		MaxWebsites:   max,
	}, nil
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
	used, max, _ := s.QuotaInfo(userID)
	return &ProjectResponse{
		StaticProject: project,
		UIState:       ComputeUIState(&project),
		WebsitesUsed:  used,
		MaxWebsites:   max,
	}, nil
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
	if !s.domainSSLReady(userID, &project) {
		return nil, ErrSSLConditionNotMet
	}

	if err := s.provisionCustomDomainTLS(project); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPublishFailed, err)
	}

	now := time.Now()
	s.DB.Model(&project).Updates(map[string]any{
		"domain_binding_status": "ssl_active",
		"domain_tls_enabled_at": &now,
	})
	s.DB.First(&project, "id = ?", project.ID)
	used, max, _ := s.QuotaInfo(userID)
	return &ProjectResponse{
		StaticProject: project,
		UIState:       ComputeUIState(&project),
		WebsitesUsed:  used,
		MaxWebsites:   max,
	}, nil
}

func (s *Service) domainSSLReady(userID uuid.UUID, project *db.StaticProject) bool {
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
	if domain.ARecordStatus != db.ARecordStatusVerified {
		return false
	}

	now := time.Now()
	project.DomainLastDNSResult = "ok"
	project.DomainLastDNSCheckAt = &now
	s.DB.Model(project).Updates(map[string]any{
		"domain_last_dns_result":   project.DomainLastDNSResult,
		"domain_last_dns_check_at": project.DomainLastDNSCheckAt,
	})
	return true
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
