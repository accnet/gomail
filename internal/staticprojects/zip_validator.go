package staticprojects

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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