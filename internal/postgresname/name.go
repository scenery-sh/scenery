// Package postgresname derives PostgreSQL-safe database, schema, and env names
// without pulling the PostgreSQL driver into configuration-only packages.
package postgresname

import (
	"fmt"
	"path/filepath"
	"strings"

	"scenery.sh/internal/identityhash"
)

func DatabaseNameFor(appID, appRoot string) string {
	root, err := filepath.Abs(strings.TrimSpace(appRoot))
	if err != nil {
		root = strings.TrimSpace(appRoot)
	}
	hash := identityhash.Short(root)
	if hash == "" {
		hash = identityhash.Short(appID)
	}
	base := sanitize(appID)
	suffix := "_" + hash
	name := base + suffix
	if len(name) <= 63 {
		return name
	}
	keep := 63 - len(suffix)
	if keep < 1 {
		return strings.TrimPrefix(suffix, "_")
	}
	return strings.TrimRight(name[:keep], "_") + suffix
}

func SchemaNameFor(service string) (string, error) {
	schema := sanitize(service)
	if schema == "" || schema == "app" {
		return "", fmt.Errorf("postgres schema name is required")
	}
	if SchemaNameReserved(schema) {
		return "", fmt.Errorf("postgres schema %q is reserved by plan 0097", schema)
	}
	return schema, nil
}

func SchemaNameReserved(schema string) bool {
	schema = strings.ToLower(strings.TrimSpace(schema))
	return schema == "scenery" || schema == "public" || schema == "information_schema" || strings.HasPrefix(schema, "pg_")
}

func ServiceDatabaseURLEnv(service string) string {
	prefix := strings.ToUpper(strings.TrimSpace(service))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range prefix {
		ok := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "DATABASE"
	}
	return out + "_DATABASE_URL"
}

func sanitize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "app"
	}
	return out
}
