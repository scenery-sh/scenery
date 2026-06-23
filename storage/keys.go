package storage

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

func ValidateKey(key string) error {
	return validateStoragePath(key, false, false)
}

func ValidatePrefix(prefix string) error {
	return validateStoragePath(prefix, true, true)
}

func NormalizeListOptions(opts ListOptions) (ListOptions, error) {
	if err := ValidatePrefix(opts.Prefix); err != nil {
		return ListOptions{}, err
	}
	if opts.Delimiter != "" && opts.Delimiter != "/" {
		return ListOptions{}, fmt.Errorf("storage list delimiter %q is not supported; use %q", opts.Delimiter, "/")
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultListLimit
	}
	if opts.Limit > MaxListLimit {
		opts.Limit = MaxListLimit
	}
	return opts, nil
}

func validateStoragePath(value string, allowEmpty, allowTrailingSlash bool) error {
	if value == "" {
		if allowEmpty {
			return nil
		}
		return &InvalidKeyError{Key: value, Reason: "empty key"}
	}
	if !utf8.ValidString(value) {
		return &InvalidKeyError{Key: value, Reason: "invalid UTF-8"}
	}
	if strings.HasPrefix(value, "/") {
		return &InvalidKeyError{Key: value, Reason: "absolute paths are not allowed"}
	}
	if strings.Contains(value, "\\") {
		return &InvalidKeyError{Key: value, Reason: "backslashes are not allowed"}
	}
	if strings.Contains(value, "//") {
		return &InvalidKeyError{Key: value, Reason: "duplicate slashes are not allowed"}
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return &InvalidKeyError{Key: value, Reason: "ASCII control characters are not allowed"}
		}
	}
	trimmed := value
	if allowTrailingSlash {
		trimmed = strings.TrimSuffix(trimmed, "/")
		if trimmed == "" {
			return &InvalidKeyError{Key: value, Reason: "root slash is not a valid prefix"}
		}
	}
	for _, part := range strings.Split(trimmed, "/") {
		if part == "." || part == ".." {
			return &InvalidKeyError{Key: value, Reason: "path traversal segments are not allowed"}
		}
	}
	if path.Clean(trimmed) != trimmed {
		return &InvalidKeyError{Key: value, Reason: "key must already be normalized"}
	}
	return nil
}
