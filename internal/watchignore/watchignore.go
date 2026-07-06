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
	base     string
	pattern  string
	negated  bool
	dirOnly  bool
	hasSlash bool
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
	for _, rule := range m.configRules {
		if rule.matches(rel, isDir) {
			return true
		}
	}
	ignored := false
	for _, rule := range m.gitRules {
		if !rule.matches(rel, isDir) {
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
		pattern:  pattern,
		dirOnly:  dirOnly,
		hasSlash: strings.Contains(pattern, "/"),
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
	return watchIgnoreRule{
		base:     normalizeWatchRel(base),
		pattern:  pattern,
		negated:  negated,
		dirOnly:  dirOnly,
		hasSlash: strings.Contains(pattern, "/"),
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

func (r watchIgnoreRule) matches(rel string, isDir bool) bool {
	sub, ok := relUnderWatchBase(rel, r.base)
	if !ok || sub == "" {
		return false
	}
	if r.negated {
		return r.matchesExact(sub, isDir)
	}
	return r.matchesSelfOrParent(sub, isDir)
}

func (r watchIgnoreRule) matchesExact(sub string, isDir bool) bool {
	if r.hasSlash {
		if !matchGitignorePath(r.pattern, sub) {
			return false
		}
		return !r.dirOnly || isDir
	}
	if !matchGitignoreSegment(r.pattern, filepath.Base(sub)) {
		return false
	}
	return !r.dirOnly || isDir
}

func (r watchIgnoreRule) matchesSelfOrParent(sub string, isDir bool) bool {
	parts := strings.Split(sub, "/")
	for i := 1; i <= len(parts); i++ {
		candidate := strings.Join(parts[:i], "/")
		candidateIsDir := i < len(parts) || isDir
		if r.hasSlash {
			if !matchGitignorePath(r.pattern, candidate) {
				continue
			}
		} else if !matchGitignoreSegment(r.pattern, parts[i-1]) {
			continue
		}
		if r.dirOnly && !candidateIsDir {
			continue
		}
		return true
	}
	return false
}

func relUnderWatchBase(rel, base string) (string, bool) {
	rel = normalizeWatchRel(rel)
	base = normalizeWatchRel(base)
	if base == "" {
		return rel, true
	}
	if rel == base {
		return "", true
	}
	if strings.HasPrefix(rel, base+"/") {
		return strings.TrimPrefix(rel, base+"/"), true
	}
	return "", false
}

func normalizeWatchRel(rel string) string {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return ""
	}
	return strings.TrimPrefix(rel, "./")
}

func matchGitignorePath(pattern, rel string) bool {
	return matchGitignorePathParts(splitWatchPath(pattern), splitWatchPath(rel))
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
