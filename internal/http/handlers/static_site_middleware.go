package handlers

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// StaticSiteMiddleware checks if the incoming Host header matches a static project
// bound to the SaaS domain. If so, it serves the static site files directly,
// bypassing the normal web app routing.
type StaticSiteMiddleware struct {
	DB         *gorm.DB
	SaaSDomain string
}

// NewStaticSiteMiddleware creates middleware that intercepts requests for the
// SaaS domain when a static project is bound to it, and serves the static files.
func NewStaticSiteMiddleware(database *gorm.DB, saasDomain string) *StaticSiteMiddleware {
	return &StaticSiteMiddleware{DB: database, SaaSDomain: saasDomain}
}

// Handler returns a Gin middleware that checks for static project bindings.
func (m *StaticSiteMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		host := c.Request.Host
		// Strip port
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			host = host[:idx]
		}

		// Only intercept if the host IS the SaaS domain itself (exact match).
		// Subdomains of the SaaS domain are handled by the static-server (nginx default_server).
		if m.SaaSDomain == "" || host != m.SaaSDomain {
			c.Next()
			return
		}

		// Look for a static project bound to this domain. Unlike HostResolver, we
		// do NOT require ssl_active — the SaaS domain already has TLS via nginx.
		var project struct {
			ID         uuid.UUID
			RootFolder string
			IsActive   bool
			Status     string
		}
		err := m.DB.Table("static_projects").
			Select("id, root_folder, is_active, status").
			Where("assigned_domain = ? AND domain_binding_status IN ? AND deleted_at IS NULL",
				host, []string{"assigned", "ssl_active"}).
			Scan(&project).Error

		if err != nil || project.ID == uuid.Nil || !project.IsActive || project.Status != "published" {
			// No static project bound → fall through to normal web app.
			c.Next()
			return
		}

		// Serve the static site file.
		serveStaticProjectFile(c, project.RootFolder)
	}
}

// serveStaticProjectFile serves a file from the project's root folder.
// Falls through to the next handler if the file doesn't exist.
func serveStaticProjectFile(c *gin.Context, rootFolder string) {
	urlPath := c.Request.URL.Path

	// Normalize: strip trailing slash (except for root)
	if urlPath != "/" && strings.HasSuffix(urlPath, "/") {
		urlPath = strings.TrimSuffix(urlPath, "/")
	}

	// Build the file path
	cleanPath := strings.TrimPrefix(urlPath, "/")
	var filePath string
	if cleanPath == "" {
		filePath = filepath.Join(rootFolder, "index.html")
	} else {
		filePath = filepath.Join(rootFolder, cleanPath)
	}

	// If the path is a directory, try index.html
	if info, err := os.Stat(filePath); err == nil && info.IsDir() {
		filePath = filepath.Join(filePath, "index.html")
	}

	// Check if the file exists
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		// For SPA-like behavior: if the file doesn't exist, serve index.html
		// (but not for /app/ paths which belong to the web app)
		if !strings.HasPrefix(urlPath, "/app/") && !strings.HasPrefix(urlPath, "/api/") {
			indexPath := filepath.Join(rootFolder, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				filePath = indexPath
			} else {
				// No index.html either, fall through to web app
				c.Next()
				return
			}
		} else {
			// /app/ or /api/ paths → fall through to web app/API
			c.Next()
			return
		}
	}

	// Security: ensure path is within root folder
	absRoot, _ := filepath.Abs(rootFolder)
	absFile, _ := filepath.Abs(filePath)
	if !strings.HasPrefix(absFile, absRoot+string(filepath.Separator)) && absFile != absRoot {
		c.Next()
		return
	}

	// Set caching headers
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".html", ".htm":
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Pragma", "no-cache")
	case ".css", ".js", ".json":
		c.Header("Cache-Control", "public, max-age=3600")
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico":
		c.Header("Cache-Control", "public, max-age=86400")
	case ".woff", ".woff2", ".ttf", ".otf", ".eot":
		c.Header("Cache-Control", "public, max-age=86400")
	default:
		c.Header("Cache-Control", "public, max-age=300")
	}

	c.File(filePath)
	c.Abort()
}
