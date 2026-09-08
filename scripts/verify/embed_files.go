package main

import (
	fs "io/fs"
	os "os"
	filepath "path/filepath"
	watchignore "scenery.sh/internal/watchignore"
	strings "strings"
)

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
