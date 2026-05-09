package objectstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const maxIdentifierLength = 63

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var reservedIdentifiers = map[string]bool{
	"all": true, "and": true, "as": true, "by": true, "case": true, "create": true,
	"delete": true, "desc": true, "drop": true, "false": true, "from": true,
	"group": true, "in": true, "insert": true, "is": true, "join": true,
	"limit": true, "not": true, "null": true, "or": true, "order": true,
	"select": true, "table": true, "true": true, "update": true, "where": true,
}

func validateName(kind, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if len(name) > maxIdentifierLength {
		return fmt.Errorf("%s name %q is longer than %d characters", kind, name, maxIdentifierLength)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%s name %q must start with a lowercase letter and contain only lowercase letters, digits, and underscores", kind, name)
	}
	if reservedIdentifiers[name] {
		return fmt.Errorf("%s name %q is reserved", kind, name)
	}
	return nil
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func qualifiedIdent(schema, name string) string {
	return quoteIdent(schema) + "." + quoteIdent(name)
}

func safeColumnName(field string, suffix string) string {
	name := field
	if suffix != "" {
		name += "_" + suffix
	}
	if len(name) <= maxIdentifierLength {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	prefix := name[:maxIdentifierLength-9]
	return strings.TrimRight(prefix, "_") + "_" + hex.EncodeToString(sum[:])[:8]
}

func physicalTableName(objectID, objectName string) string {
	return physicalNameWithSuffix(objectName, shortIdentifierSuffix(objectID))
}

func physicalColumnName(fieldID, fieldName, part string) string {
	base := fieldName
	if part != "" {
		base += "_" + part
	}
	return physicalNameWithSuffix(base, shortIdentifierSuffix(fieldID))
}

func physicalIndexName(indexID, indexName string) string {
	return physicalNameWithSuffix(indexName, shortIdentifierSuffix(indexID))
}

func physicalNameWithSuffix(base, suffix string) string {
	if suffix == "" {
		sum := sha256.Sum256([]byte(base))
		suffix = hex.EncodeToString(sum[:])[:12]
	}
	suffix = "__" + suffix
	limit := maxIdentifierLength - len(suffix)
	if limit < 1 {
		limit = 1
	}
	if len(base) > limit {
		base = strings.TrimRight(base[:limit], "_")
	}
	if base == "" {
		base = "x"
	}
	return base + suffix
}

func shortIdentifierSuffix(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	value := b.String()
	if len(value) >= 12 {
		return value[:12]
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:12]
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func advisoryLockKey(parts ...string) int64 {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func defaultLabel(name string) string {
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}
