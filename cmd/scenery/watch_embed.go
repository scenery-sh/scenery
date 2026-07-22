package main

import (
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

func cachedGoEmbedPatterns(path string, stamp fileStamp) ([]string, bool) {
	value, ok := embedPatternCache.Load(path)
	if !ok {
		return nil, false
	}
	entry, ok := value.(embedPatternCacheEntry)
	if !ok || entry.stamp.hash != stamp.hash {
		return nil, false
	}
	return entry.patterns, true
}

func storeGoEmbedPatterns(path string, stamp fileStamp, patterns []string) {
	embedPatternCache.Store(path, embedPatternCacheEntry{stamp: stamp, patterns: patterns})
}

func parseGoEmbedPatterns(src string) []string {
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

func addEmbeddedSnapshotFiles(root, pkgDir, pattern string, files, previous map[string]fileStamp, ignore *watchignore.Matcher) error {
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
		if err := addEmbeddedSnapshotPath(root, match, includeHidden, files, previous, ignore); err != nil {
			return err
		}
	}
	return nil
}

func addEmbeddedSnapshotPath(root, path string, includeHidden bool, files, previous map[string]fileStamp, ignore *watchignore.Matcher) error {
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
			stamp, reused := reusableStamp(previous, rel, info, true)
			if !reused {
				var err error
				if stamp, _, err = stampWatchedFile(path, info, true); err != nil {
					return nil
				}
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
		stamp, reused := reusableStamp(previous, rel, info, true)
		if !reused {
			if stamp, _, err = stampWatchedFile(child, info, true); err != nil {
				return nil
			}
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
