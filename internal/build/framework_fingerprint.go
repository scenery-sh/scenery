package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

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

// frameworkNeutralGoModDigest identifies the main module file at path, whose
// content digest discovery recorded under stamp, by its bytes with every local
// scenery.sh replacement that selects one of frameworkRoots spelled as the
// framework source digest instead of as a directory. Discovery binds that
// directory's content separately ("framework/scenery.sh/source"), so a
// worktree-specific snapshot path is not a build input: worktrees of one
// commit that select identical framework source get one identity and share
// their linked executables. Every other byte of the file stays an input, and a
// file without such a replacement keeps its content digest.
func frameworkNeutralGoModDigest(path string, stamp buildInputFileStamp, digest string, frameworkRoots []string, sourceDigest string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	info, err := buildInputLstat(path)
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256(data); "sha256:"+hex.EncodeToString(sum[:]) != digest || buildInputStamp(info) != stamp {
		return "", fmt.Errorf("go build input changed during discovery: %s", path)
	}
	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		// The Go command accepted this file; without a parsed replacement it
		// keeps its content digest.
		return digest, nil
	}
	type span struct{ start, end int }
	var selected []span
	for _, replacement := range file.Replace {
		if replacement.Old.Path != "scenery.sh" || replacement.New.Version != "" || replacement.Syntax == nil {
			continue
		}
		root := replacement.New.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(filepath.Dir(path), root)
		}
		if slices.Contains(frameworkRoots, filepath.Clean(root)) {
			selected = append(selected, span{replacement.Syntax.Start.Byte, replacement.Syntax.End.Byte})
		}
	}
	if len(selected) == 0 {
		return digest, nil
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "scenery.go-mod-framework-neutral\x00"+sourceDigest+"\x00")
	offset := 0
	for _, replacement := range selected {
		if replacement.start < offset || replacement.end > len(data) || replacement.start > replacement.end {
			return "", fmt.Errorf("go module file replacement is out of order: %s", path)
		}
		_, _ = hash.Write(data[offset:replacement.start])
		_, _ = io.WriteString(hash, "\x00scenery.sh framework source\x00")
		offset = replacement.end
	}
	_, _ = hash.Write(data[offset:])
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// frameworkSourceFiles is frameworkFingerprintFiles with the metadata the walk
// read for each Go file, so a caller stamping those files need not read it
// again.
func frameworkSourceFiles(repoRoot string) ([]string, map[string]os.FileInfo, error) {
	files := map[string]struct{}{}
	infos := map[string]os.FileInfo{}
	var sources []frameworkGoSource
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
		sources = append(sources, frameworkGoSource{relative: rel, path: path, info: info})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if err := readFrameworkGoSources(sources); err != nil {
		return nil, nil, err
	}
	for _, source := range sources {
		pkgDir := filepath.Dir(source.relative)
		for _, pattern := range source.patterns {
			if err := addGeneratorEmbeddedPatternFiles(repoRoot, pkgDir, pattern, files); err != nil {
				return nil, nil, err
			}
		}
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, infos, nil
}

type frameworkGoSource struct {
	relative string
	path     string
	info     os.FileInfo
	patterns []string
}

// readFrameworkGoSources finds the embed patterns of every Go source. A file
// whose patterns are not retained for its stamp is read once, on one of
// several workers, and that read also retains its content digest, which the
// source manifest asks for next.
func readFrameworkGoSources(sources []frameworkGoSource) error {
	workers := min(runtime.GOMAXPROCS(0), 8, len(sources))
	if workers < 1 {
		return nil
	}
	var next atomic.Int64
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			for {
				index := int(next.Add(1)) - 1
				if index >= len(sources) {
					return
				}
				source := &sources[index]
				if patterns, retained := retainedFrameworkEmbedPatterns(source.path, source.info); retained {
					source.patterns = patterns
					continue
				}
				var data []byte
				_, _, err := cachedBuildInputFileDigest(source.path, source.info, func(path string) ([]byte, error) {
					var readErr error
					data, readErr = os.ReadFile(path)
					return data, readErr
				})
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				// A retained digest needs no read, and a file changing under the
				// read has no digest to retain; listing needs its patterns anyway.
				if data == nil || err != nil {
					if data, err = os.ReadFile(source.path); errors.Is(err, os.ErrNotExist) {
						continue
					} else if err != nil {
						errs[worker] = err
						return
					}
				}
				source.patterns = parseGeneratorGoEmbedPatterns(string(data))
				retainFrameworkEmbedPatterns(source.path, source.info, source.patterns)
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
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
