package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// DefaultRecoveryCodeCount is the number of one-time recovery codes generated per batch.
	DefaultRecoveryCodeCount = 8
	recoveryCodeRawLen       = 16
	recoveryAlphabet         = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// GenerateRecoveryCodes generates count unique, human-friendly one-time recovery codes
// formatted as XXXX-XXXX-XXXX-XXXX (80 bits of CSPRNG entropy per code, zero modulo bias).
func GenerateRecoveryCodes(count int) ([]string, error) {
	if count <= 0 {
		count = DefaultRecoveryCodeCount
	}
	out := make([]string, 0, count)
	seen := make(map[string]struct{}, count)
	buf := make([]byte, recoveryCodeRawLen)

	for len(out) < count {
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generate recovery code entropy: %w", err)
		}
		var raw [recoveryCodeRawLen]byte
		for i, b := range buf {
			raw[i] = recoveryAlphabet[b&31] // 256 is an exact multiple of 32: zero modulo bias.
		}
		norm := string(raw[:])
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		formatted := fmt.Sprintf("%s-%s-%s-%s", norm[0:4], norm[4:8], norm[8:12], norm[12:16])
		out = append(out, formatted)
	}
	return out, nil
}

// NormalizeRecoveryCode strips hyphens and whitespace, uppercases, and validates syntax.
func NormalizeRecoveryCode(input string) (string, bool) {
	var b strings.Builder
	b.Grow(recoveryCodeRawLen)
	for _, r := range input {
		switch {
		case r == '-' || r == ' ' || r == '\t' || r == '\r' || r == '\n':
			continue
		case r >= 'a' && r <= 'z':
			r = r - 'a' + 'A'
		}
		if !strings.ContainsRune(recoveryAlphabet, r) {
			return "", false
		}
		b.WriteRune(r)
		if b.Len() > recoveryCodeRawLen {
			return "", false
		}
	}
	if b.Len() != recoveryCodeRawLen {
		return "", false
	}
	return b.String(), true
}

// HashRecoveryCode computes a domain-separated, user-bound keyed HMAC-SHA256 digest (64 lowercase hex chars).
func HashRecoveryCode(encryptionKey []byte, userID string, normalizedCode string) string {
	kdf := hmac.New(sha256.New, encryptionKey)
	_, _ = kdf.Write([]byte("lovelace:admin-recovery-code:v1"))
	subkey := kdf.Sum(nil)

	mac := hmac.New(sha256.New, subkey)
	_, _ = mac.Write([]byte(strings.ToLower(strings.TrimSpace(userID))))
	_, _ = mac.Write([]byte(":"))
	_, _ = mac.Write([]byte(normalizedCode))
	return hex.EncodeToString(mac.Sum(nil))
}

// MatchRecoveryCodeHash compares two 64-character hex recovery code digests in constant time.
func MatchRecoveryCodeHash(a, b string) bool {
	if len(a) != 64 || len(b) != 64 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
