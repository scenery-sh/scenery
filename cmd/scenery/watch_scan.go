package main

import (
	"errors"

	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/dirlisting"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/watchignore"
)

func scanWatchedFiles(root string) (fileSnapshot, error) {
	return scanWatchedFilesReusing(root, fileSnapshot{})
}

// scanWatchedFilesReusing rescans the tree while reusing content hashes from
// the previous snapshot for files whose size, permissions, and mtime are
// unchanged, so steady-state watch ticks stat files instead of re-reading and
// re-hashing the whole workspace. Directory listings of unchanged directories
// are reused (internal/dirlisting).
func scanWatchedFilesReusing(root string, previous fileSnapshot) (fileSnapshot, error) {
	return scanWatchedFilesWith(root, previous, false)
}

// scanWatchedFilesFresh rescans the tree reading every directory, as an
// observation independent of reused listings; it reconciles the listings it
// reads.
func scanWatchedFilesFresh(root string, previous fileSnapshot) (fileSnapshot, error) {
	return scanWatchedFilesWith(root, previous, true)
}

// watchListings is the directory listing tree of the watcher's scans of root.
func watchListings(root string) *dirlisting.Tree {
	return dirlisting.TreeFor("watch\x00" + filepath.Clean(root))
}

func scanWatchedFilesWith(root string, previous fileSnapshot, fresh bool) (fileSnapshot, error) {
	scanStartedAt := time.Now()
	snapshot := fileSnapshot{
		scanStartedAt: scanStartedAt,
		contract:      previous.contract, contractFiles: previous.contractFiles, contractCompiler: previous.contractCompiler,
		contractCompilerAbsent: previous.contractCompilerAbsent, membership: previous.membership,
		files: make(map[string]fileStamp, len(previous.files)), compilerValid: true,
	}
	discoverGenerated := compiler.GeneratedPaths
	if fresh {
		discoverGenerated = compiler.ReconcileGeneratedPaths
	}
	generated, err := discoverGenerated(root)
	if err != nil {
		return fileSnapshot{}, err
	}
	walk := watchListings(root).Begin(fresh)
	snapshot.generated = make(map[string]bool, len(generated))
	snapshot.generatedContent = make(map[string]fileStamp, len(generated))
	snapshot.retryGenerated = previous.retryGenerated
	for rel := range generated {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		snapshot.generated[rel] = err == nil && info.Mode().IsRegular()
		if snapshot.generated[rel] {
			stamp, reused := reusableStamp(previous.generatedContent, rel, info, false)
			if !reused {
				stamp, _, err = stampWatchedFile(filepath.Join(root, filepath.FromSlash(rel)), info, false)
				if err != nil {
					continue
				}
			}
			snapshot.generatedContent[rel] = stamp
		}
	}
	var dirs []string
	ignore := watchignore.New(root)
	err = walkWatchTree(root, ignore, walk, &snapshot.scanStats, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Tolerate entries vanishing or turning unreadable mid-scan; a
			// transient walk error must not abort the watch loop.
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
			if shouldIgnoreWatchEntryWithMatcher(rel, true, ignore) || isProductionFrontendOutputDir(root, rel) {
				return filepath.SkipDir
			}
			dirs = append(dirs, rel)
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if generated[rel] {
			return nil
		}
		if shouldIgnoreWatchEntryWithMatcher(rel, false, ignore) {
			return nil
		}
		// Tests and their embed directives do not belong to a runtime build.
		// Explicit runtime embeds can still add these bytes through the owner.
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		if !isWatchedFile(rel) && classifyAssistantWatchPath(root, rel) == "" {
			if _, ok := productionFrontendForWatchPath(root, rel); !ok {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		var data []byte
		stamp, reused := reusableStamp(previous.files, rel, info, false)
		if !reused {
			stamp, data, err = stampWatchedFile(path, info, false)
			if err != nil {
				return nil
			}
			snapshot.scanStats.filesHashed++
			snapshot.scanStats.bytesHashed += int64(len(data))
		}
		snapshot.files[rel] = stamp
		if filepath.Ext(rel) == ".go" {
			patterns, cached := cachedGoEmbedPatterns(path, stamp)
			if !cached {
				if data == nil {
					if data, err = os.ReadFile(path); err != nil {
						return nil
					}
				}
				patterns = parseGoEmbedPatterns(string(data))
				storeGoEmbedPatterns(path, stamp, patterns)
			}
			pkgDir := filepath.Dir(rel)
			for _, pattern := range patterns {
				if err := addEmbeddedSnapshotFiles(root, pkgDir, pattern, snapshot.files, previous.files, ignore); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return fileSnapshot{}, err
	}
	walk.Finish()
	// WalkDir visits each directory exactly once, so the list is already
	// unique; DFS pre-order is not string-sorted, so sort stays.
	sort.Strings(dirs)
	snapshot.dirs = dirs
	snapshot.captureCompilerRevisionFiles(root, previous)
	snapshot.capturedAt = time.Now()
	return snapshot, nil
}
