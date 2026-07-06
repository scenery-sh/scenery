package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"scenery.sh/internal/watchignore"
)

type embedPatternCacheEntry struct {
	stamp    fileStamp
	patterns []string
}

// embedPatternCache memoizes parsed //go:embed patterns per Go file so repeated
// watch scans stat files instead of re-reading every .go file in the app.
var embedPatternCache sync.Map

func embedPatternsForFile(path string, info fs.FileInfo) []string {
	stamp := fileStamp{modTime: info.ModTime().UTC().Round(0), size: info.Size()}
	if cached, ok := embedPatternCache.Load(path); ok {
		entry := cached.(embedPatternCacheEntry)
		if entry.stamp == stamp {
			return entry.patterns
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	patterns := parseGoEmbedPatterns(string(data))
	embedPatternCache.Store(path, embedPatternCacheEntry{stamp: stamp, patterns: patterns})
	return patterns
}

func discoverEmbeddedWatchFiles(root string, ignore *watchignore.Matcher) (map[string]struct{}, error) {
	files := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if d != nil && d.IsDir() && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldIgnoreWatchPathWithMatcher(rel, true, ignore) {
				return filepath.SkipDir
			}
			ignore.LoadDir(rel)
			return nil
		}
		if shouldIgnoreWatchPathWithMatcher(rel, false, ignore) {
			return nil
		}
		if filepath.Ext(rel) != ".go" || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		patterns := embedPatternsForFile(path, info)
		if len(patterns) == 0 {
			return nil
		}
		pkgDir := filepath.Dir(rel)
		for _, pattern := range patterns {
			if err := addEmbeddedPatternFiles(root, pkgDir, pattern, files, ignore); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func parseGoEmbedPatterns(src string) []string {
	var patterns []string
	for _, line := range strings.Split(src, "\n") {
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

func addEmbeddedPatternFiles(root, pkgDir, pattern string, files map[string]struct{}, ignore *watchignore.Matcher) error {
	includeHidden := false
	if strings.HasPrefix(pattern, "all:") {
		includeHidden = true
		pattern = strings.TrimPrefix(pattern, "all:")
	}
	if pattern == "" || filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
		return nil
	}
	search := filepath.Join(root, filepath.FromSlash(pkgDir), filepath.FromSlash(pattern))
	matches, err := filepath.Glob(search)
	if err != nil {
		return nil
	}
	for _, match := range matches {
		if err := addEmbeddedPath(root, match, includeHidden, files, ignore); err != nil {
			return err
		}
	}
	return nil
}

func addEmbeddedSnapshotFiles(root, pkgDir, pattern string, files map[string]fileStamp, ignore *watchignore.Matcher) error {
	includeHidden := false
	if strings.HasPrefix(pattern, "all:") {
		includeHidden = true
		pattern = strings.TrimPrefix(pattern, "all:")
	}
	if pattern == "" || filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
		return nil
	}
	search := filepath.Join(root, filepath.FromSlash(pkgDir), filepath.FromSlash(pattern))
	matches, err := filepath.Glob(search)
	if err != nil {
		return nil
	}
	for _, match := range matches {
		if err := addEmbeddedSnapshotPath(root, match, includeHidden, files, ignore); err != nil {
			return err
		}
	}
	return nil
}

func addEmbeddedSnapshotPath(root, path string, includeHidden bool, files map[string]fileStamp, ignore *watchignore.Matcher) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if rel != "." && shouldIgnoreWatchPathWithMatcher(rel, info.IsDir(), ignore) {
		return nil
	}
	if !info.IsDir() {
		if includeHidden || !hasHiddenOrUnderscorePart(rel) {
			stamp, _, err := stampWatchedFile(path, info, true)
			if err != nil {
				return nil
			}
			files[rel] = stamp
		}
		return nil
	}
	return filepath.WalkDir(path, func(child string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && child != path {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, child)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldIgnoreWatchPathWithMatcher(rel, true, ignore) {
				return filepath.SkipDir
			}
			if !includeHidden && hasHiddenOrUnderscorePart(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !includeHidden && hasHiddenOrUnderscorePart(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		stamp, _, err := stampWatchedFile(child, info, true)
		if err != nil {
			return nil
		}
		files[rel] = stamp
		return nil
	})
}

func addEmbeddedPath(root, path string, includeHidden bool, files map[string]struct{}, ignore *watchignore.Matcher) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if rel != "." && shouldIgnoreWatchPathWithMatcher(rel, info.IsDir(), ignore) {
		return nil
	}
	if !info.IsDir() {
		if includeHidden || !hasHiddenOrUnderscorePart(rel) {
			files[filepath.ToSlash(rel)] = struct{}{}
		}
		return nil
	}
	return filepath.WalkDir(path, func(child string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && child != path {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, child)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldIgnoreWatchPathWithMatcher(rel, true, ignore) {
				return filepath.SkipDir
			}
			ignore.LoadDir(rel)
			if !includeHidden && hasHiddenOrUnderscorePart(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if shouldIgnoreWatchPathWithMatcher(rel, false, ignore) {
			return nil
		}
		if includeHidden || !hasHiddenOrUnderscorePart(rel) {
			files[filepath.ToSlash(rel)] = struct{}{}
		}
		return nil
	})
}

func hasHiddenOrUnderscorePart(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return true
		}
	}
	return false
}
