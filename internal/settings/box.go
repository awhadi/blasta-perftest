// Package settings keeps what an administrator configures in the web UI (sign-in
// rules, single sign-on, email, guest limits) in the database. Secrets such as
// the SMTP password and the OIDC client secret are encrypted at rest.
package settings

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Box encrypts small secrets with AES-256-GCM.
type Box struct{ aead cipher.AEAD }

// NewBox derives an encryption key from secret key material.
func NewBox(secret []byte) (*Box, error) {
	if len(secret) < 16 {
		return nil, errors.New("the secret key must be at least 16 characters")
	}
	// This label is part of the key derivation: changing it makes every saved secret
	// (SMTP password, SSO client secret, bot-check key) undecryptable. See
	// TestSealedSecretsStayReadable.
	key, err := hkdf.Key(sha256.New, secret, []byte("blasta settings v1"), "aes-256-gcm", 32)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	return &Box{aead: g}, nil
}

// Seal encrypts plain. An empty string stays empty.
func (b *Box) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.RawStdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open decrypts a Seal result.
func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	if err != nil || !strings.HasPrefix(sealed, "v1:") || len(raw) < b.aead.NonceSize() {
		return "", errors.New("stored secret is unreadable")
	}
	n := b.aead.NonceSize()
	out, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("stored secret cannot be decrypted: the secret key changed")
	}
	return string(out), nil
}

// LoadKey finds the encryption key material: BLASTA_SECRET_KEY if set, otherwise
// a random key kept (created on first use) in secret.key inside dataDir, owner
// only. Keep the key with the database when you back it up; with several
// replicas sharing a PostgreSQL database, set BLASTA_SECRET_KEY on all of them.
func LoadKey(env, dataDir string) ([]byte, error) {
	if env = strings.TrimSpace(env); env != "" {
		return []byte(env), nil
	}
	if dataDir == "" {
		return nil, errors.New("no place to keep the encryption key: set BLASTA_SECRET_KEY or BLASTA_DATA_DIR")
	}
	path := filepath.Join(dataDir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		return []byte(strings.TrimSpace(string(b))), nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	key := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return nil, err
	}
	return []byte(key), nil
}
