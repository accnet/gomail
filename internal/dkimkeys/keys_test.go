package dkimkeys

import (
	"strings"
	"testing"
)

func TestEncryptDecryptPrivateKeyPEM(t *testing.T) {
	raw := "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"
	encrypted, err := EncryptPrivateKeyPEM(raw, "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(encrypted) {
		t.Fatalf("expected encrypted value, got %q", encrypted)
	}
	if strings.Contains(encrypted, "PRIVATE KEY") {
		t.Fatal("encrypted value contains plaintext private key")
	}

	decrypted, err := DecryptPrivateKeyPEM(encrypted, "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != raw {
		t.Fatalf("decrypted value mismatch")
	}
}

func TestDecryptAllowsLegacyPlaintextPEM(t *testing.T) {
	raw := "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"
	decrypted, err := DecryptPrivateKeyPEM(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != raw {
		t.Fatalf("legacy plaintext mismatch")
	}
}
