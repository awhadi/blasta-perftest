// Package auth provides sign-in for the BLASTA web UI: local accounts, sessions,
// registration with approval, and single sign-on with OpenID Connect.
//
// It depends only on the standard library. Passwords are hashed with
// PBKDF2-HMAC-SHA256, session tokens are stored only as hashes, and every
// credential comparison is constant time.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// pbkdf2Iterations follows the OWASP guidance for PBKDF2-HMAC-SHA256. Tests
// lower it so the suite stays fast; nothing else changes it.
var pbkdf2Iterations = 600_000

const (
	saltBytes = 16
	keyBytes  = 32
	// maxPasswordBytes bounds the input so a huge password cannot be used to burn CPU.
	maxPasswordBytes = 1024
	minPasswordRunes = 10
)

// HashPassword returns "pbkdf2-sha256$iterations$salt$hash" (base64, unpadded).
func HashPassword(password string) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", ErrWeakPassword("the password is too long")
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, keyBytes)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches an encoded hash. It runs the
// full derivation and a constant-time comparison, and returns false for any
// malformed hash.
func VerifyPassword(password, encoded string) bool {
	if len(password) > maxPasswordBytes {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 || iter > 10_000_000 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when the account does not exist, so a login for
// an unknown email takes as long as one for a real account.
var dummyHash, _ = HashPassword("blasta-timing-equaliser")

// WeakPasswordError explains why a password was refused.
type WeakPasswordError string

func (e WeakPasswordError) Error() string { return string(e) }

// ErrWeakPassword builds a WeakPasswordError.
func ErrWeakPassword(msg string) error { return WeakPasswordError(msg) }

// CheckPassword enforces the password policy: length first (NIST guidance
// favours length over composition rules), and nothing that is trivially derived
// from the email address.
func CheckPassword(password, email string) error {
	if utf8.RuneCountInString(password) < minPasswordRunes {
		return ErrWeakPassword("use at least " + strconv.Itoa(minPasswordRunes) + " characters")
	}
	if len(password) > maxPasswordBytes {
		return ErrWeakPassword("the password is too long")
	}
	lower := strings.ToLower(password)
	local := strings.ToLower(strings.SplitN(email, "@", 2)[0])
	if len(local) >= 4 && strings.Contains(lower, local) {
		return ErrWeakPassword("do not use your email address in the password")
	}
	if allSame(password) {
		return ErrWeakPassword("choose a less repetitive password")
	}
	for _, common := range []string{"password", "qwertyuiop", "1234567890", "letmein", "iloveyou"} {
		if strings.Contains(lower, common) && utf8.RuneCountInString(password) < 14 {
			return ErrWeakPassword("that password is too common")
		}
	}
	return nil
}

func allSame(s string) bool {
	rs := []rune(s)
	for _, r := range rs[1:] {
		if r != rs[0] {
			return false
		}
	}
	return true
}

// randomToken returns n random bytes as an unpadded base64url string.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenHash is how session tokens are stored: a leaked auth file cannot be
// replayed as a cookie.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

var errNotFound = errors.New("not found")
