package staticprojects

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// storeArchiveTemp writes the reader to a temporary file and returns the path.
func (s *Service) storeArchiveTemp(reader io.Reader) (tmpPath string, archiveLen int64, err error) {
	tmpFile, err := os.CreateTemp("", "gomail-zip-*")
	if err != nil {
		return "", 0, fmt.Errorf("temp file create: %w", err)
	}
	defer tmpFile.Close()

	maxSize := s.Config.StaticSitesMaxArchiveBytes
	limitedReader := io.LimitReader(reader, maxSize+1)
	written, err := io.Copy(tmpFile, limitedReader)
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", 0, fmt.Errorf("copy to temp: %w", err)
	}
	if written > maxSize {
		os.Remove(tmpFile.Name())
		return "", 0, ErrArchiveTooLarge
	}
	return tmpFile.Name(), written, nil
}

// extractAndValidateArchive opens the zip once, validates entries, checks both
// compressed and extracted limits, and extracts files to destPath.
func (s *Service) extractAndValidateArchive(zipPath, destPath string) (files []string, rootFolder string, fileCount int, err error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, "", 0, fmt.Errorf("open zip: %w", err)
	}
	defer reader.Close()

	maxArchiveBytes := s.Config.StaticSitesMaxArchiveBytes
	maxExtracted := s.Config.StaticSitesMaxExtractedBytes
	maxFiles := s.Config.StaticSitesMaxFileCount

	var totalUncompressed int64
	var totalExtracted int64

	for _, f := range reader.File {
		if err := s.validateZipEntry(f); err != nil {
			return nil, "", 0, err
		}
		if f.FileInfo().IsDir() {
			continue
		}

		uncompressed := int64(f.UncompressedSize64)
		// Check uncompressed size against archive limit (was validateArchiveSize)
		if maxArchiveBytes > 0 && totalUncompressed+uncompressed > maxArchiveBytes {
			return nil, "", 0, ErrArchiveTooLarge
		}

		// Config-driven limits
		if maxFiles > 0 && fileCount >= maxFiles {
			return nil, "", 0, fmt.Errorf("file count exceeds limit: %d", maxFiles)
		}
		if maxExtracted > 0 && totalExtracted+uncompressed > maxExtracted {
			return nil, "", 0, fmt.Errorf("extracted size exceeds limit: %d", maxExtracted)
		}

		name := filepath.ToSlash(f.Name)
		entryDest := filepath.Join(destPath, name)
		if err := os.MkdirAll(filepath.Dir(entryDest), 0o755); err != nil {
			return nil, "", 0, fmt.Errorf("mkdir: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", 0, fmt.Errorf("open entry %s: %w", f.Name, err)
		}
		outFile, err := os.OpenFile(entryDest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return nil, "", 0, fmt.Errorf("create file %s: %w", name, err)
		}
		// Limit to UncompressedSize64 to prevent zip bomb at write time
		_, err = io.CopyN(outFile, rc, uncompressed)
		rc.Close()
		outFile.Close()
		if err != nil && err != io.EOF {
			return nil, "", 0, fmt.Errorf("extract %s: %w", name, err)
		}
		files = append(files, name)
		fileCount++
		totalUncompressed += uncompressed
		totalExtracted += uncompressed
	}

	rootFolder, err = detectPublishRoot(files)
	if err != nil {
		return nil, "", 0, err
	}
	return files, rootFolder, fileCount, nil
}

// extractAndValidate is a convenience wrapper that stores a zip and extracts it.
func (s *Service) extractAndValidate(zipPath, destPath string) (rootFolder string, fileCount int, err error) {
	_, rootFolder, fileCount, err = s.extractAndValidateArchive(zipPath, destPath)
	return
}
