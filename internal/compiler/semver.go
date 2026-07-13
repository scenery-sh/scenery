package compiler

import (
	"fmt"
	"strconv"
	"strings"
)

type semanticVersion struct {
	major, minor, patch uint64
	prerelease          []string
}

func parseSemanticVersion(value string) (semanticVersion, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return semanticVersion{}, fmt.Errorf("invalid semantic version")
	}
	if core, build, ok := strings.Cut(value, "+"); ok {
		if build == "" {
			return semanticVersion{}, fmt.Errorf("invalid semantic version build metadata")
		}
		for _, identifier := range strings.Split(build, ".") {
			if identifier == "" || !semanticVersionIdentifier(identifier) {
				return semanticVersion{}, fmt.Errorf("invalid semantic version build metadata")
			}
		}
		value = core
	}
	core, prerelease, _ := strings.Cut(value, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semanticVersion{}, fmt.Errorf("semantic version must have major.minor.patch")
	}
	values := make([]uint64, 3)
	for index, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return semanticVersion{}, fmt.Errorf("invalid semantic version component %q", part)
		}
		parsed, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semanticVersion{}, fmt.Errorf("invalid semantic version component %q", part)
		}
		values[index] = parsed
	}
	version := semanticVersion{major: values[0], minor: values[1], patch: values[2]}
	if prerelease != "" {
		version.prerelease = strings.Split(prerelease, ".")
		for _, identifier := range version.prerelease {
			_, numericErr := strconv.ParseUint(identifier, 10, 64)
			if identifier == "" || !semanticVersionIdentifier(identifier) || numericErr == nil && len(identifier) > 1 && identifier[0] == '0' {
				return semanticVersion{}, fmt.Errorf("invalid semantic version prerelease")
			}
		}
	}
	return version, nil
}

func semanticVersionIdentifier(value string) bool {
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}
