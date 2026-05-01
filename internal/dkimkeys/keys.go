package dkimkeys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptedPrefix = "enc:v1:"

func EncryptPrivateKeyPEM(privateKeyPEM string, secret string) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", errors.New("DKIM key encryption secret is required")
	}
	block, err := aes.NewCipher(secretKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(privateKeyPEM), nil)
	return encryptedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func DecryptPrivateKeyPEM(value string, secret string) (string, error) {
	if !strings.HasPrefix(value, encryptedPrefix) {
		return value, nil
	}
	if strings.TrimSpace(secret) == "" {
		return "", errors.New("DKIM key encryption secret is required")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encryptedPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(secretKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted DKIM key is too short")
	}
	nonce := raw[:gcm.NonceSize()]
	ciphertext := raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encryptedPrefix)
}

func secretKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}
