package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gomail/internal/db"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// StaticSiteMiddleware checks if the incoming Host header matches a static project
// bound to the SaaS domain. If so, it serves the static site files directly,
// bypassing the normal web app routing.
type StaticSiteMiddleware struct {
	DB          *gorm.DB
	SaaSDomain  string
	LandingRoot string
}

// NewStaticSiteMiddleware creates middleware for the exact SaaS domain.
func NewStaticSiteMiddleware(database *gorm.DB, saasDomain string, landingRoot string) *StaticSiteMiddleware {
	return &StaticSiteMiddleware{DB: database, SaaSDomain: saasDomain, LandingRoot: landingRoot}
}

// Handler returns a Gin middleware that checks for static project bindings.
func (m *StaticSiteMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		host := c.Request.Host
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")

		// Only intercept if the host IS the SaaS domain itself (exact match).
		// Subdomains of the SaaS domain are handled by the static-server (nginx default_server).
		saasDomain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(m.SaaSDomain)), ".")
		if saasDomain == "" || host != saasDomain {
			c.Next()
			return
		}

		if isAppOrAPIPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		if db.GetSaaSDomainMode(m.DB) == db.SaaSDomainModeLanding {
			serveStaticProjectFile(c, m.LandingRoot)
			return
		}

		c.Redirect(http.StatusFound, "/app/")
		c.Abort()
	}
}

func isAppOrAPIPath(urlPath string) bool {
	return urlPath == "/healthz" ||
		urlPath == "/app" ||
		strings.HasPrefix(urlPath, "/app/") ||
		urlPath == "/api" ||
		strings.HasPrefix(urlPath, "/api/")
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
