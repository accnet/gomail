package relay

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func TestSignDKIMAddsSignatureHeader(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	raw := []byte("From: sender@example.com\r\nTo: user@example.net\r\nSubject: Test\r\n\r\nHello")

	signed, err := signDKIM(raw, "example.com", "gomail", privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(signed), "DKIM-Signature:") {
		t.Fatalf("signed message missing DKIM signature: %q", string(signed[:min(len(signed), 80)]))
	}
	if !strings.Contains(string(signed), "d=example.com") {
		t.Fatalf("signed message missing DKIM domain: %q", string(signed))
	}
}
