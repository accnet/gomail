package staticprojects

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gomail/internal/db"

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
		ID              uuid.UUID
		Subdomain       string
		ThumbnailPath   string
		ThumbnailStatus string
	}

	w.DB.Model(&db.StaticProject{}).
		Select("id, subdomain, thumbnail_path, thumbnail_status").
		Where("status = ? AND is_active = ? AND deleted_at IS NULL", "published", true).
		Find(&projects)

	for _, p := range projects {
		if !w.needsGeneration(p.ID, p.ThumbnailStatus, p.ThumbnailPath) {
			continue
		}
		w.generateThumbnail(p.ID, p.Subdomain, "")
	}
}

func (w *ThumbnailWorker) needsGeneration(projectID uuid.UUID, status, thumbnailPath string) bool {
	switch status {
	case "pending", "failed", "", "processing":
		return true
	case "ready":
		return w.isLegacyPlaceholder(projectID, thumbnailPath)
	default:
		return true
	}
}

func (w *ThumbnailWorker) isLegacyPlaceholder(projectID uuid.UUID, thumbnailPath string) bool {
	path := strings.TrimSpace(thumbnailPath)
	if path == "" {
		path = w.liveThumbnailPath(projectID)
	}
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	if info.Size() <= 128 {
		return true
	}
	file, err := os.Open(path)
	if err != nil {
		return true
	}
	defer file.Close()

	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return true
	}
	if config.Width <= 1 || config.Height <= 1 {
		return true
	}
	return strings.ToLower(format) == "png" && config.Width == 1 && config.Height == 1
}

// liveThumbnailPath returns the absolute path to the thumbnail for a project.
func (w *ThumbnailWorker) liveThumbnailPath(projectID uuid.UUID) string {
	return filepath.Join(w.StorageRoot, "live", projectID.String(), "thumbnail.png")
}

// generateThumbnail generates a thumbnail for the given project.
func (w *ThumbnailWorker) generateThumbnail(projectID uuid.UUID, subdomain, rootFolder string) {
	w.DB.Model(&db.StaticProject{}).Where("id = ?", projectID).Update("thumbnail_status", "processing")

	thumbnailPath := w.liveThumbnailPath(projectID)

	// Try to use chromium/screenshot tools if available
	// Fallback: generate a simple placeholder
	if err := w.takeScreenshot(subdomain, thumbnailPath); err != nil {
		log.Printf("thumbnail screenshot failed for %s: %v", subdomain, err)
		// Generate a simple HTML-based placeholder instead
		if err2 := w.generatePlaceholder(thumbnailPath); err2 != nil {
			log.Printf("thumbnail placeholder failed for %s: %v", subdomain, err2)
			w.DB.Model(&db.StaticProject{}).Where("id = ?", projectID).Update("thumbnail_status", "failed")
			return
		}
	}

	// Check if file was created
	if _, err := os.Stat(thumbnailPath); err != nil {
		w.DB.Model(&db.StaticProject{}).Where("id = ?", projectID).Update("thumbnail_status", "failed")
		return
	}

	w.DB.Model(&db.StaticProject{}).
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

	const (
		width  = 1280
		height = 720
	)

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		shade := uint8(232 - (y * 28 / height))
		band := uint8(244 - (y * 18 / height))
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: shade, G: shade + 8, B: band, A: 255})
		}
	}

	browserRect := image.Rect(96, 72, width-96, height-92)
	draw.Draw(img, browserRect, &image.Uniform{C: color.RGBA{R: 255, G: 255, B: 255, A: 255}}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(browserRect.Min.X, browserRect.Min.Y, browserRect.Max.X, browserRect.Min.Y+72), &image.Uniform{C: color.RGBA{R: 226, G: 232, B: 240, A: 255}}, image.Point{}, draw.Src)

	accent := color.RGBA{R: 37, G: 99, B: 235, A: 255}
	muted := color.RGBA{R: 203, G: 213, B: 225, A: 255}
	soft := color.RGBA{R: 241, G: 245, B: 249, A: 255}

	for i := 0; i < 3; i++ {
		dot := image.Rect(browserRect.Min.X+28+(i*24), browserRect.Min.Y+26, browserRect.Min.X+42+(i*24), browserRect.Min.Y+40)
		draw.Draw(img, dot, &image.Uniform{C: muted}, image.Point{}, draw.Src)
	}

	draw.Draw(img, image.Rect(browserRect.Min.X+28, browserRect.Min.Y+110, browserRect.Max.X-28, browserRect.Min.Y+356), &image.Uniform{C: soft}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(browserRect.Min.X+28, browserRect.Min.Y+388, browserRect.Max.X-28, browserRect.Min.Y+434), &image.Uniform{C: accent}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(browserRect.Min.X+28, browserRect.Min.Y+462, browserRect.Min.X+380, browserRect.Min.Y+492), &image.Uniform{C: muted}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(browserRect.Min.X+28, browserRect.Min.Y+510, browserRect.Max.X-240, browserRect.Min.Y+536), &image.Uniform{C: muted}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(browserRect.Min.X+28, browserRect.Min.Y+552, browserRect.Max.X-320, browserRect.Min.Y+578), &image.Uniform{C: muted}, image.Point{}, draw.Src)

	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	return png.Encode(file, img)
}
