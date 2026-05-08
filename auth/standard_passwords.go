package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMinLength = 10
	passwordMaxLength = 1024

	argonTime    uint32 = 2
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

func validatePassword(password string) error {
	if len(password) < passwordMinLength {
		return fmt.Errorf("password must be at least %d characters", passwordMinLength)
	}
	if len(password) > passwordMaxLength {
		return fmt.Errorf("password must be at most %d characters", passwordMaxLength)
	}
	return nil
}

func hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedHash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonTime,
		argonThreads,
		encodedSalt,
		encodedHash,
	), nil
}

func verifyPassword(password string, encoded string) (bool, bool, error) {
	params, salt, expected, err := parsePHCPasswordHash(encoded)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(expected)))
	ok := subtle.ConstantTimeCompare(actual, expected) == 1
	needsUpgrade := params.time != argonTime ||
		params.memory != argonMemory ||
		params.threads != argonThreads ||
		len(expected) != int(argonKeyLen)
	return ok, needsUpgrade, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parsePHCPasswordHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(strings.TrimSpace(encoded), "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return argonParams{}, nil, nil, fmt.Errorf("unsupported password hash")
	}

	params, err := parseArgonParams(parts[3])
	if err != nil {
		return argonParams{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("decode password salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("decode password hash: %w", err)
	}
	if len(salt) == 0 || len(hash) == 0 {
		return argonParams{}, nil, nil, fmt.Errorf("invalid password hash")
	}
	return params, salt, hash, nil
}

func parseArgonParams(value string) (argonParams, error) {
	var out argonParams
	for _, part := range strings.Split(value, ",") {
		key, raw, ok := strings.Cut(part, "=")
		if !ok {
			return argonParams{}, fmt.Errorf("invalid password hash parameters")
		}
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return argonParams{}, fmt.Errorf("invalid password hash parameter %q", key)
		}
		switch key {
		case "m":
			out.memory = uint32(parsed)
		case "t":
			out.time = uint32(parsed)
		case "p":
			if parsed > 255 {
				return argonParams{}, fmt.Errorf("invalid password parallelism")
			}
			out.threads = uint8(parsed)
		default:
			return argonParams{}, fmt.Errorf("unknown password hash parameter %q", key)
		}
	}
	if out.memory == 0 || out.time == 0 || out.threads == 0 {
		return argonParams{}, fmt.Errorf("incomplete password hash parameters")
	}
	return out, nil
}
