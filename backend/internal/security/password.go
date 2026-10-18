// Package security implements authentication, RBAC and guest tokens.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

// Default password hash parameters mirror werkzeug's defaults so that
// hashes written by the Python implementation verify here and vice versa.
const (
	scryptN      = 32768
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 64
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

// ErrInvalidHash is returned when a stored hash is not in a supported format.
var ErrInvalidHash = errors.New("security: unsupported password hash format")

// HashPassword produces a werkzeug-compatible scrypt hash
// ("scrypt:N:R:P$hexsalt$hexhash").
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt:%d:%d:%d$%s$%s", scryptN, scryptR, scryptP,
		hex.EncodeToString(salt), hex.EncodeToString(dk)), nil
}

// HashPasswordArgon2 produces an argon2id hash ("$argon2id$v=19$m=...,t=...,p=...$salt$hash").
func HashPasswordArgon2(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		argonMemory, argonTime, argonThreads,
		hex.EncodeToString(salt), hex.EncodeToString(dk)), nil
}

// VerifyPassword checks a plaintext password against a stored hash.
// Supported: werkzeug scrypt / pbkdf2, bcrypt, argon2id.
func VerifyPassword(stored, password string) bool {
	switch {
	case strings.HasPrefix(stored, "scrypt:"):
		return verifyScrypt(stored, password)
	case strings.HasPrefix(stored, "pbkdf2:"):
		return verifyPBKDF2(stored, password)
	case strings.HasPrefix(stored, "$argon2id$"):
		return verifyArgon2id(stored, password)
	case strings.HasPrefix(stored, "$2a$"), strings.HasPrefix(stored, "$2b$"), strings.HasPrefix(stored, "$2y$"):
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil
	}
	return false
}

func verifyScrypt(stored, password string) bool {
	parts := strings.SplitN(stored, "$", 3)
	if len(parts) != 3 {
		return false
	}
	scheme := parts[0] // "scrypt:32768:8:1"
	nums := strings.SplitN(strings.TrimPrefix(scheme, "scrypt:"), ":", 3)
	if len(nums) != 3 {
		return false
	}
	var n, r, p int
	if _, err := fmt.Sscanf(strings.Join(nums, " "), "%d %d %d", &n, &r, &p); err != nil {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	got, err := scrypt.Key([]byte(password), salt, n, r, p, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func verifyPBKDF2(stored, password string) bool {
	// werkzeug: "pbkdf2:sha256:600000$salt$hash" (salt/hash hex encoded).
	parts := strings.SplitN(stored, "$", 3)
	if len(parts) != 3 {
		return false
	}
	nums := strings.SplitN(strings.TrimPrefix(parts[0], "pbkdf2:"), ":", 2)
	if len(nums) != 2 {
		return false
	}
	var iterations int
	if _, err := fmt.Sscanf(nums[1], "%d", &iterations); err != nil {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil || len(salt) < 8 {
		return false
	}
	want, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func verifyArgon2id(stored, password string) bool {
	parts := strings.Split(stored, "$")
	// ["", "argon2id", "v=19", "m=65536,t=1,p=4", salt, hash]
	if len(parts) != 6 {
		return false
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	salt, err := hex.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
