package relay

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/emersion/go-msgauth/dkim"
)

func signDKIM(body []byte, domain string, selector string, privateKeyPEM string) ([]byte, error) {
	signer, err := parseDKIMPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}

	var signed bytes.Buffer
	err = dkim.Sign(&signed, bytes.NewReader(body), &dkim.SignOptions{
		Domain:                 domain,
		Selector:               selector,
		Signer:                 signer,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
		HeaderKeys:             []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Content-Type"},
	})
	if err != nil {
		return nil, err
	}
	return signed.Bytes(), nil
}

func parseDKIMPrivateKey(raw string) (crypto.Signer, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("private key does not implement crypto.Signer")
	}
	return signer, nil
}
