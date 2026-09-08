package repoinfo

import (
	"os"
	"path/filepath"
	"regexp"
	"scenery.sh/internal/appwalk"
	"sort"
	"strings"
)

func BuildAgents(repoRoot string) Agents {
	scopes := discoverInspectDocsAgentScopes(repoRoot)
	childIndexEntries := readChildAgentIndexEntries(repoRoot)

	discoveredChildren := make(map[string]struct{})
	for _, scope := range scopes {
		if scope.Path != "AGENTS.md" {
			discoveredChildren[scope.Path] = struct{}{}
		}
	}
	indexedChildren := stringSet(childIndexEntries)

	stale := []string{}
	for _, path := range childIndexEntries {
		if _, ok := discoveredChildren[path]; !ok {
			stale = append(stale, path)
		}
	}
	missing := []string{}
	for path := range discoveredChildren {
		if _, ok := indexedChildren[path]; !ok {
			missing = append(missing, path)
		}
	}
	sort.Strings(stale)
	sort.Strings(missing)

	return Agents{
		Scopes:                   scopes,
		ChildIndexPath:           "AGENTS.md#child-agent-index",
		ChildIndexEntries:        childIndexEntries,
		StaleChildIndexEntries:   stale,
		MissingChildIndexEntries: missing,
	}
}

func discoverInspectDocsAgentScopes(repoRoot string) []AgentScope {
	scopes := []AgentScope{}
	_ = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if appwalk.SkipDir(repoRoot, path) || (path != repoRoot && shouldSkipAgentScopeDir(entry.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "AGENTS.md" {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		scope := filepath.ToSlash(filepath.Dir(rel))
		if scope == "." {
			scope = "."
		}
		scopes = append(scopes, AgentScope{Path: rel, Scope: scope})
		return nil
	})
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].Path == "AGENTS.md" {
			return true
		}
		if scopes[j].Path == "AGENTS.md" {
			return false
		}
		return scopes[i].Path < scopes[j].Path
	})
	return scopes
}

// shouldSkipAgentScopeDir lists docs-scan-specific skips on top of the shared
// appwalk policy.
func shouldSkipAgentScopeDir(name string) bool {
	switch name {
	case ".direnv", ".idea", ".vscode", "vendor", "build", "coverage":
		return true
	default:
		return false
	}
}

var markdownAgentIndexLink = regexp.MustCompile(`\[[^\]]*AGENTS\.md[^\]]*\]\(([^)]+)\)`)

func readChildAgentIndexEntries(repoRoot string) []string {
	data, err := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))
	if err != nil {
		return nil
	}
	var entries []string
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "## ") {
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if strings.EqualFold(title, "Child Agent Index") {
				inSection = true
				continue
			}
			if inSection {
				break
			}
		}
		if !inSection || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		if path, ok := childAgentIndexPathFromBullet(repoRoot, strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))); ok {
			entries = append(entries, path)
		}
	}
	entries = uniqueSortedStrings(entries)
	return entries
}

func childAgentIndexPathFromBullet(repoRoot, bullet string) (string, bool) {
	lower := strings.ToLower(bullet)
	if strings.Contains(lower, "no child") || strings.Contains(lower, "when adding") {
		return "", false
	}
	if match := markdownAgentIndexLink.FindStringSubmatch(bullet); len(match) == 2 {
		return normalizeChildAgentIndexPath(repoRoot, match[1])
	}
	for {
		start := strings.Index(bullet, "`")
		if start < 0 {
			break
		}
		rest := bullet[start+1:]
		end := strings.Index(rest, "`")
		if end < 0 {
			break
		}
		if path, ok := normalizeChildAgentIndexPath(repoRoot, rest[:end]); ok {
			return path, true
		}
		bullet = rest[end+1:]
	}
	for _, field := range strings.Fields(bullet) {
		if path, ok := normalizeChildAgentIndexPath(repoRoot, strings.Trim(field, "`[]():,;")); ok {
			return path, true
		}
	}
	return "", false
}

func normalizeChildAgentIndexPath(repoRoot, raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "./")
	if strings.Contains(value, "#") {
		value = strings.SplitN(value, "#", 2)[0]
	}
	if value == "" {
		return "", false
	}
	if filepath.IsAbs(value) {
		rel, err := filepath.Rel(repoRoot, value)
		if err != nil {
			return "", false
		}
		value = rel
	}
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if value == "." || value == "AGENTS.md" || !strings.HasSuffix(value, "/AGENTS.md") {
		return "", false
	}
	if strings.HasPrefix(value, "../") {
		return "", false
	}
	return value, true
}

func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range values {
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}
func uniqueSortedStrings(values []string) []string {
	set := stringSet(values)
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
