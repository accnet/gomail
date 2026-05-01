package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// StaticSitesPaths holds the directory paths for a static project.
type StaticSitesPaths struct {
	Staging   string
	Live      string
	Thumbnail string
}

// StaticSitesManager manages filesystem paths for static site projects.
type StaticSitesManager struct {
	Root string
}

// NewStaticSitesManager creates a new manager with the given root directory.
func NewStaticSitesManager(root string) *StaticSitesManager {
	return &StaticSitesManager{Root: root}
}

// ProjectPaths returns the staging, live, and thumbnail paths for a project.
func (m *StaticSitesManager) ProjectPaths(projectID uuid.UUID) StaticSitesPaths {
	id := projectID.String()
	return StaticSitesPaths{
		Staging:   filepath.Join(m.Root, "staging", id),
		Live:      filepath.Join(m.Root, "live", id),
		Thumbnail: filepath.Join(m.Root, "live", id, "thumbnail.png"),
	}
}

// EnsureProjectDirs creates the staging and live directories for a project.
func (m *StaticSitesManager) EnsureProjectDirs(projectID uuid.UUID) error {
	paths := m.ProjectPaths(projectID)
	for _, dir := range []string{paths.Staging, paths.Live} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

// CleanupProjectDirs removes staging and live directories for a project.
func (m *StaticSitesManager) CleanupProjectDirs(projectID uuid.UUID) error {
	paths := m.ProjectPaths(projectID)
	for _, dir := range []string{paths.Staging, paths.Live} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove dir %s: %w", dir, err)
		}
	}
	return nil
}

// CleanupStaging removes only the staging directory.
func (m *StaticSitesManager) CleanupStaging(projectID uuid.UUID) error {
	paths := m.ProjectPaths(projectID)
	return os.RemoveAll(paths.Staging)
}
