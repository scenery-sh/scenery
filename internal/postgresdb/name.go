package postgresdb

import (
	"path/filepath"
	"strings"

	"scenery.sh/internal/identityhash"
)

func DatabaseNameFor(appID, service, appRoot string) string {
	root, err := filepath.Abs(strings.TrimSpace(appRoot))
	if err != nil {
		root = strings.TrimSpace(appRoot)
	}
	hash := identityhash.Short(root)
	if hash == "" {
		hash = identityhash.Short(appID + "/" + service)
	}
	base := sanitizePG(appID) + "_" + sanitizePG(service)
	if base == "_" {
		base = "app_db"
	}
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

func sanitizePG(value string) string {
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
