package vnext

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

func compareSemanticVersion(left, right semanticVersion) int {
	for _, values := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if values[0] < values[1] {
			return -1
		}
		if values[0] > values[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		leftID, rightID := left.prerelease[index], right.prerelease[index]
		leftNumber, leftErr := strconv.ParseUint(leftID, 10, 64)
		rightNumber, rightErr := strconv.ParseUint(rightID, 10, 64)
		switch {
		case leftErr == nil && rightErr == nil && leftNumber < rightNumber:
			return -1
		case leftErr == nil && rightErr == nil && leftNumber > rightNumber:
			return 1
		case leftErr == nil && rightErr != nil:
			return -1
		case leftErr != nil && rightErr == nil:
			return 1
		case leftID < rightID:
			return -1
		case leftID > rightID:
			return 1
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func semanticVersionSatisfies(versionText, constraintText string) bool {
	version, err := parseSemanticVersion(versionText)
	if err != nil {
		return false
	}
	constraintText = strings.TrimSpace(constraintText)
	if constraintText == "" {
		return false
	}
	for _, raw := range strings.Split(constraintText, ",") {
		raw = strings.TrimSpace(raw)
		operator := "="
		for _, candidate := range []string{">=", "<=", "!=", "==", ">", "<", "="} {
			if strings.HasPrefix(raw, candidate) {
				operator, raw = candidate, strings.TrimSpace(strings.TrimPrefix(raw, candidate))
				break
			}
		}
		expected, err := parseSemanticVersion(raw)
		if err != nil {
			return false
		}
		comparison := compareSemanticVersion(version, expected)
		matches := comparison == 0
		switch operator {
		case ">=":
			matches = comparison >= 0
		case "<=":
			matches = comparison <= 0
		case "!=":
			matches = comparison != 0
		case ">":
			matches = comparison > 0
		case "<":
			matches = comparison < 0
		}
		if !matches {
			return false
		}
	}
	return true
}
