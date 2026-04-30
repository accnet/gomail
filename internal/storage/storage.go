package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

type Local struct {
	AttachmentRoot string
	RawRoot        string
}

func NewLocal(attachmentRoot, rawRoot string) *Local {
	return &Local{AttachmentRoot: attachmentRoot, RawRoot: rawRoot}
}

func (s *Local) Ensure() error {
	if err := os.MkdirAll(s.AttachmentRoot, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(s.RawRoot, 0o755)
}

func (s *Local) SaveRaw(userID, emailID uuid.UUID, data []byte) (string, error) {
	dir := filepath.Join(s.RawRoot, userID.String(), emailID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "message.eml")
	return path, os.WriteFile(path, data, 0o640)
}

func (s *Local) SaveAttachment(userID, emailID, attachmentID uuid.UUID, filename string, r io.Reader) (path string, sha string, size int64, sniffed string, err error) {
	dir := filepath.Join(s.AttachmentRoot, userID.String(), emailID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", 0, "", err
	}
	safe := SafeFilename(filename)
	path = filepath.Join(dir, attachmentID.String()+"-"+safe)
	f, err := os.Create(path)
	if err != nil {
		return "", "", 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 512)
	n, readErr := io.ReadFull(r, buf)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return "", "", 0, "", readErr
	}
	sniffed = http.DetectContentType(buf[:n])
	if _, err := f.Write(buf[:n]); err != nil {
		return "", "", 0, "", err
	}
	h.Write(buf[:n])
	written, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return "", "", 0, "", err
	}
	size = int64(n) + written
	return path, hex.EncodeToString(h.Sum(nil)), size, sniffed, nil
}

func SafeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		name = "attachment"
	}
	name = strings.ReplaceAll(name, "\x00", "")
	re := regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	name = re.ReplaceAllString(name, "_")
	if len(name) > 180 {
		ext := filepath.Ext(name)
		name = strings.TrimSuffix(name[:180], ".") + ext
	}
	return name
}

type ScanResult struct {
	Status    string
	Result    string
	IsBlocked bool
}

func Scan(filename, contentType, sniffed string, blockFlagged bool) ScanResult {
	ext := strings.ToLower(filepath.Ext(filename))
	dangerous := map[string]bool{".exe": true, ".bat": true, ".cmd": true, ".js": true, ".vbs": true, ".scr": true, ".ps1": true, ".sh": true}
	if dangerous[ext] {
		return ScanResult{Status: "flagged", Result: "dangerous extension", IsBlocked: blockFlagged}
	}
	ct := strings.ToLower(contentType + " " + sniffed)
	if strings.Contains(ct, "application/x-msdownload") || strings.Contains(ct, "application/x-sh") || strings.Contains(ct, "javascript") {
		return ScanResult{Status: "flagged", Result: "dangerous content type", IsBlocked: blockFlagged}
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(ext)
	}
	return ScanResult{Status: "clean", Result: "", IsBlocked: false}
}
