package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestStaticSitesManagerProjectPaths(t *testing.T) {
	root := t.TempDir()
	projectID := uuid.New()

	paths := NewStaticSitesManager(root).ProjectPaths(projectID)
	id := projectID.String()

	if paths.Staging != filepath.Join(root, "staging", id) {
		t.Fatalf("staging path = %q", paths.Staging)
	}
	if paths.Live != filepath.Join(root, "live", id) {
		t.Fatalf("live path = %q", paths.Live)
	}
	if paths.Thumbnail != filepath.Join(root, "live", id, "thumbnail.png") {
		t.Fatalf("thumbnail path = %q", paths.Thumbnail)
	}
}

func TestStaticSitesManagerEnsureAndCleanupProjectDirs(t *testing.T) {
	root := t.TempDir()
	projectID := uuid.New()
	manager := NewStaticSitesManager(root)
	paths := manager.ProjectPaths(projectID)

	if err := manager.EnsureProjectDirs(projectID); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{paths.Staging, paths.Live} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}

	if err := manager.CleanupProjectDirs(projectID); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{paths.Staging, paths.Live} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, err=%v", dir, err)
		}
	}
}
