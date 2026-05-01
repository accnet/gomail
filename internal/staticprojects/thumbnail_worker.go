package staticprojects

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ThumbnailWorker generates thumbnail images for deployed static projects.
type ThumbnailWorker struct {
	DB            *gorm.DB
	StorageRoot   string
	ScreenshotURL func(subdomain string) string
}

// NewThumbnailWorker creates a new ThumbnailWorker.
func NewThumbnailWorker(database *gorm.DB, storageRoot string, screenshotURLFn func(subdomain string) string) *ThumbnailWorker {
	return &ThumbnailWorker{
		DB:            database,
		StorageRoot:   storageRoot,
		ScreenshotURL: screenshotURLFn,
	}
}

// Run starts the thumbnail worker background loop.
func (w *ThumbnailWorker) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Process immediately on start
	w.processPending()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processPending()
		}
	}
}

// processPending finds projects needing thumbnail generation.
func (w *ThumbnailWorker) processPending() {
	var projects []struct {
		ID         uuid.UUID
		Subdomain  string
		RootFolder string
	}

	w.DB.Table("static_projects").
		Select("id, subdomain, root_folder").
		Where("status = ? AND is_active = ? AND deleted_at IS NULL", "published", true).
		Where("thumbnail_status IN ?", []string{"pending", "failed", ""}).
		Find(&projects)

	for _, p := range projects {
		w.generateThumbnail(p.ID, p.Subdomain, p.RootFolder)
	}
}

// generateThumbnail generates a thumbnail for the given project.
func (w *ThumbnailWorker) generateThumbnail(projectID uuid.UUID, subdomain, rootFolder string) {
	w.DB.Model(&struct{}{}).Table("static_projects").
		Where("id = ?", projectID).
		Update("thumbnail_status", "processing")

	thumbnailPath := filepath.Join(rootFolder, "thumbnail.png")

	// Try to use chromium/screenshot tools if available
	// Fallback: generate a simple placeholder
	if err := w.takeScreenshot(subdomain, thumbnailPath); err != nil {
		log.Printf("thumbnail screenshot failed for %s: %v", subdomain, err)
		// Generate a simple HTML-based placeholder instead
		if err2 := w.generatePlaceholder(thumbnailPath); err2 != nil {
			log.Printf("thumbnail placeholder failed for %s: %v", subdomain, err2)
			w.DB.Model(&struct{}{}).Table("static_projects").
				Where("id = ?", projectID).
				Update("thumbnail_status", "failed")
			return
		}
	}

	// Check if file was created
	if _, err := os.Stat(thumbnailPath); err != nil {
		w.DB.Model(&struct{}{}).Table("static_projects").
			Where("id = ?", projectID).
			Update("thumbnail_status", "failed")
		return
	}

	w.DB.Model(&struct{}{}).Table("static_projects").
		Where("id = ?", projectID).
		Updates(map[string]any{
			"thumbnail_path":   thumbnailPath,
			"thumbnail_status": "ready",
		})
}

// takeScreenshot uses chromium to take a screenshot of the site.
func (w *ThumbnailWorker) takeScreenshot(subdomain, outputPath string) error {
	screenshotURL := w.ScreenshotURL(subdomain)
	if screenshotURL == "" {
		return errors.New("thumbnail screenshot URL is empty")
	}

	// Check if chromium/go-chromecapture is available
	if _, err := exec.LookPath("chromium-browser"); err != nil {
		if _, err2 := exec.LookPath("chromium"); err2 != nil {
			if _, err3 := exec.LookPath("google-chrome"); err3 != nil {
				return errors.New("no supported browser available")
			}
		}
	}

	// Use chromium headless to take screenshot
	args := []string{
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--screenshot=" + outputPath,
		"--window-size=1280,720",
		"--timeout=10000",
		screenshotURL,
	}

	cmd := exec.Command("chromium-browser", args...)
	// Also try "chromium" or "google-chrome"
	if _, err := exec.LookPath("chromium-browser"); err != nil {
		if _, err2 := exec.LookPath("chromium"); err2 == nil {
			cmd = exec.Command("chromium", args...)
		} else if _, err3 := exec.LookPath("google-chrome"); err3 == nil {
			cmd = exec.Command("google-chrome", args...)
		} else {
			return errors.New("no supported browser available")
		}
	}

	return cmd.Run()
}

// generatePlaceholder creates a simple placeholder image using HTML+canvas via a tiny script.
func (w *ThumbnailWorker) generatePlaceholder(outputPath string) error {
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
		0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x60, 0x00, 0x00, 0x00,
		0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc, 0x33, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	return os.WriteFile(outputPath, png, 0644)
}
