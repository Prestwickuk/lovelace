package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/tirbach/tir-bach/internal/domain"
)

const (
	MinPasswordLength = 12
	argonTime         = 3
	argonMemoryKiB    = 64 * 1024
	argonThreads      = 1
	argonKeyLen       = 32
	argonSaltLen      = 16
)

// ValidatePasswordPolicy enforces minimum password strength rules.
func ValidatePasswordPolicy(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return domain.NewValidationError(map[string]string{
			"password": fmt.Sprintf("must be at least %d characters", MinPasswordLength),
		})
	}
	if len(password) > 256 {
		return domain.NewValidationError(map[string]string{
			"password": "must not exceed 256 bytes",
		})
	}
	return nil
}

// HashPassword hashes a plaintext password using Argon2id and encodes it in PHC string format.
func HashPassword(password string) (string, error) {
	if err := ValidatePasswordPolicy(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemoryKiB,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword verifies a plaintext password against a stored Argon2id PHC hash in constant time.
func VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("incompatible argon2 version")
	}

	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, errors.New("invalid argon2 parameters")
	}
	mem, err := parseParam(params[0], "m=")
	if err != nil {
		return false, err
	}
	iters, err := parseParam(params[1], "t=")
	if err != nil {
		return false, err
	}
	threads, err := parseParam(params[2], "p=")
	if err != nil {
		return false, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}

	computed := argon2.IDKey([]byte(password), salt, uint32(iters), uint32(mem), uint8(threads), uint32(len(expectedHash)))
	return subtle.ConstantTimeCompare(computed, expectedHash) == 1, nil
}

func parseParam(s, prefix string) (int, error) {
	if !strings.HasPrefix(s, prefix) {
		return 0, fmt.Errorf("expected parameter prefix %s", prefix)
	}
	v, err := strconv.Atoi(strings.TrimPrefix(s, prefix))
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid parameter %s", s)
	}
	return v, nil
}
