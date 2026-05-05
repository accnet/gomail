package staticprojects

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	maxPublishRetries   = 10
	publishRetrySleepMs = 500
)

// publishAtomic atomically publishes files from staging to live directory.
// Uses a .new → .old rollback strategy. Preserves existing thumbnail if not in staging.
func (s *Service) publishAtomic(stagingDir, liveDir string) error {
	liveNew := liveDir + ".new"
	liveOld := liveDir + ".old"

	// Remove any leftover temp dirs
	os.RemoveAll(liveNew)
	os.RemoveAll(liveOld)

	// Copy staging to live.new
	if err := copyDir(stagingDir, liveNew); err != nil {
		os.RemoveAll(liveNew)
		return fmt.Errorf("copy staging to live.new: %w", err)
	}

	// Preserve existing thumbnail from live into live.new if staging doesn't have one
	oldThumb := filepath.Join(liveDir, "thumbnail.png")
	newThumb := filepath.Join(liveNew, "thumbnail.png")
	if _, err := os.Stat(oldThumb); err == nil {
		if _, err := os.Stat(newThumb); os.IsNotExist(err) {
			copyFile(oldThumb, newThumb)
		}
	}

	// Atomically swap live → live.old, then live.new → live
	if _, err := os.Stat(liveDir); err == nil {
		if err := os.Rename(liveDir, liveOld); err != nil {
			os.RemoveAll(liveNew)
			return fmt.Errorf("rename live to old: %w", err)
		}
	}

	if err := os.Rename(liveNew, liveDir); err != nil {
		// Rollback: restore old
		os.Rename(liveOld, liveDir)
		os.RemoveAll(liveNew)
		return fmt.Errorf("rename new to live: %w", err)
	}

	// Clean up old
	os.RemoveAll(liveOld)
	return nil
}

// publishRetry attempts to publish with retries for transient file system errors.
func (s *Service) publishRetry(stagingDir, liveDir string) error {
	var err error
	for i := 0; i < maxPublishRetries; i++ {
		err = s.publishAtomic(stagingDir, liveDir)
		if err == nil {
			return nil
		}
		// Sleep briefly between retries
		if i < maxPublishRetries-1 {
			time.Sleep(time.Duration(publishRetrySleepMs) * time.Millisecond)
		}
	}
	return fmt.Errorf("publish failed after %d retries: %w", maxPublishRetries, err)
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// copyDir recursively copies a directory from src to dst.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

