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
	"syscall"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/staticprojects"
)

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

		project, err := resolver.Resolve(host)
		if err != nil || project == nil {
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
