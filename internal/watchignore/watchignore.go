package watchignore

import (
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
)

type Matcher struct {
	root        string
	loaded      map[string]struct{}
	configRules []watchIgnoreRule
	gitRules    []watchIgnoreRule
}

type watchIgnoreRule struct {
	base         string
	baseParts    []string
	pattern      string
	patternParts []string
	negated      bool
	dirOnly      bool
	hasSlash     bool
}

func New(root string) *Matcher {
	m := &Matcher{
		root:        root,
		loaded:      make(map[string]struct{}),
		configRules: watchConfigIgnoreRules(root),
	}
	m.LoadDir("")
	return m
}

func watchConfigIgnoreRules(root string) []watchIgnoreRule {
	patterns, err := app.ReadWatchIgnorePatterns(root)
	if err != nil {
		return nil
	}
	rules := make([]watchIgnoreRule, 0, len(patterns))
	for _, pattern := range patterns {
		if rule, ok := parseWatchConfigIgnoreRule(pattern); ok {
			rules = append(rules, rule)
		}
	}
	return rules
}

func (m *Matcher) LoadDir(rel string) {
	if m == nil {
		return
	}
	rel = normalizeWatchRel(rel)
	if _, ok := m.loaded[rel]; ok {
		return
	}
	m.loaded[rel] = struct{}{}

	data, err := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(rel), ".gitignore"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rule, ok := parseWatchIgnoreRule(rel, line); ok {
			m.gitRules = append(m.gitRules, rule)
		}
	}
}

func (m *Matcher) Ignored(rel string, isDir bool) bool {
	if m == nil {
		return false
	}
	rel = normalizeWatchRel(rel)
	if rel == "" {
		return false
	}
	relParts := strings.Split(rel, "/")
	for _, rule := range m.configRules {
		if rule.matches(relParts, isDir) {
			return true
		}
	}
	ignored := false
	for _, rule := range m.gitRules {
		if !rule.matches(relParts, isDir) {
			continue
		}
		ignored = !rule.negated
	}
	return ignored
}

func parseWatchConfigIgnoreRule(line string) (watchIgnoreRule, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "!") {
		return watchIgnoreRule{}, false
	}
	dirOnly := strings.HasSuffix(line, "/")
	line = strings.TrimRight(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return watchIgnoreRule{}, false
	}
	pattern := pathpkg.Clean(filepath.ToSlash(line))
	if pattern == "." || pattern == ".." || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
		return watchIgnoreRule{}, false
	}
	return watchIgnoreRule{
		pattern:      pattern,
		patternParts: splitWatchPath(pattern),
		dirOnly:      dirOnly,
		hasSlash:     strings.Contains(pattern, "/"),
	}, true
}

func parseWatchIgnoreRule(base, line string) (watchIgnoreRule, bool) {
	line = strings.TrimSuffix(line, "\r")
	line = trimUnescapedTrailingSpaces(line)
	if line == "" {
		return watchIgnoreRule{}, false
	}
	if strings.HasPrefix(line, `\#`) {
		line = line[1:]
	} else if strings.HasPrefix(line, "#") {
		return watchIgnoreRule{}, false
	}

	negated := false
	if strings.HasPrefix(line, `\!`) {
		line = line[1:]
	} else if strings.HasPrefix(line, "!") {
		negated = true
		line = strings.TrimPrefix(line, "!")
	}
	if line == "" {
		return watchIgnoreRule{}, false
	}

	dirOnly := strings.HasSuffix(line, "/")
	line = strings.TrimRight(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return watchIgnoreRule{}, false
	}
	pattern := pathpkg.Clean(filepath.ToSlash(line))
	if pattern == "." {
		return watchIgnoreRule{}, false
	}
	normalizedBase := normalizeWatchRel(base)
	return watchIgnoreRule{
		base:         normalizedBase,
		baseParts:    splitWatchPath(normalizedBase),
		pattern:      pattern,
		patternParts: splitWatchPath(pattern),
		negated:      negated,
		dirOnly:      dirOnly,
		hasSlash:     strings.Contains(pattern, "/"),
	}, true
}

func trimUnescapedTrailingSpaces(value string) string {
	for strings.HasSuffix(value, " ") {
		backslashes := 0
		for i := len(value) - 2; i >= 0 && value[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 1 {
			break
		}
		value = strings.TrimSuffix(value, " ")
	}
	return value
}

func (r watchIgnoreRule) matches(relParts []string, isDir bool) bool {
	sub, ok := partsUnderWatchBase(relParts, r.baseParts)
	if !ok || len(sub) == 0 {
		return false
	}
	if r.negated {
		return r.matchesExact(sub, isDir)
	}
	return r.matchesSelfOrParent(sub, isDir)
}

func (r watchIgnoreRule) matchesExact(sub []string, isDir bool) bool {
	if r.hasSlash {
		if !matchGitignorePathParts(r.patternParts, sub) {
			return false
		}
		return !r.dirOnly || isDir
	}
	if !matchGitignoreSegment(r.pattern, sub[len(sub)-1]) {
		return false
	}
	return !r.dirOnly || isDir
}

func (r watchIgnoreRule) matchesSelfOrParent(sub []string, isDir bool) bool {
	for i := 1; i <= len(sub); i++ {
		candidateIsDir := i < len(sub) || isDir
		if r.hasSlash {
			if !matchGitignorePathParts(r.patternParts, sub[:i]) {
				continue
			}
		} else if !matchGitignoreSegment(r.pattern, sub[i-1]) {
			continue
		}
		if r.dirOnly && !candidateIsDir {
			continue
		}
		return true
	}
	return false
}

func partsUnderWatchBase(relParts, baseParts []string) ([]string, bool) {
	if len(baseParts) == 0 {
		return relParts, true
	}
	if len(relParts) < len(baseParts) {
		return nil, false
	}
	for i, segment := range baseParts {
		if relParts[i] != segment {
			return nil, false
		}
	}
	return relParts[len(baseParts):], true
}

func normalizeWatchRel(rel string) string {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return ""
	}
	return strings.TrimPrefix(rel, "./")
}

func matchGitignorePathParts(patternParts, relParts []string) bool {
	if len(patternParts) == 0 {
		return len(relParts) == 0
	}
	if patternParts[0] == "**" {
		for i := 0; i <= len(relParts); i++ {
			if matchGitignorePathParts(patternParts[1:], relParts[i:]) {
				return true
			}
		}
		return false
	}
	if len(relParts) == 0 {
		return false
	}
	if !matchGitignoreSegment(patternParts[0], relParts[0]) {
		return false
	}
	return matchGitignorePathParts(patternParts[1:], relParts[1:])
}

func matchGitignoreSegment(pattern, name string) bool {
	ok, err := pathpkg.Match(pattern, name)
	if err != nil {
		return pattern == name
	}
	return ok
}

func splitWatchPath(value string) []string {
	value = normalizeWatchRel(value)
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}
