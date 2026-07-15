package validation

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CollectChangedFiles lists files changed relative to base, expressed
// relative to appRoot, using the enclosing git repository. It is a package
// variable so tests can substitute a fake collector.
var CollectChangedFiles = func(ctx context.Context, appRoot, base string) ([]string, error) {
	rootCmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = appRoot
	rootOut, err := rootCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git rev-parse --show-toplevel: %w: %s", err, strings.TrimSpace(string(rootOut)))
	}
	gitRoot := strings.TrimSpace(string(rootOut))
	if physicalGitRoot, err := filepath.EvalSymlinks(gitRoot); err == nil {
		gitRoot = physicalGitRoot
	}
	appRootForRel := appRoot
	if physicalAppRoot, err := filepath.EvalSymlinks(appRoot); err == nil {
		appRootForRel = physicalAppRoot
	}
	appRel, err := filepath.Rel(gitRoot, appRootForRel)
	if err != nil {
		return nil, err
	}
	appRel = filepath.ToSlash(appRel)
	args := []string{"diff", "--name-only", base + "...HEAD"}
	if appRel != "." && appRel != "" {
		args = []string{"diff", "--name-only", "--relative=" + appRel, base + "...HEAD", "--", appRel}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = gitRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(filepath.ToSlash(line))
		if line != "" {
			files = append(files, line)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (p Planner) selectChangedProfiles(files []string) []string {
	selected := []string{}
	defaultProfile := p.ResolveProfileName("")
	if defaultProfile != "" {
		selected = append(selected, defaultProfile)
	}
	for _, match := range p.matchChangedProfiles(files) {
		if !containsString(selected, match.Profile) {
			selected = append(selected, match.Profile)
		}
	}
	return selected
}

func (p Planner) matchChangedProfiles(files []string) []ProfileMatch {
	cfg := p.Config
	names := make([]string, 0, len(cfg.Validation.Profiles))
	for name := range cfg.Validation.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	var matches []ProfileMatch
	for _, name := range names {
		prof := cfg.Validation.Profiles[name]
		if len(prof.Paths) == 0 {
			continue
		}
		var matchedPaths, matchedFiles []string
		for _, pattern := range prof.Paths {
			for _, file := range files {
				if globMatches(pattern, file) {
					if !containsString(matchedPaths, pattern) {
						matchedPaths = append(matchedPaths, pattern)
					}
					if !containsString(matchedFiles, file) {
						matchedFiles = append(matchedFiles, file)
					}
				}
			}
		}
		if len(matchedFiles) > 0 {
			matches = append(matches, ProfileMatch{Profile: name, MatchedPaths: matchedPaths, MatchedFiles: matchedFiles})
		}
	}
	return matches
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func globMatches(pattern, file string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	file = filepath.ToSlash(strings.TrimSpace(file))
	if pattern == "" || file == "" {
		return false
	}
	if pattern == file {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return file == prefix || strings.HasPrefix(file, prefix+"/")
	}
	if globMatchSegments(strings.Split(pattern, "/"), strings.Split(file, "/")) {
		return true
	}
	ok, _ := filepath.Match(pattern, file)
	if ok {
		return true
	}
	if !strings.Contains(pattern, "/") {
		ok, _ = filepath.Match(pattern, filepath.Base(file))
		return ok
	}
	return false
}

func globMatchSegments(patternParts, fileParts []string) bool {
	if len(patternParts) == 0 {
		return len(fileParts) == 0
	}
	if patternParts[0] == "**" {
		if globMatchSegments(patternParts[1:], fileParts) {
			return true
		}
		for i := range fileParts {
			if globMatchSegments(patternParts[1:], fileParts[i+1:]) {
				return true
			}
		}
		return false
	}
	if len(fileParts) == 0 {
		return false
	}
	ok, err := filepath.Match(patternParts[0], fileParts[0])
	if err != nil || !ok {
		return false
	}
	return globMatchSegments(patternParts[1:], fileParts[1:])
}
