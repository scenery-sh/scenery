package watchignore

import (
	filepath "path/filepath"
	app "scenery.sh/internal/app"
	strconv "strconv"
	strings "strings"
	unicode "unicode"
	utf8 "unicode/utf8"
)

func ParseEmbedPatterns(src string) []string {
	var patterns []string
	for remaining := src; remaining != ""; {
		var line string
		line, remaining, _ = strings.Cut(remaining, "\n")
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:embed") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "//go:embed"))
		for rest != "" {
			token, next, ok := nextEmbedToken(rest)
			if !ok {
				break
			}
			if token != "" {
				patterns = append(patterns, token)
			}
			rest = next
		}
	}
	return patterns
}

func nextEmbedToken(input string) (string, string, bool) {
	input = strings.TrimLeftFunc(input, unicode.IsSpace)
	if input == "" {
		return "", "", false
	}
	if quote, _ := utf8.DecodeRuneInString(input); quote == '"' || quote == '`' {
		for i := 1; i <= len(input); i++ {
			token, err := strconv.Unquote(input[:i])
			if err == nil {
				return token, input[i:], true
			}
		}
		return "", "", false
	}
	i := 0
	for i < len(input) {
		r, size := utf8.DecodeRuneInString(input[i:])
		if unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return input[:i], input[i:], true
}

func IgnorePath(rel string, isDir bool, ignore *Matcher) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return false
	}
	if shouldIgnoreWatchPathBuiltin(rel, isDir) {
		return true
	}
	if ignore != nil && ignore.Ignored(rel, isDir) {
		return true
	}
	return false
}

func shouldIgnoreWatchPathBuiltin(rel string, isDir bool) bool {
	if !isDir && IsRootDotFile(rel) {
		return false
	}
	rest := rel
	for rest != "" {
		part, next, found := strings.Cut(rest, "/")
		rest = next
		if part == "" || part == "." {
			continue
		}
		if !isDir && !found && part == ".gitignore" {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return true
		}
		switch part {
		case "node_modules", "scenery_internal_main":
			return true
		}
	}
	return false
}

func IsRootDotFile(rel string) bool {
	switch filepath.ToSlash(rel) {
	case ".env", ".env.local":
		return true
	default:
		return app.IsConfigFilename(rel)
	}
}

func HasHiddenPart(rel string) bool {
	rest := filepath.ToSlash(rel)
	for rest != "" {
		part, next, _ := strings.Cut(rest, "/")
		rest = next
		if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return true
		}
	}
	return false
}
