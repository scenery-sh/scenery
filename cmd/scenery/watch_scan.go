package main

import (
	"errors"

	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	// Generated path discovery walks the tree on its own listings, so it runs
	// beside the watch walk; the files the walk found are classified once both
	// finished, in walk order.
	type discovery struct {
		paths map[string]bool
		err   error
	}
	discovering := make(chan discovery, 1)
	go func() {
		paths, err := discoverGenerated(root)
		discovering <- discovery{paths: paths, err: err}
	}()
	type candidate struct {
		rel, path string
	}
	var (
		dirs       []string
		candidates []candidate
	)
	ignore := watchignore.New(root)
	walk := watchListings(root).Begin(fresh)
	walkErr := walkWatchTree(root, ignore, walk, &snapshot.scanStats, func(path, rel string, d fs.DirEntry, err error) error {
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

		if rel == "" {
			return nil
		}

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
		candidates = append(candidates, candidate{rel: rel, path: path})
		return nil
	})
	discovered := <-discovering
	if discovered.err != nil {
		return fileSnapshot{}, discovered.err
	}
	if walkErr != nil {
		return fileSnapshot{}, walkErr
	}
	walk.Finish()
	generated := discovered.paths
	snapshot.generated = make(map[string]bool, len(generated))
	snapshot.generatedContent = make(map[string]fileStamp, len(generated))
	snapshot.retryGenerated = previous.retryGenerated
	if receipt, err := compiler.ReadSQLCArtifactReceipt(root); err == nil && len(receipt.Files) > 0 {
		snapshot.consumedGenerated = make(map[string]bool, len(receipt.Files))
		for rel := range receipt.Files {
			snapshot.consumedGenerated[rel] = true
		}
	}
	genList := make([]string, 0, len(generated))
	for rel := range generated {
		genList = append(genList, rel)
	}
	if len(genList) > 0 {
		type genResult struct {
			regular bool
			has     bool
			stamp   fileStamp
		}
		genResults := make([]genResult, len(genList))
		workers := min(16, max(4, runtime.GOMAXPROCS(0)), len(genList))
		var next atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for {
					i := int(next.Add(1)) - 1
					if i >= len(genList) {
						return
					}
					rel := genList[i]
					full := filepath.Join(root, filepath.FromSlash(rel))
					info, err := os.Lstat(full)
					if err != nil || !info.Mode().IsRegular() {
						continue
					}
					genResults[i].regular = true
					stamp, reused := reusableStamp(previous.generatedContent, rel, info, false)
					if !reused {
						stamp, _, err = stampWatchedFile(full, info, false)
						if err != nil {
							continue
						}
					}
					genResults[i].has = true
					genResults[i].stamp = stamp
				}
			})
		}
		wg.Wait()
		for i, rel := range genList {
			snapshot.generated[rel] = genResults[i].regular
			if genResults[i].has {
				snapshot.generatedContent[rel] = genResults[i].stamp
			}
		}
	}
	type candResult struct {
		ok          bool
		stamp       fileStamp
		hashed      bool
		bytesHashed int64
		patterns    []string
	}
	candResults := make([]candResult, len(candidates))
	if len(candidates) > 0 {
		workers := min(16, max(4, runtime.GOMAXPROCS(0)), len(candidates))
		var next atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for {
					i := int(next.Add(1)) - 1
					if i >= len(candidates) {
						return
					}
					rel, path := candidates[i].rel, candidates[i].path
					if generated[rel] {
						continue
					}
					info, err := os.Lstat(path)
					if err != nil || !info.Mode().IsRegular() {
						continue
					}
					var data []byte
					stamp, reused := reusableStamp(previous.files, rel, info, false)
					if !reused {
						stamp, data, err = stampWatchedFile(path, info, false)
						if err != nil {
							continue
						}
					}
					res := candResult{ok: true, stamp: stamp, hashed: !reused, bytesHashed: int64(len(data))}
					if filepath.Ext(rel) == ".go" {
						patterns, cached := cachedGoEmbedPatterns(path, stamp)
						if !cached {
							if data == nil {
								if data, err = os.ReadFile(path); err != nil {
									continue
								}
							}
							patterns = parseGoEmbedPatterns(string(data))
							storeGoEmbedPatterns(path, stamp, patterns)
						}
						res.patterns = patterns
					}
					candResults[i] = res
				}
			})
		}
		wg.Wait()
	}
	for i, found := range candidates {
		res := candResults[i]
		if !res.ok {
			continue
		}
		if res.hashed {
			snapshot.scanStats.filesHashed++
			snapshot.scanStats.bytesHashed += res.bytesHashed
		}
		snapshot.files[found.rel] = res.stamp
		if len(res.patterns) > 0 {
			pkgDir := filepath.Dir(found.rel)
			for _, pattern := range res.patterns {
				if err := addEmbeddedSnapshotFiles(root, pkgDir, pattern, snapshot.files, previous.files, ignore); err != nil {
					return fileSnapshot{}, err
				}
			}
		}
	}
	// WalkDir visits each directory exactly once, so the list is already
	// unique; DFS pre-order is not string-sorted, so sort stays.
	sort.Strings(dirs)
	snapshot.dirs = dirs
	snapshot.captureCompilerRevisionFiles(root, previous, fresh)
	snapshot.capturedAt = time.Now()
	return snapshot, nil
}
