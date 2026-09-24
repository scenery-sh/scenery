package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
)

// workspaceFrameworkFingerprint identifies the framework source that a
// workspace's local scenery.sh replacement selects: the content digest of its
// source manifest. A build whose context already verified that source reuses
// the verified digest instead of reading the same tree again; the empty
// fingerprint means the workspace selects no local framework source.
func workspaceFrameworkFingerprint(ctx context.Context, workspaceDir string) (string, error) {
	repoRoot, ok, err := localSceneryReplaceRoot(filepath.Join(workspaceDir, "go.mod"))
	if err != nil || !ok {
		return "", err
	}
	if source, verified := verifiedFrameworkSource(ctx, repoRoot); verified {
		return source.Digest, nil
	}
	source, err := FrameworkSourceManifest(repoRoot)
	if err != nil {
		return "", err
	}
	return source.Digest, nil
}

func localSceneryReplaceRoot(goModPath string) (string, bool, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	file, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return "", false, err
	}
	for _, replace := range file.Replace {
		if replace.Old.Path != "scenery.sh" || replace.New.Version != "" || replace.New.Path == "" {
			continue
		}
		path := replace.New.Path
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, ".") {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(goModPath), path)
		}
		return filepath.Clean(path), true, nil
	}
	return "", false, nil
}

// frameworkFingerprintFiles lists the framework source input files: Go and
// native sources, module files and the files their embed directives select.
func frameworkFingerprintFiles(repoRoot string) ([]string, error) {
	files, _, err := frameworkSourceFiles(repoRoot)
	return files, err
}

// frameworkSourceFiles is frameworkFingerprintFiles with the metadata the walk
// read for each Go file, so a caller stamping those files need not read it
// again.
func frameworkSourceFiles(repoRoot string) ([]string, map[string]os.FileInfo, error) {
	files := map[string]struct{}{}
	infos := map[string]os.FileInfo{}
	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !frameworkSourceInputFile(rel) {
			return nil
		}
		files[rel] = struct{}{}
		if filepath.Ext(rel) != ".go" {
			return nil
		}
		info, err := buildInputLstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		infos[rel] = info
		patterns, retained := retainedFrameworkEmbedPatterns(path, info)
		if !retained {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			patterns = parseGeneratorGoEmbedPatterns(string(data))
			retainFrameworkEmbedPatterns(path, info, patterns)
		}
		pkgDir := filepath.Dir(rel)
		for _, pattern := range patterns {
			if err := addGeneratorEmbeddedPatternFiles(repoRoot, pkgDir, pattern, files); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, infos, nil
}

func frameworkSourceInputFile(rel string) bool {
	base := filepath.Base(rel)
	if base == "" || shouldSkipFile(rel) || strings.HasSuffix(base, "_test.go") {
		return false
	}
	if base == "go.mod" || base == "go.sum" {
		return true
	}
	switch filepath.Ext(rel) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".m", ".mm", ".s", ".S", ".f", ".F", ".syso":
		return true
	}
	return false
}

// frameworkEmbedPatterns retains the embed patterns of framework Go files by
// build input stamp. The stamp includes the status-change time, so restoring a
// file's modification time cannot hide an edited directive, and a file whose
// filesystem reports no status-change time is read every time.
var frameworkEmbedPatterns struct {
	sync.Mutex
	entries map[string]frameworkEmbedPatternEntry
}

type frameworkEmbedPatternEntry struct {
	stamp    buildInputFileStamp
	patterns []string
}

// frameworkEmbedPatternLimit bounds the retained entries; exceeding it
// discards them all.
const frameworkEmbedPatternLimit = 16_384

func retainedFrameworkEmbedPatterns(path string, info os.FileInfo) ([]string, bool) {
	stamp := buildInputStamp(info)
	if stamp.ChangeTimeNano == 0 {
		return nil, false
	}
	frameworkEmbedPatterns.Lock()
	defer frameworkEmbedPatterns.Unlock()
	entry, ok := frameworkEmbedPatterns.entries[filepath.Clean(path)]
	if !ok || entry.stamp != stamp {
		return nil, false
	}
	return slices.Clone(entry.patterns), true
}

// retainFrameworkEmbedPatterns retains patterns read from path when the file's
// stamp after the read equals the stamp observed before it.
func retainFrameworkEmbedPatterns(path string, before os.FileInfo, patterns []string) {
	after, err := os.Lstat(path)
	if err != nil {
		return
	}
	stamp := buildInputStamp(after)
	if stamp.ChangeTimeNano == 0 || stamp != buildInputStamp(before) {
		return
	}
	frameworkEmbedPatterns.Lock()
	defer frameworkEmbedPatterns.Unlock()
	if frameworkEmbedPatterns.entries == nil || len(frameworkEmbedPatterns.entries) >= frameworkEmbedPatternLimit {
		frameworkEmbedPatterns.entries = map[string]frameworkEmbedPatternEntry{}
	}
	frameworkEmbedPatterns.entries[filepath.Clean(path)] = frameworkEmbedPatternEntry{stamp: stamp, patterns: slices.Clone(patterns)}
}
