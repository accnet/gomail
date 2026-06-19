package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/staticprojects"
)

// projectCache provides LRU-style caching for resolved projects
type projectCache struct {
	mu      sync.RWMutex
	items   map[string]*cacheItem
	maxSize int
}

type cacheItem struct {
	project  *staticprojects.ResolvedProject
	expiry   time.Time
	lastUsed time.Time
}

func newProjectCache(maxSize int, ttl time.Duration) *projectCache {
	cache := &projectCache{
		items:   make(map[string]*cacheItem),
		maxSize: maxSize,
	}
	// Start background cleanup goroutine
	go cache.cleanup(ttl)
	return cache
}

func (c *projectCache) get(key string) (*staticprojects.ResolvedProject, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(item.expiry) {
		return nil, false
	}
	item.lastUsed = time.Now()
	return item.project, true
}

func (c *projectCache) set(key string, project *staticprojects.ResolvedProject, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Evict oldest if at capacity
	if len(c.items) >= c.maxSize {
		c.evictOldest()
	}
	
	c.items[key] = &cacheItem{
		project:  project,
		expiry:   time.Now().Add(ttl),
		lastUsed: time.Now(),
	}
}

func (c *projectCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for k, v := range c.items {
		if oldestKey == "" || v.lastUsed.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.lastUsed
		}
	}
	if oldestKey != "" {
		delete(c.items, oldestKey)
	}
}

func (c *projectCache) cleanup(ttl time.Duration) {
	ticker := time.NewTicker(ttl / 2)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if now.After(v.expiry) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	resolver := staticprojects.NewHostResolver(database, cfg.StaticSitesBaseDomain, cfg.SaaSDomain)
	
	// Initialize project cache with 1000 entries and 5-minute TTL
	cache := newProjectCache(1000, 5*time.Minute)

	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	// Main handler: resolves Host header → project → serves files
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host := r.Host

		// Skip health check
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ok":true}`))
			return
		}

		// Normalize host for cache key
		cacheKey := host
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			cacheKey = host[:idx]
		}

		// Try cache first
		project, cached := cache.get(cacheKey)
		if !cached {
			// Resolve from database
			var resolveErr error
			project, resolveErr = resolver.Resolve(host)
			if resolveErr != nil {
				http.NotFound(w, r)
				return
			}
			// Cache result (even nil for negative caching)
			cache.set(cacheKey, project, 5*time.Minute)
		}

		if project == nil {
			http.NotFound(w, r)
			return
		}

		if !project.IsActive || project.Status != "published" {
			http.Error(w, "Site is disabled", http.StatusNotFound)
			return
		}

		// Build file path
		cleanPath := strings.TrimPrefix(r.URL.Path, "/")
		filePath := project.RootFolder
		servingIndex := false

		if cleanPath != "" {
			filePath = filepath.Join(project.RootFolder, cleanPath)
		} else {
			// Root → try index.html
			indexPath := filepath.Join(project.RootFolder, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				filePath = indexPath
				servingIndex = true
			}
		}

		// SPA fallback: only browser HTML navigation without an asset extension falls back to index.html.
		if !servingIndex {
			info, err := os.Stat(filePath)
			if err != nil || info.IsDir() {
				if shouldSPAFallback(r, cleanPath) {
					fallbackPath := filepath.Join(project.RootFolder, "index.html")
					if _, err2 := os.Stat(fallbackPath); err2 == nil {
						filePath = fallbackPath
					}
				}
			}
		}

		// Security: ensure the resolved path is within the project root
		absRoot, _ := filepath.Abs(project.RootFolder)
		absFile, _ := filepath.Abs(filePath)
		if !isWithinRoot(absRoot, absFile) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Set caching headers
		ext := strings.ToLower(filepath.Ext(filePath))
		switch ext {
		case ".html", ".htm":
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
		case ".css", ".js", ".json":
			w.Header().Set("Cache-Control", "public, max-age=3600")
		case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico":
			w.Header().Set("Cache-Control", "public, max-age=86400")
		case ".woff", ".woff2", ".ttf", ".otf", ".eot":
			w.Header().Set("Cache-Control", "public, max-age=86400")
		default:
			w.Header().Set("Cache-Control", "public, max-age=300")
		}

		http.ServeFile(w, r, filePath)
	})

	// Legacy: /static/<subdomain>/ path — fallback for backward compat
	mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/static/"), "/", 2)
		if len(parts) < 1 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		subdomain := parts[0]

		var project struct {
			ID         string
			RootFolder string
			IsActive   bool
			Status     string
		}
		err := database.Table("static_projects").
			Select("id, root_folder, is_active, status").
			Where("subdomain = ? AND deleted_at IS NULL", subdomain).
			Scan(&project).Error
		if err != nil || project.ID == "" {
			http.NotFound(w, r)
			return
		}
		if !project.IsActive || project.Status != "published" {
			http.Error(w, "Site is disabled", http.StatusNotFound)
			return
		}

		filePath := project.RootFolder
		servingIndex := false
		if len(parts) == 2 && parts[1] != "" {
			filePath = filepath.Join(project.RootFolder, parts[1])
		} else {
			indexPath := filepath.Join(project.RootFolder, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				filePath = indexPath
				servingIndex = true
			}
		}

		// SPA fallback
		if !servingIndex {
			if _, err := os.Stat(filePath); err != nil {
				requestPath := ""
				if len(parts) == 2 {
					requestPath = parts[1]
				}
				if shouldSPAFallback(r, requestPath) {
					fallbackPath := filepath.Join(project.RootFolder, "index.html")
					if _, err2 := os.Stat(fallbackPath); err2 == nil {
						filePath = fallbackPath
					}
				}
			}
		}

		absRoot, _ := filepath.Abs(project.RootFolder)
		absFile, _ := filepath.Abs(filePath)
		if !isWithinRoot(absRoot, absFile) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		ext := strings.ToLower(filepath.Ext(filePath))
		switch ext {
		case ".html", ".htm":
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		case ".css", ".js":
			w.Header().Set("Cache-Control", "public, max-age=3600")
		case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico":
			w.Header().Set("Cache-Control", "public, max-age=86400")
		default:
			w.Header().Set("Cache-Control", "public, max-age=300")
		}

		http.ServeFile(w, r, filePath)
	})

	server := &http.Server{
		Addr:         cfg.StaticServerAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		// Add ReadHeaderTimeout to prevent Slowloris attacks
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("static server listening on %s", cfg.StaticServerAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("static server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down static server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("static server forced shutdown: %v", err)
	}
	fmt.Println("static server stopped")
}

func shouldSPAFallback(r *http.Request, requestPath string) bool {
	return r.Method == http.MethodGet &&
		strings.Contains(r.Header.Get("Accept"), "text/html") &&
		filepath.Ext(requestPath) == ""
}

func isWithinRoot(absRoot, absFile string) bool {
	if absFile == absRoot {
		return true
	}
	return strings.HasPrefix(absFile, absRoot+string(os.PathSeparator))
}
