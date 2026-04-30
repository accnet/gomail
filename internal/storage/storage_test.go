package storage

import "testing"

func TestSafeFilenameBlocksTraversal(t *testing.T) {
	got := SafeFilename("../../evil.exe")
	if got != "evil.exe" {
		t.Fatalf("expected base filename, got %q", got)
	}
}

func TestScanBlocksDangerousExtension(t *testing.T) {
	got := Scan("run.ps1", "text/plain", "text/plain", true)
	if got.Status != "flagged" || !got.IsBlocked {
		t.Fatalf("expected blocked flagged file, got %+v", got)
	}
}
